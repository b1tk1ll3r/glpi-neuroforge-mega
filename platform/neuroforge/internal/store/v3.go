package store

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

func (s *Store) resolveConflictLocked(in *core.Memory) []core.Memory {
	key := strings.TrimSpace(in.TruthKey)
	if key == "" {
		for _, tag := range in.Tags {
			if strings.HasPrefix(strings.ToLower(tag), "truth:") && len(tag) > 6 {
				key = strings.TrimSpace(tag[6:])
				in.TruthKey = key
				break
			}
		}
	}
	if key == "" {
		return nil
	}
	var changed []core.Memory
	group := "conflict:" + strings.ToLower(key)
	for id, meta := range s.state.Memories {
		if id == in.ID || !strings.EqualFold(strings.TrimSpace(meta.TruthKey), key) || meta.Status == core.MemoryArchived {
			continue
		}
		old, ok := s.materializeMemoryLocked(id)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(old.Text), strings.TrimSpace(in.Text)) {
			if in.Version >= old.Version {
				old.Status = core.MemorySuperseded
				in.Supersedes = appendUniqueString(in.Supersedes, old.ID)
				changed = append(changed, cloneMemory(*old))
			} else {
				in.Status = core.MemorySuperseded
			}
			continue
		}
		if in.Version > old.Version {
			old.Status = core.MemorySuperseded
			in.Supersedes = appendUniqueString(in.Supersedes, old.ID)
			changed = append(changed, cloneMemory(*old))
			continue
		}
		if old.Version > in.Version {
			in.Status = core.MemorySuperseded
			continue
		}
		newScore := knowledgeScore(in)
		oldScore := knowledgeScore(old)
		if math.Abs(newScore-oldScore) < 0.15 {
			old.Status = core.MemoryConflicted
			old.ConflictGroup = group
			in.Status = core.MemoryConflicted
			in.ConflictGroup = group
			changed = append(changed, cloneMemory(*old))
		} else if newScore > oldScore {
			old.Status = core.MemorySuperseded
			in.Supersedes = appendUniqueString(in.Supersedes, old.ID)
			changed = append(changed, cloneMemory(*old))
		} else {
			in.Status = core.MemorySuperseded
		}
	}
	return changed
}

func knowledgeScore(m *core.Memory) float64 {
	confidence := m.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	salience := m.Salience
	if salience <= 0 {
		salience = 1
	}
	return 0.55*confidence + 0.25*vector.Clamp((m.Reward+1)/2, 0, 1) + 0.20*vector.Clamp(salience/2, 0, 1)
}

func appendUniqueString(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

func (s *Store) ResolveConflict(truthKey, winnerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	winner, ok := s.materializeMemoryLocked(winnerID)
	if !ok || !strings.EqualFold(strings.TrimSpace(winner.TruthKey), strings.TrimSpace(truthKey)) {
		return errors.New("winner memory not found for truth key")
	}
	changed := []core.Memory{}
	for id, meta := range s.state.Memories {
		if !strings.EqualFold(strings.TrimSpace(meta.TruthKey), strings.TrimSpace(truthKey)) {
			continue
		}
		m, ok := s.materializeMemoryLocked(id)
		if !ok {
			continue
		}
		m.ConflictGroup = ""
		if m.ID == winnerID {
			m.Status = core.MemoryActive
		} else {
			m.Status = core.MemorySuperseded
			winner.Supersedes = appendUniqueString(winner.Supersedes, m.ID)
		}
		changed = append(changed, cloneMemory(*m))
	}
	if len(changed) == 0 {
		return errors.New("no memories found for truth key")
	}
	// Winner may have gained supersedes after its earlier copy was appended.
	for i := range changed {
		if changed[i].ID == winner.ID {
			changed[i] = cloneMemory(*winner)
		}
	}
	return s.commitLocked("memory.upsert", changed)
}

func (s *Store) ConflictsSnapshot() map[string][]core.Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string][]core.Memory{}
	for id, meta := range s.state.Memories {
		if meta.Status != core.MemoryConflicted || meta.TruthKey == "" {
			continue
		}
		if m, ok := s.fullMemoryForReadLocked(id); ok {
			out[m.TruthKey] = append(out[m.TruthKey], cloneMemory(m))
		}
	}
	return out
}

func (s *Store) SetMemoryHomeShard(id, shard string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.materializeMemoryLocked(id)
	if !ok {
		return errors.New("memory not found")
	}
	m.HomeShardID = shard
	return s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*m)})
}

func cloneGoal(g core.Goal) core.Goal {
	g.MemoryIDs = append([]string(nil), g.MemoryIDs...)
	g.Tags = append([]string(nil), g.Tags...)
	return g
}

func (s *Store) UpsertGoal(g *core.Goal) error {
	if g == nil || strings.TrimSpace(g.Title) == "" {
		return errors.New("goal title required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	newGoal := g.ID == "" || s.state.Goals[g.ID] == nil
	if g.ID == "" {
		g.ID = NewID("goal")
	}
	if g.Status == "" {
		g.Status = core.GoalActive
	}
	if g.Priority == 0 {
		g.Priority = 50
	}
	if g.IntervalMinutes <= 0 {
		g.IntervalMinutes = s.state.Config.Autonomy.DefaultGoalIntervalMinutes
	}
	if newGoal {
		g.AutoRun = s.state.Config.Autonomy.RunOnGoalCreate
		g.ResearchEnabled = s.state.Config.Research.Goal.Enabled
		if g.AutoRun {
			g.NextCycleAt = now
		}
	}
	g.Progress = vector.Clamp(g.Progress, 0, 1)
	if g.CreatedAt.IsZero() {
		if old := s.state.Goals[g.ID]; old != nil {
			g.CreatedAt = old.CreatedAt
		} else {
			g.CreatedAt = now
		}
	}
	g.UpdatedAt = now
	cp := cloneGoal(*g)
	s.state.Goals[g.ID] = &cp
	return s.commitLocked("goal.upsert", cp)
}

func (s *Store) GetGoal(id string) (*core.Goal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g := s.state.Goals[id]
	if g == nil {
		return nil, false
	}
	cp := cloneGoal(*g)
	return &cp, true
}

func (s *Store) GoalsSnapshot() []core.Goal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Goal, 0, len(s.state.Goals))
	for _, g := range s.state.Goals {
		out = append(out, cloneGoal(*g))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}

func (s *Store) PauseGoal(id string) (*core.Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state.Goals[id]
	if g == nil {
		return nil, errors.New("goal not found")
	}
	if g.Status == core.GoalCompleted || g.Status == core.GoalFailed {
		return nil, fmt.Errorf("goal in status %q cannot be paused", g.Status)
	}
	g.Status = core.GoalPaused
	g.NextCycleAt = time.Time{}
	g.UpdatedAt = time.Now().UTC()
	cp := cloneGoal(*g)
	if err := s.commitLocked("goal.upsert", cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *Store) ResumeGoal(id string) (*core.Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state.Goals[id]
	if g == nil {
		return nil, errors.New("goal not found")
	}
	if g.Status == core.GoalCompleted || g.Status == core.GoalFailed {
		return nil, fmt.Errorf("goal in status %q cannot be resumed", g.Status)
	}
	g.Status = core.GoalActive
	g.ConsecutiveErrors = 0
	g.LastError = ""
	if g.AutoRun && s.state.Config.Autonomy.Enabled {
		g.NextCycleAt = time.Now().UTC()
	} else {
		g.NextCycleAt = time.Time{}
	}
	g.UpdatedAt = time.Now().UTC()
	cp := cloneGoal(*g)
	if err := s.commitLocked("goal.upsert", cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *Store) DeleteGoal(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Goals[id]; !ok {
		return errors.New("goal not found")
	}
	delete(s.state.Goals, id)
	return s.commitLocked("goal.delete", id)
}

func (s *Store) AddLearningCycle(c core.LearningCycle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = NewID("cycle")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	s.state.Cycles = append(s.state.Cycles, c)
	if len(s.state.Cycles) > 10000 {
		s.state.Cycles = s.state.Cycles[len(s.state.Cycles)-10000:]
	}
	return s.commitLocked("cycle.add", c)
}

func (s *Store) RecentLearningCycles(limit int) []core.LearningCycle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.state.Cycles) {
		limit = len(s.state.Cycles)
	}
	out := append([]core.LearningCycle(nil), s.state.Cycles[len(s.state.Cycles)-limit:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type RetentionResult struct {
	Deleted    int      `json:"deleted"`
	Compressed int      `json:"compressed"`
	IDs        []string `json:"ids,omitempty"`
}

type retentionCandidate struct {
	id      string
	utility float64
	ageDays float64
}

func memoryUtility(m *core.Memory, now time.Time) float64 {
	ageDays := now.Sub(m.AccessedAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	freshness := math.Exp(-ageDays / 30)
	access := math.Min(1, math.Log1p(float64(m.AccessCount))/4)
	confidence := m.Confidence
	if confidence <= 0 {
		confidence = 1
	}
	reward := (m.Reward + 1) / 2
	return 0.30*freshness + 0.20*access + 0.20*vector.Clamp(m.Salience/2, 0, 1) + 0.20*vector.Clamp(confidence, 0, 1) + 0.10*vector.Clamp(reward, 0, 1)
}

func (s *Store) RunRetention(now time.Time) (RetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Retention
	result := RetentionResult{}
	if !cfg.Enabled {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	candidates := make([]retentionCandidate, 0, len(s.state.Memories))
	deleteSet := map[string]bool{}
	changed := []core.Memory{}
	for id, m := range s.state.Memories {
		if m == nil {
			continue
		}
		ageDays := now.Sub(m.CreatedAt).Hours() / 24
		if m.MemoryType == core.MemoryWorking && cfg.WorkingTTLHours > 0 && now.Sub(m.CreatedAt).Hours() >= cfg.WorkingTTLHours {
			deleteSet[id] = true
			continue
		}
		utility := memoryUtility(m, now)
		candidates = append(candidates, retentionCandidate{id: id, utility: utility, ageDays: ageDays})
		if ageDays < cfg.MinAgeDays || utility >= cfg.MinUtility || m.Status == core.MemoryArchived {
			continue
		}
		if cfg.DeleteConsolidated && m.ConsolidatedInto != "" {
			deleteSet[id] = true
			continue
		}
		full, ok := s.materializeMemoryLocked(m.ID)
		if !ok {
			continue
		}
		m = full
		m.Status = core.MemoryArchived
		m.Compressed = true
		m.Vector = nil
		m.VectorDim = 0
		if cfg.CompressChars > 0 && len([]rune(m.Text)) > cfg.CompressChars {
			r := []rune(m.Text)
			m.Text = string(r[:cfg.CompressChars]) + "…"
		}
		s.trackHotMemoryLocked(m.ID, m)
		changed = append(changed, cloneMemory(*m))
		result.Compressed++
		result.IDs = append(result.IDs, m.ID)
	}
	if cfg.MaxMemories > 0 && len(s.state.Memories)-len(deleteSet) > cfg.MaxMemories {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].utility < candidates[j].utility })
		need := len(s.state.Memories) - len(deleteSet) - cfg.MaxMemories
		for _, c := range candidates {
			if need <= 0 {
				break
			}
			m := s.state.Memories[c.id]
			if m == nil || deleteSet[c.id] || m.MemoryType == core.MemoryProcedural {
				continue
			}
			deleteSet[c.id] = true
			need--
		}
	}
	if len(changed) > 0 {
		if err := s.commitLocked("memory.upsert", changed); err != nil {
			return result, err
		}
	}
	if len(deleteSet) > 0 {
		ids := make([]string, 0, len(deleteSet))
		for id := range deleteSet {
			ids = append(ids, id)
			delete(s.state.Memories, id)
			s.untrackHotMemoryLocked(id)
			if s.pageCache != nil {
				s.pageCache.Delete(id)
			}
			for k, syn := range s.state.Synapses {
				if syn.A == id || syn.B == id {
					delete(s.state.Synapses, k)
				}
			}
		}
		sort.Strings(ids)
		result.Deleted = len(ids)
		result.IDs = append(result.IDs, ids...)
		if err := s.commitLocked("memory.delete", ids); err != nil {
			return result, err
		}
	}
	if len(changed) > 0 || len(deleteSet) > 0 {
		s.rebuildIndexesLocked()
	}
	return result, nil
}

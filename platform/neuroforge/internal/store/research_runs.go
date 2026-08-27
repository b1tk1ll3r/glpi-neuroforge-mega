package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
)

const (
	maxResearchEventsPerRun = 600
	maxResearchRuns         = 200
)

type researchEventWAL struct {
	RunID string             `json:"run_id"`
	Event core.ResearchEvent `json:"event"`
}

func cloneResearchRun(in core.ResearchRun) core.ResearchRun {
	out := in
	out.Queries = append([]string(nil), in.Queries...)
	out.Events = make([]core.ResearchEvent, len(in.Events))
	for i := range in.Events {
		out.Events[i] = in.Events[i]
		if in.Events[i].Metadata != nil {
			out.Events[i].Metadata = make(map[string]string, len(in.Events[i].Metadata))
			for k, v := range in.Events[i].Metadata {
				out.Events[i].Metadata[k] = v
			}
		}
	}
	return out
}

func (s *Store) StartResearchRun(goalID, goalTitle string) (*core.ResearchRun, error) {
	if strings.TrimSpace(goalID) == "" {
		return nil, errors.New("goal id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.ResearchRuns == nil {
		s.state.ResearchRuns = map[string]*core.ResearchRun{}
	}
	now := time.Now().UTC()
	run := core.ResearchRun{
		ID:        NewID("research"),
		GoalID:    goalID,
		GoalTitle: strings.TrimSpace(goalTitle),
		Status:    "running",
		StartedAt: now,
		UpdatedAt: now,
	}
	s.state.ResearchRuns[run.ID] = &run
	if err := s.commitLocked("research.run.upsert", run); err != nil {
		delete(s.state.ResearchRuns, run.ID)
		return nil, err
	}
	for _, oldID := range s.trimResearchRunsLocked() {
		if err := s.commitLocked("research.run.delete", oldID); err != nil {
			return nil, err
		}
	}
	cp := cloneResearchRun(run)
	return &cp, nil
}

func (s *Store) AddResearchEvent(runID string, ev core.ResearchEvent) (core.ResearchEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.state.ResearchRuns[runID]
	if run == nil {
		return core.ResearchEvent{}, errors.New("research run not found")
	}
	if ev.ID == "" {
		ev.ID = NewID("rev")
	}
	if ev.RunID == "" {
		ev.RunID = run.ID
	}
	if ev.GoalID == "" {
		ev.GoalID = run.GoalID
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	run.LastSeq++
	ev.Seq = run.LastSeq
	applyResearchEvent(run, ev)
	// Research trace events are operational telemetry, not authoritative learning
	// state. Keep them live in memory and persist the bounded run once at finish.
	// This avoids an fsync/WAL revision for every URL/chunk while normal memory,
	// source and knowledge writes retain their existing durability guarantees.
	return ev, nil
}

func applyResearchEvent(run *core.ResearchRun, ev core.ResearchEvent) {
	if ev.Seq > run.LastSeq {
		run.LastSeq = ev.Seq
	}
	run.UpdatedAt = ev.CreatedAt
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = time.Now().UTC()
	}
	if ev.Query != "" && ev.Type == "query.planned" {
		found := false
		for _, q := range run.Queries {
			if q == ev.Query {
				found = true
				break
			}
		}
		if !found {
			run.Queries = append(run.Queries, ev.Query)
			run.Stats.Queries++
		}
	}
	switch ev.Type {
	case "search.result":
		run.Stats.Results++
	case "download.started":
		run.Stats.DownloadsStarted++
	case "download.completed":
		run.Stats.DownloadsCompleted++
		if ev.Metadata["kind"] == "document" {
			run.Stats.Documents++
		} else if ev.Metadata["kind"] == "page" {
			run.Stats.Pages++
		}
	case "claim.extracted":
		run.Stats.Claims++
	case "evidence.learned":
		run.Stats.NewEvidence++
	case "evidence.duplicate", "source.duplicate":
		run.Stats.Duplicates++
	case "evidence.corroborated":
		run.Stats.Duplicates++
		run.Stats.Corroborations++
	case "source.rejected":
		run.Stats.RejectedSources++
	case "evidence.skipped":
		run.Stats.SkippedEvidence++
	}
	if ev.Status == "error" || strings.HasSuffix(ev.Type, ".error") {
		run.Stats.Errors++
		run.LastError = ev.Message
	}
	run.Events = append(run.Events, ev)
	if len(run.Events) > maxResearchEventsPerRun {
		run.Events = append([]core.ResearchEvent(nil), run.Events[len(run.Events)-maxResearchEventsPerRun:]...)
	}
}

func (s *Store) FinishResearchRun(runID, status, lastError string) (*core.ResearchRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.state.ResearchRuns[runID]
	if run == nil {
		return nil, errors.New("research run not found")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(status) == "" {
		status = "completed"
	}
	run.Status = status
	run.UpdatedAt = now
	run.CompletedAt = now
	if lastError != "" {
		run.LastError = lastError
	}
	cp := cloneResearchRun(*run)
	if err := s.commitLocked("research.run.upsert", cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *Store) GetResearchRun(id string) (*core.ResearchRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run := s.state.ResearchRuns[id]
	if run == nil {
		return nil, false
	}
	cp := cloneResearchRun(*run)
	return &cp, true
}

func (s *Store) LatestResearchRun(goalID string) (*core.ResearchRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *core.ResearchRun
	for _, run := range s.state.ResearchRuns {
		if run == nil || run.GoalID != goalID {
			continue
		}
		if best == nil || run.StartedAt.After(best.StartedAt) {
			best = run
		}
	}
	if best == nil {
		return nil, false
	}
	cp := cloneResearchRun(*best)
	return &cp, true
}

func (s *Store) ResearchRunsSnapshot(goalID string, limit int) []core.ResearchRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.ResearchRun, 0)
	for _, run := range s.state.ResearchRuns {
		if run == nil || (goalID != "" && run.GoalID != goalID) {
			continue
		}
		out = append(out, cloneResearchRun(*run))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit <= 0 {
		limit = 10
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) trimResearchRunsLocked() []string {
	if len(s.state.ResearchRuns) <= maxResearchRuns {
		return nil
	}
	type pair struct {
		id string
		t  time.Time
	}
	xs := make([]pair, 0, len(s.state.ResearchRuns))
	for id, r := range s.state.ResearchRuns {
		if r != nil {
			xs = append(xs, pair{id: id, t: r.StartedAt})
		}
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].t.Before(xs[j].t) })
	deleted := []string{}
	for len(s.state.ResearchRuns) > maxResearchRuns && len(xs) > 0 {
		delete(s.state.ResearchRuns, xs[0].id)
		deleted = append(deleted, xs[0].id)
		xs = xs[1:]
	}
	return deleted
}

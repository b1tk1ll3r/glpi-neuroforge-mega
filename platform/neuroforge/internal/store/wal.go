package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

type walEvent struct {
	Revision uint64          `json:"revision"`
	Time     time.Time       `json:"time"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data"`
}

type indexSnapshotBundle struct {
	Revision uint64                         `json:"revision"`
	Indexes  map[string]vector.HNSWSnapshot `json:"indexes"`
}

func (s *Store) commitLocked(kind string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ev := walEvent{Revision: s.state.Revision + 1, Time: time.Now().UTC(), Type: kind, Data: data}
	if err := s.appendWALLocked(ev); err != nil {
		return err
	}
	if err := s.appendSegmentEventLocked(ev); err != nil {
		return err
	}
	s.state.Revision = ev.Revision
	s.walEventsSinceCheckpoint++
	every := s.state.Config.Storage.CheckpointEvery
	if every <= 0 {
		every = 500
	}
	if s.walEventsSinceCheckpoint >= every {
		return s.checkpointLocked()
	}
	return nil
}

func (s *Store) appendWALLocked(ev walEvent) error {
	dir := filepath.Join(s.dir, "wal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "wal-active.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(ev); err != nil {
		_ = f.Close()
		return err
	}
	if s.state.Config.Storage.WALSync {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	maxBytes := s.state.Config.Storage.MaxWALSegmentBytes
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxBytes {
		archived := filepath.Join(dir, fmt.Sprintf("wal-%020d.jsonl", ev.Revision))
		if err := os.Rename(path, archived); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) replayWAL() error {
	dir := filepath.Join(s.dir, "wal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	paths := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		if ent.Name() == "wal-active.jsonl" || strings.HasPrefix(ent.Name(), "wal-") {
			paths = append(paths, filepath.Join(dir, ent.Name()))
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		ai, aj := filepath.Base(paths[i]), filepath.Base(paths[j])
		if ai == "wal-active.jsonl" {
			return false
		}
		if aj == "wal-active.jsonl" {
			return true
		}
		return ai < aj
	})
	for _, path := range paths {
		if err := s.replayWALFile(path); err != nil {
			return fmt.Errorf("replay %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func (s *Store) replayWALFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	buf := make([]byte, 64<<10)
	scan.Buffer(buf, 16<<20)
	for scan.Scan() {
		var ev walEvent
		if err := json.Unmarshal(scan.Bytes(), &ev); err != nil {
			return err
		}
		if ev.Revision <= s.state.Revision {
			continue
		}
		if ev.Revision != s.state.Revision+1 {
			return fmt.Errorf("WAL revision gap: have %d, got %d", s.state.Revision, ev.Revision)
		}
		if err := s.applyWALEvent(ev); err != nil {
			return fmt.Errorf("revision %d type %s: %w", ev.Revision, ev.Type, err)
		}
		if err := s.appendSegmentEventLocked(ev); err != nil {
			return fmt.Errorf("segment replay revision %d type %s: %w", ev.Revision, ev.Type, err)
		}
		s.state.Revision = ev.Revision
	}
	return scan.Err()
}

func (s *Store) appendSegmentEventLocked(ev walEvent) error {
	if s.segments == nil {
		return nil
	}
	switch ev.Type {
	case "memory.upsert":
		var items []core.Memory
		if err := json.Unmarshal(ev.Data, &items); err != nil {
			return err
		}
		return s.segments.AppendUpsert(ev.Revision, items)
	case "memory.delete":
		var ids []string
		if err := json.Unmarshal(ev.Data, &ids); err != nil {
			return err
		}
		return s.segments.AppendDelete(ev.Revision, ids)
	default:
		return nil
	}
}

func (s *Store) applyWALEvent(ev walEvent) error {
	switch ev.Type {
	case "config.set":
		return json.Unmarshal(ev.Data, &s.state.Config)
	case "memory.upsert":
		var items []core.Memory
		if err := json.Unmarshal(ev.Data, &items); err != nil {
			return err
		}
		for i := range items {
			m := items[i]
			s.state.Memories[m.ID] = &m
		}
	case "memory.delete":
		var ids []string
		if err := json.Unmarshal(ev.Data, &ids); err != nil {
			return err
		}
		for _, id := range ids {
			delete(s.state.Memories, id)
			for k, syn := range s.state.Synapses {
				if syn.A == id || syn.B == id {
					delete(s.state.Synapses, k)
				}
			}
		}
	case "synapse.upsert":
		var syn core.Synapse
		if err := json.Unmarshal(ev.Data, &syn); err != nil {
			return err
		}
		s.state.Synapses[edgeKey(syn.A, syn.B)] = &syn
		s.indexSynapseLocked(&syn)
	case "synapse.replace":
		var items []core.Synapse
		if err := json.Unmarshal(ev.Data, &items); err != nil {
			return err
		}
		s.state.Synapses = make(map[string]*core.Synapse, len(items))
		for i := range items {
			x := items[i]
			s.state.Synapses[edgeKey(x.A, x.B)] = &x
		}
		s.rebuildSynapseAdjLocked()
	case "usage.add":
		var x core.UsageEvent
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		s.state.Usage = append(s.state.Usage, x)
	case "job.upsert":
		var x core.Job
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		// v1.6.0 could leave a large WAL containing completed vector.relink
		// jobs with full target/candidate vectors. Compact each terminal event as
		// it is replayed so recovery memory remains bounded by one WAL record
		// instead of accumulating every historical vector payload in state.
		if x.Type == "vector.relink" && x.Status == "done" {
			x.Payload = nil
			x.Result = nil
		}
		s.state.Jobs[x.ID] = &x
	case "job.delete":
		var ids []string
		if err := json.Unmarshal(ev.Data, &ids); err != nil {
			return err
		}
		for _, id := range ids {
			delete(s.state.Jobs, id)
		}
	case "maintenance.set":
		return json.Unmarshal(ev.Data, &s.state.Maintenance)
	case "goal.upsert":
		var x core.Goal
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		s.state.Goals[x.ID] = &x
	case "goal.delete":
		var id string
		if err := json.Unmarshal(ev.Data, &id); err != nil {
			return err
		}
		delete(s.state.Goals, id)
	case "cycle.add":
		var x core.LearningCycle
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		s.state.Cycles = append(s.state.Cycles, x)
		if len(s.state.Cycles) > 10000 {
			s.state.Cycles = s.state.Cycles[len(s.state.Cycles)-10000:]
		}
	case "knowledge.event":
		var x core.KnowledgeEvent
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		s.state.KnowledgeEvents = append(s.state.KnowledgeEvents, x)
		if len(s.state.KnowledgeEvents) > maxKnowledgeEvents {
			s.state.KnowledgeEvents = s.state.KnowledgeEvents[len(s.state.KnowledgeEvents)-maxKnowledgeEvents:]
		}
	case "source.upsert":
		var x core.KnowledgeSource
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		if s.state.Sources == nil {
			s.state.Sources = map[string]*core.KnowledgeSource{}
		}
		s.state.Sources[x.ID] = &x
	case "research.run.upsert":
		var x core.ResearchRun
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		if s.state.ResearchRuns == nil {
			s.state.ResearchRuns = map[string]*core.ResearchRun{}
		}
		cp := cloneResearchRun(x)
		s.state.ResearchRuns[x.ID] = &cp
	case "research.run.delete":
		var id string
		if err := json.Unmarshal(ev.Data, &id); err != nil {
			return err
		}
		delete(s.state.ResearchRuns, id)
	case "research.event.add":
		var x researchEventWAL
		if err := json.Unmarshal(ev.Data, &x); err != nil {
			return err
		}
		if s.state.ResearchRuns == nil {
			s.state.ResearchRuns = map[string]*core.ResearchRun{}
		}
		run := s.state.ResearchRuns[x.RunID]
		if run == nil {
			run = &core.ResearchRun{ID: x.RunID, GoalID: x.Event.GoalID, Status: "running", StartedAt: x.Event.CreatedAt, UpdatedAt: x.Event.CreatedAt}
			s.state.ResearchRuns[x.RunID] = run
		}
		applyResearchEvent(run, x.Event)
	case "cluster.state":
		return json.Unmarshal(ev.Data, &s.state.Cluster)
	default:
		return fmt.Errorf("unknown WAL event type %q", ev.Type)
	}
	return nil
}

func (s *Store) checkpointLocked() error {
	if s.segments != nil {
		s.state.MemoryCatalog = core.MemoryCatalogState{SegmentBacked: true, Count: len(s.state.Memories), Revision: s.state.Revision}
	} else {
		s.state.MemoryCatalog = core.MemoryCatalogState{}
	}
	checkpoint := s.state
	if s.segments != nil {
		// v0.5: segment files are the authoritative memory catalog and body store.
		// Keep state.json O(non-memory-state) instead of O(memory-count).
		checkpoint.Memories = nil
	}
	// Persist acceleration state before the authoritative non-memory checkpoint.
	// If the process dies after the index snapshot but before state.json, the WAL
	// remains intact; boot replays it to the same revision and can immediately use
	// the already-written index. The previous order could advance state.json first,
	// then die during a large HNSW snapshot and force a full synchronous rebuild on
	// every restart.
	if s.state.Config.Storage.IndexSnapshot && s.state.Config.Brain.Index.Enabled {
		if err := s.writeIndexSnapshotLocked(); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(s.dir, "state.json"), 0600, &checkpoint); err != nil {
		return err
	}
	// The checkpoint and (when enabled) memory segments now cover every WAL
	// event through state.Revision. Prune those already-checkpointed log files
	// only after all checkpoint artifacts succeeded, otherwise long bulk
	// ingests retain a second full copy of memory payloads indefinitely.
	if err := s.pruneWALLocked(); err != nil {
		return err
	}
	s.walEventsSinceCheckpoint = 0
	return nil
}

func (s *Store) pruneWALLocked() error {
	dir := filepath.Join(s.dir, "wal")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		if ent.Name() == "wal-active.jsonl" || strings.HasPrefix(ent.Name(), "wal-") {
			if err := os.Remove(filepath.Join(dir, ent.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (s *Store) writeIndexSnapshotLocked() error {
	if indexMode(s.state.Config) == "disk-pq" {
		return nil
	}
	return s.writeSegmentedIndexSnapshotLocked()
}

func (s *Store) loadIndexSnapshotLocked() bool {
	if !s.state.Config.Storage.IndexSnapshot || !s.state.Config.Brain.Index.Enabled || indexMode(s.state.Config) == "disk-pq" {
		return false
	}
	if s.loadSegmentedIndexSnapshotLocked() {
		return true
	}
	return s.loadLegacyIndexSnapshotLocked()
}

func memorySearchable(m *core.Memory) bool {
	return m != nil && (m.Status == "" || m.Status == core.MemoryActive || m.Status == core.MemoryConflicted)
}

func (s *Store) ForceCheckpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkpointLocked()
}

func (s *Store) WALStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := filepath.Join(s.dir, "wal")
	entries, _ := os.ReadDir(dir)
	segments := 0
	var bytes int64
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		segments++
		if info, err := ent.Info(); err == nil {
			bytes += info.Size()
		}
	}
	out := map[string]any{"revision": s.state.Revision, "segments": segments, "bytes": bytes, "events_since_checkpoint": s.walEventsSinceCheckpoint}
	if s.segments != nil {
		out["memory_segments"] = s.segments.Stats()
	}
	return out
}

package store

import (
	"fmt"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

// AddMemoriesBatch applies a bounded batch as one WAL/segment transaction.
// Callers should keep batches reasonably small (hundreds, not millions) so WAL
// records remain easy to replay and memory spikes stay bounded.
func (s *Store) AddMemoriesBatch(items []core.Memory) error {
	if len(items) == 0 {
		return nil
	}
	if len(items) > 4096 {
		return fmt.Errorf("batch too large: %d > 4096", len(items))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	affected := make([]core.Memory, 0, len(items))
	created := make([]core.Memory, 0, len(items))
	indexBatches := map[int][]vector.HNSWItem{}
	for i := range items {
		m := cloneMemory(items[i])
		if m.ID == "" {
			m.ID = NewID("mem")
		}
		if _, exists := s.state.Memories[m.ID]; exists {
			return fmt.Errorf("memory %s already exists", m.ID)
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now
		}
		if m.AccessedAt.IsZero() {
			m.AccessedAt = now
		}
		if m.Salience == 0 {
			m.Salience = 1
		}
		if m.Confidence == 0 {
			m.Confidence = 1
		}
		if m.MemoryType == "" {
			m.MemoryType = inferMemoryType(m.Kind)
		}
		if m.ShardID == "" {
			m.ShardID = s.state.Config.Sharding.LocalShardID
		}
		if m.OriginShardID == "" {
			m.OriginShardID = m.ShardID
		}
		if m.HomeShardID == "" {
			m.HomeShardID = m.ShardID
		}
		if m.Status == "" {
			m.Status = core.MemoryActive
		}
		if m.Version == 0 {
			m.Version = 1
		}
		if m.VectorDim == 0 && len(m.Vector) > 0 {
			m.VectorDim = len(m.Vector)
		}
		affected = append(affected, s.resolveConflictLocked(&m)...)
		stored := cloneMemory(m)
		s.state.Memories[m.ID] = &stored
		s.indexProvenanceSourceLocked(m.ID, stored.Provenance.Source)
		s.trackHotMemoryLocked(m.ID, &stored)
		if s.state.Config.Brain.Index.Enabled && indexMode(s.state.Config) != "disk-pq" && len(m.Vector) > 0 {
			dim := len(m.Vector)
			indexBatches[dim] = append(indexBatches[dim], vector.HNSWItem{ID: m.ID, Vector: m.Vector})
		}
		created = append(created, cloneMemory(m))
		affected = append(affected, m)
	}
	for dim, batch := range indexBatches {
		idx := s.indexes[dim]
		if idx == nil {
			idx = s.newIndexLocked()
			s.indexes[dim] = idx
		}
		idx.AddBatch(batch)
	}
	if s.vectorJournal != nil {
		if err := s.vectorJournal.AppendNew(s.state.Revision+1, created); err != nil {
			return err
		}
	}
	return s.commitLocked("memory.upsert", affected)
}

// DeleteMemoriesBatch removes a bounded set of memories as one store mutation
// and rebuilds the in-memory ANN indexes only once. This is intentionally used
// by integration sync paths where a document update can replace many chunks.
func (s *Store) DeleteMemoriesBatch(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 4096 {
		return fmt.Errorf("batch too large: %d > 4096", len(ids))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(ids))
	removed := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		old, exists := s.state.Memories[id]
		if !exists {
			continue
		}
		if old != nil {
			s.unindexProvenanceSourceLocked(id, old.Provenance.Source)
		}
		delete(s.state.Memories, id)
		s.untrackHotMemoryLocked(id)
		if s.pageCache != nil {
			s.pageCache.Delete(id)
		}
		for key, syn := range s.state.Synapses {
			if syn.A == id || syn.B == id {
				s.unindexSynapseLocked(syn)
				delete(s.state.Synapses, key)
			}
		}
		removed = append(removed, id)
	}
	if len(removed) == 0 {
		return nil
	}
	s.rebuildIndexesLocked()
	return s.commitLocked("memory.delete", removed)
}

package store

import (
	"errors"
	"strings"

	"neuroforge/internal/core"
)

// provenanceSourceIDs is a rebuildable in-memory secondary index. It keeps
// integration namespace/source filtering proportional to the source itself
// instead of the complete memory catalog.
func (s *Store) rebuildProvenanceSourceIndexLocked() {
	s.provenanceSourceIDs = make(map[string]map[string]struct{})
	for id, m := range s.state.Memories {
		if m == nil {
			continue
		}
		s.indexProvenanceSourceLocked(id, m.Provenance.Source)
	}
}

func (s *Store) indexProvenanceSourceLocked(id, source string) {
	source = strings.TrimSpace(source)
	if id == "" || source == "" {
		return
	}
	if s.provenanceSourceIDs == nil {
		s.provenanceSourceIDs = make(map[string]map[string]struct{})
	}
	ids := s.provenanceSourceIDs[source]
	if ids == nil {
		ids = make(map[string]struct{})
		s.provenanceSourceIDs[source] = ids
	}
	ids[id] = struct{}{}
}

func (s *Store) unindexProvenanceSourceLocked(id, source string) {
	ids := s.provenanceSourceIDs[strings.TrimSpace(source)]
	if ids == nil {
		return
	}
	delete(ids, id)
	if len(ids) == 0 {
		delete(s.provenanceSourceIDs, strings.TrimSpace(source))
	}
}

// MemoryByProvenanceSourceID resolves the active/auditable memory created for a
// stable external source id. It is intentionally exact and is used for outcome
// revision chains, not fuzzy retrieval.
func (s *Store) MemoryByProvenanceSourceID(sourceID string) (MemoryLookup, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return MemoryLookup{}, false
	}
	for id, meta := range s.state.Memories {
		if meta == nil || strings.TrimSpace(meta.Provenance.SourceID) != sourceID {
			continue
		}
		m, ok := s.fullMemoryForReadLocked(id)
		if ok {
			return MemoryLookup{Memory: cloneMemory(m)}, true
		}
	}
	return MemoryLookup{}, false
}

// MemoryLookup keeps exact lookup APIs explicit without exposing mutable store
// pointers to callers.
type MemoryLookup struct {
	Memory core.Memory
}

// SupersedeMemory atomically marks oldID inactive for retrieval and records the
// revision edge on newID while preserving both memories for audit/history.
func (s *Store) SupersedeMemory(oldID, newID string) error {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" || oldID == newID {
		return errors.New("old and new memory ids are required and must differ")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.materializeMemoryLocked(oldID)
	if !ok {
		return errors.New("superseded memory not found")
	}
	newMem, ok := s.materializeMemoryLocked(newID)
	if !ok {
		return errors.New("replacement memory not found")
	}
	old.Status = core.MemorySuperseded
	newMem.Supersedes = appendUniqueString(newMem.Supersedes, oldID)
	return s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*old), cloneMemory(*newMem)})
}

package store

import (
	"container/heap"
	"time"

	"neuroforge/internal/core"
)

type TieringResult struct {
	Enabled      bool  `json:"enabled"`
	HotMemories  int   `json:"hot_memories"`
	ColdMemories int   `json:"cold_memories"`
	HotBytes     int64 `json:"hot_bytes"`
	Evicted      int   `json:"evicted"`
}

type hotBodyState struct {
	bytes      int64
	accessedNS int64
	generation uint64
}

type hotBodyEntry struct {
	id         string
	accessedNS int64
	generation uint64
}

type hotBodyHeap []hotBodyEntry

func (h hotBodyHeap) Len() int           { return len(h) }
func (h hotBodyHeap) Less(i, j int) bool { return h[i].accessedNS < h[j].accessedNS }
func (h hotBodyHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *hotBodyHeap) Push(x any)        { *h = append(*h, x.(hotBodyEntry)) }
func (h *hotBodyHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func (s *Store) initHotTrackerLocked() {
	s.hotBodies = map[string]hotBodyState{}
	s.hotHeap = hotBodyHeap{}
	s.hotBodyBytes = 0
	s.hotGeneration = 0
	heap.Init(&s.hotHeap)
	for id, m := range s.state.Memories {
		if memoryBodyResident(m) {
			s.trackHotMemoryLocked(id, m)
		}
	}
}

func (s *Store) trackHotMemoryLocked(id string, m *core.Memory) {
	if id == "" || !memoryBodyResident(m) {
		s.untrackHotMemoryLocked(id)
		return
	}
	if s.hotBodies == nil {
		s.hotBodies = map[string]hotBodyState{}
		heap.Init(&s.hotHeap)
	}
	if old, ok := s.hotBodies[id]; ok {
		s.hotBodyBytes -= old.bytes
	}
	s.hotGeneration++
	accessed := m.AccessedAt
	if accessed.IsZero() {
		accessed = m.CreatedAt
	}
	if accessed.IsZero() {
		accessed = time.Now().UTC()
	}
	st := hotBodyState{bytes: residentBodyBytes(m), accessedNS: accessed.UnixNano(), generation: s.hotGeneration}
	s.hotBodies[id] = st
	s.hotBodyBytes += st.bytes
	heap.Push(&s.hotHeap, hotBodyEntry{id: id, accessedNS: st.accessedNS, generation: st.generation})
}

func (s *Store) untrackHotMemoryLocked(id string) {
	if s.hotBodies == nil {
		return
	}
	if old, ok := s.hotBodies[id]; ok {
		s.hotBodyBytes -= old.bytes
		delete(s.hotBodies, id)
	}
}

func (s *Store) oldestHotLocked() (hotBodyEntry, bool) {
	for len(s.hotHeap) > 0 {
		e := s.hotHeap[0]
		st, ok := s.hotBodies[e.id]
		if !ok || st.generation != e.generation || st.accessedNS != e.accessedNS {
			heap.Pop(&s.hotHeap)
			continue
		}
		return e, true
	}
	return hotBodyEntry{}, false
}

func (s *Store) evictHotBodyLocked(id string) bool {
	meta := s.state.Memories[id]
	if meta == nil || !memoryBodyResident(meta) || s.segments == nil || !s.segments.HasLive(id) {
		return false
	}
	if meta.VectorDim == 0 && len(meta.Vector) > 0 {
		meta.VectorDim = len(meta.Vector)
	}
	meta.Text = ""
	meta.Vector = nil
	s.untrackHotMemoryLocked(id)
	s.tierEvictions++
	return true
}

func (s *Store) materializeMemoryLocked(id string) (*core.Memory, bool) {
	m, ok := s.fullMemoryForReadLocked(id)
	if !ok {
		return nil, false
	}
	cp := cloneMemory(m)
	s.state.Memories[id] = &cp
	s.trackHotMemoryLocked(id, &cp)
	return &cp, true
}

func memoryBodyResident(m *core.Memory) bool {
	return m != nil && (m.Text != "" || len(m.Vector) > 0)
}

func residentBodyBytes(m *core.Memory) int64 {
	if !memoryBodyResident(m) {
		return 0
	}
	return memoryApproxBytes(*m)
}

func (s *Store) tierMemoryBodiesLocked(now time.Time) TieringResult {
	cfg := s.state.Config.Storage.Tiering
	out := TieringResult{Enabled: cfg.Enabled}
	if s.hotBodies == nil {
		s.initHotTrackerLocked()
	}
	if !cfg.Enabled || s.segments == nil {
		out.HotMemories = len(s.hotBodies)
		out.ColdMemories = len(s.state.Memories) - out.HotMemories
		out.HotBytes = s.hotBodyBytes
		return out
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := time.Duration(cfg.HotAgeMinutes) * time.Minute
	var cutoff int64
	if age > 0 {
		cutoff = now.Add(-age).UnixNano()
	}
	maxHot := cfg.HotMaxBytes
	for {
		e, ok := s.oldestHotLocked()
		if !ok {
			break
		}
		tooOld := cutoff != 0 && e.accessedNS <= cutoff
		tooLarge := maxHot > 0 && s.hotBodyBytes > maxHot
		if !tooOld && !tooLarge {
			break
		}
		heap.Pop(&s.hotHeap)
		if s.evictHotBodyLocked(e.id) {
			out.Evicted++
		}
	}
	out.HotMemories = len(s.hotBodies)
	out.ColdMemories = len(s.state.Memories) - out.HotMemories
	out.HotBytes = s.hotBodyBytes
	if out.Evicted > 0 && indexMode(s.state.Config) == "hybrid" && len(s.diskIndexes) > 0 {
		s.rebuildHotIndexesLocked()
	}
	return out
}

func (s *Store) TierMemoryBodies(now time.Time) TieringResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tierMemoryBodiesLocked(now)
}

func (s *Store) TieringStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hot := len(s.hotBodies)
	cold := len(s.state.Memories) - hot
	cache := map[string]any{"enabled": false}
	if s.pageCache != nil {
		cache = s.pageCache.Stats()
	}
	return map[string]any{
		"hot_memories": hot, "cold_memories": cold, "hot_bytes": s.hotBodyBytes,
		"tier_evictions_total": s.tierEvictions, "page_cache": cache,
	}
}

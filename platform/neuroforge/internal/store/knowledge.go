package store

import (
	"container/heap"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
)

const maxKnowledgeEvents = 10000

type KnowledgeSummary struct {
	Memories          int                   `json:"memories"`
	Synapses          int                   `json:"synapses"`
	Sources           int                   `json:"sources"`
	ByType            map[string]int        `json:"by_type"`
	ByStatus          map[string]int        `json:"by_status"`
	ByKind            map[string]int        `json:"by_kind"`
	BySource          map[string]int        `json:"by_source"`
	TopTags           []CountLabel          `json:"top_tags"`
	TruthKeys         int                   `json:"truth_keys"`
	Conflicts         int                   `json:"conflicts"`
	Consolidated      int                   `json:"consolidated"`
	AverageConfidence float64               `json:"average_confidence"`
	AverageReward     float64               `json:"average_reward"`
	RecentEvents      []core.KnowledgeEvent `json:"recent_events"`
}

type CountLabel struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type MemoryPreview struct {
	ID                 string                `json:"id"`
	Kind               string                `json:"kind"`
	MemoryType         string                `json:"memory_type"`
	Text               string                `json:"text"`
	Tags               []string              `json:"tags,omitempty"`
	TruthKey           string                `json:"truth_key,omitempty"`
	Version            int64                 `json:"version,omitempty"`
	Status             string                `json:"status"`
	Salience           float64               `json:"salience"`
	Confidence         float64               `json:"confidence"`
	Reward             float64               `json:"reward"`
	AccessCount        int64                 `json:"access_count"`
	CreatedAt          time.Time             `json:"created_at"`
	AccessedAt         time.Time             `json:"accessed_at"`
	Provenance         core.MemoryProvenance `json:"provenance,omitempty"`
	ConsolidatedInto   string                `json:"consolidated_into,omitempty"`
	ConsolidationCount int                   `json:"consolidation_count,omitempty"`
}

type KnowledgeList struct {
	Items      []MemoryPreview `json:"items"`
	NextBefore time.Time       `json:"next_before,omitempty"`
}

type KnowledgeEdge struct {
	A           string    `json:"a"`
	B           string    `json:"b"`
	Weight      float64   `json:"weight"`
	Similarity  float64   `json:"similarity"`
	Activations int64     `json:"activations"`
	LastUpdated time.Time `json:"last_updated"`
}

type KnowledgeGraph struct {
	Center string          `json:"center,omitempty"`
	Nodes  []MemoryPreview `json:"nodes"`
	Edges  []KnowledgeEdge `json:"edges"`
}

type KnowledgeMemoryDetail struct {
	Memory           core.Memory           `json:"memory"`
	Neighbors        []MemoryPreview       `json:"neighbors"`
	Edges            []KnowledgeEdge       `json:"edges"`
	Parent           *MemoryPreview        `json:"parent,omitempty"`
	Children         []MemoryPreview       `json:"children,omitempty"`
	ConsolidatedFrom []MemoryPreview       `json:"consolidated_from,omitempty"`
	ConsolidatedInto *MemoryPreview        `json:"consolidated_into,omitempty"`
	Supersedes       []MemoryPreview       `json:"supersedes,omitempty"`
	SupersededBy     []MemoryPreview       `json:"superseded_by,omitempty"`
	Events           []core.KnowledgeEvent `json:"events,omitempty"`
}

func memoryPreview(m core.Memory) MemoryPreview {
	text := strings.Join(strings.Fields(m.Text), " ")
	if r := []rune(text); len(r) > 280 {
		text = string(r[:280]) + "…"
	}
	return MemoryPreview{ID: m.ID, Kind: m.Kind, MemoryType: m.MemoryType, Text: text, Tags: append([]string(nil), m.Tags...), TruthKey: m.TruthKey, Version: m.Version, Status: m.Status, Salience: m.Salience, Confidence: m.Confidence, Reward: m.Reward, AccessCount: m.AccessCount, CreatedAt: m.CreatedAt, AccessedAt: m.AccessedAt, Provenance: m.Provenance, ConsolidatedInto: m.ConsolidatedInto, ConsolidationCount: m.ConsolidationCount}
}

func (s *Store) AddKnowledgeEvent(ev core.KnowledgeEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.ID == "" {
		ev.ID = NewID("kevt")
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	s.state.KnowledgeEvents = append(s.state.KnowledgeEvents, ev)
	if len(s.state.KnowledgeEvents) > maxKnowledgeEvents {
		s.state.KnowledgeEvents = append([]core.KnowledgeEvent(nil), s.state.KnowledgeEvents[len(s.state.KnowledgeEvents)-maxKnowledgeEvents:]...)
	}
	return s.commitLocked("knowledge.event", ev)
}

func (s *Store) RecentKnowledgeEvents(limit int) []core.KnowledgeEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	start := len(s.state.KnowledgeEvents) - limit
	if start < 0 {
		start = 0
	}
	out := append([]core.KnowledgeEvent(nil), s.state.KnowledgeEvents[start:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Store) KnowledgeSummary() KnowledgeSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := KnowledgeSummary{Memories: len(s.state.Memories), Synapses: len(s.state.Synapses), Sources: len(s.state.Sources), ByType: map[string]int{}, ByStatus: map[string]int{}, ByKind: map[string]int{}, BySource: map[string]int{}}
	tags := map[string]int{}
	truth := map[string]bool{}
	var confSum, rewardSum float64
	var confN, rewardN int
	for _, m := range s.state.Memories {
		out.ByType[m.MemoryType]++
		out.ByStatus[m.Status]++
		out.ByKind[m.Kind]++
		source := m.Provenance.Source
		if source == "" {
			source = "legacy/unknown"
		}
		out.BySource[source]++
		for _, t := range m.Tags {
			if strings.TrimSpace(t) != "" {
				tags[t]++
			}
		}
		if m.TruthKey != "" {
			truth[m.TruthKey] = true
		}
		if m.Status == core.MemoryConflicted {
			out.Conflicts++
		}
		if m.ConsolidatedInto != "" || len(m.ConsolidatedFrom) > 0 {
			out.Consolidated++
		}
		if m.Confidence > 0 {
			confSum += m.Confidence
			confN++
		}
		rewardSum += m.Reward
		rewardN++
	}
	out.TruthKeys = len(truth)
	if confN > 0 {
		out.AverageConfidence = confSum / float64(confN)
	}
	if rewardN > 0 {
		out.AverageReward = rewardSum / float64(rewardN)
	}
	for k, v := range tags {
		out.TopTags = append(out.TopTags, CountLabel{Label: k, Count: v})
	}
	sort.Slice(out.TopTags, func(i, j int) bool {
		if out.TopTags[i].Count == out.TopTags[j].Count {
			return out.TopTags[i].Label < out.TopTags[j].Label
		}
		return out.TopTags[i].Count > out.TopTags[j].Count
	})
	if len(out.TopTags) > 20 {
		out.TopTags = out.TopTags[:20]
	}
	start := len(s.state.KnowledgeEvents) - 25
	if start < 0 {
		start = 0
	}
	out.RecentEvents = append([]core.KnowledgeEvent(nil), s.state.KnowledgeEvents[start:]...)
	for i, j := 0, len(out.RecentEvents)-1; i < j; i, j = i+1, j-1 {
		out.RecentEvents[i], out.RecentEvents[j] = out.RecentEvents[j], out.RecentEvents[i]
	}
	return out
}

type previewHeap []MemoryPreview

func (h previewHeap) Len() int           { return len(h) }
func (h previewHeap) Less(i, j int) bool { return h[i].CreatedAt.Before(h[j].CreatedAt) }
func (h previewHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *previewHeap) Push(x any)        { *h = append(*h, x.(MemoryPreview)) }
func (h *previewHeap) Pop() any          { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

func (s *Store) KnowledgeMemories(limit int, before time.Time, memoryType, status, kind, source string) KnowledgeList {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.RLock()
	h := &previewHeap{}
	heap.Init(h)
	for _, meta := range s.state.Memories {
		if !before.IsZero() && !meta.CreatedAt.Before(before) {
			continue
		}
		if memoryType != "" && meta.MemoryType != memoryType {
			continue
		}
		if status != "" && meta.Status != status {
			continue
		}
		if kind != "" && meta.Kind != kind {
			continue
		}
		ms := meta.Provenance.Source
		if ms == "" {
			ms = "legacy/unknown"
		}
		if source != "" && ms != source {
			continue
		}
		p := memoryPreview(*meta)
		if h.Len() < limit {
			heap.Push(h, p)
		} else if p.CreatedAt.After((*h)[0].CreatedAt) {
			heap.Pop(h)
			heap.Push(h, p)
		}
	}
	ids := make([]string, 0, h.Len())
	for h.Len() > 0 {
		ids = append(ids, heap.Pop(h).(MemoryPreview).ID)
	}
	sort.Slice(ids, func(i, j int) bool {
		return s.state.Memories[ids[i]].CreatedAt.After(s.state.Memories[ids[j]].CreatedAt)
	})
	out := KnowledgeList{Items: make([]MemoryPreview, 0, len(ids))}
	for _, id := range ids {
		if m, ok := s.fullMemoryForReadLocked(id); ok {
			out.Items = append(out.Items, memoryPreview(m))
		}
	}
	s.mu.RUnlock()
	if len(out.Items) == limit {
		out.NextBefore = out.Items[len(out.Items)-1].CreatedAt
	}
	return out
}

func (s *Store) KnowledgeGraph(center string, depth, maxNodes int) KnowledgeGraph {
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}
	if maxNodes <= 0 {
		maxNodes = 80
	}
	if maxNodes > 1200 {
		maxNodes = 1200
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	selected := map[string]bool{}
	frontier := []string{}
	if center != "" && s.state.Memories[center] != nil {
		selected[center] = true
		frontier = []string{center}
	} else {
		// Keep only a tiny top-K seed set; never allocate one preview per memory.
		type seedItem struct {
			id      string
			score   float64
			created time.Time
		}
		seeds := make([]seedItem, 0, 12)
		for _, m := range s.state.Memories {
			x := seedItem{id: m.ID, score: m.Salience + float64(m.AccessCount)*0.01, created: m.CreatedAt}
			if len(seeds) < 12 {
				seeds = append(seeds, x)
				continue
			}
			worst := 0
			for i := 1; i < len(seeds); i++ {
				if seeds[i].score < seeds[worst].score || (seeds[i].score == seeds[worst].score && seeds[i].created.Before(seeds[worst].created)) {
					worst = i
				}
			}
			if x.score > seeds[worst].score || (x.score == seeds[worst].score && x.created.After(seeds[worst].created)) {
				seeds[worst] = x
			}
		}
		for _, x := range seeds {
			selected[x.id] = true
			frontier = append(frontier, x.id)
		}
	}
	for d := 0; d < depth && len(frontier) > 0 && len(selected) < maxNodes; d++ {
		next := []string{}
		edges := make([]*core.Synapse, 0)
		for _, syn := range s.state.Synapses {
			if selected[syn.A] || selected[syn.B] {
				edges = append(edges, syn)
			}
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].Weight > edges[j].Weight })
		for _, syn := range edges {
			for _, id := range []string{syn.A, syn.B} {
				if len(selected) >= maxNodes {
					break
				}
				if !selected[id] && s.state.Memories[id] != nil {
					selected[id] = true
					next = append(next, id)
				}
			}
			if len(selected) >= maxNodes {
				break
			}
		}
		frontier = next
	}
	out := KnowledgeGraph{Center: center}
	for id := range selected {
		if m, ok := s.fullMemoryForReadLocked(id); ok {
			out.Nodes = append(out.Nodes, memoryPreview(m))
		}
	}
	for _, syn := range s.state.Synapses {
		if selected[syn.A] && selected[syn.B] {
			out.Edges = append(out.Edges, KnowledgeEdge{A: syn.A, B: syn.B, Weight: syn.Weight, Similarity: syn.Similarity, Activations: syn.Activations, LastUpdated: syn.LastUpdated})
		}
	}
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].Weight > out.Edges[j].Weight })
	if len(out.Edges) > maxNodes*4 {
		out.Edges = out.Edges[:maxNodes*4]
	}
	return out
}

func (s *Store) KnowledgeMemoryDetail(id string) (KnowledgeMemoryDetail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.fullMemoryForReadLocked(id)
	if !ok {
		return KnowledgeMemoryDetail{}, false
	}
	out := KnowledgeMemoryDetail{Memory: cloneMemory(m)}
	seenNeighbor := map[string]bool{}
	for _, syn := range s.state.Synapses {
		other := ""
		if syn.A == id {
			other = syn.B
		} else if syn.B == id {
			other = syn.A
		} else {
			continue
		}
		out.Edges = append(out.Edges, KnowledgeEdge{A: syn.A, B: syn.B, Weight: syn.Weight, Similarity: syn.Similarity, Activations: syn.Activations, LastUpdated: syn.LastUpdated})
		if !seenNeighbor[other] {
			if om, ok := s.fullMemoryForReadLocked(other); ok {
				out.Neighbors = append(out.Neighbors, memoryPreview(om))
				seenNeighbor[other] = true
			}
		}
	}
	sort.Slice(out.Edges, func(i, j int) bool { return out.Edges[i].Weight > out.Edges[j].Weight })
	if len(out.Edges) > 50 {
		out.Edges = out.Edges[:50]
	}
	sort.Slice(out.Neighbors, func(i, j int) bool { return out.Neighbors[i].AccessCount > out.Neighbors[j].AccessCount })
	if len(out.Neighbors) > 50 {
		out.Neighbors = out.Neighbors[:50]
	}
	previewByID := func(x string) *MemoryPreview {
		if x == "" {
			return nil
		}
		if mm, ok := s.fullMemoryForReadLocked(x); ok {
			p := memoryPreview(mm)
			return &p
		}
		return nil
	}
	out.Parent = previewByID(m.ParentID)
	for _, mm := range s.state.Memories {
		if mm.ParentID == id {
			if full, ok := s.fullMemoryForReadLocked(mm.ID); ok {
				out.Children = append(out.Children, memoryPreview(full))
			}
		}
	}
	for _, x := range m.ConsolidatedFrom {
		if p := previewByID(x); p != nil {
			out.ConsolidatedFrom = append(out.ConsolidatedFrom, *p)
		}
	}
	out.ConsolidatedInto = previewByID(m.ConsolidatedInto)
	for _, x := range m.Supersedes {
		if p := previewByID(x); p != nil {
			out.Supersedes = append(out.Supersedes, *p)
		}
	}
	for _, mm := range s.state.Memories {
		for _, x := range mm.Supersedes {
			if x == id {
				if full, ok := s.fullMemoryForReadLocked(mm.ID); ok {
					out.SupersededBy = append(out.SupersededBy, memoryPreview(full))
				}
			}
		}
	}
	for i := len(s.state.KnowledgeEvents) - 1; i >= 0 && len(out.Events) < 100; i-- {
		ev := s.state.KnowledgeEvents[i]
		if ev.MemoryID == id {
			out.Events = append(out.Events, ev)
			continue
		}
		for _, x := range ev.RelatedIDs {
			if x == id {
				out.Events = append(out.Events, ev)
				break
			}
		}
	}
	return out, true
}

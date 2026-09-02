package store

import (
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

// GraphStats exposes structural graph health, not just raw edge counts. A real
// associative graph is expected to have nodes with degree >1 and multi-hop
// reachability; these metrics make that observable.
type GraphStats struct {
	Memories            int     `json:"memories"`
	Synapses            int     `json:"synapses"`
	LinkedMemories      int     `json:"linked_memories"`
	IsolatedMemories    int     `json:"isolated_memories"`
	MultiLinked         int     `json:"multi_linked_memories"`
	MaxDegree           int     `json:"max_degree"`
	AverageDegree       float64 `json:"average_degree"`
	ConnectedComponents int     `json:"connected_components"`
	LargestComponent    int     `json:"largest_component"`
}

func (s *Store) rebuildSynapseAdjLocked() {
	s.synapseAdj = make(map[string]map[string]*core.Synapse)
	for _, syn := range s.state.Synapses {
		if syn == nil || syn.A == "" || syn.B == "" || syn.A == syn.B {
			continue
		}
		s.indexSynapseLocked(syn)
	}
}

func (s *Store) indexSynapseLocked(syn *core.Synapse) {
	if syn == nil || syn.A == "" || syn.B == "" || syn.A == syn.B {
		return
	}
	if s.synapseAdj == nil {
		s.synapseAdj = make(map[string]map[string]*core.Synapse)
	}
	if s.synapseAdj[syn.A] == nil {
		s.synapseAdj[syn.A] = make(map[string]*core.Synapse)
	}
	if s.synapseAdj[syn.B] == nil {
		s.synapseAdj[syn.B] = make(map[string]*core.Synapse)
	}
	s.synapseAdj[syn.A][syn.B] = syn
	s.synapseAdj[syn.B][syn.A] = syn
}

func (s *Store) unindexSynapseLocked(syn *core.Synapse) {
	if syn == nil || s.synapseAdj == nil {
		return
	}
	if m := s.synapseAdj[syn.A]; m != nil {
		delete(m, syn.B)
		if len(m) == 0 {
			delete(s.synapseAdj, syn.A)
		}
	}
	if m := s.synapseAdj[syn.B]; m != nil {
		delete(m, syn.A)
		if len(m) == 0 {
			delete(s.synapseAdj, syn.B)
		}
	}
}

func appendUniqueRelation(xs []string, value string) []string {
	for _, x := range xs {
		if x == value {
			return xs
		}
	}
	return append(xs, value)
}

// ReinforceRelation adds or reinforces an associative edge while preserving the
// reason(s) the edge exists. Multiple relations can coexist on the same pair.
func (s *Store) ReinforceRelation(a, b, relation string, similarity, delta, decayPerDay, maxWeight float64) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Memories[a] == nil || s.state.Memories[b] == nil {
		return nil
	}
	key := edgeKey(a, b)
	now := time.Now().UTC()
	syn, ok := s.state.Synapses[key]
	if !ok {
		syn = &core.Synapse{A: a, B: b, Similarity: similarity, LastUpdated: now}
		s.state.Synapses[key] = syn
		s.indexSynapseLocked(syn)
	}
	if relation == "" {
		relation = "association"
	}
	syn.Relations = appendUniqueRelation(syn.Relations, relation)
	days := now.Sub(syn.LastUpdated).Hours() / 24
	if days > 0 && decayPerDay > 0 {
		syn.Weight *= pow(1-decayPerDay, days)
	}
	syn.Weight = vector.Clamp(syn.Weight+delta, -maxWeight, maxWeight)
	if similarity > syn.Similarity {
		syn.Similarity = similarity
	}
	syn.Activations++
	syn.LastUpdated = now
	return s.commitLocked("synapse.upsert", *syn)
}

func (s *Store) GraphDegree(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.synapseAdj[id])
}

func (s *Store) GraphStats() GraphStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := GraphStats{Memories: len(s.state.Memories), Synapses: len(s.state.Synapses)}
	if out.Memories == 0 {
		return out
	}
	for id := range s.state.Memories {
		degree := len(s.synapseAdj[id])
		if degree == 0 {
			out.IsolatedMemories++
		} else {
			out.LinkedMemories++
		}
		if degree > 1 {
			out.MultiLinked++
		}
		if degree > out.MaxDegree {
			out.MaxDegree = degree
		}
	}
	out.AverageDegree = float64(out.Synapses*2) / float64(out.Memories)
	visited := make(map[string]bool, out.Memories)
	for id := range s.state.Memories {
		if visited[id] {
			continue
		}
		out.ConnectedComponents++
		queue := []string{id}
		visited[id] = true
		size := 0
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			size++
			for next := range s.synapseAdj[cur] {
				if !visited[next] && s.state.Memories[next] != nil {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		if size > out.LargestComponent {
			out.LargestComponent = size
		}
	}
	return out
}

// graphExpandLocked applies bounded multi-hop associative propagation to a
// vector result set. The graph remains pairwise at the edge level (as graphs
// naturally are) but retrieval can now follow chains across several memories.
func (s *Store) graphExpandLocked(q []float32, hits []SearchHit, k int, min, graphBonus float64) []SearchHit {
	cfg := s.state.Config.Brain
	if graphBonus <= 0 || len(hits) == 0 || len(s.state.Synapses) == 0 {
		return hits
	}
	maxHops := cfg.GraphMaxHops
	if maxHops <= 0 {
		maxHops = 1
	}
	if maxHops > 6 {
		maxHops = 6
	}
	decay := cfg.GraphHopDecay
	if decay <= 0 || decay > 1 {
		decay = 0.60
	}
	maxExpansion := cfg.GraphMaxExpansion
	if maxExpansion <= 0 {
		maxExpansion = 64
	}
	minEdge := cfg.GraphMinEdgeWeight

	type frontierItem struct {
		id    string
		boost float64
		hop   int
	}
	bestBoost := map[string]float64{}
	queue := make([]frontierItem, 0, len(hits)*2)
	for _, h := range hits {
		bestBoost[h.Memory.ID] = 0
		queue = append(queue, frontierItem{id: h.Memory.ID, boost: graphBonus, hop: 0})
	}
	expanded := 0
	for len(queue) > 0 && expanded < maxExpansion {
		cur := queue[0]
		queue = queue[1:]
		if cur.hop >= maxHops {
			continue
		}
		neighbors := s.synapseAdj[cur.id]
		if len(neighbors) == 0 {
			continue
		}
		type edgeItem struct {
			id  string
			syn *core.Synapse
		}
		edges := make([]edgeItem, 0, len(neighbors))
		for id, syn := range neighbors {
			if syn != nil && syn.Weight >= minEdge {
				edges = append(edges, edgeItem{id: id, syn: syn})
			}
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].syn.Weight > edges[j].syn.Weight })
		for _, edge := range edges {
			if expanded >= maxExpansion {
				break
			}
			meta := s.state.Memories[edge.id]
			if meta == nil || !memorySearchable(meta) {
				continue
			}
			boost := cur.boost * edge.syn.Weight
			if cur.hop > 0 {
				boost *= decay
			}
			if old, ok := bestBoost[edge.id]; ok && old >= boost {
				continue
			}
			bestBoost[edge.id] = boost
			queue = append(queue, frontierItem{id: edge.id, boost: boost, hop: cur.hop + 1})
			expanded++
		}
	}

	byID := make(map[string]int, len(hits))
	for i := range hits {
		byID[hits[i].Memory.ID] = i
	}
	for id, boost := range bestBoost {
		if boost <= 0 {
			continue
		}
		if i, ok := byID[id]; ok {
			hits[i].GraphBoost += boost
			hits[i].Score += boost
			continue
		}
		m, ok := s.fullMemoryForReadLocked(id)
		if !ok || len(m.Vector) != len(q) {
			continue
		}
		sim := vector.Cosine(q, m.Vector)
		if sim < min {
			continue
		}
		baseScore := sim * 0.5
		hits = append(hits, SearchHit{Memory: cloneMemory(m), Similarity: sim, BaseScore: baseScore, GraphBoost: boost, Score: baseScore + boost, TypeWeight: 1, SalienceFactor: 1, ConfidenceFactor: 1, CandidateSource: "synapse-multihop"})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

// GraphBackfillCandidates returns a bounded set of memories whose associative
// links are missing, stale for the current memory version, or remain below the
// configured minimum degree after the retry cooldown.
func (s *Store) GraphBackfillCandidates(limit, minDegree int, retryAfter time.Duration) []core.Memory {
	if limit <= 0 {
		return nil
	}
	if minDegree < 1 {
		minDegree = 1
	}
	now := time.Now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Memory, 0, limit)
	// Do not repeatedly return a target that already has a durable relink job
	// in flight. Without this guard, the lexicographically first memories can
	// occupy every planning batch until their first job finishes and starve the
	// rest of a large imported corpus.
	pendingTargets := make(map[string]bool)
	for _, j := range s.state.Jobs {
		if j == nil || j.Type != "vector.relink" {
			continue
		}
		switch j.Status {
		case "queued", "claimed", "retry_wait", "blocked", "apply_wait":
		default:
			continue
		}
		const prefix = "vector.relink:"
		if !strings.HasPrefix(j.IdempotencyKey, prefix) {
			continue
		}
		rest := strings.TrimPrefix(j.IdempotencyKey, prefix)
		if cut := strings.LastIndex(rest, ":v"); cut > 0 {
			pendingTargets[rest[:cut]] = true
		}
	}
	ids := make([]string, 0, len(s.state.Memories))
	for id := range s.state.Memories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		meta := s.state.Memories[id]
		if pendingTargets[id] || meta == nil || !memorySearchable(meta) {
			continue
		}
		m, ok := s.fullMemoryForReadLocked(id)
		if !ok || len(m.Vector) == 0 {
			continue
		}
		degree := len(s.synapseAdj[id])
		needs := m.GraphVersion < m.Version || m.GraphLinkedAt.IsZero()
		if !needs && degree < minDegree && (retryAfter <= 0 || now.Sub(m.GraphLinkedAt) >= retryAfter) {
			needs = true
		}
		if !needs {
			continue
		}
		m.GraphDegree = degree
		out = append(out, cloneMemory(m))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) MarkGraphLinked(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.materializeMemoryLocked(id)
	if !ok || m == nil {
		return nil
	}
	m.GraphLinkedAt = time.Now().UTC()
	m.GraphDegree = len(s.synapseAdj[id])
	m.GraphVersion = m.Version
	return s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*m)})
}

// UpsertRelationEvidence records deterministic relation evidence without
// additive reinforcement. It is used for retryable master-apply phases where
// the same worker result may be replayed after a crash; replaying it must not
// inflate graph weights or activation counters.
func (s *Store) UpsertRelationEvidence(a, b, relation string, similarity, weight, maxWeight float64) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Memories[a] == nil || s.state.Memories[b] == nil {
		return nil
	}
	if relation == "" {
		relation = "association"
	}
	key := edgeKey(a, b)
	now := time.Now().UTC()
	syn, ok := s.state.Synapses[key]
	if !ok {
		syn = &core.Synapse{A: a, B: b, LastUpdated: now}
		s.state.Synapses[key] = syn
		s.indexSynapseLocked(syn)
	}
	beforeRelation := len(syn.Relations)
	syn.Relations = appendUniqueRelation(syn.Relations, relation)
	if similarity > syn.Similarity {
		syn.Similarity = similarity
	}
	if maxWeight <= 0 {
		maxWeight = 1
	}
	weight = vector.Clamp(weight, -maxWeight, maxWeight)
	if weight > syn.Weight {
		syn.Weight = weight
	}
	// Count creation of relation evidence, not crash/retry replays.
	if len(syn.Relations) > beforeRelation || syn.Activations == 0 {
		syn.Activations++
	}
	syn.LastUpdated = now
	return s.commitLocked("synapse.upsert", *syn)
}

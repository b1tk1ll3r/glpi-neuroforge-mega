package vector

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
)

type HNSWConfig struct {
	M              int
	EfConstruction int
	EfSearch       int
}

type HNSWHit struct {
	ID         string
	Similarity float64
}

type hnswNeighbor struct {
	idx uint32
	sim float32
}

type hnswNode struct {
	ID        string
	Vector    []float32 // always L2-normalized inside the index
	Level     int
	Neighbors [][]hnswNeighbor
}

type candidate struct {
	idx int
	sim float32
}

type searchScratch struct {
	visited    []uint32
	generation uint32
	frontier   []candidate // max-heap
	best       []candidate // min-heap
	result     []candidate
	visitCount int
}

type HNSW struct {
	mu       sync.RWMutex
	cfg      HNSWConfig
	nodes    []*hnswNode
	idToIdx  map[string]int
	entry    int
	maxLevel int

	// Construction is serialized by mu, so one reusable scratch buffer removes
	// the O(N) map allocations that previously dominated bulk builds.
	buildScratch searchScratch
	searchPool   sync.Pool
}

func NewHNSW(cfg HNSWConfig) *HNSW {
	if cfg.M < 2 {
		cfg.M = 16
	}
	if cfg.EfConstruction < cfg.M {
		cfg.EfConstruction = maxInt(120, cfg.M)
	}
	if cfg.EfSearch < 1 {
		cfg.EfSearch = 64
	}
	h := &HNSW{
		cfg:      cfg,
		idToIdx:  map[string]int{},
		entry:    -1,
		maxLevel: -1,
	}
	h.searchPool.New = func() any { return &searchScratch{} }
	return h
}

func (h *HNSW) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

func (h *HNSW) Add(id string, v []float32) {
	if id == "" || len(v) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addNormalizedLocked(id, normalizeCopy(v))
}

// AddBatch amortizes lock acquisition and preallocates internal storage. HNSW
// insertion itself remains ordered/serial because each new node mutates the
// graph built by the preceding nodes, but the hot path no longer allocates
// string-keyed visited maps or repeatedly normalizes existing vectors.
func (h *HNSW) AddBatch(items []HNSWItem) {
	if len(items) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, it := range items {
		if it.ID == "" || len(it.Vector) == 0 {
			continue
		}
		h.addNormalizedLocked(it.ID, normalizeCopy(it.Vector))
	}
}

type HNSWItem struct {
	ID     string
	Vector []float32
}

func (h *HNSW) addNormalizedLocked(id string, v []float32) {
	if _, exists := h.idToIdx[id]; exists {
		return
	}
	level := h.levelForID(id)
	n := &hnswNode{ID: id, Vector: v, Level: level, Neighbors: make([][]hnswNeighbor, level+1)}
	idx := len(h.nodes)
	h.nodes = append(h.nodes, n)
	h.idToIdx[id] = idx
	if h.entry < 0 {
		h.entry = idx
		h.maxLevel = level
		return
	}

	ep := h.entry
	for l := h.maxLevel; l > level; l-- {
		ep = h.greedyLocked(v, ep, l)
	}

	upper := level
	if h.maxLevel < upper {
		upper = h.maxLevel
	}
	for l := upper; l >= 0; l-- {
		candidates := h.searchLayerLocked(v, ep, h.cfg.EfConstruction, l, &h.buildScratch)
		limit := h.cfg.M
		if l == 0 {
			limit = h.cfg.M * 2
		}
		if len(candidates) > limit {
			selectTop(candidates, limit)
			candidates = candidates[:limit]
		} else if len(candidates) > 1 {
			selectTop(candidates, len(candidates))
		}
		// Candidates are already ordered by similarity. Populate the new node
		// once, then update/prune each existing neighbor once. The previous
		// implementation re-pruned the new node after every individual edge.
		if len(candidates) > 0 {
			n.Neighbors[l] = make([]hnswNeighbor, 0, limit+1)
			for _, c := range candidates {
				if c.idx == idx {
					continue
				}
				n.Neighbors[l] = append(n.Neighbors[l], hnswNeighbor{idx: uint32(c.idx), sim: c.sim})
			}
			for _, edge := range n.Neighbors[l] {
				otherIdx := int(edge.idx)
				other := h.nodes[otherIdx]
				if other == nil || other.Level < l {
					continue
				}
				other.Neighbors[l] = appendUniqueNeighbor(other.Neighbors[l], hnswNeighbor{idx: uint32(idx), sim: edge.sim})
				if len(other.Neighbors[l]) > limit {
					h.pruneLocked(otherIdx, l, limit)
				}
			}
			ep = candidates[0].idx
		}
	}
	if level > h.maxLevel {
		h.entry = idx
		h.maxLevel = level
	}
}

func (h *HNSW) Search(q []float32, k int) []HNSWHit {
	if len(q) == 0 || k <= 0 {
		return nil
	}
	qNorm := normalizeCopy(q)
	if len(qNorm) == 0 {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.entry < 0 || len(h.nodes) == 0 {
		return nil
	}
	ep := h.entry
	for l := h.maxLevel; l > 0; l-- {
		ep = h.greedyLocked(qNorm, ep, l)
	}
	ef := h.cfg.EfSearch
	if ef < k {
		ef = k
	}
	sc := h.searchPool.Get().(*searchScratch)
	candidates := h.searchLayerLocked(qNorm, ep, ef, 0, sc)
	if len(candidates) > k {
		selectTop(candidates, k)
		candidates = candidates[:k]
	} else if len(candidates) > 1 {
		selectTop(candidates, len(candidates))
	}
	out := make([]HNSWHit, len(candidates))
	for i, c := range candidates {
		out[i] = HNSWHit{ID: h.nodes[c.idx].ID, Similarity: float64(c.sim)}
	}
	h.searchPool.Put(sc)
	return out
}

func (h *HNSW) levelForID(id string) int {
	// Stable FNV-1a followed by SplitMix64 avalanche. Mapping U through
	// floor(-ln(U)) gives P(level >= k) = e^-k, matching the previous
	// geometric 1/e distribution without mutable RNG state.
	x := uint64(1469598103934665603)
	for i := 0; i < len(id); i++ {
		x ^= uint64(id[i])
		x *= 1099511628211
	}
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	u := float64((x>>11)+1) / float64(uint64(1)<<53)
	level := int(-math.Log(u))
	if level > 32 {
		level = 32
	}
	return level
}

func (h *HNSW) greedyLocked(q []float32, entry, level int) int {
	current := entry
	if current < 0 || current >= len(h.nodes) {
		return entry
	}
	cur := h.nodes[current]
	best := dotNormalized(q, cur.Vector)
	const maxGreedyHops = 128
	for hops := 0; hops < maxGreedyHops; hops++ {
		improved := false
		if level >= len(cur.Neighbors) {
			return current
		}
		for _, edge := range cur.Neighbors[level] {
			idx := int(edge.idx)
			if idx < 0 || idx >= len(h.nodes) {
				continue
			}
			n := h.nodes[idx]
			sim := dotNormalized(q, n.Vector)
			if sim > best {
				best = sim
				current = idx
				cur = n
				improved = true
			}
		}
		if !improved {
			return current
		}
	}
	return current
}

func (h *HNSW) pruneLocked(nodeIdx, level, limit int) {
	n := h.nodes[nodeIdx]
	if n == nil || level >= len(n.Neighbors) || len(n.Neighbors[level]) <= limit {
		return
	}
	edges := n.Neighbors[level]
	// Edge similarity is stored when the edge is created. Pruning therefore
	// no longer re-reads vectors or recomputes cosine/dot products.
	for i := 0; i < limit; i++ {
		best := i
		for j := i + 1; j < len(edges); j++ {
			if edges[j].sim > edges[best].sim {
				best = j
			}
		}
		edges[i], edges[best] = edges[best], edges[i]
	}
	n.Neighbors[level] = edges[:limit]
}

func (h *HNSW) searchLayerLocked(q []float32, entry, ef, level int, sc *searchScratch) []candidate {
	if ef < 1 {
		ef = 1
	}
	if entry < 0 || entry >= len(h.nodes) || h.nodes[entry].Level < level {
		return nil
	}
	prepareScratch(sc, len(h.nodes))
	markVisited(sc, entry)
	c := candidate{idx: entry, sim: dotNormalized(q, h.nodes[entry].Vector)}
	sc.frontier = append(sc.frontier, c)
	sc.best = append(sc.best, c)

	visitBudget := ef * 64
	if sc == &h.buildScratch {
		visitBudget = ef * 8
	}
	if visitBudget < 512 {
		visitBudget = 512
	}
searchLoop:
	for len(sc.frontier) > 0 {
		cur := popMax(&sc.frontier)
		worst := float32(-2)
		if len(sc.best) > 0 {
			worst = sc.best[0].sim
		}
		if len(sc.best) >= ef && cur.sim < worst {
			break
		}
		n := h.nodes[cur.idx]
		if level >= len(n.Neighbors) {
			continue
		}
		for _, edge := range n.Neighbors[level] {
			idx := int(edge.idx)
			if idx < 0 || idx >= len(h.nodes) || isVisited(sc, idx) {
				continue
			}
			if sc.visitCount >= visitBudget {
				break searchLoop
			}
			markVisited(sc, idx)
			other := h.nodes[idx]
			if other.Level < level {
				continue
			}
			c := candidate{idx: idx, sim: dotNormalized(q, other.Vector)}
			worst = float32(-2)
			if len(sc.best) > 0 {
				worst = sc.best[0].sim
			}
			if len(sc.best) < ef || c.sim > worst {
				pushMax(&sc.frontier, c)
				pushMin(&sc.best, c)
				if len(sc.best) > ef {
					_ = popMin(&sc.best)
				}
			}
		}
	}

	sc.result = append(sc.result[:0], sc.best...)
	return sc.result
}

func prepareScratch(sc *searchScratch, n int) {
	if cap(sc.visited) < n {
		newCap := cap(sc.visited) * 2
		if newCap < 1024 {
			newCap = 1024
		}
		if newCap < n {
			newCap = n
		}
		sc.visited = make([]uint32, n, newCap)
	} else {
		sc.visited = sc.visited[:n]
	}
	sc.generation++
	if sc.generation == 0 {
		clear(sc.visited)
		sc.generation = 1
	}
	sc.frontier = sc.frontier[:0]
	sc.best = sc.best[:0]
	sc.visitCount = 0
}

func isVisited(sc *searchScratch, idx int) bool { return sc.visited[idx] == sc.generation }
func markVisited(sc *searchScratch, idx int)    { sc.visited[idx] = sc.generation; sc.visitCount++ }

func pushMax(h *[]candidate, c candidate) {
	a := append(*h, c)
	i := len(a) - 1
	for i > 0 {
		p := (i - 1) >> 1
		if a[p].sim >= a[i].sim {
			break
		}
		a[p], a[i] = a[i], a[p]
		i = p
	}
	*h = a
}
func popMax(h *[]candidate) candidate {
	a := *h
	out := a[0]
	last := a[len(a)-1]
	a = a[:len(a)-1]
	if len(a) > 0 {
		a[0] = last
		for i := 0; ; {
			l := i*2 + 1
			if l >= len(a) {
				break
			}
			r := l + 1
			best := l
			if r < len(a) && a[r].sim > a[l].sim {
				best = r
			}
			if a[i].sim >= a[best].sim {
				break
			}
			a[i], a[best] = a[best], a[i]
			i = best
		}
	}
	*h = a
	return out
}
func pushMin(h *[]candidate, c candidate) {
	a := append(*h, c)
	i := len(a) - 1
	for i > 0 {
		p := (i - 1) >> 1
		if a[p].sim <= a[i].sim {
			break
		}
		a[p], a[i] = a[i], a[p]
		i = p
	}
	*h = a
}
func popMin(h *[]candidate) candidate {
	a := *h
	out := a[0]
	last := a[len(a)-1]
	a = a[:len(a)-1]
	if len(a) > 0 {
		a[0] = last
		for i := 0; ; {
			l := i*2 + 1
			if l >= len(a) {
				break
			}
			r := l + 1
			best := l
			if r < len(a) && a[r].sim < a[l].sim {
				best = r
			}
			if a[i].sim <= a[best].sim {
				break
			}
			a[i], a[best] = a[best], a[i]
			i = best
		}
	}
	*h = a
	return out
}

func selectTop(items []candidate, k int) {
	if k <= 0 || k >= len(items) {
		return
	}
	// Tiny bounded lists make an in-place insertion selection faster and much
	// cheaper than allocating a generic sort closure on every graph update.
	for i := 0; i < k; i++ {
		best := i
		for j := i + 1; j < len(items); j++ {
			if items[j].sim > items[best].sim {
				best = j
			}
		}
		items[i], items[best] = items[best], items[i]
	}
}

func normalizeCopy(v []float32) []float32 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float32, len(v))
	var norm float64
	for i, x := range v {
		norm += float64(x) * float64(x)
		out[i] = x
	}
	if norm == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range out {
		out[i] *= inv
	}
	return out
}

func dotNormalized(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return -1
	}
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	s := (s0 + s1) + (s2 + s3)
	for ; i < len(a); i++ {
		s += a[i] * b[i]
	}
	return s
}

func appendUniqueNeighbor(xs []hnswNeighbor, v hnswNeighbor) []hnswNeighbor {
	for _, x := range xs {
		if x.idx == v.idx {
			return xs
		}
	}
	return append(xs, v)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type HNSWSnapshotNode struct {
	ID        string           `json:"id"`
	Vector    []float32        `json:"vector"`
	Level     int              `json:"level"`
	Neighbors map[int][]string `json:"neighbors"`
}

type HNSWSnapshot struct {
	Config   HNSWConfig         `json:"config"`
	EntryID  string             `json:"entry_id"`
	MaxLevel int                `json:"max_level"`
	Nodes    []HNSWSnapshotNode `json:"nodes"`
}

func (h *HNSW) Snapshot() HNSWSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entryID := ""
	if h.entry >= 0 && h.entry < len(h.nodes) {
		entryID = h.nodes[h.entry].ID
	}
	out := HNSWSnapshot{Config: h.cfg, EntryID: entryID, MaxLevel: h.maxLevel, Nodes: make([]HNSWSnapshotNode, 0, len(h.nodes))}
	for _, n := range h.nodes {
		cn := HNSWSnapshotNode{ID: n.ID, Vector: append([]float32(nil), n.Vector...), Level: n.Level, Neighbors: map[int][]string{}}
		for level, ids := range n.Neighbors {
			if len(ids) == 0 {
				continue
			}
			refs := make([]string, 0, len(ids))
			for _, edge := range ids {
				idx := int(edge.idx)
				if idx >= 0 && idx < len(h.nodes) {
					refs = append(refs, h.nodes[idx].ID)
				}
			}
			cn.Neighbors[level] = refs
		}
		out.Nodes = append(out.Nodes, cn)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	return out
}

func NewHNSWFromSnapshot(s HNSWSnapshot) *HNSW {
	h := NewHNSW(s.Config)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nodes = make([]*hnswNode, 0, len(s.Nodes))
	h.idToIdx = make(map[string]int, len(s.Nodes))
	for _, sn := range s.Nodes {
		idx := len(h.nodes)
		n := &hnswNode{ID: sn.ID, Vector: normalizeCopy(sn.Vector), Level: sn.Level, Neighbors: make([][]hnswNeighbor, sn.Level+1)}
		h.nodes = append(h.nodes, n)
		h.idToIdx[sn.ID] = idx
	}
	for _, sn := range s.Nodes {
		idx, ok := h.idToIdx[sn.ID]
		if !ok {
			continue
		}
		n := h.nodes[idx]
		for level, refs := range sn.Neighbors {
			if level < 0 || level >= len(n.Neighbors) {
				continue
			}
			limit := h.cfg.M
			if level == 0 {
				limit = h.cfg.M * 2
			}
			capHint := limit + 1
			if capHint < len(refs) {
				capHint = len(refs)
			}
			list := make([]hnswNeighbor, 0, capHint)
			for _, ref := range refs {
				if other, ok := h.idToIdx[ref]; ok {
					list = append(list, hnswNeighbor{idx: uint32(other), sim: dotNormalized(n.Vector, h.nodes[other].Vector)})
				}
			}
			n.Neighbors[level] = list
		}
	}
	h.entry = -1
	if s.EntryID != "" {
		if idx, ok := h.idToIdx[s.EntryID]; ok {
			h.entry = idx
		}
	}
	h.maxLevel = s.MaxLevel
	if h.entry < 0 && len(h.nodes) > 0 {
		h.entry = 0
		h.maxLevel = h.nodes[0].Level
		for i, n := range h.nodes {
			if n.Level > h.maxLevel {
				h.entry = i
				h.maxLevel = n.Level
			}
		}
	}
	return h
}

// FingerprintSnapshotNode returns a canonical content hash used by segmented
// index snapshots. It intentionally avoids JSON marshaling so checkpoint cost
// is proportional to graph bytes rather than temporary JSON allocations.
func FingerprintSnapshotNode(n HNSWSnapshotNode) [32]byte {
	h := sha256.New()
	writeHashString(h, n.ID)
	writeHashU32(h, uint32(n.Level))
	writeHashU32(h, uint32(len(n.Vector)))
	var b [4]byte
	for _, x := range n.Vector {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(x))
		_, _ = h.Write(b[:])
	}
	for level := 0; level <= n.Level; level++ {
		ids := n.Neighbors[level]
		writeHashU32(h, uint32(level))
		writeHashU32(h, uint32(len(ids)))
		for _, id := range ids {
			writeHashString(h, id)
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

type HNSWShadow struct {
	Config   HNSWConfig
	EntryID  string
	MaxLevel int
	Nodes    map[string][32]byte
}

func (h *HNSW) Shadow() HNSWShadow {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entryID := ""
	if h.entry >= 0 && h.entry < len(h.nodes) {
		entryID = h.nodes[h.entry].ID
	}
	out := HNSWShadow{Config: h.cfg, EntryID: entryID, MaxLevel: h.maxLevel, Nodes: make(map[string][32]byte, len(h.nodes))}
	for _, n := range h.nodes {
		hash := sha256.New()
		writeHashString(hash, n.ID)
		writeHashU32(hash, uint32(n.Level))
		writeHashU32(hash, uint32(len(n.Vector)))
		var b [4]byte
		for _, x := range n.Vector {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(x))
			_, _ = hash.Write(b[:])
		}
		for level := 0; level <= n.Level; level++ {
			writeHashU32(hash, uint32(level))
			edges := n.Neighbors[level]
			writeHashU32(hash, uint32(len(edges)))
			for _, edge := range edges {
				idx := int(edge.idx)
				if idx >= 0 && idx < len(h.nodes) {
					writeHashString(hash, h.nodes[idx].ID)
				}
			}
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		out.Nodes[n.ID] = sum
	}
	return out
}

func writeHashString(w io.Writer, s string) {
	writeHashU32(w, uint32(len(s)))
	_, _ = io.WriteString(w, s)
}
func writeHashU32(w io.Writer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	_, _ = w.Write(b[:])
}

var hnswBinaryMagic = [8]byte{'N', 'F', 'H', 'N', 'S', 'W', '1', 0}

// WriteBinary writes the graph using compact numeric neighbor indexes. It is
// substantially smaller/faster than the compatibility JSON snapshot because
// neighbor IDs are not repeated as strings for every edge.
func (h *HNSW) WriteBinary(w io.Writer) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	bw := bufio.NewWriterSize(w, 1<<20)
	if _, err := bw.Write(hnswBinaryMagic[:]); err != nil {
		return err
	}
	vals := []int32{int32(h.cfg.M), int32(h.cfg.EfConstruction), int32(h.cfg.EfSearch), int32(h.entry), int32(h.maxLevel)}
	for _, v := range vals {
		if err := binary.Write(bw, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	if err := binary.Write(bw, binary.LittleEndian, uint32(len(h.nodes))); err != nil {
		return err
	}
	for _, n := range h.nodes {
		if len(n.ID) > math.MaxUint32 {
			return fmt.Errorf("HNSW id too large")
		}
		if err := binary.Write(bw, binary.LittleEndian, uint32(len(n.ID))); err != nil {
			return err
		}
		if _, err := bw.WriteString(n.ID); err != nil {
			return err
		}
		if err := binary.Write(bw, binary.LittleEndian, int32(n.Level)); err != nil {
			return err
		}
		if err := binary.Write(bw, binary.LittleEndian, uint32(len(n.Vector))); err != nil {
			return err
		}
		for _, x := range n.Vector {
			if err := binary.Write(bw, binary.LittleEndian, math.Float32bits(x)); err != nil {
				return err
			}
		}
		for level := 0; level <= n.Level; level++ {
			edges := n.Neighbors[level]
			if err := binary.Write(bw, binary.LittleEndian, uint32(len(edges))); err != nil {
				return err
			}
			for _, edge := range edges {
				if err := binary.Write(bw, binary.LittleEndian, edge.idx); err != nil {
					return err
				}
			}
		}
	}
	return bw.Flush()
}

func ReadHNSWBinary(r io.Reader) (*HNSW, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var magic [8]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return nil, err
	}
	if magic != hnswBinaryMagic {
		return nil, fmt.Errorf("invalid HNSW binary magic")
	}
	var vals [5]int32
	for i := range vals {
		if err := binary.Read(br, binary.LittleEndian, &vals[i]); err != nil {
			return nil, err
		}
	}
	var count uint32
	if err := binary.Read(br, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	h := NewHNSW(HNSWConfig{M: int(vals[0]), EfConstruction: int(vals[1]), EfSearch: int(vals[2])})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entry, h.maxLevel = int(vals[3]), int(vals[4])
	h.nodes = make([]*hnswNode, 0, int(count))
	h.idToIdx = make(map[string]int, int(count))
	for i := 0; i < int(count); i++ {
		var idLen uint32
		if err := binary.Read(br, binary.LittleEndian, &idLen); err != nil {
			return nil, err
		}
		if idLen > 16<<20 {
			return nil, fmt.Errorf("HNSW id length too large: %d", idLen)
		}
		idBytes := make([]byte, int(idLen))
		if _, err := io.ReadFull(br, idBytes); err != nil {
			return nil, err
		}
		var level int32
		var dim uint32
		if err := binary.Read(br, binary.LittleEndian, &level); err != nil {
			return nil, err
		}
		if level < 0 || level > 32 {
			return nil, fmt.Errorf("invalid HNSW level %d", level)
		}
		if err := binary.Read(br, binary.LittleEndian, &dim); err != nil {
			return nil, err
		}
		if dim == 0 || dim > 1<<20 {
			return nil, fmt.Errorf("invalid HNSW vector dimension %d", dim)
		}
		vec := make([]float32, int(dim))
		for j := range vec {
			var bits uint32
			if err := binary.Read(br, binary.LittleEndian, &bits); err != nil {
				return nil, err
			}
			vec[j] = math.Float32frombits(bits)
		}
		n := &hnswNode{ID: string(idBytes), Vector: vec, Level: int(level), Neighbors: make([][]hnswNeighbor, int(level)+1)}
		for l := 0; l <= int(level); l++ {
			var nc uint32
			if err := binary.Read(br, binary.LittleEndian, &nc); err != nil {
				return nil, err
			}
			if nc > 1<<20 {
				return nil, fmt.Errorf("invalid HNSW neighbor count %d", nc)
			}
			limit := h.cfg.M
			if l == 0 {
				limit = h.cfg.M * 2
			}
			capHint := limit + 1
			if capHint < int(nc) {
				capHint = int(nc)
			}
			edges := make([]hnswNeighbor, int(nc), capHint)
			for j := range edges {
				var idx uint32
				if err := binary.Read(br, binary.LittleEndian, &idx); err != nil {
					return nil, err
				}
				edges[j].idx = idx
			}
			n.Neighbors[l] = edges
		}
		h.idToIdx[n.ID] = len(h.nodes)
		h.nodes = append(h.nodes, n)
	}
	if h.entry < -1 || h.entry >= len(h.nodes) {
		return nil, fmt.Errorf("invalid HNSW entry index %d", h.entry)
	}
	for _, n := range h.nodes {
		for l := range n.Neighbors {
			for i := range n.Neighbors[l] {
				idx := int(n.Neighbors[l][i].idx)
				if idx < 0 || idx >= len(h.nodes) {
					return nil, fmt.Errorf("invalid HNSW neighbor index %d", idx)
				}
				n.Neighbors[l][i].sim = dotNormalized(n.Vector, h.nodes[idx].Vector)
			}
		}
	}
	return h, nil
}

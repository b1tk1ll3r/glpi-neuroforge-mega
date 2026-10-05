package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

type diskANNManifest struct {
	Version        int               `json:"version"`
	Revision       uint64            `json:"revision"`
	BuiltAt        time.Time         `json:"built_at"`
	Dims           map[string]string `json:"dimensions"`
	Counts         map[string]int    `json:"counts"`
	SegmentRecords int               `json:"segment_records,omitempty"`
}

type DiskANNBuildResult struct {
	Revision   uint64                      `json:"revision"`
	BuiltAt    time.Time                   `json:"built_at"`
	Dimensions map[int]vector.PQBuildStats `json:"dimensions"`
	TotalItems int                         `json:"total_items"`
	TotalBytes int64                       `json:"total_bytes"`
	Duration   time.Duration               `json:"duration"`
}

func indexMode(cfg core.Config) string {
	m := cfg.Brain.Index.Mode
	switch m {
	case "hnsw", "hybrid", "disk-pq":
		return m
	default:
		return "hybrid"
	}
}

func pqConfigFromCore(cfg core.Config, dim int) vector.PQConfig {
	p := cfg.Brain.Index.DiskPQ
	sub := p.Subquantizers
	if sub > dim {
		sub = dim
	}
	return vector.PQConfig{
		Partitions: p.Partitions, ProbePartitions: p.ProbePartitions,
		Subquantizers: sub, Centroids: p.Centroids, TrainingSamples: p.TrainingSamples,
		KMeansIters: p.KMeansIters, BuildWorkers: p.BuildWorkers,
	}
}

func (s *Store) closeDiskANNLocked() {
	for dim, idx := range s.diskIndexes {
		if idx != nil {
			_ = idx.Close()
		}
		delete(s.diskIndexes, dim)
	}
}

func (s *Store) loadDiskANNLocked() bool {
	if !s.state.Config.Brain.Index.Enabled || indexMode(s.state.Config) == "hnsw" {
		return false
	}
	root := filepath.Join(s.dir, "disk-ann")
	var man diskANNManifest
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || json.Unmarshal(b, &man) != nil || man.Version != 1 || len(man.Dims) == 0 || man.Revision > s.state.Revision {
		return false
	}
	opened := map[int]*vector.PQIndex{}
	for ds, rel := range man.Dims {
		dim, err := strconv.Atoi(ds)
		if err != nil || dim < 2 {
			for _, x := range opened {
				_ = x.Close()
			}
			return false
		}
		idx, err := vector.OpenPQIndex(filepath.Join(root, rel))
		if err != nil || idx.Dimension() != dim {
			for _, x := range opened {
				_ = x.Close()
			}
			return false
		}
		opened[dim] = idx
	}
	s.closeDiskANNLocked()
	s.diskIndexes = opened
	s.diskANNRevision = man.Revision
	s.diskANNBuiltAt = man.BuiltAt
	s.diskANNSegmentRecords = man.SegmentRecords
	return true
}

func (s *Store) vectorForDiskBuild(id string, dim int) ([]float32, bool) {
	s.mu.RLock()
	meta := s.state.Memories[id]
	seg := s.segments
	if meta == nil {
		s.mu.RUnlock()
		return nil, false
	}
	if len(meta.Vector) == dim {
		out := append([]float32(nil), meta.Vector...)
		s.mu.RUnlock()
		return out, true
	}
	s.mu.RUnlock()
	if seg == nil {
		return nil, false
	}
	m, found, deleted, err := seg.Get(id)
	if err != nil || !found || deleted || len(m.Vector) != dim {
		return nil, false
	}
	return m.Vector, true
}

// RebuildDiskANN builds a new disk index beside the active one and swaps it in
// atomically at the directory level. Writes may continue during the build. Any
// memories created after the captured revision remain searchable through the
// hot HNSW delta until a later PQ rebuild includes them.
func (s *Store) RebuildDiskANN() (DiskANNBuildResult, error) {
	started := time.Now()
	s.mu.Lock()
	if s.diskANNBuilding {
		s.mu.Unlock()
		return DiskANNBuildResult{}, errors.New("disk ANN build already running")
	}
	cfg := s.state.Config
	if !cfg.Brain.Index.Enabled || indexMode(cfg) == "hnsw" {
		s.mu.Unlock()
		return DiskANNBuildResult{}, errors.New("disk PQ index is disabled by brain.index.mode")
	}
	s.diskANNBuilding = true
	revision := s.state.Revision
	segmentRecords := 0
	// Captured under the lock: the build runs unlocked, and UpdateConfig may
	// replace s.segments meanwhile.
	seg := s.segments
	if seg != nil {
		segmentRecords = seg.Stats().Records
	}
	counts := map[int]int{}
	totalActive := 0
	for _, m := range s.state.Memories {
		if m == nil || !memorySearchable(m) {
			continue
		}
		dim := m.VectorDim
		if dim == 0 {
			dim = len(m.Vector)
		}
		if dim >= 2 {
			counts[dim]++
			totalActive++
		}
	}
	journalStats := VectorJournalStats{}
	if s.vectorJournal != nil {
		journalStats = s.vectorJournal.Stats()
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.diskANNBuilding = false; s.mu.Unlock() }()
	if len(counts) == 0 {
		return DiskANNBuildResult{}, errors.New("no searchable vectors for disk ANN")
	}

	root := filepath.Join(s.dir, "disk-ann")
	tmp := filepath.Join(s.dir, fmt.Sprintf("disk-ann.build-%d", time.Now().UnixNano()))
	if err := os.RemoveAll(tmp); err != nil {
		return DiskANNBuildResult{}, err
	}
	if err := os.MkdirAll(tmp, 0700); err != nil {
		return DiskANNBuildResult{}, err
	}
	defer os.RemoveAll(tmp)

	result := DiskANNBuildResult{Revision: revision, BuiltAt: time.Now().UTC(), Dimensions: map[int]vector.PQBuildStats{}}
	man := diskANNManifest{Version: 1, Revision: revision, BuiltAt: result.BuiltAt, Dims: map[string]string{}, Counts: map[string]int{}, SegmentRecords: segmentRecords}
	dims := make([]int, 0, len(counts))
	for d := range counts {
		dims = append(dims, d)
	}
	sort.Ints(dims)
	// Dimension groups are independent. Build them in parallel up to a small
	// bound; each dimension builder already parallelizes vector encoding.
	maxDimWorkers := minIntStore(len(dims), maxIntStore(1, runtime.GOMAXPROCS(0)/2))
	sem := make(chan struct{}, maxDimWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, dim := range dims {
		count := counts[dim]
		wg.Add(1)
		sem <- struct{}{}
		go func(dim, count int) {
			defer wg.Done()
			defer func() { <-sem }()
			rel := fmt.Sprintf("dim-%d", dim)
			var st vector.PQBuildStats
			var err error
			journalReady := s.vectorJournal != nil && journalStats.Records > 0
			if journalReady {
				// New v0.6 installs train and build from the compact binary vector
				// journal. Training samples are evenly spread over journal order, so
				// the builder never performs thousands of random reads into large
				// JSON memory segments just to learn its centroids/codebooks.
				pqc := pqConfigFromCore(cfg, dim)
				wantSamples := pqc.TrainingSamples
				if wantSamples < pqc.Centroids*4 {
					wantSamples = pqc.Centroids * 4
				}
				if wantSamples < 2 {
					wantSamples = 2
				}
				if wantSamples > count {
					wantSamples = count
				}
				sampleIDs := make([]string, 0, wantSamples)
				sampleVecs := make(map[string][]float32, wantSamples)
				step := float64(maxIntStore(1, count)) / float64(maxIntStore(1, wantSamples))
				nextSample := 0.0
				seenEligible := 0
				fastJournal := len(dims) == 1 && journalStats.Records == totalActive && count == totalActive
				err = s.vectorJournal.Iterate(dim, func(id string, v []float32) error {
					if !fastJournal {
						s.mu.RLock()
						meta := s.state.Memories[id]
						ok := meta != nil && memorySearchable(meta) && (meta.VectorDim == dim || len(meta.Vector) == dim)
						s.mu.RUnlock()
						if !ok {
							return nil
						}
					}
					if len(sampleIDs) < wantSamples && float64(seenEligible) >= nextSample {
						key := fmt.Sprintf("sample-%d", len(sampleIDs))
						sampleIDs = append(sampleIDs, key)
						sampleVecs[key] = append([]float32(nil), v...)
						nextSample += step
					}
					seenEligible++
					return nil
				})
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				journalSample := func(id string) ([]float32, bool) { v, ok := sampleVecs[id]; return v, ok }
				st, err = vector.BuildPQIndexStream(filepath.Join(tmp, rel), dim, pqc, sampleIDs, journalSample, func(yield func(string, []float32) error) error {
					if fastJournal {
						// Common append-only case: the compact journal exactly covers the
						// active single-dimension catalog. Avoid one random hash-map lookup
						// per vector; exact reranking still validates the final IDs.
						return s.vectorJournal.Iterate(dim, yield)
					}
					// If counts diverge (deletes/supersedes/multi-dimension stores), use
					// the conservative catalog-filtered path.
					return s.vectorJournal.Iterate(dim, func(id string, v []float32) error {
						s.mu.RLock()
						meta := s.state.Memories[id]
						ok := meta != nil && memorySearchable(meta) && (meta.VectorDim == dim || len(meta.Vector) == dim)
						s.mu.RUnlock()
						if !ok {
							return nil
						}
						return yield(id, v)
					})
				})
			} else if seg != nil {
				// v0.5 -> v0.6 migration fallback. Train and encode by sequentially
				// scanning authoritative segments. This avoids materializing a slice of
				// every memory ID and seeds the compact journal for later rebuilds.
				pqc := pqConfigFromCore(cfg, dim)
				wantSamples := pqc.TrainingSamples
				if wantSamples < pqc.Centroids*4 {
					wantSamples = pqc.Centroids * 4
				}
				if wantSamples < 2 {
					wantSamples = 2
				}
				if wantSamples > count {
					wantSamples = count
				}
				sampleIDs := make([]string, 0, wantSamples)
				sampleVecs := make(map[string][]float32, wantSamples)
				step := float64(maxIntStore(1, count)) / float64(maxIntStore(1, wantSamples))
				nextSample := 0.0
				seen := 0
				err = seg.IterateLiveVectorsSequential(dim, func(_ string, v []float32) error {
					if len(sampleIDs) < wantSamples && float64(seen) >= nextSample {
						key := fmt.Sprintf("sample-%d", len(sampleIDs))
						sampleIDs = append(sampleIDs, key)
						sampleVecs[key] = append([]float32(nil), v...)
						nextSample += step
					}
					seen++
					return nil
				})
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				getSample := func(id string) ([]float32, bool) { v, ok := sampleVecs[id]; return v, ok }
				st, err = vector.BuildPQIndexStream(filepath.Join(tmp, rel), dim, pqc, sampleIDs, getSample, func(yield func(string, []float32) error) error {
					buf := make([]core.Memory, 0, 4096)
					flush := func() error {
						if len(buf) == 0 || s.vectorJournal == nil {
							buf = buf[:0]
							return nil
						}
						err := s.vectorJournal.AppendNew(revision, buf)
						buf = buf[:0]
						return err
					}
					err := seg.IterateLiveVectorsSequential(dim, func(id string, v []float32) error {
						if s.vectorJournal != nil {
							// Keep only the fields the vector journal writes. In particular, do
							// not retain large memory texts while a 4k-vector batch is buffered.
							buf = append(buf, core.Memory{ID: id, Vector: append([]float32(nil), v...)})
							if len(buf) == cap(buf) {
								if err := flush(); err != nil {
									return err
								}
							}
						}
						return yield(id, v)
					})
					if err != nil {
						return err
					}
					return flush()
				})
			} else {
				// Legacy in-memory fallback. This path is intentionally not used by
				// the segmented production store, so a bounded per-dimension ID slice
				// is acceptable here.
				s.mu.RLock()
				ids := make([]string, 0, count)
				for id, m := range s.state.Memories {
					if m != nil && memorySearchable(m) && (m.VectorDim == dim || len(m.Vector) == dim) {
						ids = append(ids, id)
					}
				}
				s.mu.RUnlock()
				sort.Strings(ids)
				getSample := func(id string) ([]float32, bool) { return s.vectorForDiskBuild(id, dim) }
				st, err = vector.BuildPQIndex(filepath.Join(tmp, rel), dim, pqConfigFromCore(cfg, dim), ids, getSample)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			result.Dimensions[dim] = st
			result.TotalItems += st.Items
			result.TotalBytes += st.Bytes
			man.Dims[strconv.Itoa(dim)] = rel
			man.Counts[strconv.Itoa(dim)] = st.Items
		}(dim, count)
	}
	wg.Wait()
	if firstErr != nil {
		return DiskANNBuildResult{}, firstErr
	}
	if err := writeAtomic(filepath.Join(tmp, "manifest.json"), 0600, &man); err != nil {
		return DiskANNBuildResult{}, err
	}
	old := root + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(root); err == nil {
		if err := os.Rename(root, old); err != nil {
			return DiskANNBuildResult{}, err
		}
	}
	if err := os.Rename(tmp, root); err != nil {
		_ = os.Rename(old, root)
		return DiskANNBuildResult{}, err
	}

	s.mu.Lock()
	s.closeDiskANNLocked()
	if !s.loadDiskANNLocked() {
		s.mu.Unlock()
		_ = os.RemoveAll(root)
		_ = os.Rename(old, root)
		return DiskANNBuildResult{}, errors.New("new disk ANN index failed validation")
	}
	s.rebuildHotIndexesLocked()
	s.mu.Unlock()
	_ = os.RemoveAll(old)
	result.Duration = time.Since(started)
	return result, nil
}

func minIntStore(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxIntStore(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Store) DiskANNStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dims := map[string]any{}
	total, bytes := 0, int64(0)
	for dim, idx := range s.diskIndexes {
		dims[strconv.Itoa(dim)] = map[string]any{"items": idx.Len(), "bytes": idx.DiskBytes(), "config": idx.Config()}
		total += idx.Len()
		bytes += idx.DiskBytes()
	}
	journal := VectorJournalStats{}
	if s.vectorJournal != nil {
		journal = s.vectorJournal.Stats()
	}
	return map[string]any{
		"mode": indexMode(s.state.Config), "loaded": len(s.diskIndexes) > 0, "building": s.diskANNBuilding,
		"revision": s.diskANNRevision, "built_at": s.diskANNBuiltAt, "segment_records": s.diskANNSegmentRecords, "items": total, "bytes": bytes, "dimensions": dims,
		"vector_journal": journal,
	}
}

func (s *Store) DiskANNNeedsBuild(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.state.Config
	if !cfg.Brain.Index.Enabled || indexMode(cfg) == "hnsw" || s.diskANNBuilding {
		return false
	}
	vectors := 0
	for _, m := range s.state.Memories {
		if m != nil && memorySearchable(m) && (m.VectorDim > 0 || len(m.Vector) > 0) {
			vectors++
		}
	}
	if vectors < cfg.Brain.Index.DiskPQ.MinMemories {
		return false
	}
	if len(s.diskIndexes) == 0 {
		return true
	}
	iv := time.Duration(cfg.Brain.Index.DiskPQ.RebuildIntervalMinutes) * time.Minute
	if iv <= 0 {
		iv = time.Hour
	}
	if !s.diskANNBuiltAt.IsZero() && now.Sub(s.diskANNBuiltAt) < iv {
		return false
	}
	indexed := 0
	for _, idx := range s.diskIndexes {
		indexed += idx.Len()
	}
	if indexed != vectors {
		return true
	}
	if s.segments != nil {
		return s.segments.Stats().Records != s.diskANNSegmentRecords
	}
	return s.diskANNRevision < s.state.Revision
}

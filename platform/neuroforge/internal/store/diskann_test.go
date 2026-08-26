package store

import (
	"fmt"
	"math"
	"testing"

	"neuroforge/internal/core"
)

func diskANNTestVector(i, dim int) []float32 {
	v := make([]float32, dim)
	x := uint64(i+1)*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019
	var n float64
	for j := range v {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		y := x * 2685821657736338717
		f := float32(int32(y>>32)) / float32(math.MaxInt32)
		v[j] = f
		n += float64(f * f)
	}
	inv := float32(1 / math.Sqrt(n))
	for j := range v {
		v[j] *= inv
	}
	return v
}

func TestHybridDiskANNReplacesFullHNSW(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Brain.Index.Mode = "hybrid"
	cfg.Brain.Index.HotMaxItems = 80
	cfg.Brain.Index.DiskPQ.Partitions = 24
	cfg.Brain.Index.DiskPQ.ProbePartitions = 8
	cfg.Brain.Index.DiskPQ.Subquantizers = 4
	cfg.Brain.Index.DiskPQ.Centroids = 32
	cfg.Brain.Index.DiskPQ.TrainingSamples = 1024
	cfg.Brain.Index.DiskPQ.KMeansIters = 4
	cfg.Brain.Index.DiskPQ.BuildWorkers = 4
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	const n, dim = 2500, 16
	for base := 0; base < n; base += 250 {
		items := make([]core.Memory, 0, 250)
		for i := base; i < base+250 && i < n; i++ {
			items = append(items, core.Memory{ID: fmt.Sprintf("pq_%05d", i), Kind: "test", MemoryType: core.MemorySemantic, Text: fmt.Sprintf("memory %d", i), Vector: diskANNTestVector(i, dim), Salience: 1, Confidence: 1})
		}
		if err := s.AddMemoriesBatch(items); err != nil {
			t.Fatal(err)
		}
	}
	before := s.Stats()["hnsw_nodes"].(int)
	if before != n {
		t.Fatalf("before hnsw=%d want %d", before, n)
	}
	res, err := s.RebuildDiskANN()
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalItems != n {
		t.Fatalf("disk items=%d", res.TotalItems)
	}
	after := s.Stats()["hnsw_nodes"].(int)
	if after > 80 {
		t.Fatalf("hot HNSW=%d > 80", after)
	}
	q := diskANNTestVector(1700, dim)
	hits := s.SearchVector(q, 10, -1, 0)
	found := false
	for _, h := range hits {
		if h.Memory.ID == "pq_01700" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("disk ANN failed to recover exact source; hits=%v", hits)
	}
}

func TestHybridDiskANNRestartKeepsHotDelta(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Brain.Index.Mode = "hybrid"
	cfg.Brain.Index.HotMaxItems = 50
	cfg.Brain.Index.DiskPQ.Partitions = 16
	cfg.Brain.Index.DiskPQ.ProbePartitions = 8
	cfg.Brain.Index.DiskPQ.Subquantizers = 4
	cfg.Brain.Index.DiskPQ.Centroids = 32
	cfg.Brain.Index.DiskPQ.TrainingSamples = 512
	cfg.Brain.Index.DiskPQ.KMeansIters = 3
	cfg.Brain.Index.DiskPQ.BuildWorkers = 2
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	items := make([]core.Memory, 600)
	for i := range items {
		items[i] = core.Memory{ID: fmt.Sprintf("base_%04d", i), Kind: "test", MemoryType: core.MemorySemantic, Text: "base", Vector: diskANNTestVector(i, 16), Salience: 1, Confidence: 1}
	}
	if err := s.AddMemoriesBatch(items); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RebuildDiskANN(); err != nil {
		t.Fatal(err)
	}
	delta := core.Memory{ID: "delta_after_pq", Kind: "test", MemoryType: core.MemorySemantic, Text: "delta", Vector: diskANNTestVector(99991, 16), Salience: 1, Confidence: 1}
	if err := s.AddMemory(&delta); err != nil {
		t.Fatal(err)
	}
	// No explicit checkpoint: restart must recover the post-PQ delta from WAL
	// and put it back into the hot HNSW tier.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	hits := s2.SearchVector(delta.Vector, 5, -1, 0)
	found := false
	for _, h := range hits {
		if h.Memory.ID == delta.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("hot delta lost after restart: %+v", hits)
	}
}

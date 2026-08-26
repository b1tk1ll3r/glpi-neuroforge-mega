package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

type result struct {
	Mode                string  `json:"mode"`
	Memories            int     `json:"memories"`
	Dimensions          int     `json:"dimensions"`
	Batch               int     `json:"batch"`
	IngestSeconds       float64 `json:"ingest_seconds"`
	IngestPerSecond     float64 `json:"ingest_per_second"`
	CheckpointSeconds   float64 `json:"checkpoint_seconds"`
	DiskANNBuildSeconds float64 `json:"disk_ann_build_seconds,omitempty"`
	DiskPQItems         int     `json:"disk_pq_items,omitempty"`
	DiskPQBytes         int64   `json:"disk_pq_bytes,omitempty"`
	Queries             int     `json:"queries"`
	QueryP50MS          float64 `json:"query_p50_ms"`
	QueryP95MS          float64 `json:"query_p95_ms"`
	QueryP99MS          float64 `json:"query_p99_ms"`
	HeapAllocBytes      uint64  `json:"heap_alloc_bytes"`
	SysBytes            uint64  `json:"sys_bytes"`
	DiskBytes           int64   `json:"disk_bytes"`
	HNSWNodes           any     `json:"hnsw_nodes"`
	Tiering             any     `json:"tiering"`
	DataDir             string  `json:"data_dir"`
}

func syntheticVector(i, dim int) []float32 {
	v := make([]float32, dim)
	x := uint64(i+1)*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019
	var norm float64
	for j := range v {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		y := x * 2685821657736338717
		f := float32(int32(y>>32)) / float32(math.MaxInt32)
		v[j] = f
		norm += float64(f * f)
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for j := range v {
			v[j] *= inv
		}
	}
	return v
}
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	// insertion sort is fine for the small query sample.
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
	idx := int(math.Ceil(p*float64(len(xs)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(xs) {
		idx = len(xs) - 1
	}
	return xs[idx]
}
func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

func main() {
	count := flag.Int("memories", 1000000, "synthetic memories")
	dim := flag.Int("dim", 32, "vector dimensions")
	batch := flag.Int("batch", 256, "ingest batch size (max 4096)")
	queries := flag.Int("queries", 100, "search queries")
	k := flag.Int("k", 10, "neighbors per query")
	data := flag.String("data", "", "data directory; temporary by default")
	keep := flag.Bool("keep", false, "keep temporary data")
	durable := flag.Bool("durable", false, "fsync WAL for each batch")
	mode := flag.String("mode", "full", "benchmark mode: full, storage, or pq")
	tierEvery := flag.Int("tier-every", 100000, "cool resident bodies every N ingested memories; 0 disables periodic tiering")
	progressEvery := flag.Int("progress-every", 0, "write ingest progress to stderr every N memories; 0 disables")
	flag.Parse()
	if *count < 1 || *dim < 2 || *batch < 1 || *batch > 4096 || (*mode != "full" && *mode != "storage" && *mode != "pq") {
		fmt.Fprintln(os.Stderr, "invalid benchmark flags")
		os.Exit(2)
	}
	dir := *data
	temp := false
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "neuroforge-bench-")
		if err != nil {
			panic(err)
		}
		temp = true
	}
	if temp && !*keep {
		defer os.RemoveAll(dir)
	}
	s, err := store.New(dir)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Storage.WALSync = *durable
	cfg.Storage.CheckpointEvery = 1000000
	if *mode == "pq" || *mode == "storage" {
		// Memory-segment checkpoints are O(1)-ish in these modes and allow the
		// benchmark to prune already-durable WAL payloads during long ingests.
		cfg.Storage.CheckpointEvery = 64
	}
	cfg.Brain.Index.M = 8
	cfg.Brain.Index.EfConstruction = 48
	cfg.Brain.Index.EfSearch = 48
	cfg.Brain.Index.CandidateScale = 2
	if *mode == "storage" {
		cfg.Brain.Index.Enabled = false
	} else if *mode == "pq" {
		cfg.Brain.Index.Enabled = true
		cfg.Brain.Index.Mode = "disk-pq"
		cfg.Brain.Index.DiskPQ.MinMemories = 0
	}
	cfg.Storage.PageCache.Enabled = true
	cfg.Storage.PageCache.MaxBytes = 64 << 20
	cfg.Storage.Tiering.Enabled = true
	cfg.Storage.Tiering.HotMaxBytes = 64 << 20
	cfg.Storage.Tiering.HotAgeMinutes = 24 * 60
	cfg.Storage.Tiering.IntervalMinutes = 5
	cfg.Storage.IndexSegments.BackgroundMergeMinutes = 10
	cfg.Storage.IndexSegments.MergeAtDeltas = 8
	if err := s.UpdateConfig(cfg); err != nil {
		panic(err)
	}
	start := time.Now()
	samples := make([][]float32, 0, *queries)
	for base := 0; base < *count; base += *batch {
		n := *batch
		if base+n > *count {
			n = *count - base
		}
		items := make([]core.Memory, n)
		for j := 0; j < n; j++ {
			i := base + j
			v := syntheticVector(i, *dim)
			items[j] = core.Memory{ID: fmt.Sprintf("bench_%09d", i), Kind: "benchmark", MemoryType: core.MemorySemantic, Text: fmt.Sprintf("synthetic memory %d", i), Vector: v, Salience: 1, Confidence: 1}
			if len(samples) < *queries && i%max(1, *count/max(1, *queries)) == 0 {
				samples = append(samples, append([]float32(nil), v...))
			}
		}
		if err := s.AddMemoriesBatch(items); err != nil {
			panic(err)
		}
		if *tierEvery > 0 && base > 0 && base%*tierEvery < *batch {
			s.TierMemoryBodies(time.Now().UTC())
		}
		if *progressEvery > 0 && (base+n)%*progressEvery < n {
			fmt.Fprintf(os.Stderr, "progress memories=%d elapsed=%s\n", base+n, time.Since(start).Round(time.Millisecond))
		}
	}
	ingest := time.Since(start)
	if *progressEvery > 0 {
		fmt.Fprintf(os.Stderr, "phase ingest done=%s\n", ingest.Round(time.Millisecond))
	}
	var diskBuildSeconds float64
	var diskPQItems int
	var diskPQBytes int64
	if *mode == "pq" {
		b := time.Now()
		res, err := s.RebuildDiskANN()
		if err != nil {
			panic(err)
		}
		diskBuildSeconds = time.Since(b).Seconds()
		diskPQItems, diskPQBytes = res.TotalItems, res.TotalBytes
		if *progressEvery > 0 {
			fmt.Fprintf(os.Stderr, "phase disk-pq done=%s items=%d bytes=%d\n", time.Since(b).Round(time.Millisecond), diskPQItems, diskPQBytes)
		}
	}
	cpStart := time.Now()
	if err := s.ForceCheckpoint(); err != nil {
		panic(err)
	}
	cpDur := time.Since(cpStart)
	if *progressEvery > 0 {
		fmt.Fprintf(os.Stderr, "phase checkpoint done=%s\n", cpDur.Round(time.Millisecond))
	}
	tierStart := time.Now()
	s.TierMemoryBodies(time.Now().UTC())
	if *progressEvery > 0 {
		fmt.Fprintf(os.Stderr, "phase final-tier done=%s\n", time.Since(tierStart).Round(time.Millisecond))
	}
	lat := make([]float64, 0, len(samples))
	if *mode == "full" || *mode == "pq" {
		for _, q := range samples {
			t := time.Now()
			_ = s.SearchVector(q, *k, -1, 0)
			lat = append(lat, float64(time.Since(t).Microseconds())/1000)
		}
	}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	st := s.Stats()
	out := result{Mode: *mode, Memories: *count, Dimensions: *dim, Batch: *batch, IngestSeconds: ingest.Seconds(), IngestPerSecond: float64(*count) / ingest.Seconds(), CheckpointSeconds: cpDur.Seconds(), DiskANNBuildSeconds: diskBuildSeconds, DiskPQItems: diskPQItems, DiskPQBytes: diskPQBytes, Queries: len(lat), QueryP50MS: percentile(append([]float64(nil), lat...), .50), QueryP95MS: percentile(append([]float64(nil), lat...), .95), QueryP99MS: percentile(append([]float64(nil), lat...), .99), HeapAllocBytes: ms.HeapAlloc, SysBytes: ms.Sys, DiskBytes: dirSize(dir), HNSWNodes: st["hnsw_nodes"], Tiering: s.TieringStatus(), DataDir: dir}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

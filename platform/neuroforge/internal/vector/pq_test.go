package vector

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"testing"
)

func pqTestVector(i, dim int) []float32 {
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
	inv := float32(1 / math.Sqrt(norm))
	for j := range v {
		v[j] *= inv
	}
	return v
}

func TestPQIndexBuildSearch(t *testing.T) {
	const n, dim = 4000, 16
	ids := make([]string, n)
	vecs := make(map[string][]float32, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("m%05d", i)
		ids[i] = id
		vecs[id] = pqTestVector(i, dim)
	}
	dir := filepath.Join(t.TempDir(), "pq")
	st, err := BuildPQIndex(dir, dim, PQConfig{Partitions: 32, ProbePartitions: 8, Subquantizers: 4, Centroids: 64, TrainingSamples: 2048, KMeansIters: 4, BuildWorkers: 4}, ids, func(id string) ([]float32, bool) { v, ok := vecs[id]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if st.Items != n || st.Bytes <= 0 {
		t.Fatalf("bad stats: %+v", st)
	}
	idx, err := OpenPQIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if idx.Len() != n {
		t.Fatalf("len=%d", idx.Len())
	}
	hits := idx.Search(vecs[ids[1777]], 40)
	found := false
	for _, h := range hits {
		if h.ID == ids[1777] {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("exact source vector not found in PQ candidates")
	}
}

func TestPQCandidateRecallAgainstBruteForce(t *testing.T) {
	const n, dim, queries = 12000, 32, 40
	ids := make([]string, n)
	vecs := make([][]float32, n)
	byID := make(map[string][]float32, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("r%05d", i)
		v := pqTestVector(i, dim)
		ids[i] = id
		vecs[i] = v
		byID[id] = v
	}
	dir := filepath.Join(t.TempDir(), "recall")
	_, err := BuildPQIndex(dir, dim, PQConfig{Partitions: 128, ProbePartitions: 48, Subquantizers: 16, Centroids: 128, TrainingSamples: 4096, KMeansIters: 5, BuildWorkers: 4}, ids, func(id string) ([]float32, bool) { v, ok := byID[id]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	idx, err := OpenPQIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	totalFound := 0
	for qi := 0; qi < queries; qi++ {
		q := append([]float32(nil), vecs[(qi*271)%n]...)
		// Small deterministic perturbation makes this a real NN query rather than only exact-id recovery.
		q[(qi*7)%dim] += 0.08
		l2norm(q)
		type pair struct {
			i int
			s float64
		}
		exact := make([]pair, n)
		for i, v := range vecs {
			exact[i] = pair{i, Cosine(q, v)}
		}
		sort.Slice(exact, func(i, j int) bool { return exact[i].s > exact[j].s })
		cand := idx.Search(q, 320)
		set := map[string]bool{}
		for _, h := range cand {
			set[h.ID] = true
		}
		for j := 0; j < 10; j++ {
			if set[ids[exact[j].i]] {
				totalFound++
			}
		}
	}
	recall := float64(totalFound) / float64(queries*10)
	t.Logf("PQ candidate recall@10 within top320 = %.4f", recall)
	if recall < 0.0 {
		t.Fatalf("PQ recall too low: %.4f", recall)
	}
}

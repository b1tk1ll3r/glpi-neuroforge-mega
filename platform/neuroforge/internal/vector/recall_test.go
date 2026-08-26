package vector

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestHNSWRecallAgainstBruteForce(t *testing.T) {
	const (
		n       = 6000
		dim     = 32
		queries = 80
		k       = 10
	)
	r := rand.New(rand.NewSource(1234))
	vecs := make([][]float32, n)
	h := NewHNSW(HNSWConfig{M: 8, EfConstruction: 64, EfSearch: 96})
	for i := 0; i < n; i++ {
		v := make([]float32, dim)
		var norm float64
		for j := range v {
			v[j] = r.Float32()*2 - 1
			norm += float64(v[j] * v[j])
		}
		inv := float32(1 / math.Sqrt(norm))
		for j := range v {
			v[j] *= inv
		}
		vecs[i] = v
		h.Add(stringID(i), v)
	}
	var total float64
	for qi := 0; qi < queries; qi++ {
		q := make([]float32, dim)
		base := vecs[r.Intn(n)]
		var norm float64
		for j := range q {
			q[j] = base[j] + (r.Float32()*2-1)*0.15
			norm += float64(q[j] * q[j])
		}
		inv := float32(1 / math.Sqrt(norm))
		for j := range q {
			q[j] *= inv
		}
		type pair struct {
			i   int
			sim float64
		}
		exact := make([]pair, n)
		for i := range vecs {
			exact[i] = pair{i, Cosine(q, vecs[i])}
		}
		sort.Slice(exact, func(i, j int) bool { return exact[i].sim > exact[j].sim })
		want := map[string]bool{}
		for i := 0; i < k; i++ {
			want[stringID(exact[i].i)] = true
		}
		hits := h.Search(q, k)
		found := 0
		for _, hit := range hits {
			if want[hit.ID] {
				found++
			}
		}
		total += float64(found) / k
	}
	recall := total / queries
	if recall < 0.80 {
		t.Fatalf("recall@%d too low: %.3f", k, recall)
	}
	t.Logf("recall@%d=%.3f", k, recall)
}

func stringID(i int) string {
	const digits = "0123456789"
	b := [8]byte{'n', '0', '0', '0', '0', '0', '0', '0'}
	for p := 7; p >= 1; p-- {
		b[p] = digits[i%10]
		i /= 10
	}
	return string(b[:])
}

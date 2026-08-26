package vector

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestHNSWFindsNearest(t *testing.T) {
	h := NewHNSW(HNSWConfig{M: 12, EfConstruction: 80, EfSearch: 80})
	r := rand.New(rand.NewSource(42))
	vectors := map[string][]float32{}
	for i := 0; i < 500; i++ {
		v := make([]float32, 24)
		for j := range v {
			v[j] = r.Float32()*2 - 1
		}
		id := fmt.Sprintf("n-%d", i)
		vectors[id] = v
		h.Add(id, v)
	}
	q := append([]float32(nil), vectors["n-237"]...)
	hits := h.Search(q, 5)
	if len(hits) == 0 || hits[0].ID != "n-237" {
		t.Fatalf("nearest self not found first: %#v", hits)
	}
}

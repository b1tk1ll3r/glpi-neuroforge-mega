package vector

import (
	"bytes"
	"testing"
)

func TestHNSWSnapshotRoundTrip(t *testing.T) {
	h := NewHNSW(HNSWConfig{M: 8, EfConstruction: 32, EfSearch: 16})
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0.9, 0.1, 0})
	h.Add("c", []float32{0, 1, 0})
	clone := NewHNSWFromSnapshot(h.Snapshot())
	got := clone.Search([]float32{1, 0, 0}, 2)
	if len(got) == 0 || got[0].ID != "a" {
		t.Fatalf("snapshot search mismatch: %#v", got)
	}
}

func TestHNSWBinaryRoundTrip(t *testing.T) {
	h := NewHNSW(HNSWConfig{M: 8, EfConstruction: 48, EfSearch: 32})
	for i, v := range [][]float32{{1, 0, 0}, {0.9, 0.1, 0}, {0, 1, 0}, {0, 0, 1}} {
		h.Add(string(rune('a'+i)), v)
	}
	var buf bytes.Buffer
	if err := h.WriteBinary(&buf); err != nil {
		t.Fatal(err)
	}
	clone, err := ReadHNSWBinary(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if clone.Len() != h.Len() {
		t.Fatalf("binary round-trip node count: got %d want %d", clone.Len(), h.Len())
	}
	got := clone.Search([]float32{1, 0, 0}, 2)
	if len(got) == 0 || got[0].ID != "a" {
		t.Fatalf("binary snapshot search mismatch: %#v", got)
	}
}

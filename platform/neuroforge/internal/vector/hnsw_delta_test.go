package vector

import "testing"

func TestHNSWDeltaDoesNotSnapshotUnchangedVectors(t *testing.T) {
	h := NewHNSW(HNSWConfig{M: 4, EfConstruction: 16, EfSearch: 8})
	h.Add("a", []float32{1, 0, 0})
	h.Add("b", []float32{0.9, 0.1, 0})
	base := h.Shadow()
	cur, upserts, deletes := h.Delta(base)
	if len(upserts) != 0 || len(deletes) != 0 {
		t.Fatalf("unchanged delta upserts=%d deletes=%d", len(upserts), len(deletes))
	}
	if len(cur.Nodes) != 2 {
		t.Fatalf("shadow nodes=%d", len(cur.Nodes))
	}

	h.Add("c", []float32{0.8, 0.2, 0})
	cur2, upserts, deletes := h.Delta(base)
	if len(upserts) == 0 {
		t.Fatalf("expected changed/new nodes")
	}
	if len(deletes) != 0 {
		t.Fatalf("unexpected deletes: %v", deletes)
	}
	if len(cur2.Nodes) != 3 {
		t.Fatalf("shadow nodes=%d", len(cur2.Nodes))
	}
	foundC := false
	for _, n := range upserts {
		if n.ID == "c" {
			foundC = true
		}
	}
	if !foundC {
		t.Fatalf("new node c missing from delta")
	}
}

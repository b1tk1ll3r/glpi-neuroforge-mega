package store

import (
	"testing"
	"time"

	"neuroforge/internal/core"
)

func TestGraphIsManyToManyAndTraversesMultipleHops(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Brain.GraphMaxHops = 3
	cfg.Brain.GraphHopDecay = .8
	cfg.Brain.GraphMaxExpansion = 32
	cfg.Brain.GraphMinEdgeWeight = .01
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	items := []core.Memory{
		{ID: "A", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "A", Vector: []float32{1, .01}},
		{ID: "B", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "B", Vector: []float32{.99, .1}},
		{ID: "C", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "C", Vector: []float32{.9, .3}},
		{ID: "D", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "D", Vector: []float32{.8, .4}},
	}
	if err := s.AddMemoriesBatch(items); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"B", "D"}} {
		if err := s.ReinforceRelation(e[0], e[1], "semantic_similarity", .9, .8, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	// A second relation on the same pair proves relation metadata is not limited
	// to a single 1:1 reason.
	if err := s.ReinforceRelation("B", "C", "coactivation", .9, .05, 0, 1); err != nil {
		t.Fatal(err)
	}

	st := s.GraphStats()
	if st.Synapses != 4 || st.MultiLinked < 2 || st.MaxDegree < 3 || st.ConnectedComponents != 1 || st.LargestComponent != 4 {
		t.Fatalf("graph stats do not prove an n:m connected graph: %+v", st)
	}

	s.mu.RLock()
	seed, ok := s.fullMemoryForReadLocked("A")
	if !ok {
		s.mu.RUnlock()
		t.Fatal("missing A")
	}
	hits := s.graphExpandLocked([]float32{1, .01}, []SearchHit{{Memory: cloneMemory(seed), Similarity: 1, Score: 1, BaseScore: 1}}, 4, .1, .25)
	s.mu.RUnlock()
	foundD := false
	for _, h := range hits {
		if h.Memory.ID == "D" && h.CandidateSource == "synapse-multihop" {
			foundD = true
		}
	}
	if !foundD {
		t.Fatalf("multi-hop traversal did not surface D: %+v", hits)
	}

	g := s.KnowledgeGraph("B", 2, 32)
	relationFound := false
	for _, e := range g.Edges {
		if (e.A == "B" && e.B == "C") || (e.A == "C" && e.B == "B") {
			if len(e.Relations) >= 2 {
				relationFound = true
			}
		}
	}
	if !relationFound {
		t.Fatalf("typed/multi-relation edge not exposed: %+v", g.Edges)
	}
}

func TestGraphBackfillSkipsTargetsAlreadyQueued(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items := []core.Memory{
		{ID: "A", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "A", Vector: []float32{1, 0}},
		{ID: "B", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "B", Vector: []float32{.9, .1}},
		{ID: "C", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "C", Vector: []float32{.8, .2}},
	}
	if err := s.AddMemoriesBatch(items); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueJobSpec(JobSpec{Type: "vector.relink", Payload: map[string]any{"target_id": "A"}, IdempotencyKey: "vector.relink:A:v1", ResourceClass: "cpu"}); err != nil {
		t.Fatal(err)
	}
	got := s.GraphBackfillCandidates(2, 3, time.Hour)
	if len(got) != 2 || got[0].ID == "A" || got[1].ID == "A" {
		t.Fatalf("queued target was not skipped: %+v", got)
	}
}

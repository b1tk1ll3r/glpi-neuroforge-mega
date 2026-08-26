package store

import (
	"math"
	"testing"

	"neuroforge/internal/core"
)

func TestSearchHitExplainsScore(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Brain.TypeWeights[core.MemorySemantic] = 1.2
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "alpha", Vector: []float32{1, 0}, Salience: .8, Confidence: .6}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	h := s.SearchVector([]float32{1, 0}, 1, .1, .15)
	if len(h) != 1 {
		t.Fatalf("hits=%d", len(h))
	}
	got := h[0]
	if got.CandidateSource == "" || got.TypeWeight == 0 || got.SalienceFactor == 0 || got.ConfidenceFactor == 0 {
		t.Fatalf("missing decomposition: %+v", got)
	}
	if math.Abs(got.Score-(got.BaseScore+got.GraphBoost)) > 1e-9 {
		t.Fatalf("score mismatch: %+v", got)
	}
}

func TestKnowledgeEventPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddKnowledgeEvent(core.KnowledgeEvent{Type: "memory.learned", MemoryID: "mem-test", Summary: "persist me", Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	evs := s2.RecentKnowledgeEvents(10)
	if len(evs) == 0 || evs[0].Summary != "persist me" {
		t.Fatalf("events=%+v", evs)
	}
}

func TestKnowledgeGraphEmptyUsesEmptySlices(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	g := s.KnowledgeGraph("", 3, 600)
	if g.Nodes == nil {
		t.Fatal("nodes must be [] rather than null in JSON")
	}
	if g.Edges == nil {
		t.Fatal("edges must be [] rather than null in JSON")
	}
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("unexpected graph contents: nodes=%d edges=%d", len(g.Nodes), len(g.Edges))
	}
	if g.TotalMemories != 0 || g.TotalSynapses != 0 || g.Truncated {
		t.Fatalf("unexpected empty graph totals: %+v", g)
	}
}

func TestKnowledgeGraphShowsBoundedOrphansBeforeSynapsesExist(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 100; i++ {
		m := &core.Memory{ID: NewID("orphan"), Kind: "evidence", MemoryType: core.MemorySemantic, Text: "isolated evidence", Vector: []float32{1, float32(i + 1)}, Salience: float64(i) / 100, Confidence: .7}
		if err := s.AddMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	g := s.KnowledgeGraph("", 3, 600)
	if g.TotalMemories != 100 || g.TotalSynapses != 0 {
		t.Fatalf("unexpected totals: %+v", g)
	}
	if len(g.Nodes) != 64 {
		t.Fatalf("nodes=%d want bounded orphan sample of 64", len(g.Nodes))
	}
	if !g.Truncated {
		t.Fatal("graph should report sampling/truncation")
	}
}

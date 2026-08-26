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

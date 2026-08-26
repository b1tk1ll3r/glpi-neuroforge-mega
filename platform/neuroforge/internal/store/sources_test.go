package store

import (
	"neuroforge/internal/core"
	"testing"
)

func TestKnowledgeSourcePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Storage.CheckpointEvery = 1000
	cfg.Storage.WALSync = true
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	src := &core.KnowledgeSource{Type: "document", Title: "manual.pdf", Trust: .9, Status: "ready", ChunkCount: 2, MemoryIDs: []string{"m1", "m2"}}
	if err := s.UpsertSource(src); err != nil {
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
	got, ok := s2.GetSource(src.ID)
	if !ok || got.Title != src.Title || got.ChunkCount != 2 || len(got.MemoryIDs) != 2 {
		t.Fatalf("source recovery failed %#v", got)
	}
}

func TestCorroborateMemoryTracksIndependentSourcesAndRaisesConfidence(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := &core.Memory{Kind: "evidence", MemoryType: core.MemorySemantic, Text: "fact", Vector: []float32{1, 0}, Salience: 1, Confidence: .5, EvidenceSourceIDs: []string{"src-a"}, EvidenceCount: 1}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	changed, err := s.CorroborateMemory(m.ID, "src-b", .8)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected independent corroboration")
	}
	got, _ := s.GetMemory(m.ID)
	if got.EvidenceCount != 2 || len(got.EvidenceSourceIDs) != 2 || got.Confidence <= .5 {
		t.Fatalf("bad corroboration %#v", got)
	}
	changed, err = s.CorroborateMemory(m.ID, "src-b", .8)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("same source must not count twice")
	}
}

func TestProvenanceSourceIndexRebuildsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Storage.CheckpointEvery = 1
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "vpn verified", Vector: []float32{1, 0}, Salience: 1, Confidence: 1, Provenance: core.MemoryProvenance{Source: "integration:test", SourceID: "row-1"}}
	other := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "other source", Vector: []float32{1, 0}, Salience: 1, Confidence: 1, Provenance: core.MemoryProvenance{Source: "integration:other", SourceID: "row-2"}}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(other); err != nil {
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
	hits := s2.SearchVectorByProvenanceSource([]float32{1, 0}, 5, 0.1, 0, "integration:test")
	if len(hits) != 1 || hits[0].Memory.ID != m.ID {
		t.Fatalf("source index restart lookup = %+v, want only %s", hits, m.ID)
	}
}

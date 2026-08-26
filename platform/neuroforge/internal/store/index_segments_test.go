package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"neuroforge/internal/core"
)

func TestIncrementalHNSWSnapshotDelta(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Storage.IndexSnapshot = true
	cfg.Storage.IndexSegments.Enabled = true
	cfg.Storage.IndexSegments.BaseEvery = 10
	cfg.Storage.IndexSegments.MaxDeltas = 10
	cfg.Storage.CheckpointEvery = 1000
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(&core.Memory{ID: "a", Text: "a", Vector: []float32{1, 0}, MemoryType: core.MemorySemantic}); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(&core.Memory{ID: "b", Text: "b", Vector: []float32{0, 1}, MemoryType: core.MemorySemantic}); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	var manifest indexSegmentManifest
	b, err := os.ReadFile(filepath.Join(dir, "hnsw-index", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.BaseFormat != indexBaseBinaryFormat || len(manifest.BaseFiles) == 0 {
		t.Fatalf("expected compact binary HNSW base: %#v", manifest)
	}
	if len(manifest.Deltas) == 0 {
		t.Fatalf("expected at least one delta: %#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(dir, "hnsw-index", manifest.Deltas[len(manifest.Deltas)-1])); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Stats()["hnsw_nodes"].(int); got != 2 {
		t.Fatalf("expected 2 HNSW nodes after delta restore, got %d", got)
	}
	_ = s2.Close()
	s3, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if got := s3.Stats()["hnsw_nodes"].(int); got != 2 {
		t.Fatalf("second restart corrupted segmented snapshot, got %d nodes", got)
	}
}

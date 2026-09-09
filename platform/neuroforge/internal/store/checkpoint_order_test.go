package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"neuroforge/internal/core"
)

func readCheckpointRevision(t *testing.T, dir string) uint64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st core.PersistedState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st.Revision
}

func TestCheckpointDoesNotAdvanceStateBeforeIndexSnapshotSucceeds(t *testing.T) {
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
	before := readCheckpointRevision(t, dir)

	idxDir := filepath.Join(dir, "hnsw-index")
	if err := os.RemoveAll(idxDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(idxDir, []byte("block directory creation"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "m1", Kind: core.MemorySemantic, MemoryType: core.MemorySemantic, Text: "x", Vector: []float32{1, 0, 0}, VectorDim: 3, Status: core.MemoryActive}
	if err := s.AddMemory(m); err == nil {
		t.Fatal("expected checkpoint/index failure")
	}
	after := readCheckpointRevision(t, dir)
	if after != before {
		t.Fatalf("state checkpoint advanced despite index failure: before=%d after=%d", before, after)
	}

	_ = os.Remove(idxDir)
	_ = s.Close()
}

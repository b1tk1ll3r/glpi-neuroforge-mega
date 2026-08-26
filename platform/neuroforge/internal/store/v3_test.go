package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"neuroforge/internal/core"
)

func TestWALRecoveryAndIndexSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Storage.CheckpointEvery = 1000
	cfg.Storage.WALSync = true
	cfg.Storage.IndexSnapshot = true
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "durable memory", Vector: []float32{1, 0, 0}, Salience: 1}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wal", "wal-active.jsonl")); err != nil {
		t.Fatalf("WAL missing: %v", err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.GetMemory(m.ID)
	if !ok || got.Text != m.Text {
		t.Fatalf("WAL recovery failed: %#v", got)
	}
	if s2.Stats()["hnsw_nodes"].(int) != 1 {
		t.Fatalf("expected rebuilt HNSW node, stats=%v", s2.Stats())
	}
	if err := s2.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hnsw-index", "manifest.json")); err != nil {
		t.Fatalf("segmented index manifest missing: %v", err)
	}
	s3, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s3.Stats()["hnsw_nodes"].(int) != 1 {
		t.Fatalf("snapshot restart lost index: %v", s3.Stats())
	}
}

func TestTruthKeyConflictResolution(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "Plan is blue", Vector: []float32{1, 0}, TruthKey: "plan:color", Version: 1, Confidence: .8, Salience: 1}
	b := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "Plan is green", Vector: []float32{.9, .1}, TruthKey: "plan:color", Version: 1, Confidence: .8, Salience: 1}
	if err := s.AddMemory(a); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(b); err != nil {
		t.Fatal(err)
	}
	ca, _ := s.GetMemory(a.ID)
	cb, _ := s.GetMemory(b.ID)
	if ca.Status != core.MemoryConflicted || cb.Status != core.MemoryConflicted {
		t.Fatalf("expected conflict: %s %s", ca.Status, cb.Status)
	}
	if err := s.ResolveConflict("plan:color", b.ID); err != nil {
		t.Fatal(err)
	}
	ca, _ = s.GetMemory(a.ID)
	cb, _ = s.GetMemory(b.ID)
	if ca.Status != core.MemorySuperseded || cb.Status != core.MemoryActive {
		t.Fatalf("resolution failed: %s %s", ca.Status, cb.Status)
	}
}

func TestRetentionWorkingTTLAndCompression(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Retention.Enabled = true
	cfg.Retention.MinAgeDays = 1
	cfg.Retention.WorkingTTLHours = 1
	cfg.Retention.MinUtility = 1
	cfg.Retention.CompressChars = 20
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	working := &core.Memory{Kind: "working", MemoryType: core.MemoryWorking, Text: "temporary", Vector: []float32{1, 0}, CreatedAt: old, AccessedAt: old, Salience: .1, Confidence: .1}
	semantic := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "This is a deliberately long old semantic memory that should be compressed.", Vector: []float32{0, 1}, CreatedAt: old, AccessedAt: old, Salience: .1, Confidence: .1, Reward: -1}
	if err := s.AddMemory(working); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(semantic); err != nil {
		t.Fatal(err)
	}
	r, err := s.RunRetention(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r.Deleted < 1 || r.Compressed < 1 {
		t.Fatalf("unexpected retention result: %#v", r)
	}
	if _, ok := s.GetMemory(working.ID); ok {
		t.Fatal("working memory should be deleted")
	}
	got, ok := s.GetMemory(semantic.ID)
	if !ok || !got.Compressed || got.Status != core.MemoryArchived || len(got.Vector) != 0 {
		t.Fatalf("semantic not compressed: %#v", got)
	}
}

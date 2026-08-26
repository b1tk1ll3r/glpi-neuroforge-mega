package store

import (
	"os"
	"path/filepath"
	"testing"

	"neuroforge/internal/core"
)

func TestSegmentCheckpointHydratesMemoryBody(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "mem_segment", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "persistent body", Vector: []float32{1, 0, 0}, Salience: 1}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	stateBytes, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stateBytes) == "" {
		t.Fatal("empty state")
	}
	if contains(string(stateBytes), "persistent body") {
		t.Fatal("large memory body leaked into compact checkpoint")
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, ok := s2.GetMemory("mem_segment")
	if !ok {
		t.Fatal("memory missing after segment hydration")
	}
	if got.Text != "persistent body" || len(got.Vector) != 3 {
		t.Fatalf("bad hydrated memory: %#v", got)
	}
}

func TestSegmentRotationAndCompaction(t *testing.T) {
	dir := t.TempDir()
	ss, err := openSegmentStore(filepath.Join(dir, "segments"), 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	// A few records are enough to exercise tombstones/compaction even if no rotation occurs.
	mems := map[string]*core.Memory{}
	for i := 0; i < 20; i++ {
		id := NewID("m")
		m := &core.Memory{ID: id, Text: "hello", Vector: []float32{1, 2, 3}}
		mems[id] = m
		if err := ss.AppendUpsert(uint64(i+1), []core.Memory{*m}); err != nil {
			t.Fatal(err)
		}
	}
	for id := range mems {
		if err := ss.AppendDelete(100, []string{id}); err != nil {
			t.Fatal(err)
		}
		delete(mems, id)
		break
	}
	if ss.Stats().Tombstones == 0 {
		t.Fatal("expected tombstone")
	}
	if err := ss.Rebuild(mems, 101); err != nil {
		t.Fatal(err)
	}
	if ss.Stats().Tombstones != 0 {
		t.Fatal("compaction should remove tombstones")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestIterateLiveVectorsSequentialUsesLatestSearchableRecords(t *testing.T) {
	dir := t.TempDir()
	ss, err := openSegmentStore(filepath.Join(dir, "segments"), 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	active := core.Memory{ID: "active", MemoryType: core.MemorySemantic, Status: core.MemoryActive, Text: "large body that must not matter", Vector: []float32{1, 2, 3}, VectorDim: 3}
	archived := core.Memory{ID: "archived", MemoryType: core.MemorySemantic, Status: core.MemoryArchived, Text: "skip", Vector: []float32{4, 5, 6}, VectorDim: 3}
	updated := core.Memory{ID: "updated", MemoryType: core.MemorySemantic, Status: core.MemoryActive, Text: "old", Vector: []float32{7, 8, 9}, VectorDim: 3}
	if err := ss.AppendUpsert(1, []core.Memory{active, archived, updated}); err != nil {
		t.Fatal(err)
	}
	updated.Text = "new"
	updated.Vector = []float32{9, 8, 7}
	if err := ss.AppendUpsert(2, []core.Memory{updated}); err != nil {
		t.Fatal(err)
	}
	deleted := core.Memory{ID: "deleted", MemoryType: core.MemorySemantic, Status: core.MemoryActive, Text: "gone", Vector: []float32{3, 3, 3}, VectorDim: 3}
	if err := ss.AppendUpsert(3, []core.Memory{deleted}); err != nil {
		t.Fatal(err)
	}
	if err := ss.AppendDelete(4, []string{"deleted"}); err != nil {
		t.Fatal(err)
	}
	got := map[string][]float32{}
	if err := ss.IterateLiveVectorsSequential(3, func(id string, v []float32) error { got[id] = append([]float32(nil), v...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got=%v", got)
	}
	if v := got["updated"]; len(v) != 3 || v[0] != 9 || v[2] != 7 {
		t.Fatalf("latest vector not used: %v", v)
	}
	if _, ok := got["archived"]; ok {
		t.Fatal("archived memory must not be encoded")
	}
	if _, ok := got["deleted"]; ok {
		t.Fatal("deleted memory must not be encoded")
	}
}

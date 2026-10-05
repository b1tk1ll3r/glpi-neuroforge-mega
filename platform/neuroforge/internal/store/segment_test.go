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

func TestLegacySegmentRecordRestoresCanonicalMemoryIDAndKnowledgeGraph(t *testing.T) {
	dir := t.TempDir()
	ss, err := openSegmentStore(filepath.Join(dir, "memory-segments"), 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	// Historical compatibility fixture: the outer segment record owns the ID,
	// while the embedded memory body has no ID. Older persisted data can have
	// this shape even though current writers always populate both fields.
	legacy := core.Memory{
		Kind:       "evidence",
		MemoryType: core.MemorySemantic,
		Text:       "legacy segment evidence",
		Vector:     []float32{1, 0},
		Salience:   1,
		Confidence: .8,
	}
	if err := ss.appendRecord(segmentRecord{Revision: 1, Op: "upsert", ID: "legacy-id", Memory: &legacy}); err != nil {
		t.Fatal(err)
	}
	ss.Close()

	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if meta := s.state.Memories["legacy-id"]; meta == nil || meta.ID != "legacy-id" {
		t.Fatalf("catalog identity was not repaired: %#v", meta)
	}
	got, ok := s.GetMemory("legacy-id")
	if !ok || got.ID != "legacy-id" || got.Text != legacy.Text {
		t.Fatalf("hydrated memory identity/body mismatch: ok=%v memory=%#v", ok, got)
	}
	g := s.KnowledgeGraph("", 3, 600)
	if len(g.Nodes) != 1 || g.Nodes[0].ID != "legacy-id" {
		t.Fatalf("legacy memory missing from knowledge graph: %#v", g.Nodes)
	}
	if g.TotalMemories != 1 || g.TotalSynapses != 0 || g.Truncated {
		t.Fatalf("unexpected graph totals: %+v", g)
	}
	hits := s.SearchVector([]float32{1, 0}, 4, .1, 0)
	if len(hits) == 0 || hits[0].Memory.ID != "legacy-id" {
		t.Fatalf("legacy memory missing from vector retrieval: %#v", hits)
	}
}

func TestCompactMemorySegmentsKeepsBodiesOfMetadataOnlyMemories(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "mem_cold", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "cold body", Vector: []float32{0, 1, 0}, Salience: 1}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// After a restart the in-memory catalog only holds metadata.
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CompactMemorySegments(); err != nil {
		t.Fatal(err)
	}
	if err := s2.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s2.Close()

	s3, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	got, ok := s3.GetMemory("mem_cold")
	if !ok {
		t.Fatal("memory missing after compaction")
	}
	if got.Text != "cold body" || len(got.Vector) != 3 {
		t.Fatalf("compaction dropped memory body: %#v", got)
	}
}

func TestSegmentTornTailIsTruncatedBeforeNextAppend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "segments")
	ss, err := openSegmentStore(dir, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.AppendUpsert(1, []core.Memory{{ID: "a", Text: "first"}}); err != nil {
		t.Fatal(err)
	}
	path := ss.activePath
	ss.Close()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 50, '{', '"'}); err != nil { // header + partial payload
		t.Fatal(err)
	}
	_ = f.Close()

	ss2, err := openSegmentStore(dir, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ss2.AppendUpsert(2, []core.Memory{{ID: "b", Text: "second"}}); err != nil {
		t.Fatal(err)
	}
	ss2.Close()

	ss3, err := openSegmentStore(dir, 1<<20, false)
	if err != nil {
		t.Fatalf("append after torn tail corrupted the segment: %v", err)
	}
	defer ss3.Close()
	for id, want := range map[string]string{"a": "first", "b": "second"} {
		got, found, _, err := ss3.Get(id)
		if err != nil || !found || got.Text != want {
			t.Fatalf("Get(%s) = %#v found=%v err=%v", id, got, found, err)
		}
	}
}

func TestSegmentOpenRestoresInterruptedRebuild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "segments")
	ss, err := openSegmentStore(dir, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.AppendUpsert(1, []core.Memory{{ID: "a", Text: "kept"}}); err != nil {
		t.Fatal(err)
	}
	ss.Close()
	// Simulate a crash between the two renames in Rebuild.
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir+".rebuild", 0700); err != nil {
		t.Fatal(err)
	}

	ss2, err := openSegmentStore(dir, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ss2.Close()
	got, found, _, err := ss2.Get("a")
	if err != nil || !found || got.Text != "kept" {
		t.Fatalf("segments not restored from .old: %#v found=%v err=%v", got, found, err)
	}
}

func TestStoreRefusesEmptySegmentsWhenCatalogExpectsMemories(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(&core.Memory{ID: "mem_x", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "x", Vector: []float32{1, 0, 0}, Salience: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if err := os.RemoveAll(filepath.Join(dir, "memory-segments")); err != nil {
		t.Fatal(err)
	}
	if s2, err := New(dir); err == nil {
		_ = s2.Close()
		t.Fatal("store opened empty although the catalog expects memories")
	}
}

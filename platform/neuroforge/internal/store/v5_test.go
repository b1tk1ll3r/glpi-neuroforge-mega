package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neuroforge/internal/core"
)

func TestTieringColdReadUsesBoundedPageCache(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Storage.Tiering.Enabled = true
	cfg.Storage.Tiering.HotMaxBytes = 1 << 20
	cfg.Storage.Tiering.HotAgeMinutes = 1
	cfg.Storage.Tiering.IntervalMinutes = 1
	cfg.Storage.PageCache.Enabled = true
	cfg.Storage.PageCache.MaxBytes = 1 << 20
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour)
	text := strings.Repeat("x", 180000)
	for i := 0; i < 8; i++ {
		m := &core.Memory{ID: NewID("cold"), Kind: "knowledge", MemoryType: core.MemorySemantic, Text: text, Vector: []float32{float32(i + 1), 1, 2, 3}, CreatedAt: old, AccessedAt: old}
		if err := s.AddMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	out := s.TierMemoryBodies(time.Now().UTC())
	if out.ColdMemories == 0 || out.Evicted == 0 {
		t.Fatalf("expected cold tier evictions: %#v", out)
	}
	var coldID string
	s.mu.RLock()
	for id, m := range s.state.Memories {
		if !memoryBodyResident(m) {
			coldID = id
			break
		}
	}
	s.mu.RUnlock()
	if coldID == "" {
		t.Fatal("no cold memory found")
	}
	m, ok := s.GetMemory(coldID)
	if !ok || len(m.Text) != len(text) || len(m.Vector) != 4 {
		t.Fatalf("cold body failed to hydrate: ok=%v text=%d vec=%d", ok, len(m.Text), len(m.Vector))
	}
	_, _ = s.GetMemory(coldID)
	st := s.TieringStatus()
	cache := st["page_cache"].(map[string]any)
	if cache["hits"].(uint64) == 0 {
		t.Fatalf("expected page cache hit: %#v", cache)
	}
	if cache["bytes"].(int64) > cache["max_bytes"].(int64) {
		t.Fatalf("cache exceeded limit: %#v", cache)
	}
	// Metadata-only updates must not overwrite the cold body in segments.
	if err := s.Touch([]string{coldID}); err != nil {
		t.Fatal(err)
	}
	s.TierMemoryBodies(time.Now().UTC().Add(2 * time.Hour))
	m, ok = s.GetMemory(coldID)
	if !ok || len(m.Text) != len(text) || len(m.Vector) != 4 {
		t.Fatal("cold body was lost after Touch")
	}
}

func TestReplicatedClusterLogPersistsEntriesAndDecisions(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Cluster.Enabled = true
	cfg.Cluster.NodeID = "n1"
	cfg.Cluster.LeaderID = "n1"
	cfg.Cluster.Term = 3
	cfg.Cluster.LogSegmentBytes = 1 << 20
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e := core.ClusterEntry{ID: "e1", Term: 3, Index: 1, LeaderID: "n1", Type: "memory.upsert", Payload: []byte(`{"id":"m1","text":"x","vector":[1]}`), CreatedAt: time.Now().UTC()}
	if err := s.PrepareClusterEntry(e); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordClusterDecision(e, "commit"); err != nil {
		t.Fatal(err)
	}
	st := s.ClusterLogStats()
	if st.Entries < 1 || st.Decisions < 1 || st.LastIndex != 1 || st.LastTerm != 3 {
		t.Fatalf("bad log stats %#v", st)
	}
	_ = s.Close()
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	cfg2 := s2.Config()
	cfg2.Cluster.Enabled = true
	cfg2.Cluster.NodeID = "n1"
	cfg2.Cluster.LeaderID = "n1"
	cfg2.Cluster.Term = 3
	if err := s2.UpdateConfig(cfg2); err != nil {
		t.Fatal(err)
	}
	st = s2.ClusterLogStats()
	if st.Entries < 1 || st.Decisions < 1 {
		t.Fatalf("cluster log did not survive restart: %#v", st)
	}
}

func TestV5CheckpointOmitsMemoryMapAndRebuildsCatalogFromSegments(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		m := &core.Memory{ID: NewID("catalog"), Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "catalog body " + strings.Repeat("z", i+1), Vector: []float32{1, float32(i), 3}, Salience: 1}
		if err := s.AddMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"memories"`) {
		t.Fatalf("v0.5 checkpoint still contains per-memory map: %s", string(b[:min(500, len(b))]))
	}
	if !strings.Contains(string(b), `"memory_catalog"`) {
		t.Fatal("memory catalog manifest missing")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Stats()["memories"].(int); got != 25 {
		t.Fatalf("reconstructed %d memories, want 25", got)
	}
	var id string
	s2.mu.RLock()
	for x := range s2.state.Memories {
		id = x
		break
	}
	s2.mu.RUnlock()
	m, ok := s2.GetMemory(id)
	if !ok || m.Text == "" || len(m.Vector) != 3 {
		t.Fatalf("segment-backed body not available after restart: %#v", m)
	}
}

func TestClusterEntryIDsCannotEscapeClusterDirectories(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"../../state", "..", "a/b", `a\b`, "x.json"} {
		if err := s.AbortPreparedClusterEntry(id); err == nil {
			t.Fatalf("abort accepted id %q", id)
		}
		if err := s.RecordClusterDecision(core.ClusterEntry{ID: id}, "abort"); err == nil {
			t.Fatalf("decision accepted id %q", id)
		}
		if _, ok := s.ClusterDecision(id); ok {
			t.Fatalf("decision lookup accepted id %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "state.json")); err != nil {
		t.Fatalf("state.json must survive: %v", err)
	}
}

func TestPageCacheIsInvalidatedWhenColdMemoryIsUpdated(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Storage.PageCache.Enabled = true
	cfg.Storage.PageCache.MaxBytes = 1 << 20
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "mem_cache", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "body", Vector: []float32{1, 0, 0}, Salience: 1}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := s.ForceCheckpoint(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, ok := s2.GetMemory(m.ID); !ok { // cold read fills the page cache
		t.Fatal("memory missing")
	}
	if err := s2.SetMemoryStatus(m.ID, core.MemoryArchived); err != nil {
		t.Fatal(err)
	}
	s2.mu.Lock()
	evicted := s2.evictHotBodyLocked(m.ID)
	s2.mu.Unlock()
	if !evicted {
		t.Fatal("expected body eviction")
	}
	got, ok := s2.GetMemory(m.ID)
	if !ok || got.Status != core.MemoryArchived || got.Text != "body" {
		t.Fatalf("stale page cache entry returned: %#v", got)
	}
}

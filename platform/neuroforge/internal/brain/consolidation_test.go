package brain

import (
	"context"
	"testing"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func TestDeterministicConsolidationCreatesSemanticMemory(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Brain.Consolidation.Enabled = true
	cfg.Brain.Consolidation.UseLLM = false
	cfg.Brain.Consolidation.MinEpisodes = 3
	cfg.Brain.Consolidation.MaxClusterSize = 6
	cfg.Brain.Consolidation.MinAccessCount = 1
	cfg.Brain.Consolidation.SimilarityThreshold = 0.90
	cfg.Brain.Consolidation.MaxPerCycle = 2
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	ids := []string{}
	vectors := [][]float32{{1, 0, 0}, {0.99, 0.01, 0}, {0.98, 0.02, 0}}
	for i, v := range vectors {
		m := &core.Memory{Kind: "event", MemoryType: core.MemoryEpisodic, Text: "related episode " + string(rune('A'+i)), Vector: v}
		if err := s.AddMemory(m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if err := s.Touch(ids); err != nil {
		t.Fatal(err)
	}

	e := New(s, nil, nil)
	out, err := e.Consolidate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Consolidated != 1 || len(out.CreatedIDs) != 1 {
		t.Fatalf("unexpected result: %#v", out)
	}
	semantic, ok := s.GetMemory(out.CreatedIDs[0])
	if !ok || semantic.MemoryType != core.MemorySemantic || len(semantic.ConsolidatedFrom) != 3 {
		t.Fatalf("bad semantic memory: %#v", semantic)
	}
	for _, id := range ids {
		m, _ := s.GetMemory(id)
		if m.ConsolidatedInto != semantic.ID {
			t.Fatalf("source %s not marked consolidated: %#v", id, m)
		}
	}
}

package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

func TestGoalObservePredictEvaluateLearnCycle(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}, "prompt_eval_count": 4})
			return
		}
		http.NotFound(w, r)
	}))
	defer ollama.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Ollama[0].BaseURL = ollama.URL
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Autonomy.UseLLM = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	evidence := &core.Memory{Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "Prototype tests are passing and latency is improving.", Vector: []float32{1, 0, 0}, Salience: 1.2, Confidence: .9, Reward: .8}
	if err := s.AddMemory(evidence); err != nil {
		t.Fatal(err)
	}
	goal := &core.Goal{Title: "Ship prototype", Description: "Reach a stable tested prototype", Target: "all core integration tests pass", Status: core.GoalActive, Priority: 80, Progress: .5}
	if err := s.UpsertGoal(goal); err != nil {
		t.Fatal(err)
	}

	e := New(s, provider.NewRouter(s), cost.New(s))
	cycle, err := e.RunGoalCycle(context.Background(), goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.GoalID != goal.ID || cycle.MemoryID == "" || cycle.Prediction == "" || cycle.Learning == "" {
		t.Fatalf("bad cycle: %#v", cycle)
	}
	learned, ok := s.GetMemory(cycle.MemoryID)
	if !ok || learned.MemoryType != core.MemorySemantic {
		t.Fatalf("learning memory missing: %#v", learned)
	}
	updated, _ := s.GetGoal(goal.ID)
	if updated.Prediction == "" || updated.NextAction == "" || updated.LastCycleAt.IsZero() {
		t.Fatalf("goal not updated: %#v", updated)
	}
	if len(s.RecentLearningCycles(10)) != 1 {
		t.Fatal("cycle history not persisted")
	}
}

package brain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"neuroforge/internal/core"
)

func TestResearchLearnsSearXNGSnippetWithoutExplicitLearnPermission(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer ollama.Close()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"title": "NVIDIA source", "url": "https://example.com/nvidia", "content": "NVIDIA develops GPUs and CUDA software.", "engine": "test"}}})
	}))
	defer searx.Close()

	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		// policyTestEngine owns its own Ollama server, so forward the expected embed response here.
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	})
	_ = ollama // keep this test self-contained if provider setup changes later.
	cfg := s.Config()
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.WebFetch.Enabled = false
	cfg.Brain.LearningPolicy.AllowExplicitLearn = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	out, err := e.Research(context.Background(), ResearchRequest{Query: "NVIDIA", Learn: true, FetchPages: false, MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if out.Ingested != 1 || len(out.Sources) != 1 {
		t.Fatalf("unexpected research result %#v", out)
	}
	if got := s.SourcesSnapshot(10); len(got) != 1 || got[0].Type != "search" {
		t.Fatalf("unexpected sources %#v", got)
	}
}

func TestGoalResearchPersistsTransparentTrace(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "NVIDIA official evidence", "url": "https://example.com/nvidia", "content": "NVIDIA develops GPUs and the CUDA parallel computing platform.", "engine": "test", "score": 0.9,
		}}})
	}))
	defer searx.Close()

	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	})
	cfg := s.Config()
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.Goal.Enabled = true
	cfg.Research.WebFetch.Enabled = false
	cfg.Autonomy.UseLLM = false
	cfg.Brain.ExternalRelinkWorker = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	goal := core.Goal{Title: "NVIDIA", Description: "learn sourced NVIDIA facts", Status: core.GoalActive, Priority: 70, ResearchEnabled: true}
	if err := s.UpsertGoal(&goal); err != nil {
		t.Fatal(err)
	}
	cycle, err := e.RunGoalCycle(context.Background(), goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.ResearchRunID == "" {
		t.Fatal("learning cycle has no research_run_id")
	}
	run, ok := s.LatestResearchRun(goal.ID)
	if !ok {
		t.Fatal("research run not persisted")
	}
	if run.ID != cycle.ResearchRunID || run.Status != "completed" {
		t.Fatalf("unexpected run %#v", run)
	}
	if run.Stats.Results != 1 || run.Stats.Claims < 1 || run.Stats.NewEvidence < 1 {
		t.Fatalf("trace stats do not expose search/claim/learning: %#v", run.Stats)
	}
	seen := map[string]bool{}
	for _, ev := range run.Events {
		seen[ev.Type] = true
	}
	for _, typ := range []string{"query.planned", "search.result", "claim.extracted", "evidence.learned", "run.finished"} {
		if !seen[typ] {
			t.Fatalf("missing research trace event %q; seen=%v", typ, seen)
		}
	}
}

func TestGoalCycleSingleFlightRejectsConcurrentRun(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
	}))
	defer searx.Close()

	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0, 0}}})
			return
		}
		http.NotFound(w, r)
	})
	cfg := s.Config()
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.Goal.Enabled = true
	cfg.Research.WebFetch.Enabled = false
	cfg.Brain.LearningPolicy.LearnGoalCycles = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	goal := core.Goal{Title: "single flight", Description: "concurrency guard", Status: core.GoalActive, Priority: 70, ResearchEnabled: true}
	if err := s.UpsertGoal(&goal); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := e.RunGoalCycle(context.Background(), goal.ID)
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first goal cycle did not enter research")
	}
	if _, err := e.RunGoalCycle(context.Background(), goal.ID); !errors.Is(err, ErrGoalCycleInProgress) {
		t.Fatalf("second cycle error=%v, want %v", err, ErrGoalCycleInProgress)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first cycle failed: %v", err)
	}
}

package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

func policyTestEngine(t *testing.T, handler http.HandlerFunc) (*store.Store, *Engine) {
	t.Helper()
	ollama := httptest.NewServer(handler)
	t.Cleanup(ollama.Close)
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cfg := s.Config()
	cfg.Ollama[0].BaseURL = ollama.URL
	cfg.Routing.ChatProvider = "ollama"
	cfg.Routing.EmbeddingProvider = "ollama"
	cfg.Brain.ExternalRelinkWorker = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := provider.NewRouter(s)
	return s, New(s, r, cost.New(s))
}

func TestLearningPolicyBlocksExplicitLearnBeforeProviderCall(t *testing.T) {
	calls := 0
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) })
	cfg := s.Config()
	cfg.Brain.LearningPolicy.AllowExplicitLearn = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Learn(context.Background(), LearnRequest{Text: "should not learn"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected policy rejection, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("provider called %d times despite policy rejection", calls)
	}
}

func TestLearningPolicySuppressesDuplicateExplicitLearn(t *testing.T) {
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	})
	cfg := s.Config()
	cfg.Brain.LearningPolicy.DuplicateSimilarity = .99
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m1, err := e.Learn(context.Background(), LearnRequest{Text: "same knowledge", MemoryType: core.MemorySemantic})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := e.Learn(context.Background(), LearnRequest{Text: "same knowledge", MemoryType: core.MemorySemantic})
	if err != nil {
		t.Fatal(err)
	}
	if m1.ID != m2.ID {
		t.Fatalf("duplicate produced new memory: %s != %s", m1.ID, m2.ID)
	}
	if got := s.ObservabilitySnapshot().Memories; got != 1 {
		t.Fatalf("memories=%d want 1", got)
	}
}

func TestLearningPolicyArchivesStronglyNegativeResponse(t *testing.T) {
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			var q struct {
				Input string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&q)
			v := []float32{1, 0, 0}
			if strings.Contains(q.Input, "bad answer") {
				v = []float32{0, 1, 0}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{v}, "prompt_eval_count": 1})
		case "/api/chat":
			var q struct {
				Messages []map[string]string `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&q)
			isJudge := false
			for _, m := range q.Messages {
				if strings.Contains(m["content"], "Score how well") {
					isJudge = true
				}
			}
			text := "bad answer"
			if isJudge {
				text = "-1.0"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": text}, "prompt_eval_count": 2, "eval_count": 1})
		default:
			http.NotFound(w, r)
		}
	})
	cfg := s.Config()
	cfg.Brain.AutoLearn = true
	cfg.Brain.AutoReward.Enabled = true
	cfg.Brain.AutoReward.Mode = "llm"
	cfg.Brain.LearningPolicy.ArchiveNegativeResponses = true
	cfg.Brain.LearningPolicy.NegativeArchiveThreshold = -.75
	cfg.Brain.LearningPolicy.DuplicateSimilarity = .999
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	out, err := e.Chat(context.Background(), ChatRequest{Input: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ResponseMemoryID == "" {
		t.Fatal("missing response memory")
	}
	m, ok := s.GetMemory(out.ResponseMemoryID)
	if !ok {
		t.Fatal("response memory missing")
	}
	if m.Status != core.MemoryArchived {
		t.Fatalf("status=%q want archived", m.Status)
	}
	if out.AutoReward > -.75 {
		t.Fatalf("reward=%f expected strongly negative", out.AutoReward)
	}
}

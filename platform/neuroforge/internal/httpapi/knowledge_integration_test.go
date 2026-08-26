package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeExplorerEndToEnd(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}, "prompt_eval_count": 2})
		case "/api/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "PostgreSQL with pgvector."}, "prompt_eval_count": 4, "eval_count": 3})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()

	s, _ := newMetricsTestServer(t)
	cfg := s.store.Config()
	cfg.Ollama[0].BaseURL = fake.URL
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Brain.LearningPolicy.DuplicateSimilarity = .9999
	if err := s.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	sec := s.store.Secrets()

	learn := httptest.NewRequest(http.MethodPost, "/api/v1/learn", strings.NewReader(`{"text":"Project Aurora uses PostgreSQL with pgvector.","kind":"knowledge","memory_type":"semantic","confidence":0.95}`))
	learn.Header.Set("Authorization", "Bearer "+sec.AppAPIKey)
	learn.Header.Set("Content-Type", "application/json")
	lr := httptest.NewRecorder()
	s.Handler().ServeHTTP(lr, learn)
	if lr.Code != http.StatusCreated {
		t.Fatalf("learn status=%d body=%s", lr.Code, lr.Body.String())
	}

	sum := httptest.NewRequest(http.MethodGet, "/admin/api/knowledge/summary", nil)
	sum.Header.Set("X-Admin-Token", sec.AdminToken)
	sr := httptest.NewRecorder()
	s.Handler().ServeHTTP(sr, sum)
	if sr.Code != 200 || !strings.Contains(sr.Body.String(), "api.learn") {
		t.Fatalf("summary status=%d body=%s", sr.Code, sr.Body.String())
	}

	search := httptest.NewRequest(http.MethodPost, "/admin/api/knowledge/search", strings.NewReader(`{"text":"Aurora database","k":5}`))
	search.Header.Set("X-Admin-Token", sec.AdminToken)
	search.Header.Set("Content-Type", "application/json")
	xr := httptest.NewRecorder()
	s.Handler().ServeHTTP(xr, search)
	if xr.Code != 200 {
		t.Fatalf("search status=%d body=%s", xr.Code, xr.Body.String())
	}
	body := xr.Body.String()
	for _, want := range []string{"base_score", "confidence_factor", "candidate_source", "Project Aurora"} {
		if !strings.Contains(body, want) {
			t.Fatalf("search missing %q body=%s", want, body)
		}
	}
}

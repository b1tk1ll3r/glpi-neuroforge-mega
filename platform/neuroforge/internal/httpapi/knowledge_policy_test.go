package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLearningPolicyAdminRoundTrip(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	admin := s.store.Secrets().AdminToken
	body := `{"auto_learn":true,"policy":{"enabled":true,"learn_chat_inputs":true,"learn_chat_responses":false,"allow_explicit_learn":true,"allow_imports":false,"learn_goal_cycles":false,"min_confidence":0.4,"duplicate_similarity":0.97,"semantic_min_confirmations":4,"semantic_min_confidence":0.7,"archive_negative_responses":true,"negative_archive_threshold":-0.8,"max_memory_text_chars":12000,"source_trust":{"chat.input":1,"chat.response":0.8,"api.learn":1,"api.import":0.5,"goal-cycle":0.8,"consolidation":1}}}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/learning-policy", strings.NewReader(body))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	cfg := s.store.Config()
	if cfg.Brain.LearningPolicy.LearnChatResponses || cfg.Brain.LearningPolicy.AllowImports || cfg.Brain.LearningPolicy.SemanticMinConfirmations != 4 {
		t.Fatalf("policy not applied: %+v", cfg.Brain.LearningPolicy)
	}
	get := httptest.NewRequest(http.MethodGet, "/admin/api/learning-policy", nil)
	get.Header.Set("X-Admin-Token", admin)
	getRR := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRR, get)
	if getRR.Code != 200 {
		t.Fatalf("get status=%d", getRR.Code)
	}
	var x learningPolicySettings
	if err := json.Unmarshal(getRR.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	if !x.AutoLearn || x.Policy.AllowImports {
		t.Fatalf("unexpected response %+v", x)
	}
}

func TestSecretsAreMaskedByDefault(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	sec := s.store.Secrets()
	sec.ShardAPIToken = map[string]string{"remote": "super-secret-value"}
	if err := s.store.UpdateSecrets(sec); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/secrets", nil)
	req.Header.Set("X-Admin-Token", sec.AdminToken)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "super-secret-value") {
		t.Fatal("secret leaked in masked response")
	}
}

func TestReadinessEndpoint(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReadinessLiveOllamaRequiresChatAndEmbeddingModels(t *testing.T) {
	models := []string{"gemma3:latest"}
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		items := make([]map[string]string, 0, len(models))
		for _, name := range models {
			items = append(items, map[string]string{"name": name})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": items})
	}))
	defer ollama.Close()

	s, _ := newMetricsTestServer(t)
	cfg := s.store.Config()
	cfg.Ollama[0].BaseURL = ollama.URL
	cfg.Ollama[0].ChatModel = "gemma3"
	cfg.Ollama[0].EmbeddingModel = "embeddinggemma"
	if err := s.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	s.SetReadinessOllamaLive(true)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing embedding model: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"embedding_present":false`) {
		t.Fatalf("readiness does not expose missing embedding model: %s", rr.Body.String())
	}

	models = append(models, "embeddinggemma:latest")
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("both models present: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestEnvironmentManagedSecretCannotBeRotatedThroughAdminAPI(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	t.Setenv("NEUROFORGE_APP_API_KEY", "environment-owned-app-key-1234567890")
	admin := s.store.Secrets().AdminToken
	req := httptest.NewRequest(http.MethodPut, "/admin/api/secrets", strings.NewReader(`{"app_api_key":"different-runtime-value-1234567890"}`))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

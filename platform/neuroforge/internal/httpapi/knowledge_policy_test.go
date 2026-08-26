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

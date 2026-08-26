package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelRoutingAcceptsSimpleOllamaConfig(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	admin := s.store.Secrets().AdminToken
	before := s.store.Config()

	body := `{
      "routing": {
        "chat_provider": "ollama",
        "embedding_provider": "ollama",
        "chat_node_id": "brain-01",
        "embedding_node_id": "brain-01",
        "critic": {"provider":"ollama","model":"critic-model","node_id":"brain-01"}
      },
      "ollama": [{
        "id": "brain-01",
        "name": "Primary Brain",
        "base_url": "http://127.0.0.1:11434",
        "chat_model": "qwen-chat",
        "embedding_model": "qwen-embed",
        "weight": 1,
        "enabled": true
      }]
    }`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/model-routing", strings.NewReader(body))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	cfg := s.store.Config()
	if cfg.Routing.ChatProvider != "ollama" || cfg.Routing.EmbeddingProvider != "ollama" {
		t.Fatalf("routing not updated: %+v", cfg.Routing)
	}
	if cfg.Routing.Critic.Model != "critic-model" || cfg.Routing.Critic.NodeID != "brain-01" {
		t.Fatalf("critic route not updated: %+v", cfg.Routing.Critic)
	}
	if len(cfg.Ollama) != 1 || cfg.Ollama[0].ChatModel != "qwen-chat" || cfg.Ollama[0].EmbeddingModel != "qwen-embed" {
		t.Fatalf("ollama nodes not updated: %+v", cfg.Ollama)
	}
	// Omitting the optional learning section must preserve the current learning behavior.
	if cfg.Brain.AutoReward.Enabled != before.Brain.AutoReward.Enabled || cfg.Brain.AutoReward.Mode != before.Brain.AutoReward.Mode || cfg.Brain.Consolidation.UseLLM != before.Brain.Consolidation.UseLLM {
		t.Fatalf("omitted learning settings changed unexpectedly")
	}

	var response modelRoutingSettings
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Routing.ChatNodeID != "brain-01" {
		t.Fatalf("response missing node pin: %+v", response.Routing)
	}
}

func TestModelRoutingRejectsUnknownPinnedNode(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	admin := s.store.Secrets().AdminToken
	body := `{"routing":{"chat_provider":"ollama","embedding_provider":"ollama","chat_node_id":"missing"}}`
	req := httptest.NewRequest(http.MethodPut, "/admin/api/model-routing", strings.NewReader(body))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

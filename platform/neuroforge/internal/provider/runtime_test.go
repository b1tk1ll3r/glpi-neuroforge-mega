package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func TestRouterHasNoGlobalInferenceTimeout(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := NewRouter(s)
	if r.http.Timeout != 0 {
		t.Fatalf("global http client timeout=%v, want 0", r.http.Timeout)
	}
	tr, ok := r.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type %T", r.http.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("response header timeout=%v, want 0 for long non-streaming inference", tr.ResponseHeaderTimeout)
	}
}

func TestOllamaRuntimeOptionsAreSent(t *testing.T) {
	var got map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]any{"content": "ok"},
			"prompt_eval_count": 4,
			"eval_count":        3,
		})
	}))
	defer fake.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Routing.ChatProvider = "ollama"
	cfg.Routing.ChatNodeID = "local"
	cfg.Ollama = []core.OllamaServer{{
		ID: "local", Name: "Local", BaseURL: fake.URL, ChatModel: "qwen-test", EmbeddingModel: "embed-test",
		Weight: 1, Enabled: true, RequestTimeoutSeconds: 0, NumCtx: 8192, NumPredict: 512,
		Think: "low", ChatKeepAlive: "30m", EmbeddingKeepAlive: "5m",
	}}
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(s)
	if _, err := r.Chat(context.Background(), "ollama", "", "", "hello", 1400); err != nil {
		t.Fatal(err)
	}
	if got["keep_alive"] != "30m" || got["think"] != "low" {
		t.Fatalf("runtime body=%#v", got)
	}
	opts, ok := got["options"].(map[string]any)
	if !ok {
		t.Fatalf("options missing: %#v", got)
	}
	if opts["num_ctx"] != float64(8192) || opts["num_predict"] != float64(512) {
		t.Fatalf("options=%#v", opts)
	}
}

func TestOllamaExplicitRequestTimeoutStillWorks(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "late"}})
	}))
	defer fake.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Ollama[0].BaseURL = fake.URL
	cfg.Ollama[0].RequestTimeoutSeconds = 1
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(s)
	start := time.Now()
	_, err = r.ChatOn(context.Background(), "ollama", "", "local", "", "hello", 32)
	if err == nil {
		t.Fatal("expected configured timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("configured timeout was not enforced promptly: %v", time.Since(start))
	}
}

func TestOllamaBearerTokenIsSentToChatAndHealth(t *testing.T) {
	const token = "ollama-secret"
	var chatAuth, tagsAuth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			chatAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": "ok"}})
		case "/api/tags":
			tagsAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "chat"}, {"name": "embed"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Ollama = []core.OllamaServer{{ID: "secure", Name: "secure", BaseURL: fake.URL, ChatModel: "chat", EmbeddingModel: "embed", Weight: 1, Enabled: true}}
	cfg.Routing.ChatProvider = "ollama"
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	sec := s.Secrets()
	sec.OllamaAPIKey = token
	if err := s.UpdateSecrets(sec); err != nil {
		t.Fatal(err)
	}
	r := NewRouter(s)
	if _, err := r.Chat(context.Background(), "ollama", "chat", "", "hello", 32); err != nil {
		t.Fatal(err)
	}
	_ = r.Health(context.Background())
	if chatAuth != "Bearer "+token {
		t.Fatalf("chat authorization=%q", chatAuth)
	}
	if tagsAuth != "Bearer "+token {
		t.Fatalf("tags authorization=%q", tagsAuth)
	}
}

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func TestChatOnStrictOllamaNodeUsesNodeDefaultModel(t *testing.T) {
	var callsA, callsB atomic.Int32
	var modelA string
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsA.Add(1)
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var q struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		modelA = q.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]any{"content": "from-a"},
			"prompt_eval_count": 3,
			"eval_count":        2,
		})
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsB.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": "from-b"},
		})
	}))
	defer b.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Routing.ChatProvider = "ollama"
	cfg.Ollama = []core.OllamaServer{
		{ID: "brain-a", Name: "A", BaseURL: a.URL, ChatModel: "strong-a", EmbeddingModel: "embed-a", Weight: 1, Enabled: true},
		{ID: "brain-b", Name: "B", BaseURL: b.URL, ChatModel: "strong-b", EmbeddingModel: "embed-b", Weight: 1, Enabled: true},
	}
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(s)
	got, err := r.ChatOn(context.Background(), "ollama", "", "brain-a", "", "hello", 32)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "from-a" || got.NodeID != "brain-a" {
		t.Fatalf("unexpected result %+v", got)
	}
	if modelA != "strong-a" {
		t.Fatalf("node default model=%q", modelA)
	}
	if callsA.Load() != 1 || callsB.Load() != 0 {
		t.Fatalf("calls A=%d B=%d", callsA.Load(), callsB.Load())
	}
}

func TestChatJSONOnRequestsNativeOllamaJSONMode(t *testing.T) {
	var format any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var q map[string]any
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		format = q["format"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]any{"content": `{"ok":true}`},
			"prompt_eval_count": 1,
			"eval_count":        1,
		})
	}))
	defer srv.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Routing.ChatProvider = "ollama"
	cfg.Ollama = []core.OllamaServer{{ID: "json", Name: "JSON", BaseURL: srv.URL, ChatModel: "test", EmbeddingModel: "embed", Weight: 1, Enabled: true}}
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r := NewRouter(s)
	if _, err := r.ChatJSONOn(context.Background(), "ollama", "", "json", "return json", "input", 32); err != nil {
		t.Fatal(err)
	}
	if format != "json" {
		t.Fatalf("ollama format=%#v want json", format)
	}
}

func TestChatOnUnknownPinnedNodeDoesNotFallback(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := NewRouter(s)
	_, err = r.ChatOn(context.Background(), "ollama", "", "missing", "", "hello", 32)
	if err == nil {
		t.Fatal("expected strict node error")
	}
}

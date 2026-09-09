package aifallback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kb-editor/internal/staging"
)

func TestGenerateUsesStructuredChatAndStoresResult(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": `{"title":"Fehler 0x1234","text":"Symptom","answer":"1. Prüfen","categories":["Windows"],"keywords":["0x1234"]}`},
			"done":    true,
		})
	}))
	defer server.Close()

	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{BaseURL: server.URL, Model: "test:latest", Timeout: time.Second, MaxConcurrent: 1, MinScore: 0.78}, st)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Generate(context.Background(), "0x1234 unbekannter Fehler")
	if err != nil {
		t.Fatal(err)
	}
	if got["stream"] != false || got["format"] == nil {
		t.Fatalf("request did not ask for structured non-streaming output: %+v", got)
	}
	if result.Document["title"] != "Fehler 0x1234" || result.Document["auto_reply"] != false {
		t.Fatalf("result=%+v", result)
	}
	if _, err := svc.GetStaging(result.Key); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateSendsOllamaBearerToken(t *testing.T) {
	const token = "ollama-secret"
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"title":"T","text":"X","answer":"A","categories":[],"keywords":[]}`}})
	}))
	defer server.Close()
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{BaseURL: server.URL, Model: "test", APIKey: token, Timeout: time.Second, MaxConcurrent: 1, MinScore: 0.78}, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Generate(context.Background(), "secure ollama request"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+token {
		t.Fatalf("authorization=%q", auth)
	}
}

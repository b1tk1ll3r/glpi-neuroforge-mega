package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostOllamaSendsBearerToken(t *testing.T) {
	const token = "ollama-secret"
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()
	var out map[string]any
	if err := postOllama(context.Background(), srv.Client(), srv.URL, token, map[string]any{"x": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+token {
		t.Fatalf("authorization=%q", auth)
	}
}

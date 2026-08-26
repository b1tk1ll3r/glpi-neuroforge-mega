package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV8TextDocumentAndResearchEndpoints(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer ollama.Close()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format=%q", r.URL.Query().Get("format"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"title": "GPU", "url": "https://example.com/gpu", "content": "GPU evidence from search", "engine": "test"}}})
	}))
	defer searx.Close()

	srv, _ := newMetricsTestServer(t)
	cfg := srv.store.Config()
	cfg.Ollama[0].BaseURL = ollama.URL
	cfg.Routing.EmbeddingProvider = "ollama"
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.WebFetch.Enabled = false
	if err := srv.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	admin := srv.store.Secrets().AdminToken

	// Plain text source.
	body := `{"title":"manual","text":"NVIDIA CUDA fact","trust":0.9}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/text", strings.NewReader(body))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("text ingest status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Multipart text document.
	var mb bytes.Buffer
	mw := multipart.NewWriter(&mb)
	fw, _ := mw.CreateFormFile("file", "note.txt")
	_, _ = fw.Write([]byte("A second document fact about CUDA."))
	_ = mw.WriteField("title", "note")
	_ = mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ingest/document", &mb)
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("document ingest status=%d body=%s", rr.Code, rr.Body.String())
	}

	// SearXNG-backed research learns the snippet without web-page fetching.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/research", strings.NewReader(`{"query":"NVIDIA CUDA","learn":true,"fetch_pages":false}`))
	req.Header.Set("X-Admin-Token", admin)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("research status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(srv.store.SourcesSnapshot(10)) < 3 {
		t.Fatalf("expected source records, got %#v", srv.store.SourcesSnapshot(10))
	}
}

package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neuroforge/internal/core"
)

func TestGoalPauseResumeDeleteEndpoints(t *testing.T) {
	srv, _ := newMetricsTestServer(t)
	admin := srv.store.Secrets().AdminToken
	g := core.Goal{Title: "NVIDIA research", Description: "learn NVIDIA", Status: core.GoalActive, Priority: 70, AutoRun: true, IntervalMinutes: 10}
	if err := srv.store.UpsertGoal(&g); err != nil {
		t.Fatal(err)
	}

	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("X-Admin-Token", admin)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := call(http.MethodPost, "/api/v1/goals/"+g.ID+"/pause"); rr.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", rr.Code, rr.Body.String())
	}
	paused, _ := srv.store.GetGoal(g.ID)
	if paused.Status != core.GoalPaused || !paused.NextCycleAt.IsZero() {
		t.Fatalf("goal not paused correctly %#v", paused)
	}
	if rr := call(http.MethodPost, "/api/v1/goals/"+g.ID+"/cycle"); rr.Code == http.StatusOK {
		t.Fatal("paused goal must not run a manual cycle")
	}
	if rr := call(http.MethodPost, "/api/v1/goals/"+g.ID+"/resume"); rr.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", rr.Code, rr.Body.String())
	}
	active, _ := srv.store.GetGoal(g.ID)
	if active.Status != core.GoalActive {
		t.Fatalf("goal not active after resume %#v", active)
	}
	if rr := call(http.MethodDelete, "/api/v1/goals/"+g.ID); rr.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := srv.store.GetGoal(g.ID); ok {
		t.Fatal("goal still exists after delete")
	}
}

func TestResearchIngestsSearXNGDocumentResult(t *testing.T) {
	var doc bytes.Buffer
	zw := zip.NewWriter(&doc)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>NVIDIA CUDA document evidence from SearXNG.</w:t></w:r></w:p></w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	docSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Disposition", `attachment; filename="cuda.docx"`)
		_, _ = w.Write(doc.Bytes())
	}))
	defer docSrv.Close()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "CUDA paper", "url": docSrv.URL + "/cuda.docx", "content": "document result", "engine": "test", "template": "file.html", "filename": "cuda.docx", "mimetype": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		}}})
	}))
	defer searx.Close()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer ollama.Close()

	srv, _ := newMetricsTestServer(t)
	cfg := srv.store.Config()
	cfg.Ollama[0].BaseURL = ollama.URL
	cfg.Routing.EmbeddingProvider = "ollama"
	cfg.Brain.ExternalRelinkWorker = false
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.WebFetch.Enabled = true
	cfg.Research.WebFetch.AllowPrivateTargets = true
	if err := srv.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/research", strings.NewReader(`{"query":"CUDA docs","learn":true,"fetch_pages":true,"max_results":1,"max_pages":1}`))
	req.Header.Set("X-Admin-Token", srv.store.Secrets().AdminToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("research status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["documents_ingested"].(float64) != 1 {
		t.Fatalf("expected one ingested document, got %s", rr.Body.String())
	}
	found := false
	for _, src := range srv.store.SourcesSnapshot(20) {
		if src.Type == "research-document" && strings.Contains(src.FileName, "cuda.docx") && src.ChunkCount > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("research document source not persisted: %#v", srv.store.SourcesSnapshot(20))
	}
}

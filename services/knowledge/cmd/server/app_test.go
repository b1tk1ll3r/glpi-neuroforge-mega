package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kb-editor/internal/aifallback"
	"kb-editor/internal/staging"
	"kb-editor/internal/store"
)

func TestBulkAllMatchingUsesJSONFilterNames(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backups")
	t.Setenv("BACKUP_DIR", backup)
	write := func(name string, auto bool) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"id": name, "title": name, "auto_reply": auto, "language": "de-DE"})
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("true-one", true)
	write("false-one", false)

	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web).routes()

	body := []byte(`{"keys":[],"all_matching":true,"query":{"auto_reply":"false"},"patch":{"set_language":"en-US"},"dry_run":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result store.BulkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Targeted != 1 || result.Changed != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGoogleModeBlocksWrites(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]any{"id": "KB-1", "title": "Test", "answer": "Lösung"})
	if err := os.WriteFile(filepath.Join(dir, "one.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web, appConfig{Mode: "google", Title: "Helpdesk", Writable: false}).routes()

	item := s.List(store.Query{Page: 1, PageSize: 10}).Items[0]
	req := httptest.NewRequest(http.MethodPut, "/api/items/"+item.Key, bytes.NewBufferString(`{"title":"changed"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	doc, _, err := s.Get(item.Key)
	if err != nil {
		t.Fatal(err)
	}
	if doc["title"] != "Test" {
		t.Fatalf("document changed in google mode: %+v", doc)
	}
}

func TestSearchEndpointReturnsRankedHits(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, doc map[string]any) {
		t.Helper()
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("exact", map[string]any{"id": "0x80070005", "title": "Zugriff verweigert", "text": "Berechtigungen prüfen"})
	write("mention", map[string]any{"id": "KB-2", "title": "Allgemeiner Windows-Fehler", "answer": "Kann 0x80070005 enthalten"})

	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web, appConfig{Mode: "google", Title: "Helpdesk", Writable: false}).routes()

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=0x80070005&page=1&page_size=20", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result store.SearchResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Items[0].ID != "0x80070005" {
		t.Fatalf("exact ID should rank first: %+v", result.Items)
	}
}

func TestAIFallbackOnlyRunsForZeroResultsAndReturnsStagingArticle(t *testing.T) {
	knowledge := t.TempDir()
	b, _ := json.Marshal(map[string]any{"id": "KB-KNOWN", "title": "Bekannter Fehler", "answer": "Bekannte Lösung"})
	if err := os.WriteFile(filepath.Join(knowledge, "known.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := store.New(knowledge)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": `{"title":"KI-Entwurf","text":"Symptom","answer":"1. Diagnose","categories":["Windows"],"keywords":["unbekannt"]}`},
			"done":    true,
		})
	}))
	defer ollama.Close()

	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ai, err := aifallback.New(aifallback.Config{BaseURL: ollama.URL, Model: "test-model", Timeout: time.Second, MaxConcurrent: 1, MinScore: 0.78}, st)
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web, appConfig{Mode: "google", Title: "Helpdesk", Writable: false, AIFallbackEnabled: true}).withStaging(st).withAI(ai).routes()

	// Existing results must block the AI path before Ollama is called.
	req := httptest.NewRequest(http.MethodPost, "/api/ai/fallback", bytes.NewBufferString(`{"query":"Bekannter Fehler"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("known query status=%d body=%s", rr.Code, rr.Body.String())
	}
	if calls != 0 {
		t.Fatalf("Ollama should not be called when KB has hits, calls=%d", calls)
	}

	// Unknown query is generated and stored in staging.
	req = httptest.NewRequest(http.MethodPost, "/api/ai/fallback", bytes.NewBufferString(`{"query":"0xDEADBEEF völlig unbekannt"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("unknown query status=%d body=%s", rr.Code, rr.Body.String())
	}
	var generated aifallback.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || generated.Key == "" {
		t.Fatalf("calls=%d result=%+v", calls, generated)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/staging/"+generated.Key, nil)
	getRR := httptest.NewRecorder()
	h.ServeHTTP(getRR, get)
	if getRR.Code != http.StatusOK {
		t.Fatalf("staging get status=%d body=%s", getRR.Code, getRR.Body.String())
	}
}

func TestEditorCanReviewPromoteAndDeleteStaging(t *testing.T) {
	knowledge := t.TempDir()
	stagingDir := t.TempDir()
	t.Setenv("BACKUP_DIR", filepath.Join(t.TempDir(), "backups"))
	s, err := store.New(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.Save("unbekannt 0xAABBCCDD", "test-model", staging.Draft{
		Title: "Zu prüfender Entwurf", Text: "Symptom", Answer: "Lösung", Keywords: []string{"0xAABBCCDD"},
	}, false, 0.78)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.Save("anderer Entwurf", "test-model", staging.Draft{
		Title: "Zu löschender Entwurf", Text: "Symptom", Answer: "Lösung",
	}, false, 0.78)
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web, appConfig{Mode: "editor", Title: "Editor", Writable: true}).withStaging(st).routes()

	listReq := httptest.NewRequest(http.MethodGet, "/api/staging?q=AABBCCDD&page=1&page_size=20", nil)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRR.Code, listRR.Body.String())
	}
	var list staging.ListResult
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Items[0].Key != first.Key {
		t.Fatalf("unexpected staging list: %+v", list)
	}

	updated := first.Document
	updated["title"] = "Geprüfter Entwurf"
	updated["auto_reply"] = true
	body, _ := json.Marshal(updated)
	putReq := httptest.NewRequest(http.MethodPut, "/api/staging/"+first.Key, bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	putRR := httptest.NewRecorder()
	h.ServeHTTP(putRR, putReq)
	if putRR.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", putRR.Code, putRR.Body.String())
	}

	promoteReq := httptest.NewRequest(http.MethodPost, "/api/staging/"+first.Key+"/promote", bytes.NewBufferString(`{}`))
	promoteReq.Header.Set("Content-Type", "application/json")
	promoteRR := httptest.NewRecorder()
	h.ServeHTTP(promoteRR, promoteReq)
	if promoteRR.Code != http.StatusCreated {
		t.Fatalf("promote status=%d body=%s", promoteRR.Code, promoteRR.Body.String())
	}
	if s.Count() != 1 || st.Count() != 1 {
		t.Fatalf("counts after promote: production=%d staging=%d", s.Count(), st.Count())
	}
	prod := s.List(store.Query{Page: 1, PageSize: 10})
	if prod.Items[0].Title != "Geprüfter Entwurf" || prod.Items[0].AutoReply == nil || !*prod.Items[0].AutoReply {
		t.Fatalf("promoted item not preserved: %+v", prod.Items[0])
	}
	if _, err := st.Get(first.Key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("promoted staging file should be gone, err=%v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/staging/"+second.Key, nil)
	deleteRR := httptest.NewRecorder()
	h.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	if st.Count() != 0 {
		t.Fatalf("staging should be empty, count=%d", st.Count())
	}
	approved, err := filepath.Glob(filepath.Join(stagingDir, ".approved", "*.json"))
	if err != nil || len(approved) != 1 {
		t.Fatalf("expected one promoted draft in .approved, files=%v err=%v", approved, err)
	}
	trash, err := filepath.Glob(filepath.Join(stagingDir, ".trash", "*.json"))
	if err != nil || len(trash) != 1 {
		t.Fatalf("expected one deleted draft in .trash, files=%v err=%v", trash, err)
	}
}

func TestEditorBulkStagingPromote(t *testing.T) {
	knowledge := t.TempDir()
	s, err := store.New(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Save("a", "model", staging.Draft{Title: "A", Answer: "Lösung A"}, false, .78)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Save("b", "model", staging.Draft{Title: "B", Answer: "Lösung B"}, false, .78)
	if err != nil {
		t.Fatal(err)
	}
	web, _ := fs.Sub(webFS, "web")
	h := newApp(s, web, appConfig{Mode: "editor", Writable: true}).withStaging(st).routes()
	payload, _ := json.Marshal(map[string]any{"keys": []string{a.Key, b.Key}, "action": "promote"})
	req := httptest.NewRequest(http.MethodPost, "/api/staging/bulk", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result struct{ Succeeded, Failed int }
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 2 || result.Failed != 0 || s.Count() != 2 || st.Count() != 0 {
		t.Fatalf("unexpected bulk result=%+v prod=%d staging=%d", result, s.Count(), st.Count())
	}
}

func TestIntegrationDraftCanOnlyEnterStaging(t *testing.T) {
	t.Setenv("KB_INTEGRATION_TOKEN", "integration-secret")
	knowledge := t.TempDir()
	s, err := store.New(knowledge)
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web).withStaging(st).routes()

	payload := `{"source":"NeuroForge Research","query":"VPN Fehler","title":"VPN Diagnose","text":"Symptom","answer":"Erst Gateway prüfen","categories":["VPN"],"keywords":["gateway"],"min_score":0.9}`
	unauth := httptest.NewRequest(http.MethodPost, "/api/integrations/staging", bytes.NewBufferString(payload))
	unauth.Header.Set("Content-Type", "application/json")
	unauthRR := httptest.NewRecorder()
	h.ServeHTTP(unauthRR, unauth)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d body=%s", unauthRR.Code, unauthRR.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/integrations/staging", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer integration-secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if s.Count() != 0 {
		t.Fatalf("integration proposal must not write production, count=%d", s.Count())
	}
	if st.Count() != 1 {
		t.Fatalf("staging count=%d", st.Count())
	}
	items, err := st.List(staging.Query{Page: 1, PageSize: 10})
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("staging list err=%v items=%+v", err, items.Items)
	}
	result, err := st.Get(items.Items[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := result.Document["auto_reply"].(bool); got {
		t.Fatal("machine-generated integration draft must never enable auto_reply")
	}
	if source := fmt.Sprint(result.Document["source"]); !strings.Contains(source, "NeuroForge Research") || !strings.Contains(source, "AI-Staging") {
		t.Fatalf("unexpected proposal source %q", source)
	}
}

func TestEditorBasicAuthDoesNotLeakCredentialsToIntegrationClient(t *testing.T) {
	t.Setenv("BASIC_AUTH_USER", "editor")
	t.Setenv("BASIC_AUTH_PASSWORD", "editor-secret")
	t.Setenv("KB_INTEGRATION_TOKEN", "integration-secret")
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, _ := fs.Sub(webFS, "web")
	h := optionalBasicAuth(newApp(s, web).withStaging(st).routes())

	payload := `{"source":"NeuroForge Research","query":"x","title":"Draft","answer":"Review me"}`
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/staging", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer integration-secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("integration should not require editor credentials: status=%d body=%s", rr.Code, rr.Body.String())
	}

	items := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	itemsRR := httptest.NewRecorder()
	h.ServeHTTP(itemsRR, items)
	if itemsRR.Code != http.StatusUnauthorized {
		t.Fatalf("editor API unexpectedly bypassed basic auth: %d", itemsRR.Code)
	}
}

func TestIntegrationDraftWithStableKeyUpdatesInsteadOfDuplicating(t *testing.T) {
	t.Setenv("KB_INTEGRATION_TOKEN", "integration-secret")
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, _ := fs.Sub(webFS, "web")
	h := newApp(s, web).withStaging(st).routes()
	post := func(answer string) {
		payload := fmt.Sprintf(`{"source":"NeuroForge Research","query":"NVIDIA","title":"NVIDIA","answer":%q,"integration_key":"neuroforge-goal:g1","metadata":{"research_goal_id":"g1"}}`, answer)
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/staging", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer integration-secret")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
	}
	post("erste Fassung")
	post("zweite Fassung")
	if st.Count() != 1 {
		t.Fatalf("expected one active draft, got %d", st.Count())
	}
	items, _ := st.List(staging.Query{Page: 1, PageSize: 10})
	got, _ := st.Get(items.Items[0].Key)
	if got.Document["answer"] != "zweite Fassung" {
		t.Fatalf("draft not refreshed: %#v", got.Document)
	}
}

func TestBrowserWriteSameOriginGuardRejectsCrossSiteWrite(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := newApp(s, web, appConfig{Mode: "editor", Writable: true}).routes()
	req := httptest.NewRequest(http.MethodPost, "/api/bulk", bytes.NewBufferString(`{"keys":[],"dry_run":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.invalid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPromotionRollsBackProductionWhenStagingArchiveFails(t *testing.T) {
	knowledgeDir := t.TempDir()
	stagingDir := t.TempDir()
	t.Setenv("BACKUP_DIR", filepath.Join(t.TempDir(), "backups"))
	s, err := store.New(knowledgeDir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := st.Save("rollback", "test", staging.Draft{Title: "Rollback", Text: "Symptom", Answer: "Lösung"}, false, .8)
	if err != nil {
		t.Fatal(err)
	}
	// Force ArchiveApproved to fail after ImportDocument by occupying the archive
	// directory path with a regular file.
	if err := os.WriteFile(filepath.Join(stagingDir, ".approved"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(s, web, appConfig{Mode: "editor", Writable: true}).withStaging(st)
	if _, err := a.promoteStaging(draft.Key); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("promotion error=%v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("production count=%d, want rollback to zero", s.Count())
	}
	if _, err := st.Get(draft.Key); err != nil {
		t.Fatalf("staging draft should remain for retry: %v", err)
	}
}

func TestIntegrationStagingHealthBypassesUIBasicAuthButRequiresBearer(t *testing.T) {
	t.Setenv("BASIC_AUTH_USER", "editor")
	t.Setenv("BASIC_AUTH_PASSWORD", "knowledge-password-123456")
	t.Setenv("KB_INTEGRATION_TOKEN", "integration-token-12345678901234567890")
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := staging.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}
	h := optionalBasicAuth(newApp(s, web, appConfig{Mode: "editor", Writable: true}).withStaging(st).routes())

	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/integrations/staging/health", nil))
	if unauth.Code != http.StatusUnauthorized || strings.Contains(unauth.Body.String(), "authentication required") {
		t.Fatalf("request should reach bearer guard, status=%d body=%s", unauth.Code, unauth.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/staging/health", nil)
	req.Header.Set("Authorization", "Bearer integration-token-12345678901234567890")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

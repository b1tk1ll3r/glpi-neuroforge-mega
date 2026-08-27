package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func integrationRequest(t *testing.T, s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestIntegrationKnowledgeLifecycleAndNamespaceIsolation(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	key := s.store.Secrets().IntegrationToken

	unauth := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/upsert", "", `{"namespace":"agent","document_id":"KB-1","chunks":[{"index":0,"text":"vpn","vector":[1,0],"content_hash":"a"}]}`)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d body=%s", unauth.Code, unauth.Body.String())
	}

	body := `{"namespace":"agent","document_id":"KB-1","title":"VPN","chunks":[{"index":0,"text":"vpn gateway","vector":[1,0],"content_hash":"a"},{"index":1,"text":"reset token","vector":[0,1],"content_hash":"b"}]}`
	rr := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/upsert", key, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", rr.Code, rr.Body.String())
	}
	var first map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first["created"] != float64(2) || first["updated"] != float64(0) {
		t.Fatalf("unexpected first upsert: %#v", first)
	}

	// A second namespace with the same vector must never leak into agent search.
	other := `{"namespace":"other","document_id":"KB-X","chunks":[{"index":0,"text":"other vpn","vector":[1,0],"content_hash":"x"}]}`
	rr = integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/upsert", key, other)
	if rr.Code != http.StatusOK {
		t.Fatalf("other upsert status=%d body=%s", rr.Code, rr.Body.String())
	}

	search := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/search", key, `{"namespace":"agent","vector":[1,0],"k":10}`)
	if search.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", search.Code, search.Body.String())
	}
	var hits []struct {
		Memory struct {
			Text       string `json:"text"`
			Provenance struct {
				Source         string `json:"source"`
				SourceMemoryID string `json:"source_memory_id"`
			} `json:"provenance"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(search.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Memory.Provenance.SourceMemoryID != "KB-1" {
		t.Fatalf("unexpected scoped hits: %+v", hits)
	}
	for _, h := range hits {
		if h.Memory.Provenance.Source != "integration:agent" || h.Memory.Provenance.SourceMemoryID == "KB-X" {
			t.Fatalf("namespace leak: %+v", h)
		}
	}

	// Replace both existing chunks in one request and remove chunk 1.
	update := `{"namespace":"agent","document_id":"KB-1","title":"VPN updated","chunks":[{"index":0,"text":"vpn gateway updated","vector":[1,0],"content_hash":"a2"}]}`
	rr = integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/upsert", key, update)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}
	var changed map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if changed["updated"] != float64(1) || changed["deleted"] != float64(1) {
		t.Fatalf("unexpected replacement counts: %#v", changed)
	}

	event := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/events", key, `{"type":"knowledge.search","source":"agent","query":"vpn","message":"search completed","hits":[{"id":"KB-1","score":0.98}]}`)
	if event.Code != http.StatusAccepted {
		t.Fatalf("event status=%d body=%s", event.Code, event.Body.String())
	}

	deleted := integrationRequest(t, s, http.MethodDelete, "/api/v1/integrations/knowledge/agent/KB-1", key, "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":1`) {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	search = integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/knowledge/search", key, `{"namespace":"agent","vector":[1,0],"k":10}`)
	if search.Code != http.StatusOK {
		t.Fatalf("post-delete search status=%d body=%s", search.Code, search.Body.String())
	}
	if strings.Contains(search.Body.String(), "KB-1") {
		t.Fatalf("deleted document still searchable: %s", search.Body.String())
	}
}

func TestScopedTokensCannotCrossTrustBoundaries(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	sec := s.store.Secrets()

	// The general App key is deliberately insufficient for integration writes.
	if rr := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/events", sec.AppAPIKey, `{"type":"test","source":"agent","message":"x"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("app key wrote integration event: status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Control is read-only and cannot write integration events or generic learn.
	if rr := integrationRequest(t, s, http.MethodPost, "/api/v1/integrations/events", sec.ControlReadToken, `{"type":"test","source":"control","message":"x"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("control token wrote integration event: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := integrationRequest(t, s, http.MethodPost, "/api/v1/learn", sec.ControlReadToken, `{"text":"must not learn"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("control token reached /learn: status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Integration credentials are not generic app credentials either.
	if rr := integrationRequest(t, s, http.MethodPost, "/api/v1/learn", sec.IntegrationToken, `{"text":"must not learn"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("integration token reached /learn: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := integrationRequest(t, s, http.MethodGet, "/api/v1/stats", sec.ControlReadToken, ""); rr.Code != http.StatusOK {
		t.Fatalf("control token cannot read stats: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

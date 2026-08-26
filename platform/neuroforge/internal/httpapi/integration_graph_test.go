package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neuroforge/internal/core"
)

func appGraphRequest(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+s.store.Secrets().AppAPIKey)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestIntegrationGraphEndpointsRequireAppKey(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	for _, path := range []string{"/api/v1/integrations/graph/brain", "/api/v1/integrations/graph/research"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestIntegrationBrainGraphIsBoundedAndRedacted(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	m := &core.Memory{ID: "m-graph", Kind: "fact", MemoryType: core.MemorySemantic, Text: "secretly long operational text", Vector: []float32{1, 2, 3}, VectorDim: 3, Salience: .9, Confidence: .8, Status: core.MemoryActive, CreatedAt: time.Now().UTC(), Provenance: core.MemoryProvenance{Source: "glpi.outcome.accepted", SourceTitle: "VPN fix"}}
	if err := s.store.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	rr := appGraphRequest(t, s, "/api/v1/integrations/graph/brain?max_nodes=50")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"vector"`) || strings.Contains(rr.Body.String(), "secretly long operational text") {
		t.Fatalf("graph leaked full memory data: %s", rr.Body.String())
	}
	var g integrationGraphPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) == 0 || g.Nodes[0].Kind == "" {
		t.Fatalf("missing graph nodes: %+v", g)
	}
}

func TestIntegrationResearchGraphShowsProvenanceChain(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	run, err := s.store.StartResearchRun("goal-1", "VPN research")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []core.ResearchEvent{
		{Type: "query.planned", Query: "vpn client issue"},
		{Type: "search.result", Query: "vpn client issue", URL: "https://example.invalid/vpn", Title: "VPN source", SourceID: "src-1", Score: .7},
		{Type: "evidence.learned", SourceID: "src-1", MemoryID: "m-research", Preview: "verified workaround", Confidence: .65},
	} {
		if _, err := s.store.AddResearchEvent(run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.FinishResearchRun(run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	rr := appGraphRequest(t, s, "/api/v1/integrations/graph/research?runs=2&max_events=50")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"research_goal", "query", "source", "memory", "learned_as"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
}

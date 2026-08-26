package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEmbeddedEngineeringGraphHasUsefulStructure(t *testing.T) {
	g, err := loadEngineeringGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) < 500 || len(g.Edges) < 1000 {
		t.Fatalf("graph unexpectedly small: nodes=%d edges=%d", len(g.Nodes), len(g.Edges))
	}
	kinds := map[string]bool{}
	for _, n := range g.Nodes {
		kinds[n.Kind] = true
	}
	for _, want := range []string{"component", "package", "file", "function", "route", "service"} {
		if !kinds[want] {
			t.Fatalf("missing kind %q", want)
		}
	}
}

func TestEngineeringGraphEndpointHonorsNodeBudget(t *testing.T) {
	s := &server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/engineering?max_nodes=80", nil)
	s.handleEngineeringGraph(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var g graphPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) > 80 || len(g.Nodes) == 0 {
		t.Fatalf("node budget violated: %d", len(g.Nodes))
	}
}

func TestEngineeringImpactRequiresQueryAndReturnsBoundedBlastRadius(t *testing.T) {
	s := &server{}
	missing := httptest.NewRecorder()
	s.handleEngineeringImpact(missing, httptest.NewRequest(http.MethodGet, "/api/graph/impact", nil))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing query status=%d", missing.Code)
	}

	g0, err := loadEngineeringGraph()
	if err != nil {
		t.Fatal(err)
	}
	var query string
	for _, n := range g0.Nodes {
		if n.Kind == "route" {
			query = n.Label
			break
		}
	}
	if strings.TrimSpace(query) == "" {
		t.Fatal("no route available for impact test")
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph/impact?q="+url.QueryEscape(query)+"&depth=1&max_nodes=70", nil)
	s.handleEngineeringImpact(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var g graphPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) == 0 || len(g.Nodes) > 70 {
		t.Fatalf("bad impact node count=%d", len(g.Nodes))
	}
	if g.Meta["risk"] == nil {
		t.Fatalf("missing risk metadata: %+v", g.Meta)
	}
}

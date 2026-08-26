package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neuroforge/internal/core"
)

func TestAdminStatusKeepsFlatDashboardFields(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	if err := s.store.AddMemory(&core.Memory{Text: "status dashboard regression", MemoryType: "semantic"}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/status", nil)
	req.Header.Set("X-Admin-Token", s.store.Secrets().AdminToken)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	stats, ok := body["stats"].(map[string]any)
	if !ok {
		t.Fatalf("missing nested stats: %#v", body["stats"])
	}
	for _, key := range []string{"revision", "memories", "synapses", "pending_jobs", "hnsw_nodes", "disk_pq_items", "index_mode", "maintenance"} {
		flat, exists := body[key]
		if !exists {
			t.Fatalf("flat status field %q missing", key)
		}
		nested, exists := stats[key]
		if !exists {
			t.Fatalf("nested status field %q missing", key)
		}
		if key != "maintenance" && key != "index_mode" && flat != nested {
			t.Fatalf("field %q flat=%v nested=%v", key, flat, nested)
		}
	}
	if got := int(body["memories"].(float64)); got != 1 {
		t.Fatalf("memories=%d want 1", got)
	}
}

func TestAdminDashboardNormalizesNestedStatusResponse(t *testing.T) {
	b, err := webFS.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{
		"function normalizeStatus(s)",
		"s=normalizeStatus(raw)",
		"bereit · rev '+fmtN(s.revision)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard missing status compatibility guard %q", want)
		}
	}
}

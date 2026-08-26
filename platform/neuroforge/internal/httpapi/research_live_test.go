package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"neuroforge/internal/core"
)

func TestGoalResearchLiveReturnsEventDelta(t *testing.T) {
	srv, _ := newMetricsTestServer(t)
	g := core.Goal{Title: "Live NVIDIA", Status: core.GoalActive, Priority: 50}
	if err := srv.store.UpsertGoal(&g); err != nil {
		t.Fatal(err)
	}
	run, err := srv.store.StartResearchRun(g.ID, g.Title)
	if err != nil {
		t.Fatal(err)
	}
	ev1, err := srv.store.AddResearchEvent(run.ID, core.ResearchEvent{Type: "query.planned", Query: "NVIDIA CUDA", Status: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.store.AddResearchEvent(run.ID, core.ResearchEvent{Type: "search.result", URL: "https://example.com", Title: "Example", Status: "ok"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/goals/"+g.ID+"/research/live?run_id="+run.ID+"&after="+jsonNumber(ev1.Seq), nil)
	req.Header.Set("X-Admin-Token", srv.store.Secrets().AdminToken)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Run    core.ResearchRun     `json:"run"`
		Events []core.ResearchEvent `json:"events"`
		Reset  bool                 `json:"reset"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Reset || out.Run.ID != run.ID || len(out.Events) != 1 || out.Events[0].Type != "search.result" {
		t.Fatalf("unexpected live delta %#v", out)
	}
}

func jsonNumber(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

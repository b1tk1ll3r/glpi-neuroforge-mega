package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoalsCreateReturnsConflictForDuplicateActiveGoal(t *testing.T) {
	srv, _ := newMetricsTestServer(t)
	payload := `{"title":"FortiClient SSLVPN 7200","description":"Create one support article","target":"1 hochwertiger Wissensartikel","status":"active"}`

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/goals", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+srv.store.Secrets().AppAPIKey)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	if rr := post(); rr.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := post(); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create status=%d body=%s", rr.Code, rr.Body.String())
	}
}

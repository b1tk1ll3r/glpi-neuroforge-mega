package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapHandlerExposesLivenessAndStartupUI(t *testing.T) {
	b := newBootstrapHandler()
	b.SetPhase("hnsw.rebuild")

	rr := httptest.NewRecorder()
	b.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "hnsw.rebuild") {
		t.Fatalf("livez=%d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	b.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	b.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "hnsw.rebuild") {
		t.Fatalf("admin=%d %s", rr.Code, rr.Body.String())
	}

	b.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rr = httptest.NewRecorder()
	b.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delegated=%d", rr.Code)
	}
}

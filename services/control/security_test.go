package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlBasicAuthProtectsOperationsButNotHealth(t *testing.T) {
	t.Setenv("CONTROL_BASIC_AUTH_USER", "operator")
	t.Setenv("CONTROL_BASIC_AUTH_PASSWORD", "control-password-123456")
	h := controlBasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d", health.Code)
	}

	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", unauth.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.SetBasicAuth("operator", "control-password-123456")
	auth := httptest.NewRecorder()
	h.ServeHTTP(auth, req)
	if auth.Code != http.StatusOK {
		t.Fatalf("auth status=%d", auth.Code)
	}
}

func TestControlSecretValidationRejectsPlaceholder(t *testing.T) {
	t.Setenv("NEUROFORGE_API_KEY", "valid-neuroforge-read-token-123456")
	t.Setenv("CONTROL_READ_TOKEN", "valid-agent-control-token-123456")
	t.Setenv("CONTROL_BASIC_AUTH_USER", "operator")
	t.Setenv("CONTROL_BASIC_AUTH_PASSWORD", "CHANGE_ME_CONTROL_PASSWORD")
	if err := validateControlSecrets(); err == nil {
		t.Fatal("placeholder password accepted")
	}
}

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppEndpointsAcceptAdminTokenWithoutAppKeyReveal(t *testing.T) {
	s, _ := newMetricsTestServer(t)
	sec := s.store.Secrets()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("X-Admin-Token", sec.AdminToken)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin-auth app endpoint status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer "+sec.AppAPIKey)
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("app bearer status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d want=%d", rr.Code, http.StatusUnauthorized)
	}
}

func TestAdminDashboardDoesNotPutMaskedAppSecretInAuthorizationHeader(t *testing.T) {
	b, err := webFS.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if strings.Contains(html, "getAppKey()") {
		t.Fatal("dashboard must not fetch App API key for its own application calls")
	}
	if strings.Contains(html, "Authorization':'Bearer '+(await getAppKey())") {
		t.Fatal("dashboard still builds Authorization from masked App API key")
	}
	if !strings.Contains(html, "validHeaderToken") {
		t.Fatal("dashboard should validate admin token before fetch")
	}
}

func TestAdminDashboardChatInputDoesNotCollideWithWindowPrompt(t *testing.T) {
	b, err := webFS.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if strings.Contains(html, `id="prompt"`) {
		t.Fatal("dashboard chat textarea must not use reserved browser global name prompt")
	}
	if strings.Contains(html, "input:prompt.value") || strings.Contains(html, "input: prompt.value") {
		t.Fatal("dashboard chat must not read window.prompt as the request input")
	}
	if !strings.Contains(html, `document.getElementById('chatPrompt')`) {
		t.Fatal("dashboard chat should resolve its textarea explicitly")
	}
}

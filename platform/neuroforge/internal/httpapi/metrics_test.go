package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neuroforge/internal/brain"
	"neuroforge/internal/cost"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

func newMetricsTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := provider.NewRouter(s)
	c := cost.New(s)
	b := brain.New(s, r, c)
	return New(s, b, r, c), s.Secrets().MetricsToken
}

func TestMetricsEndpointRequiresBearerToken(t *testing.T) {
	s, token := newMetricsTestServer(t)
	if token == "" {
		t.Fatal("metrics token was not generated")
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMetricsEndpointExportsNormalizedHTTPMetrics(t *testing.T) {
	s, token := newMetricsTestServer(t)

	// Generate one instrumented request before scraping.
	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRR := httptest.NewRecorder()
	s.Handler().ServeHTTP(healthRR, health)
	if healthRR.Code != http.StatusOK {
		t.Fatalf("health status=%d", healthRR.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") || !strings.Contains(got, "version=0.0.4") {
		t.Fatalf("unexpected content type %q", got)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"# TYPE neuroforge_memories gauge",
		"neuroforge_up 1",
		"neuroforge_http_requests_total{method=\"GET\",route=\"/healthz\",code=\"200\"} 1",
		"neuroforge_http_request_duration_seconds_bucket{method=\"GET\",route=\"/healthz\",le=\"+Inf\"} 1",
		"neuroforge_page_cache_hits_total",
		"neuroforge_openai_budget_usd",
		"neuroforge_vector_journal_compression_savings_percent",
		"neuroforge_vector_journal_sqar_blocks",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q\n%s", want, body)
		}
	}
}

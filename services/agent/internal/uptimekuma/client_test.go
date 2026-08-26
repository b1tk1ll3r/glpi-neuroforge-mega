package uptimekuma

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchIssuesUsesPublicStatusPageEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status-page/it":
			w.Write([]byte(`{"config":{"slug":"it","title":"IT"},"incident":{"title":"VPN-Störung","content":"Wir untersuchen die Störung","pin":true},"publicGroupList":[{"name":"Netzwerk","monitorList":[{"id":7,"name":"VPN","type":"http"}]}],"maintenanceList":[]}`))
		case "/api/status-page/heartbeat/it":
			w.Write([]byte(`{"heartbeatList":{"7":[{"status":0,"time":"2026-07-27T08:01:00Z","msg":"timeout"},{"status":1,"time":"2026-07-27T08:00:00Z","msg":"OK"}]},"uptimeList":{"7_24":0.95}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "status_page", "", time.Second)
	issues, err := c.FetchIssues(context.Background(), []string{"it"}, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("issues=%d: %+v", len(issues), issues)
	}
	if issues[0].Kind != "pinned_incident" || issues[1].Status != "down" || issues[1].MonitorID != 7 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestFetchIssuesMetricsUsesAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("glpi-ai-agent:secret"))
		if r.Header.Get("Authorization") != want {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(strings.Join([]string{
			`monitor_status{monitor_id="7",monitor_name="VPN Gateway",monitor_type="http"} 0`,
			`monitor_status{monitor_id="8",monitor_name="Website",monitor_type="http"} 1`,
		}, "\n")))
	}))
	defer srv.Close()
	c := New(srv.URL, "metrics", "secret", time.Second)
	issues, err := c.FetchIssues(context.Background(), nil, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].MonitorID != 7 || issues[0].Status != "down" {
		t.Fatalf("issues=%+v", issues)
	}
}

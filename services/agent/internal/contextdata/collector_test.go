package contextdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
)

type fakeGLPI struct {
	changes []model.ChangeContext
	inc     []model.MajorIncidentContext
	devices []model.UserDeviceContext
	err     error
}

func (f fakeGLPI) ListChanges(context.Context, string, int, string) ([]model.ChangeContext, error) {
	return f.changes, f.err
}
func (f fakeGLPI) ListMajorIncidents(context.Context, int, string) ([]model.MajorIncidentContext, error) {
	return f.inc, f.err
}
func (f fakeGLPI) ListUserDevices(context.Context, int64, []string, string, int) ([]model.UserDeviceContext, error) {
	return f.devices, f.err
}

type fakeKuma struct {
	issues []model.ServiceIssueContext
	err    error
}

func (f fakeKuma) FetchIssues(context.Context, []string, bool, int) ([]model.ServiceIssueContext, error) {
	return f.issues, f.err
}

func baseCfg() config.Config {
	return config.Config{ContextEnabled: true, ContextTimeout: time.Second, ChangeCalendarEnabled: true, GLPIChangePath: "/Assistance/Change", GLPIChangeLimit: 100, ChangeLookback: 48 * time.Hour, ChangeLookahead: 24 * time.Hour, MajorIncidentsEnabled: true, GLPIMajorIncidentFilter: "x", GLPIMajorIncidentLimit: 10, UserDeviceContextEnabled: true, GLPIUserDevicePaths: []string{"/Assets/Computer"}, GLPIUserDeviceFilterTemplate: "user.id=={{user_id}}", GLPIUserDeviceLimit: 10, UptimeKumaEnabled: true, UptimeKumaStatusPages: []string{"it"}, UptimeKumaMaxIssues: 10}
}

func TestCollectScoresRelevantIncident(t *testing.T) {
	cfg := baseCfg()
	g := fakeGLPI{inc: []model.MajorIncidentContext{{ID: 1, Name: "VPN Gateway Ausfall"}}, devices: []model.UserDeviceContext{{ID: 2, ItemType: "Computer", Name: "NB-1"}}}
	k := fakeKuma{issues: []model.ServiceIssueContext{{Kind: "monitor", MonitorID: 7, MonitorName: "VPN Gateway", Status: "down"}}}
	c := New(cfg, g, k)
	c.now = func() time.Time { return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC) }
	s := c.Collect(context.Background(), model.Ticket{ID: 1, Name: "VPN funktioniert nicht", RequesterIDs: []int64{5}})
	if len(s.MajorIncidents) != 1 || s.MajorIncidents[0].Relevance <= 0 {
		t.Fatalf("incidents=%+v", s.MajorIncidents)
	}
	if len(s.ServiceIssues) != 1 || s.ServiceIssues[0].Relevance <= 0 {
		t.Fatalf("issues=%+v", s.ServiceIssues)
	}
	if len(s.UserDevices) != 1 {
		t.Fatalf("devices=%+v", s.UserDevices)
	}
}

func TestProviderFailureMarksSnapshotIncomplete(t *testing.T) {
	cfg := baseCfg()
	cfg.ChangeCalendarEnabled = false
	cfg.MajorIncidentsEnabled = false
	cfg.UserDeviceContextEnabled = false
	c := New(cfg, fakeGLPI{}, fakeKuma{err: errors.New("boom")})
	s := c.Collect(context.Background(), model.Ticket{ID: 1})
	if !s.Incomplete || len(s.Warnings) == 0 {
		t.Fatalf("snapshot=%+v", s)
	}
}

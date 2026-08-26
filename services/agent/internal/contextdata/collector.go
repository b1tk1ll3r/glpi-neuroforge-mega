package contextdata

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
)

type GLPIReader interface {
	ListChanges(context.Context, string, int, string) ([]model.ChangeContext, error)
	ListMajorIncidents(context.Context, int, string) ([]model.MajorIncidentContext, error)
	ListUserDevices(context.Context, int64, []string, string, int) ([]model.UserDeviceContext, error)
}

type UptimeKumaReader interface {
	FetchIssues(context.Context, []string, bool, int) ([]model.ServiceIssueContext, error)
}

type Collector struct {
	cfg  config.Config
	glpi GLPIReader
	kuma UptimeKumaReader
	now  func() time.Time
}

func New(cfg config.Config, glpi GLPIReader, kuma UptimeKumaReader) *Collector {
	return &Collector{cfg: cfg, glpi: glpi, kuma: kuma, now: time.Now}
}

func (c *Collector) Collect(parent context.Context, ticket model.Ticket) model.ContextSnapshot {
	snapshot := model.ContextSnapshot{FetchedAt: c.now()}
	if !c.cfg.ContextEnabled {
		return snapshot
	}
	ctx, cancel := context.WithTimeout(parent, c.cfg.ContextTimeout)
	defer cancel()

	// Device context is collected first because device names improve relevance
	// matching for changes and incidents.
	if c.cfg.UserDeviceContextEnabled {
		devices, err := c.collectDevices(ctx, ticket)
		if err != nil {
			snapshot.Warnings = append(snapshot.Warnings, "user_devices: "+err.Error())
			snapshot.Incomplete = true
		} else {
			snapshot.UserDevices = devices
		}
	}

	query := ticket.Name + "\n" + ticket.Content
	for _, d := range snapshot.UserDevices {
		query += "\n" + d.Name + " " + d.ItemType + " " + d.Location
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	addWarning := func(prefix string, err error) {
		mu.Lock()
		snapshot.Warnings = append(snapshot.Warnings, prefix+": "+err.Error())
		snapshot.Incomplete = true
		mu.Unlock()
	}

	if c.cfg.ChangeCalendarEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changes, err := c.glpi.ListChanges(ctx, c.cfg.GLPIChangePath, c.cfg.GLPIChangeLimit, c.cfg.GLPIChangeFilter)
			if err != nil {
				addWarning("change_calendar", err)
				return
			}
			changes = c.filterChanges(query, changes)
			mu.Lock()
			snapshot.Changes = changes
			mu.Unlock()
		}()
	}

	if c.cfg.MajorIncidentsEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			incidents, err := c.glpi.ListMajorIncidents(ctx, c.cfg.GLPIMajorIncidentLimit, c.cfg.GLPIMajorIncidentFilter)
			if err != nil {
				addWarning("major_incidents", err)
				return
			}
			for i := range incidents {
				incidents[i].Relevance = relevance(query, incidents[i].Name+" "+incidents[i].Content)
			}
			sort.SliceStable(incidents, func(i, j int) bool { return incidents[i].Relevance > incidents[j].Relevance })
			mu.Lock()
			snapshot.MajorIncidents = trimIncidents(incidents, 10)
			mu.Unlock()
		}()
	}

	if c.cfg.UptimeKumaEnabled && c.kuma != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issues, err := c.kuma.FetchIssues(ctx, c.cfg.UptimeKumaStatusPages, c.cfg.UptimeKumaIncludeMaintenance, c.cfg.UptimeKumaMaxIssues)
			if err != nil {
				addWarning("uptime_kuma", err)
				return
			}
			for i := range issues {
				candidate := issues[i].MonitorName + " " + issues[i].Message + " " + issues[i].IncidentTitle + " " + issues[i].IncidentContent + " " + issues[i].StatusPage
				issues[i].Relevance = relevance(query, candidate)
			}
			sort.SliceStable(issues, func(i, j int) bool { return issues[i].Relevance > issues[j].Relevance })
			mu.Lock()
			snapshot.ServiceIssues = issues
			mu.Unlock()
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil && parent.Err() == nil {
		snapshot.Incomplete = true
		if !containsPrefix(snapshot.Warnings, "context_timeout:") {
			snapshot.Warnings = append(snapshot.Warnings, "context_timeout: "+err.Error())
		}
	}
	return snapshot
}

func (c *Collector) collectDevices(ctx context.Context, ticket model.Ticket) ([]model.UserDeviceContext, error) {
	seen := map[string]struct{}{}
	out := make([]model.UserDeviceContext, 0, len(ticket.Items)+4)
	for _, item := range ticket.Items {
		if item.ID <= 0 || strings.TrimSpace(item.ItemType) == "" {
			continue
		}
		key := strings.ToLower(item.ItemType) + ":" + fmt.Sprint(item.ID)
		seen[key] = struct{}{}
		out = append(out, model.UserDeviceContext{ItemType: item.ItemType, ID: item.ID, Name: item.Name, Source: "glpi-ticket-link"})
	}
	for _, userID := range ticket.RequesterIDs {
		devices, err := c.glpi.ListUserDevices(ctx, userID, c.cfg.GLPIUserDevicePaths, c.cfg.GLPIUserDeviceFilterTemplate, c.cfg.GLPIUserDeviceLimit)
		if err != nil {
			return out, err
		}
		for _, d := range devices {
			key := strings.ToLower(d.ItemType) + ":" + fmt.Sprint(d.ID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, d)
			if len(out) >= c.cfg.GLPIUserDeviceLimit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (c *Collector) filterChanges(query string, changes []model.ChangeContext) []model.ChangeContext {
	now := c.now()
	from := now.Add(-c.cfg.ChangeLookback)
	to := now.Add(c.cfg.ChangeLookahead)
	out := make([]model.ChangeContext, 0, len(changes))
	for _, ch := range changes {
		if !changeOverlaps(ch, from, to) {
			continue
		}
		ch.Relevance = relevance(query, ch.Name+" "+ch.Content)
		out = append(out, ch)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Relevance > out[j].Relevance })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func changeOverlaps(ch model.ChangeContext, from, to time.Time) bool {
	begin, bok := parseGLPITime(ch.PlannedBegin)
	end, eok := parseGLPITime(ch.PlannedEnd)
	if bok || eok {
		if !bok {
			begin = end
		}
		if !eok {
			end = begin
		}
		return !end.Before(from) && !begin.After(to)
	}
	if mod, ok := parseGLPITime(ch.DateMod); ok {
		return !mod.Before(from) && !mod.After(to)
	}
	// If the installed schema omits all date fields, keep the record. The
	// relevance score still limits its usefulness to the model.
	return true
}

func parseGLPITime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func relevance(a, b string) float64 {
	aTokens := tokens(a)
	bTokens := tokens(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}
	matches := 0
	for tok := range bTokens {
		if _, ok := aTokens[tok]; ok {
			matches++
		}
	}
	den := len(bTokens)
	if len(aTokens) < den {
		den = len(aTokens)
	}
	if den == 0 {
		return 0
	}
	score := float64(matches) / float64(den)
	al, bl := strings.ToLower(a), strings.ToLower(b)
	if len(strings.TrimSpace(bl)) >= 4 && strings.Contains(al, strings.TrimSpace(bl)) {
		score += 0.25
	}
	if score > 1 {
		return 1
	}
	return score
}

var stop = map[string]struct{}{
	"der": {}, "die": {}, "das": {}, "den": {}, "dem": {}, "des": {}, "ein": {}, "eine": {}, "einer": {}, "und": {}, "oder": {}, "ist": {}, "sind": {}, "nicht": {}, "mit": {}, "von": {}, "für": {}, "fuer": {}, "auf": {}, "im": {}, "in": {}, "am": {}, "an": {}, "zu": {}, "zur": {}, "zum": {}, "seit": {}, "heute": {}, "aktuell": {}, "stoerung": {}, "störung": {}, "problem": {}, "fehler": {}, "service": {}, "ticket": {},
}

func tokens(s string) map[string]struct{} {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' })
	out := map[string]struct{}{}
	for _, p := range parts {
		p = strings.Trim(p, "-_ ")
		if len([]rune(p)) < 3 {
			continue
		}
		if _, skip := stop[p]; skip {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func trimIncidents(v []model.MajorIncidentContext, n int) []model.MajorIncidentContext {
	if len(v) > n {
		return v[:n]
	}
	return v
}

func containsPrefix(v []string, prefix string) bool {
	for _, s := range v {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

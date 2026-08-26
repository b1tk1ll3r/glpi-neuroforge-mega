package uptimekuma

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

type Client struct {
	baseURL string
	mode    string
	apiKey  string
	http    *http.Client
}

func New(baseURL, mode, apiKey string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), mode: strings.ToLower(strings.TrimSpace(mode)), apiKey: apiKey, http: &http.Client{Timeout: timeout}}
}

type statusPageResponse struct {
	Config struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"config"`
	Incident *struct {
		Title           string `json:"title"`
		Content         string `json:"content"`
		Style           string `json:"style"`
		CreatedDate     string `json:"createdDate"`
		LastUpdatedDate string `json:"lastUpdatedDate"`
		Pin             bool   `json:"pin"`
	} `json:"incident"`
	PublicGroupList []struct {
		Name        string `json:"name"`
		MonitorList []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"monitorList"`
	} `json:"publicGroupList"`
	MaintenanceList []struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Active      bool   `json:"active"`
		Status      string `json:"status"`
	} `json:"maintenanceList"`
}

type heartbeatResponse struct {
	HeartbeatList map[string][]struct {
		Status int     `json:"status"`
		Time   string  `json:"time"`
		Msg    string  `json:"msg"`
		Ping   float64 `json:"ping"`
	} `json:"heartbeatList"`
	UptimeList map[string]float64 `json:"uptimeList"`
}

func (c *Client) FetchIssues(ctx context.Context, slugs []string, includeMaintenance bool, maxIssues int) ([]model.ServiceIssueContext, error) {
	var out []model.ServiceIssueContext
	var err error
	switch c.mode {
	case "metrics":
		out, err = c.fetchMetricsIssues(ctx)
	case "status_page":
		for _, slug := range slugs {
			var issues []model.ServiceIssueContext
			issues, err = c.fetchPage(ctx, slug, includeMaintenance)
			if err != nil {
				return nil, fmt.Errorf("status page %q: %w", slug, err)
			}
			out = append(out, issues...)
		}
	default:
		return nil, fmt.Errorf("unsupported Uptime Kuma mode %q", c.mode)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return issueRank(out[i]) > issueRank(out[j]) })
	if maxIssues > 0 && len(out) > maxIssues {
		out = out[:maxIssues]
	}
	return out, nil
}

func (c *Client) fetchMetricsIssues(ctx context.Context) ([]model.ServiceIssueContext, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain")
	// Uptime Kuma documents the API key as the Basic-Auth password; the username is ignored.
	req.SetBasicAuth("glpi-ai-agent", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("metrics HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out []model.ServiceIssueContext
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 8<<20))
	buf := make([]byte, 64*1024)
	sc.Buffer(buf, 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "monitor_status{") {
			continue
		}
		labels, value, ok := parsePromSample(line)
		if !ok {
			continue
		}
		status := heartbeatStatus(int(value))
		if status == "up" {
			continue
		}
		id, _ := strconv.ParseInt(labels["monitor_id"], 10, 64)
		name := labels["monitor_name"]
		if name == "" {
			name = labels["monitor_url"]
		}
		out = append(out, model.ServiceIssueContext{Source: "uptime-kuma", StatusPage: "metrics", Kind: "monitor", MonitorID: id, MonitorName: name, Status: status, Message: labels["monitor_type"]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parsePromSample(line string) (map[string]string, float64, bool) {
	close := strings.LastIndex(line, "}")
	if close < 0 || close+1 >= len(line) {
		return nil, 0, false
	}
	open := strings.Index(line, "{")
	if open < 0 || open >= close {
		return nil, 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(line[close+1:]), 64)
	if err != nil {
		return nil, 0, false
	}
	labels := map[string]string{}
	for _, part := range splitPromLabels(line[open+1 : close]) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		v, err := strconv.Unquote(strings.TrimSpace(kv[1]))
		if err != nil {
			continue
		}
		labels[strings.TrimSpace(kv[0])] = v
	}
	return labels, value, true
}

func splitPromLabels(s string) []string {
	var out []string
	start := 0
	quoted, escaped := false, false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quoted {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
			continue
		}
		if r == ',' && !quoted {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func (c *Client) fetchPage(ctx context.Context, slug string, includeMaintenance bool) ([]model.ServiceIssueContext, error) {
	var page statusPageResponse
	if err := c.getJSON(ctx, "/api/status-page/"+url.PathEscape(slug), &page); err != nil {
		return nil, err
	}
	var hb heartbeatResponse
	if err := c.getJSON(ctx, "/api/status-page/heartbeat/"+url.PathEscape(slug), &hb); err != nil {
		return nil, err
	}
	pageName := strings.TrimSpace(page.Config.Title)
	if pageName == "" {
		pageName = slug
	}
	var out []model.ServiceIssueContext
	if page.Incident != nil && page.Incident.Pin {
		out = append(out, model.ServiceIssueContext{Source: "uptime-kuma", StatusPage: pageName, Kind: "pinned_incident", Status: "incident", IncidentTitle: page.Incident.Title, IncidentContent: page.Incident.Content, LastHeartbeat: page.Incident.LastUpdatedDate})
	}
	monitorNames := map[int64]string{}
	for _, group := range page.PublicGroupList {
		for _, mon := range group.MonitorList {
			name := strings.TrimSpace(mon.Name)
			if group.Name != "" {
				name = group.Name + " / " + name
			}
			monitorNames[mon.ID] = name
		}
	}
	for idRaw, beats := range hb.HeartbeatList {
		if len(beats) == 0 {
			continue
		}
		id, _ := strconv.ParseInt(idRaw, 10, 64)
		latest := beats[0]
		latestAt, _ := time.Parse(time.RFC3339Nano, latest.Time)
		for _, beat := range beats[1:] {
			if bt, err := time.Parse(time.RFC3339Nano, beat.Time); err == nil && (latestAt.IsZero() || bt.After(latestAt)) {
				latest = beat
				latestAt = bt
			}
		}
		status := heartbeatStatus(latest.Status)
		if status == "up" {
			continue
		}
		issue := model.ServiceIssueContext{Source: "uptime-kuma", StatusPage: pageName, Kind: "monitor", MonitorID: id, MonitorName: monitorNames[id], Status: status, Message: latest.Msg, LastHeartbeat: latest.Time}
		for _, key := range []string{fmt.Sprintf("%d_24", id), fmt.Sprintf("%d_24h", id)} {
			if v, ok := hb.UptimeList[key]; ok {
				issue.Uptime24h = v
				break
			}
		}
		out = append(out, issue)
	}
	if includeMaintenance {
		for _, m := range page.MaintenanceList {
			if m.Status != "under-maintenance" {
				continue
			}
			out = append(out, model.ServiceIssueContext{Source: "uptime-kuma", StatusPage: pageName, Kind: "maintenance", MonitorID: m.ID, MonitorName: m.Title, Status: "maintenance", Message: m.Description})
		}
	}
	return out, nil
}

func heartbeatStatus(v int) string {
	switch v {
	case 0:
		return "down"
	case 1:
		return "up"
	case 2:
		return "pending"
	case 3:
		return "maintenance"
	default:
		return "unknown"
	}
}

func issueRank(i model.ServiceIssueContext) int {
	if i.Kind == "pinned_incident" {
		return 100
	}
	switch i.Status {
	case "down":
		return 80
	case "pending":
		return 60
	case "maintenance":
		return 40
	default:
		return 10
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed engineering-graph.json
var engineeringFS embed.FS

type graphNode struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Label     string         `json:"label"`
	Group     string         `json:"group,omitempty"`
	Community string         `json:"community,omitempty"`
	Status    string         `json:"status,omitempty"`
	Score     float64        `json:"score,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}
type graphEdge struct {
	ID     string         `json:"id"`
	From   string         `json:"from"`
	To     string         `json:"to"`
	Kind   string         `json:"kind"`
	Label  string         `json:"label,omitempty"`
	Status string         `json:"status,omitempty"`
	Weight float64        `json:"weight,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}
type graphPayload struct {
	Scope string         `json:"scope"`
	Title string         `json:"title"`
	Nodes []graphNode    `json:"nodes"`
	Edges []graphEdge    `json:"edges"`
	Meta  map[string]any `json:"meta,omitempty"`
}

var engineeringOnce sync.Once
var engineeringGraph graphPayload
var engineeringErr error

func loadEngineeringGraph() (graphPayload, error) {
	engineeringOnce.Do(func() {
		b, err := engineeringFS.ReadFile("engineering-graph.json")
		if err != nil {
			engineeringErr = err
			return
		}
		engineeringErr = json.Unmarshal(b, &engineeringGraph)
	})
	return engineeringGraph, engineeringErr
}

func (s *server) handleGraphRuns(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, s.agentURL+"/api/control/runs?limit="+strconv.Itoa(boundInt(r.URL.Query().Get("limit"), 40, 1, 100)), bearerHeader(s.agentReadToken))
}
func (s *server) handleTicketGraph(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("run_id"))
	if id == "" {
		http.Error(w, "run_id required", http.StatusBadRequest)
		return
	}
	s.proxyJSON(w, r, s.agentURL+"/api/control/graph/runs/"+urlPathSegment(id), bearerHeader(s.agentReadToken))
}
func (s *server) handleLearningGraph(w http.ResponseWriter, r *http.Request) {
	limit := boundInt(r.URL.Query().Get("limit"), 180, 1, 500)
	s.proxyJSON(w, r, s.agentURL+"/api/control/graph/learning?limit="+strconv.Itoa(limit), bearerHeader(s.agentReadToken))
}
func (s *server) handleResearchGraph(w http.ResponseWriter, r *http.Request) {
	runs := boundInt(r.URL.Query().Get("runs"), 6, 1, 20)
	events := boundInt(r.URL.Query().Get("max_events"), 320, 20, 800)
	s.proxyJSON(w, r, fmt.Sprintf("%s/api/v1/integrations/graph/research?runs=%d&max_events=%d", s.neuroforgeURL, runs, events), bearerHeader(s.neuroforgeKey))
}
func (s *server) handleBrainGraph(w http.ResponseWriter, r *http.Request) {
	max := boundInt(r.URL.Query().Get("max_nodes"), 320, 50, 700)
	s.proxyJSON(w, r, fmt.Sprintf("%s/api/v1/integrations/graph/brain?max_nodes=%d", s.neuroforgeURL, max), bearerHeader(s.neuroforgeKey))
}

func (s *server) proxyJSON(w http.ResponseWriter, r *http.Request, url string, auth string) {
	if strings.TrimSpace(url) == "" {
		http.Error(w, "backend not configured", http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		http.Error(w, "graph backend unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}

func (s *server) handleRuntimeGraph(w http.ResponseWriter, r *http.Request) {
	statuses := s.statusSnapshot(r.Context())
	statusByName := map[string]status{}
	for _, st := range statuses {
		statusByName[st.Name] = st
	}
	g := graphPayload{Scope: "runtime", Title: "Runtime & Trust Boundaries", Meta: map[string]any{"read_only": true}}
	add := func(id, kind, label, group, community string, meta map[string]any) {
		statusText := "configured"
		for _, t := range s.targets {
			if t.ID == id {
				if st, ok := statusByName[t.Name]; ok {
					if st.OK {
						statusText = "online"
					} else {
						statusText = "problem"
					}
					if meta == nil {
						meta = map[string]any{}
					}
					meta["latency_ms"] = st.LatencyMS
					meta["public_url"] = st.PublicURL
				}
				break
			}
		}
		g.Nodes = append(g.Nodes, graphNode{ID: id, Kind: kind, Label: label, Group: group, Community: community, Status: statusText, Meta: meta})
	}
	add("glpi", "external", "GLPI", "external", "external", nil)
	add("agent", "service", "GLPI AI Agent", "operations", "operations", map[string]any{"authority": "policy + GLPI writes"})
	add("knowledge", "service", "Knowledgebase", "governance", "knowledge", map[string]any{"authority": "authoring + staging + promotion"})
	add("neuroforge", "service", "NeuroForge Brain", "brain", "brain", map[string]any{"authority": "memory + retrieval + research"})
	add("ollama", "model_runtime", "Ollama Pool", "runtime", "ai-runtime", nil)
	add("searxng", "research_runtime", "SearXNG", "runtime", "research", map[string]any{"optional": true, "enabled": s.searxngEnabled})
	add("control", "service", "Control Center", "observability", "control", map[string]any{"authority": "read-only"})
	if s.codebaseMemoryPublicURL != "" || s.codebaseMemoryURL != "" {
		add("codebase-memory", "engineering", "Codebase Memory MCP", "engineering", "engineering", map[string]any{"optional": true, "public_url": s.codebaseMemoryPublicURL})
	}
	edges := []graphEdge{
		{From: "glpi", To: "agent", Kind: "tickets_api", Label: "OAuth/API"}, {From: "glpi", To: "knowledge", Kind: "kb_sync", Label: "KnowbaseItem + relations"},
		{From: "agent", To: "neuroforge", Kind: "knowledge_and_outcomes", Label: "App-key scoped"}, {From: "agent", To: "ollama", Kind: "inference"},
		{From: "knowledge", To: "ollama", Kind: "draft_inference"}, {From: "knowledge", To: "neuroforge", Kind: "activity_events"},
		{From: "neuroforge", To: "ollama", Kind: "inference"}, {From: "neuroforge", To: "searxng", Kind: "research", Status: boolStatus(s.researchEnabled == "true" && s.searxngEnabled == "true")},
		{From: "control", To: "agent", Kind: "read_only_graph", Label: "CONTROL_READ_TOKEN"}, {From: "control", To: "knowledge", Kind: "health_read"}, {From: "control", To: "neuroforge", Kind: "read_only_graph", Label: "App key"},
	}
	if s.codebaseMemoryPublicURL != "" || s.codebaseMemoryURL != "" {
		edges = append(edges, graphEdge{From: "control", To: "codebase-memory", Kind: "engineering_link", Status: "optional"})
	}
	for i := range edges {
		edges[i].ID = edges[i].From + "->" + edges[i].To + ":" + edges[i].Kind
	}
	g.Edges = edges
	writeJSON(w, 200, g)
}

func (s *server) handleEngineeringGraph(w http.ResponseWriter, r *http.Request) {
	base, err := loadEngineeringGraph()
	if err != nil {
		http.Error(w, "engineering graph unavailable: "+err.Error(), 500)
		return
	}
	max := boundInt(r.URL.Query().Get("max_nodes"), 650, 50, 1400)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	kinds := csvSet(r.URL.Query().Get("kinds"))
	edgeKinds := csvSet(r.URL.Query().Get("edge_kinds"))
	selected := map[string]bool{}
	matches := func(n graphNode) bool {
		if len(kinds) > 0 && !kinds[n.Kind] {
			return false
		}
		if q == "" {
			return engineeringPriority(n.Kind) <= 4
		}
		blob := strings.ToLower(n.ID + " " + n.Label + " " + fmt.Sprint(n.Meta))
		return strings.Contains(blob, q)
	}
	candidates := append([]graphNode(nil), base.Nodes...)
	sort.SliceStable(candidates, func(i, j int) bool {
		pi, pj := engineeringPriority(candidates[i].Kind), engineeringPriority(candidates[j].Kind)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(candidates[i].Label) < strings.ToLower(candidates[j].Label)
	})
	for _, n := range candidates {
		if matches(n) && len(selected) < max {
			selected[n.ID] = true
		}
	}
	// Expand one hop around explicit search hits, then fill with structural nodes.
	if q != "" {
		for pass := 0; pass < 2 && len(selected) < max; pass++ {
			for _, e := range base.Edges {
				if !(selected[e.From] || selected[e.To]) {
					continue
				}
				if !selected[e.From] && len(selected) < max {
					selected[e.From] = true
				}
				if !selected[e.To] && len(selected) < max {
					selected[e.To] = true
				}
			}
		}
	}
	if len(selected) < max {
		for _, n := range candidates {
			if len(kinds) > 0 && !kinds[n.Kind] {
				continue
			}
			selected[n.ID] = true
			if len(selected) >= max {
				break
			}
		}
	}
	out := graphPayload{Scope: "engineering", Title: "Engineering Graph", Meta: map[string]any{"total_nodes": len(base.Nodes), "total_edges": len(base.Edges), "node_budget": max, "query": q, "codebase_memory_url": s.codebaseMemoryPublicURL}}
	for _, n := range base.Nodes {
		if selected[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range base.Edges {
		if !selected[e.From] || !selected[e.To] {
			continue
		}
		if len(edgeKinds) > 0 && !edgeKinds[e.Kind] {
			continue
		}
		out.Edges = append(out.Edges, e)
	}
	writeJSON(w, 200, out)
}

// handleEngineeringImpact returns a bounded structural blast-radius graph for
// a file, package, route or symbol query. The analysis is deliberately static
// and read-only: it expresses architectural reachability, not production risk
// certainty. Incoming and outgoing dependencies are traversed so callers can
// see both what a symbol uses and what may depend on it.
func (s *server) handleEngineeringImpact(w http.ResponseWriter, r *http.Request) {
	base, err := loadEngineeringGraph()
	if err != nil {
		http.Error(w, "engineering graph unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		http.Error(w, "q required", http.StatusBadRequest)
		return
	}
	depth := boundInt(r.URL.Query().Get("depth"), 2, 1, 4)
	max := boundInt(r.URL.Query().Get("max_nodes"), 450, 30, 1200)

	nodeByID := make(map[string]graphNode, len(base.Nodes))
	selected := map[string]bool{}
	frontier := []string{}
	for _, n := range base.Nodes {
		nodeByID[n.ID] = n
		blob := strings.ToLower(n.ID + " " + n.Label + " " + fmt.Sprint(n.Meta))
		if strings.Contains(blob, q) && len(selected) < max {
			selected[n.ID] = true
			frontier = append(frontier, n.ID)
		}
	}
	if len(frontier) == 0 {
		writeJSON(w, http.StatusOK, graphPayload{Scope: "impact", Title: "Engineering Change Impact", Meta: map[string]any{"query": q, "depth": depth, "risk": "none", "matches": 0, "node_budget": max}})
		return
	}
	seedCount := len(frontier)

	adj := make(map[string][]string, len(base.Nodes))
	for _, e := range base.Edges {
		if !impactEdgeKind(e.Kind) {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	for step := 0; step < depth && len(frontier) > 0 && len(selected) < max; step++ {
		next := make([]string, 0)
		for _, id := range frontier {
			for _, other := range adj[id] {
				if selected[other] || len(selected) >= max {
					continue
				}
				selected[other] = true
				next = append(next, other)
			}
		}
		frontier = next
	}

	out := graphPayload{Scope: "impact", Title: "Engineering Change Impact"}
	components := map[string]bool{}
	routes, services := 0, 0
	for _, n := range base.Nodes {
		if !selected[n.ID] {
			continue
		}
		if n.Group != "" {
			components[n.Group] = true
		}
		if n.Kind == "route" {
			routes++
		}
		if n.Kind == "service" {
			services++
		}
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range base.Edges {
		if selected[e.From] && selected[e.To] && impactEdgeKind(e.Kind) {
			out.Edges = append(out.Edges, e)
		}
	}
	risk := impactRisk(len(out.Nodes), len(components), routes, services)
	out.Meta = map[string]any{
		"query": q, "depth": depth, "node_budget": max, "matches": seedCount,
		"affected_nodes": len(out.Nodes), "affected_components": sortedBoolKeys(components),
		"routes": routes, "services": services, "risk": risk,
		"interpretation": "static structural reachability; validate with tests and runtime evidence before deployment",
	}
	writeJSON(w, http.StatusOK, out)
}

func impactEdgeKind(kind string) bool {
	switch kind {
	case "calls", "calls_package", "imports", "handles", "defines_route", "depends_on", "defines", "contains_file", "contains_package":
		return true
	default:
		return false
	}
}

func impactRisk(nodes, components, routes, services int) string {
	switch {
	case services > 1 || components > 2 || routes > 4 || nodes >= 120:
		return "high"
	case services > 0 || components > 1 || routes > 0 || nodes >= 35:
		return "medium"
	default:
		return "low"
	}
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if strings.TrimSpace(k) != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func boundInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}
func csvSet(v string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Split(v, ",") {
		x = strings.TrimSpace(x)
		if x != "" {
			m[x] = true
		}
	}
	return m
}
func bearerHeader(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return "Bearer " + v
}
func urlPathSegment(v string) string {
	r := strings.NewReplacer("%", "%25", "/", "%2F", "?", "%3F", "#", "%23", " ", "%20")
	return r.Replace(v)
}
func engineeringPriority(k string) int {
	switch k {
	case "component", "service":
		return 0
	case "route":
		return 1
	case "package":
		return 2
	case "file":
		return 3
	case "function":
		return 4
	default:
		return 5
	}
}
func boolStatus(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

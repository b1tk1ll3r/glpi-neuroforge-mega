package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"neuroforge/internal/core"
)

type integrationGraphNode struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Label     string         `json:"label"`
	Group     string         `json:"group,omitempty"`
	Community string         `json:"community,omitempty"`
	Status    string         `json:"status,omitempty"`
	Score     float64        `json:"score,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type integrationGraphEdge struct {
	ID     string         `json:"id"`
	From   string         `json:"from"`
	To     string         `json:"to"`
	Kind   string         `json:"kind"`
	Label  string         `json:"label,omitempty"`
	Status string         `json:"status,omitempty"`
	Weight float64        `json:"weight,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

type integrationGraphPayload struct {
	Scope string                 `json:"scope"`
	Title string                 `json:"title"`
	Nodes []integrationGraphNode `json:"nodes"`
	Edges []integrationGraphEdge `json:"edges"`
	Meta  map[string]any         `json:"meta,omitempty"`
}

// integrationResearchGraph exposes only bounded research provenance metadata.
// Full source bodies and prompts stay behind their existing dedicated APIs.
func (s *Server) integrationResearchGraph(w http.ResponseWriter, r *http.Request) {
	limit := graphBoundedInt(r.URL.Query().Get("runs"), 6, 1, 20)
	maxEvents := graphBoundedInt(r.URL.Query().Get("max_events"), 320, 20, 800)
	runs := s.store.ResearchRunsSnapshot("", limit)
	g := integrationGraphPayload{Scope: "research", Title: "Research Provenance", Meta: map[string]any{"runs": len(runs), "max_events": maxEvents}}
	seen := map[string]bool{}
	addNode := func(n integrationGraphNode) {
		if n.ID == "" || seen[n.ID] {
			return
		}
		seen[n.ID] = true
		g.Nodes = append(g.Nodes, n)
	}
	addEdge := func(e integrationGraphEdge) {
		if e.ID == "" {
			e.ID = e.From + "->" + e.To + ":" + e.Kind
		}
		g.Edges = append(g.Edges, e)
	}
	eventsLeft := maxEvents
	for _, run := range runs {
		goalID := "goal:" + run.GoalID
		runID := "research-run:" + run.ID
		addNode(integrationGraphNode{ID: goalID, Kind: "research_goal", Label: graphCompact(firstGraphNonEmpty(run.GoalTitle, run.GoalID), 90), Group: "research", Community: "goal", Status: "goal"})
		addNode(integrationGraphNode{ID: runID, Kind: "research_run", Label: graphCompact(firstGraphNonEmpty(run.GoalTitle, run.ID), 90), Group: "research", Community: "run", Status: run.Status, Meta: map[string]any{"started_at": run.StartedAt, "completed_at": run.CompletedAt, "stats": run.Stats, "last_error": graphCompact(run.LastError, 180)}})
		addEdge(integrationGraphEdge{From: goalID, To: runID, Kind: "research_cycle", Status: run.Status})
		for _, q := range run.Queries {
			qid := "query:" + run.ID + ":" + shortGraphHash(q)
			addNode(integrationGraphNode{ID: qid, Kind: "query", Label: graphCompact(q, 100), Group: "research", Community: "search", Status: "planned"})
			addEdge(integrationGraphEdge{From: runID, To: qid, Kind: "planned_query"})
		}
		for _, ev := range run.Events {
			if eventsLeft <= 0 {
				break
			}
			eventsLeft--
			qid := ""
			if strings.TrimSpace(ev.Query) != "" {
				qid = "query:" + run.ID + ":" + shortGraphHash(ev.Query)
				addNode(integrationGraphNode{ID: qid, Kind: "query", Label: graphCompact(ev.Query, 100), Group: "research", Community: "search"})
			}
			sourceID := ""
			if ev.SourceID != "" {
				sourceID = "source:" + ev.SourceID
			} else if ev.URL != "" {
				sourceID = "url:" + shortGraphHash(ev.URL)
			}
			if sourceID != "" {
				status := ev.Status
				if status == "" {
					status = "seen"
				}
				addNode(integrationGraphNode{ID: sourceID, Kind: "source", Label: graphCompact(firstGraphNonEmpty(ev.Title, ev.URL, ev.SourceID), 100), Group: "source", Community: "research-source", Status: status, Score: ev.Score, Meta: map[string]any{"url": ev.URL, "source_id": ev.SourceID, "phase": ev.Phase, "engine": ev.Metadata["engine"], "mimetype": ev.Metadata["mimetype"]}})
				from := runID
				if qid != "" {
					from = qid
				}
				addEdge(integrationGraphEdge{From: from, To: sourceID, Kind: graphResearchEdgeKind(ev.Type), Label: ev.Type, Status: ev.Status, Weight: ev.Score})
			}
			if ev.Type == "claim.extracted" {
				cid := "claim:" + run.ID + ":" + strconv.FormatUint(ev.Seq, 10)
				addNode(integrationGraphNode{ID: cid, Kind: "claim", Label: graphCompact(ev.Preview, 120), Group: "evidence", Community: "claim", Status: ev.Status, Score: ev.Confidence, Meta: map[string]any{"phase": ev.Phase, "message": graphCompact(ev.Message, 160)}})
				from := runID
				if sourceID != "" {
					from = sourceID
				}
				addEdge(integrationGraphEdge{From: from, To: cid, Kind: "claim_extracted", Status: ev.Status})
			}
			if ev.MemoryID != "" {
				mid := "memory:" + ev.MemoryID
				status := ev.Status
				if strings.Contains(ev.Type, "corroborated") {
					status = "corroborated"
				}
				if strings.Contains(ev.Type, "duplicate") {
					status = "duplicate"
				}
				addNode(integrationGraphNode{ID: mid, Kind: "memory", Label: graphCompact(firstGraphNonEmpty(ev.Preview, ev.Message, ev.MemoryID), 120), Group: "brain", Community: "evidence", Status: status, Score: firstGraphScore(ev.Confidence, ev.Similarity), Meta: map[string]any{"memory_id": ev.MemoryID, "event": ev.Type, "similarity": ev.Similarity, "confidence": ev.Confidence}})
				from := runID
				if sourceID != "" {
					from = sourceID
				}
				kind := "learned_as"
				if strings.Contains(ev.Type, "corroborated") {
					kind = "corroborates"
				} else if strings.Contains(ev.Type, "duplicate") {
					kind = "matches_existing"
				}
				addEdge(integrationGraphEdge{From: from, To: mid, Kind: kind, Label: ev.Type, Status: ev.Status, Weight: firstGraphScore(ev.Confidence, ev.Similarity)})
			}
		}
	}
	s.json(w, http.StatusOK, g)
}

// integrationBrainGraph is a bounded, redacted operational graph. It is not a
// memory export: vectors and full text are omitted, and the caller controls only
// the visualization window size.
func (s *Server) integrationBrainGraph(w http.ResponseWriter, r *http.Request) {
	maxNodes := graphBoundedInt(r.URL.Query().Get("max_nodes"), 320, 50, 700)
	memories := s.store.MemoriesSnapshot()
	sort.SliceStable(memories, func(i, j int) bool {
		a, b := memoryGraphPriority(memories[i]), memoryGraphPriority(memories[j])
		if a == b {
			return memories[i].CreatedAt.After(memories[j].CreatedAt)
		}
		return a > b
	})
	if len(memories) > maxNodes {
		memories = memories[:maxNodes]
	}
	g := integrationGraphPayload{Scope: "brain", Title: "NeuroForge Brain", Meta: map[string]any{"nodes_budget": maxNodes, "total_memories": len(s.store.MemoriesSnapshot())}}
	seen := map[string]core.Memory{}
	for _, m := range memories {
		seen[m.ID] = m
		label := graphCompact(firstGraphNonEmpty(m.Provenance.SourceTitle, m.TruthKey, m.Text, m.ID), 110)
		community := m.Provenance.Source
		if community == "" {
			community = m.MemoryType
		}
		g.Nodes = append(g.Nodes, integrationGraphNode{ID: "memory:" + m.ID, Kind: "memory_" + m.MemoryType, Label: label, Group: "brain", Community: graphCompact(community, 48), Status: firstGraphNonEmpty(m.Status, core.MemoryActive), Score: m.Salience, Meta: map[string]any{"memory_id": m.ID, "kind": m.Kind, "source": m.Provenance.Source, "confidence": m.Confidence, "reward": m.Reward, "salience": m.Salience, "access_count": m.AccessCount, "created_at": m.CreatedAt, "source_id": m.Provenance.SourceMemoryID}})
	}
	for _, syn := range s.store.SynapsesSnapshot() {
		_, aok := seen[syn.A]
		_, bok := seen[syn.B]
		if !aok || !bok {
			continue
		}
		g.Edges = append(g.Edges, integrationGraphEdge{ID: "syn:" + syn.A + ":" + syn.B, From: "memory:" + syn.A, To: "memory:" + syn.B, Kind: "synapse", Weight: syn.Weight, Meta: map[string]any{"similarity": syn.Similarity, "activations": syn.Activations}})
	}
	for _, m := range memories {
		for _, old := range m.Supersedes {
			if _, ok := seen[old]; ok {
				g.Edges = append(g.Edges, integrationGraphEdge{From: "memory:" + m.ID, To: "memory:" + old, Kind: "supersedes", Status: "active", Weight: 1})
			}
		}
		for _, old := range m.ConsolidatedFrom {
			if _, ok := seen[old]; ok {
				g.Edges = append(g.Edges, integrationGraphEdge{From: "memory:" + old, To: "memory:" + m.ID, Kind: "consolidated_into", Weight: 1})
			}
		}
	}
	s.json(w, http.StatusOK, g)
}

func graphBoundedInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}
func graphCompact(v string, n int) string {
	v = strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
	rr := []rune(v)
	if n > 0 && len(rr) > n {
		return string(rr[:n]) + "…"
	}
	return v
}
func firstGraphNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}
func firstGraphScore(xs ...float64) float64 {
	for _, x := range xs {
		if x != 0 {
			return x
		}
	}
	return 0
}
func shortGraphHash(s string) string {
	var h uint64 = 1469598103934665603
	for _, b := range []byte(s) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%x", h)
}
func graphResearchEdgeKind(t string) string {
	if strings.HasPrefix(t, "search.") {
		return "search_result"
	}
	if strings.HasPrefix(t, "download.") {
		return "fetched"
	}
	if strings.HasPrefix(t, "source.") {
		return "source_event"
	}
	return "research_event"
}
func memoryGraphPriority(m core.Memory) float64 {
	p := m.Salience + m.Confidence*.5 + float64(m.AccessCount)*.01
	if m.Status == core.MemoryActive {
		p += .5
	}
	if strings.HasPrefix(m.Provenance.Source, "glpi.outcome.") {
		p += 1
	}
	if strings.HasPrefix(m.Provenance.Source, "integration:") {
		p += .4
	}
	return p
}

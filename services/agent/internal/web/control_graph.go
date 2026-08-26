package web

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/model"
)

// graphNode/graphEdge are deliberately generic. They form the small, read-only
// interchange contract consumed by the Mega Control Center. The contract does
// not expose raw prompts, credentials or provider URLs.
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

func (s *Server) controlReadAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(s.cfg.ControlReadToken)
		if token == "" {
			http.NotFound(w, r)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) controlRuns(w http.ResponseWriter, r *http.Request) {
	limit := boundedInt(r.URL.Query().Get("limit"), 40, 1, 100)
	runs := s.state.Recent(limit)
	type runSummary struct {
		RunID       string  `json:"run_id"`
		TicketID    int64   `json:"ticket_id"`
		TicketName  string  `json:"ticket_name"`
		Outcome     string  `json:"outcome"`
		Trigger     string  `json:"trigger,omitempty"`
		KnowledgeID string  `json:"knowledge_id,omitempty"`
		Score       float64 `json:"knowledge_score,omitempty"`
		Reply       bool    `json:"reply_proposed"`
		FinishedAt  any     `json:"finished_at"`
	}
	out := make([]runSummary, 0, len(runs))
	for _, x := range runs {
		out = append(out, runSummary{RunID: x.RunID, TicketID: x.TicketID, TicketName: x.TicketName, Outcome: x.Outcome, Trigger: x.Trigger, KnowledgeID: x.KnowledgeID, Score: x.KnowledgeScore, Reply: x.ReplyProposed, FinishedAt: x.FinishedAt})
	}
	respondJSON(w, out)
}

func (s *Server) controlRunGraph(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("id"))
	run, ok := s.state.FindRun(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	respondJSON(w, buildRunGraph(run, s.feedback.TicketOutcomes()))
}

func (s *Server) controlLearningGraph(w http.ResponseWriter, r *http.Request) {
	limit := boundedInt(r.URL.Query().Get("limit"), 180, 1, 500)
	items := s.feedback.TicketOutcomes()
	if len(items) > limit {
		items = items[:limit]
	}
	respondJSON(w, buildLearningGraph(items))
}

func boundedInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func buildRunGraph(run model.RunRecord, outcomes []learning.TicketOutcome) graphPayload {
	g := graphPayload{Scope: "ticket", Title: fmt.Sprintf("Ticket #%d · %s", run.TicketID, run.TicketName), Meta: map[string]any{"run_id": run.RunID, "ticket_id": run.TicketID, "outcome": run.Outcome, "trigger": run.Trigger, "dry_run": run.DryRun}}
	seen := map[string]bool{}
	addNode := func(n graphNode) {
		if n.ID == "" || seen[n.ID] {
			return
		}
		seen[n.ID] = true
		g.Nodes = append(g.Nodes, n)
	}
	addEdge := func(e graphEdge) {
		if e.ID == "" {
			e.ID = e.From + "->" + e.To + ":" + e.Kind
		}
		g.Edges = append(g.Edges, e)
	}

	ticketID := fmt.Sprintf("ticket:%d", run.TicketID)
	runNode := "run:" + run.RunID
	addNode(graphNode{ID: ticketID, Kind: "ticket", Label: fmt.Sprintf("#%d · %s", run.TicketID, compactGraph(run.TicketName, 72)), Group: "ticket", Community: "decision", Status: run.Outcome, Meta: map[string]any{"source_version": run.SourceVersion, "trigger": run.Trigger}})
	addNode(graphNode{ID: runNode, Kind: "run", Label: "AI Run", Group: "decision", Community: "decision", Status: run.Outcome, Meta: map[string]any{"reason": run.Reason, "policy_reason": run.PolicyReason, "started_at": run.StartedAt, "finished_at": run.FinishedAt}})
	addEdge(graphEdge{From: ticketID, To: runNode, Kind: "analysed_by", Label: run.Trigger})

	if run.CategoryBefore > 0 {
		id := fmt.Sprintf("category:%d", run.CategoryBefore)
		label := run.CategoryBeforeName
		if label == "" {
			label = fmt.Sprintf("Kategorie #%d", run.CategoryBefore)
		}
		addNode(graphNode{ID: id, Kind: "category", Label: label, Group: "policy", Community: "classification", Status: "current"})
		addEdge(graphEdge{From: ticketID, To: id, Kind: "categorized_as", Status: "current"})
	}
	if run.AIRecommendedCategoryID > 0 {
		id := fmt.Sprintf("category:%d", run.AIRecommendedCategoryID)
		label := run.AIRecommendedCategoryName
		if label == "" {
			label = fmt.Sprintf("Kategorie #%d", run.AIRecommendedCategoryID)
		}
		addNode(graphNode{ID: id, Kind: "category", Label: label, Group: "policy", Community: "classification", Status: run.CategoryDecision, Score: run.AICategoryConfidence})
		addEdge(graphEdge{From: runNode, To: id, Kind: "recommended_category", Label: run.CategoryDecision, Weight: run.AICategoryConfidence})
	}

	candidates := run.ReplyKnowledgeCandidates
	if len(candidates) == 0 {
		candidates = run.KnowledgeCandidates
	}
	if len(candidates) > 24 {
		candidates = candidates[:24]
	}
	for _, c := range candidates {
		id := "knowledge:" + c.ID
		status := "candidate"
		if c.ID == run.KnowledgeID || c.ID == run.AIKnowledgeID {
			status = "selected"
		}
		addNode(graphNode{ID: id, Kind: "knowledge", Label: compactGraph(c.Title, 80), Group: "knowledge", Community: "evidence", Status: status, Score: c.Score, Meta: map[string]any{"source": c.Source, "semantic_score": c.SemanticScore, "category_score": c.CategoryScore, "auto_reply": c.AutoReply, "selection_reason": c.SelectionReason, "excerpt": compactGraph(c.BestChunkExcerpt, 220)}})
		addEdge(graphEdge{From: runNode, To: id, Kind: "retrieved", Label: fmt.Sprintf("rank %d", c.RetrievalRank), Weight: c.Score, Status: status})
	}

	for _, x := range run.ValidatedOutcomeCandidates {
		id := "memory:" + x.MemoryID
		addNode(graphNode{ID: id, Kind: "validated_outcome", Label: compactGraph(x.Text, 110), Group: "learning", Community: "evidence", Status: x.Decision, Score: x.Similarity, Meta: map[string]any{"source": x.Source, "ticket_id": x.TicketID, "outcome_id": x.OutcomeID, "knowledge_id": x.KnowledgeID}})
		addEdge(graphEdge{From: runNode, To: id, Kind: "experience_evidence", Label: x.Decision, Weight: x.Similarity})
	}

	checks := append([]model.RuleCheck(nil), run.CategoryChecks...)
	checks = append(checks, run.ReplyChecks...)
	checks = append(checks, run.ExecutionChecks...)
	for i, c := range checks {
		id := fmt.Sprintf("check:%d:%s", i, c.Code)
		addNode(graphNode{ID: id, Kind: "policy_check", Label: compactGraph(c.Label, 90), Group: "policy", Community: "gates", Status: c.Status, Meta: map[string]any{"code": c.Code, "blocking": c.Blocking, "actual": c.Actual, "expected": c.Expected, "detail": compactGraph(c.Detail, 220)}})
		addEdge(graphEdge{From: runNode, To: id, Kind: "checked", Status: c.Status, Weight: boolWeight(c.Blocking)})
	}

	for i, c := range run.ContextDetails {
		id := fmt.Sprintf("context:%s:%d:%d", c.Kind, c.ID, i)
		addNode(graphNode{ID: id, Kind: "context_" + c.Kind, Label: compactGraph(c.Name, 90), Group: "context", Community: "context", Status: c.Status, Score: c.Relevance, Meta: map[string]any{"detail": compactGraph(c.Detail, 220)}})
		addEdge(graphEdge{From: ticketID, To: id, Kind: "context", Weight: c.Relevance})
	}

	for _, a := range run.Analyses {
		id := "analysis:" + a.AnalysisID
		addNode(graphNode{ID: id, Kind: "analysis", Label: strings.Title(strings.ReplaceAll(a.AnalysisType, "_", " ")), Group: "analysis", Community: "decision", Status: a.Outcome, Score: a.Confidence, Meta: map[string]any{"duration_ms": a.DurationMS, "model": a.Model, "prompt_version": a.PromptVersion, "reason_codes": a.ReasonCodes, "explanation": compactGraph(a.Explanation, 260)}})
		addEdge(graphEdge{From: runNode, To: id, Kind: "analysis_stage", Weight: a.Confidence})
		for _, attempt := range a.Provider.Attempts {
			node := "model:" + attempt.NodeName + ":" + attempt.ModelDigest
			addNode(graphNode{ID: node, Kind: "model_node", Label: attempt.NodeName, Group: "runtime", Community: "runtime", Status: attempt.Outcome, Meta: map[string]any{"model_digest": attempt.ModelDigest, "duration_ms": attempt.DurationMS, "http_status": attempt.HTTPStatus}})
			addEdge(graphEdge{From: id, To: node, Kind: "executed_on", Status: attempt.Outcome})
		}
	}

	if run.ReplyProposed || run.ReplyProposedText != "" {
		status := "proposed"
		if run.ReplyWritten {
			status = "written"
		}
		if run.ReplyDecision != "" && !run.ReplyProposed {
			status = "blocked"
		}
		replyID := "reply:" + run.RunID
		addNode(graphNode{ID: replyID, Kind: "reply", Label: compactGraph(run.ReplyProposedText, 120), Group: "decision", Community: "decision", Status: status, Score: run.AIReplyConfidence, Meta: map[string]any{"decision": run.ReplyDecision, "knowledge_id": run.KnowledgeID, "written": run.ReplyWritten}})
		addEdge(graphEdge{From: runNode, To: replyID, Kind: "proposed_reply", Status: status, Weight: run.AIReplyConfidence})
	}

	byID := map[string]learning.TicketOutcome{}
	for _, x := range outcomes {
		byID[x.ID] = x
	}
	for _, x := range outcomes {
		if x.RunID != run.RunID {
			continue
		}
		appendOutcomeToGraph(&g, seen, x, byID, ticketID, "reply:"+run.RunID)
	}

	return g
}

func buildLearningGraph(items []learning.TicketOutcome) graphPayload {
	g := graphPayload{Scope: "learning", Title: "Learning Lineage", Meta: map[string]any{"outcomes": len(items)}}
	seen := map[string]bool{}
	byID := make(map[string]learning.TicketOutcome, len(items))
	for _, x := range items {
		byID[x.ID] = x
	}
	for _, x := range items {
		ticketID := fmt.Sprintf("ticket:%d", x.TicketID)
		if !seen[ticketID] {
			seen[ticketID] = true
			g.Nodes = append(g.Nodes, graphNode{ID: ticketID, Kind: "ticket", Label: fmt.Sprintf("Ticket #%d", x.TicketID), Group: "ticket", Community: "learning"})
		}
		appendOutcomeToGraph(&g, seen, x, byID, ticketID, "")
	}
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	return g
}

func appendOutcomeToGraph(g *graphPayload, seen map[string]bool, x learning.TicketOutcome, byID map[string]learning.TicketOutcome, ticketID, replyID string) {
	id := "outcome:" + x.ID
	if !seen[id] {
		seen[id] = true
		g.Nodes = append(g.Nodes, graphNode{ID: id, Kind: "human_outcome", Label: compactGraph(x.ConfirmedReply, 110), Group: "learning", Community: "learning", Status: x.Decision, Meta: map[string]any{"actor": x.Actor, "created_at": x.CreatedAt, "sync_status": x.SyncStatus, "note": compactGraph(x.Note, 180), "run_id": x.RunID}})
	}
	from := ticketID
	if replyID != "" && seen[replyID] {
		from = replyID
	}
	g.Edges = append(g.Edges, graphEdge{ID: from + "->" + id, From: from, To: id, Kind: "validated_by", Label: x.Decision, Status: x.SyncStatus, Weight: 1})
	if x.KnowledgeID != "" {
		kid := "knowledge:" + x.KnowledgeID
		if !seen[kid] {
			seen[kid] = true
			g.Nodes = append(g.Nodes, graphNode{ID: kid, Kind: "knowledge", Label: x.KnowledgeID, Group: "knowledge", Community: "learning"})
		}
		g.Edges = append(g.Edges, graphEdge{ID: id + "->" + kid, From: id, To: kid, Kind: "based_on"})
	}
	if x.NeuroForgeID != "" {
		mid := "memory:" + x.NeuroForgeID
		if !seen[mid] {
			seen[mid] = true
			g.Nodes = append(g.Nodes, graphNode{ID: mid, Kind: "memory", Label: "NeuroForge Memory", Group: "brain", Community: "learning", Status: x.SyncStatus, Meta: map[string]any{"memory_id": x.NeuroForgeID}})
		}
		g.Edges = append(g.Edges, graphEdge{ID: id + "->" + mid, From: id, To: mid, Kind: "learned_as", Status: x.SyncStatus})
	}
	if x.SupersedesID != "" {
		prevID := "outcome:" + x.SupersedesID
		if prev, ok := byID[x.SupersedesID]; ok && !seen[prevID] {
			seen[prevID] = true
			g.Nodes = append(g.Nodes, graphNode{ID: prevID, Kind: "human_outcome", Label: compactGraph(prev.ConfirmedReply, 110), Group: "learning", Community: "learning", Status: "superseded"})
		}
		g.Edges = append(g.Edges, graphEdge{ID: id + "->" + prevID, From: id, To: prevID, Kind: "supersedes", Status: "active", Weight: 1})
	}
}

func compactGraph(v string, n int) string {
	v = strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
	if n <= 0 {
		return v
	}
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return string(r[:n]) + "…"
}

func boolWeight(v bool) float64 {
	if v {
		return 1
	}
	return .25
}

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/model"
)

func TestControlReadAuthIsScopedBearerOnly(t *testing.T) {
	s := &Server{cfg: config.Config{ControlReadToken: "01234567890123456789012345678901"}}
	h := s.controlReadAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer no", http.StatusUnauthorized},
		{"valid", "Bearer 01234567890123456789012345678901", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/control/runs", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d", rr.Code, tc.want)
			}
		})
	}
}

func TestBuildRunGraphContainsEvidencePoliciesAndOutcome(t *testing.T) {
	r := model.RunRecord{RunID: "run-1", TicketID: 42, TicketName: "VPN geht nicht", Outcome: "processed", KnowledgeID: "KB-1", ReplyProposed: true, ReplyProposedText: "VPN neu verbinden", AIReplyConfidence: .94,
		ReplyKnowledgeCandidates:   []model.KnowledgeCandidateAudit{{ID: "KB-1", Title: "VPN", Source: "internal-kb", Score: .91, RetrievalRank: 1, AutoReply: true}},
		ValidatedOutcomeCandidates: []model.ValidatedOutcomeEvidence{{MemoryID: "m-old", OutcomeID: "o-old", Decision: "accepted", Text: "Adapter reset", Similarity: .82, Source: "glpi.outcome.accepted"}},
		ReplyChecks:                []model.RuleCheck{{Code: "evidence", Label: "Evidence ausreichend", Status: "pass", Blocking: true}},
	}
	outs := []learning.TicketOutcome{{ID: "o1", RunID: "run-1", TicketID: 42, Decision: "corrected", ConfirmedReply: "VPN Adapter neu starten", NeuroForgeID: "m1", SyncStatus: "learned"}}
	g := buildRunGraph(r, outs)
	kinds := map[string]bool{}
	for _, n := range g.Nodes {
		kinds[n.Kind] = true
	}
	for _, want := range []string{"ticket", "run", "knowledge", "validated_outcome", "policy_check", "reply", "human_outcome", "memory"} {
		if !kinds[want] {
			t.Fatalf("missing node kind %q in %#v", want, kinds)
		}
	}
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.Kind] = true
	}
	for _, want := range []string{"retrieved", "experience_evidence", "checked", "proposed_reply", "validated_by", "learned_as"} {
		if !edges[want] {
			t.Fatalf("missing edge kind %q in %#v", want, edges)
		}
	}
}

func TestBuildLearningGraphPreservesSupersession(t *testing.T) {
	items := []learning.TicketOutcome{{ID: "new", RunID: "r", TicketID: 7, Decision: "corrected", ConfirmedReply: "new", SupersedesID: "old", SyncStatus: "learned"}, {ID: "old", RunID: "r", TicketID: 7, Decision: "accepted", ConfirmedReply: "old", SyncStatus: "learned"}}
	g := buildLearningGraph(items)
	found := false
	for _, e := range g.Edges {
		if e.Kind == "supersedes" && e.From == "outcome:new" && e.To == "outcome:old" {
			found = true
		}
	}
	if !found {
		t.Fatalf("supersession edge missing: %+v", g.Edges)
	}
}

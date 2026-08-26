package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
)

func escalationTestConfig() config.Config {
	return config.Config{
		EscalationEnabled:               true,
		AutoEscalation:                  true,
		EscalationMinAge:                time.Hour,
		EscalationMinInactivity:         30 * time.Minute,
		EscalationConfidence:            .8,
		EscalationMaxLevel:              4,
		EscalationSLARiskWindow:         2 * time.Hour,
		EscalationServiceOwnerMinLevel:  2,
		EscalationManagerReviewMinLevel: 3,
		EscalationMajorIncidentMinScore: .5,
		EscalationAllowedReasonCodes:    []string{"no_human_response", "unassigned", "sla_at_risk", "sla_breached", "business_deadline", "no_workaround", "security_incident_suspected", "major_incident_candidate"},
		EscalationAllowedActions:        []string{"none", "raise_priority", "assign_second_level", "assign_security_team", "notify_service_owner", "link_major_incident", "request_manager_review"},
		EscalationSecondLevelGroupID:    42,
		EscalationSecurityGroupID:       51,
		EscalationServiceOwnerGroupID:   61,
		EscalationServiceOwnerUserID:    62,
		EscalationManagerReviewGroupID:  71,
		EscalationManagerReviewUserID:   72,
		EscalationAddPrivateFollowup:    true,
		EscalationSecondLevelNote:       "Second Level {{ticket_id}} {{level}} {{reason_codes}}",
		EscalationSecurityNote:          "Security {{ticket_id}} {{reason}}",
		EscalationServiceOwnerNote:      "Owner {{ticket_id}}",
		EscalationMajorIncidentNote:     "Major {{major_incident_id}} {{major_incident_name}}",
		EscalationManagerReviewNote:     "Manager {{ticket_id}}",
		GLPIEscalationGroupPatchField:   "assigned_groups",
		GLPIEscalationUserPatchField:    "assigned_users",
		GLPIEscalationITILLinkPath:      "/ITIL/Link",
		GLPIEscalationITILLinkBody:      `{"source":{{ticket_id}},"target":{{major_incident_id}}}`,
		GLPIAgentUserID:                 999,
	}
}

func TestBuildEscalationEvidenceUsesInactivitySLAAndMajorIncident(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC)
	ticket := model.Ticket{
		ID:            10,
		DateCreation:  now.Add(-5 * time.Hour).Format(time.RFC3339),
		TimeToResolve: now.Add(90 * time.Minute).Format(time.RFC3339),
	}
	followups := []model.Followup{
		{ID: 1, UserID: cfg.GLPIAgentUserID, Date: now.Add(-10 * time.Minute).Format(time.RFC3339)},
		{ID: 2, UserID: 123, Date: now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	contextData := model.ContextSnapshot{MajorIncidents: []model.MajorIncidentContext{
		{ID: 80, Name: "weniger relevant", Relevance: .6},
		{ID: 81, Name: "Druckausfall", Relevance: .9},
	}}

	e := buildEscalationEvidence(cfg, ticket, followups, contextData, now)
	if !e.NoHumanResponse || e.Unassigned != true || !e.SLAAtRisk || e.SLABreached {
		t.Fatalf("unexpected deterministic evidence: %+v", e)
	}
	if e.MajorIncidentID != 81 || e.MajorIncidentName != "Druckausfall" {
		t.Fatalf("wrong major incident selected: %+v", e)
	}

	followups = append(followups, model.Followup{ID: 3, UserID: 124, Date: now.Add(-10 * time.Minute).Format(time.RFC3339)})
	e = buildEscalationEvidence(cfg, ticket, followups, contextData, now)
	if e.NoHumanResponse {
		t.Fatalf("recent human activity was ignored: %+v", e)
	}
}

func TestEvaluateEscalationAcceptsConfiguredActionPlan(t *testing.T) {
	cfg := escalationTestConfig()
	st, err := state.Open(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ticket := model.Ticket{ID: 20, DateCreation: now.Add(-4 * time.Hour).Format(time.RFC3339), Priority: 3}
	contextData := model.ContextSnapshot{MajorIncidents: []model.MajorIncidentContext{{ID: 200, Name: "Standortausfall", Relevance: .95}}}
	decision := model.EscalationDecision{
		Escalate: true, Level: 3,
		RecommendedActions: []string{"assign_security_team", "link_major_incident", "request_manager_review"},
		ReasonCodes:        []string{"no_human_response", "security_incident_suspected", "major_incident_candidate"},
		Confidence:         .95, Reason: "Mehrere kontrollierte Eskalationssignale liegen vor.",
	}

	result := evaluateEscalation(cfg, st, ticket, nil, contextData, decision, now)
	if !result.Accepted || result.Decision != "escalation_accepted" || len(result.Actions) != 3 {
		t.Fatalf("unexpected escalation result: %+v", result)
	}
	for _, action := range result.Actions {
		if !action.Accepted || action.IdempotencyKey == "" {
			t.Fatalf("action was not accepted: %+v", action)
		}
	}
	if result.Actions[1].Target != "ticket:200" {
		t.Fatalf("major incident target=%q", result.Actions[1].Target)
	}
}

func TestEvaluateEscalationBlocksSecurityWithoutSecurityReason(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Now()
	ticket := model.Ticket{ID: 21, DateCreation: now.Add(-4 * time.Hour).Format(time.RFC3339), Priority: 3}
	decision := model.EscalationDecision{
		Escalate: true, Level: 2, RecommendedActions: []string{"assign_security_team"},
		ReasonCodes: []string{"no_human_response"}, Confidence: .95, Reason: "Keine Reaktion.",
	}
	result := evaluateEscalation(cfg, nil, ticket, nil, model.ContextSnapshot{}, decision, now)
	if result.Accepted || len(result.Actions) != 1 || result.Actions[0].Decision != "escalation_action_prerequisite_missing" {
		t.Fatalf("security action was not blocked: %+v", result)
	}
}

func TestExecuteEscalationPlanWritesMultipleIndependentActions(t *testing.T) {
	cfg := escalationTestConfig()
	g := &fakeGLPI{ticket: model.Ticket{ID: 30, Priority: 3, AssignedGroups: []int64{8}}}
	svc := &Service{cfg: cfg, glpi: g, metrics: metrics.New()}
	decision := model.EscalationDecision{Escalate: true, Level: 2, ReasonCodes: []string{"no_human_response", "unassigned", "security_incident_suspected"}, Reason: "Test", Confidence: .95}
	result := model.EscalationResult{Accepted: true, Decision: "escalation_accepted", Actions: []model.EscalationActionResult{
		{Action: "raise_priority", Accepted: true, IdempotencyKey: "ticket=30;level=2;action=raise_priority;target=priority:4"},
		{Action: "assign_second_level", Target: "group:42", Accepted: true, IdempotencyKey: "ticket=30;level=2;action=assign_second_level;target=group:42"},
		{Action: "assign_security_team", Target: "group:51", Accepted: true, IdempotencyKey: "ticket=30;level=2;action=assign_security_team;target=group:51"},
	}}

	audit, err := svc.executeEscalationPlan(context.Background(), g.ticket, decision, result, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Executed || len(audit.Steps) != 3 || g.priorityValue != 4 {
		t.Fatalf("unexpected action audit: %+v priority=%d", audit, g.priorityValue)
	}
	if len(g.assignedGroups) != 3 || g.assignedGroups[0] != 8 || g.assignedGroups[1] != 42 || g.assignedGroups[2] != 51 {
		t.Fatalf("assignments were not merged: %v", g.assignedGroups)
	}
	if len(g.privateNotes) != 3 {
		t.Fatalf("private notes=%d want 3", len(g.privateNotes))
	}
	for _, step := range audit.Steps {
		if !step.Executed || step.Result == "" {
			t.Fatalf("incomplete action step: %+v", step)
		}
	}
}

func TestServiceOwnerActionSendsAuthenticatedIdempotentWebhook(t *testing.T) {
	var got map[string]any
	var gotAuth, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := escalationTestConfig()
	cfg.EscalationWebhookURL = server.URL
	cfg.EscalationWebhookBearerToken = "secret"
	cfg.EscalationWebhookTimeout = time.Second
	g := &fakeGLPI{ticket: model.Ticket{ID: 40, Name: "Service gestört", Priority: 4}}
	svc := &Service{cfg: cfg, glpi: g, metrics: metrics.New()}
	decision := model.EscalationDecision{Escalate: true, Level: 2, ReasonCodes: []string{"business_deadline"}, Reason: "Frist gefährdet", Confidence: .93}
	action := model.EscalationActionResult{Action: "notify_service_owner", Target: "group:61,user:62,webhook", Accepted: true, IdempotencyKey: "ticket=40;level=2;action=notify_service_owner;target=group:61,user:62,webhook"}

	warnings, err := svc.executeEscalationAction(context.Background(), &g.ticket, decision, action, model.ContextSnapshot{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("action error=%v warnings=%v", err, warnings)
	}
	if gotAuth != "Bearer secret" || gotKey != action.IdempotencyKey || got["action"] != "notify_service_owner" {
		t.Fatalf("unexpected webhook auth=%q key=%q body=%v", gotAuth, gotKey, got)
	}
	if len(g.assignedGroups) != 1 || g.assignedGroups[0] != 61 || len(g.assignedUsers) != 1 || g.assignedUsers[0] != 62 {
		t.Fatalf("service owner targets not assigned: groups=%v users=%v", g.assignedGroups, g.assignedUsers)
	}
}

func TestMajorIncidentActionUsesDeterministicTarget(t *testing.T) {
	cfg := escalationTestConfig()
	g := &fakeGLPI{ticket: model.Ticket{ID: 50, Priority: 4}}
	svc := &Service{cfg: cfg, glpi: g, metrics: metrics.New()}
	contextData := model.ContextSnapshot{MajorIncidents: []model.MajorIncidentContext{{ID: 500, Name: "A", Relevance: .7}, {ID: 501, Name: "B", Relevance: .9}}}
	decision := model.EscalationDecision{Escalate: true, Level: 2, ReasonCodes: []string{"major_incident_candidate"}, Reason: "Passender Major Incident", Confidence: .95}
	action := model.EscalationActionResult{Action: "link_major_incident", Target: "ticket:501", Accepted: true, IdempotencyKey: "ticket=50;level=2;action=link_major_incident;target=ticket:501"}

	warnings, err := svc.executeEscalationAction(context.Background(), &g.ticket, decision, action, contextData)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("action error=%v warnings=%v", err, warnings)
	}
	if len(g.linkedTargets) != 1 || g.linkedTargets[0] != 501 || len(g.privateNotes) != 1 {
		t.Fatalf("major incident action incomplete: links=%v notes=%v", g.linkedTargets, g.privateNotes)
	}
}

func TestEvaluateEscalationBlocksHallucinatedDeterministicReason(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Now()
	ticket := model.Ticket{ID: 22, DateCreation: now.Add(-4 * time.Hour).Format(time.RFC3339), Priority: 3}
	decision := model.EscalationDecision{
		Escalate: true, Level: 2, RecommendedActions: []string{"raise_priority"},
		ReasonCodes: []string{"sla_breached"}, Confidence: .95, Reason: "SLA angeblich verletzt.",
	}
	result := evaluateEscalation(cfg, nil, ticket, nil, model.ContextSnapshot{}, decision, now)
	if result.Accepted || result.Decision != "escalation_reason_not_evidenced" {
		t.Fatalf("hallucinated SLA reason was not blocked: %+v", result)
	}
}

func TestLastHumanFollowupParsesMixedDateFormats(t *testing.T) {
	followups := []model.Followup{
		{ID: 1, UserID: 1, Date: "2026-08-02 12:00:00"},
		{ID: 2, UserID: 2, Date: "2026-08-02T13:00:00Z"},
		{ID: 3, UserID: 999, Date: "2026-08-02T14:00:00Z"},
	}
	got := lastHumanFollowup(followups, 999)
	if got == nil || got.ID != 2 {
		t.Fatalf("latest human followup=%+v", got)
	}
}

func TestPrimaryEscalationWriteRemainsExecutedWhenPrivateNoteFails(t *testing.T) {
	cfg := escalationTestConfig()
	g := &fakeGLPI{ticket: model.Ticket{ID: 31, Priority: 3}, privateNoteErr: errors.New("followup forbidden")}
	svc := &Service{cfg: cfg, glpi: g, metrics: metrics.New()}
	decision := model.EscalationDecision{Escalate: true, Level: 1, ReasonCodes: []string{"no_human_response"}, Reason: "Test", Confidence: .95}
	result := model.EscalationResult{Accepted: true, Decision: "escalation_accepted", Actions: []model.EscalationActionResult{
		{Action: "raise_priority", Accepted: true, IdempotencyKey: "ticket=31;level=1;action=raise_priority;target=priority:4"},
	}}

	audit, err := svc.executeEscalationPlan(context.Background(), g.ticket, decision, result, model.ContextSnapshot{})
	if err != nil {
		t.Fatalf("ancillary note error must not fail the primary write: %v", err)
	}
	if !audit.Executed || len(audit.Steps) != 1 || !audit.Steps[0].Executed || audit.Steps[0].Error == "" || g.priorityValue != 4 {
		t.Fatalf("unexpected warning audit: %+v priority=%d", audit, g.priorityValue)
	}
}

func TestLiveEscalationRechecksFollowupsBeforeWrite(t *testing.T) {
	now := time.Now()
	g := &fakeGLPI{
		ticket: model.Ticket{ID: 60, Name: "Alt", DateCreation: now.Add(-5 * time.Hour).Format(time.RFC3339), DateMod: "v1", StatusID: 1, Priority: 3},
		cats:   []model.Category{{ID: 1}}, injectFollowupOnSecondCheck: true,
	}
	svc := newTestService(t, g, model.Decision{}, false)
	svc.cfg.EscalationEnabled = true
	svc.cfg.AutoEscalation = true
	svc.cfg.DryRun = false
	svc.cfg.EscalationMinAge = time.Hour
	svc.cfg.EscalationMinInactivity = time.Hour
	svc.cfg.EscalationConfidence = .8
	svc.cfg.EscalationMaxLevel = 3
	svc.cfg.EscalationAllowedReasonCodes = []string{"no_human_response"}
	svc.cfg.EscalationAllowedActions = []string{"raise_priority"}
	svc.cfg.GLPIAgentUserID = 999
	svc.ai = fakeAI{escalation: model.EscalationDecision{Escalate: true, Level: 1, RecommendedActions: []string{"raise_priority"}, ReasonCodes: []string{"no_human_response"}, Confidence: .95, Reason: "Keine Bearbeitung."}}

	if err := svc.ProcessWork(context.Background(), queue.WorkItem{TicketID: 60, Trigger: "scheduled_escalation"}); err != nil {
		t.Fatal(err)
	}
	if g.setPriority != 0 {
		t.Fatalf("priority was written despite a fresh human followup: %d", g.setPriority)
	}
	run := svc.state.Recent(1)[0]
	if run.PolicyReason != "escalation_recent_human_activity" || len(run.Analyses) != 1 {
		t.Fatalf("unexpected prewrite result: %+v", run)
	}
}

func TestEscalationFailsClosedForUnparseableHumanFollowupDate(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Now()
	ticket := model.Ticket{ID: 63, DateCreation: now.Add(-5 * time.Hour).Format(time.RFC3339), Priority: 3}
	followups := []model.Followup{{ID: 8, UserID: 123, Date: "unbekannt"}}
	decision := model.EscalationDecision{Escalate: true, Level: 1, RecommendedActions: []string{"raise_priority"}, ReasonCodes: []string{"no_human_response"}, Confidence: .95, Reason: "Keine Bearbeitung."}

	result := evaluateEscalation(cfg, nil, ticket, followups, model.ContextSnapshot{}, decision, now)
	if result.Accepted || result.Decision != "escalation_human_activity_time_unknown" {
		t.Fatalf("unparseable human activity did not fail closed: %+v", result)
	}
}

func TestRaisePriorityIsIdempotentPerTicketAndLevel(t *testing.T) {
	cfg := escalationTestConfig()
	st, err := state.Open(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	a := model.AnalysisRun{AnalysisID: "old", AnalysisType: "escalation", Action: model.ActionAudit{Type: "escalation_plan", Steps: []model.ActionStepAudit{{Step: "raise_priority", Executed: true, Result: "ticket=70;level=2;action=raise_priority; executed"}}}}
	if err := st.Append(model.RunRecord{RunID: "old-run", TicketID: 70, Trigger: "scheduled_escalation", Outcome: "processed", FinishedAt: time.Now(), Analyses: []model.AnalysisRun{a}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ticket := model.Ticket{ID: 70, DateCreation: now.Add(-5 * time.Hour).Format(time.RFC3339), Priority: 4}
	decision := model.EscalationDecision{Escalate: true, Level: 2, RecommendedActions: []string{"raise_priority"}, ReasonCodes: []string{"no_human_response"}, Confidence: .95, Reason: "Keine Bearbeitung."}
	result := evaluateEscalation(cfg, st, ticket, nil, model.ContextSnapshot{}, decision, now)
	if result.Accepted || len(result.Actions) != 1 || result.Actions[0].Decision != "escalation_action_duplicate" {
		t.Fatalf("priority action repeated within same escalation level: %+v", result)
	}
}

func TestSLAEscalationCanProceedDespiteRecentHumanActivity(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Now()
	ticket := model.Ticket{ID: 64, DateCreation: now.Add(-5 * time.Hour).Format(time.RFC3339), TimeToResolve: now.Add(-time.Minute).Format(time.RFC3339), Priority: 4}
	followups := []model.Followup{{ID: 9, UserID: 123, Date: now.Add(-5 * time.Minute).Format(time.RFC3339)}}
	decision := model.EscalationDecision{Escalate: true, Level: 2, RecommendedActions: []string{"notify_service_owner"}, ReasonCodes: []string{"sla_breached"}, Confidence: .95, Reason: "SLA verletzt."}

	result := evaluateEscalation(cfg, nil, ticket, followups, model.ContextSnapshot{}, decision, now)
	if !result.Accepted || result.Decision != "escalation_accepted" {
		t.Fatalf("SLA escalation was incorrectly blocked by recent activity: %+v", result)
	}
}

func TestEscalationActionOrderIsDeterministic(t *testing.T) {
	got := normalizeEscalationActions(model.EscalationDecision{RecommendedActions: []string{"request_manager_review", "raise_priority", "assign_security_team"}})
	want := []string{"assign_security_team", "raise_priority", "request_manager_review"}
	if len(got) != len(want) {
		t.Fatalf("actions=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("actions=%v want=%v", got, want)
		}
	}
}

func TestEvaluateEscalationRejectsMissingReasonCode(t *testing.T) {
	cfg := escalationTestConfig()
	now := time.Now()
	ticket := model.Ticket{ID: 90, DateCreation: now.Add(-4 * time.Hour).Format(time.RFC3339), Priority: 3}
	decision := model.EscalationDecision{
		Escalate: true, Level: 1, RecommendedActions: []string{"raise_priority"},
		Confidence: .95, Reason: "Eskalation ohne strukturierten Grund.",
	}
	result := evaluateEscalation(cfg, nil, ticket, nil, model.ContextSnapshot{}, decision, now)
	if result.Accepted || result.Decision != "escalation_reason_missing" {
		t.Fatalf("missing reason code was not rejected: %+v", result)
	}
}

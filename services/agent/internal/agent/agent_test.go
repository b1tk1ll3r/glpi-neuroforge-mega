package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/knowledge"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
)

type fakeGLPI struct {
	ticket                      model.Ticket
	recent                      []model.Ticket
	followups                   []model.Followup
	cats                        []model.Category
	setCategory                 int
	setPriority                 int
	priorityValue               int64
	addReply                    int
	replyText                   string
	replyHTML                   bool
	assignedGroups              []int64
	assignedUsers               []int64
	privateNotes                []string
	privateNoteErr              error
	linkedTargets               []int64
	ticketReads                 int
	followupReads               int
	injectFollowupOnSecondCheck bool
}

func (f *fakeGLPI) Ping(context.Context) error             { return nil }
func (f *fakeGLPI) ValidateContract(context.Context) error { return nil }
func (f *fakeGLPI) ListRecentTickets(context.Context, int, string) ([]model.Ticket, error) {
	return append([]model.Ticket(nil), f.recent...), nil
}
func (f *fakeGLPI) GetTicket(context.Context, int64) (model.Ticket, error) {
	f.ticketReads++
	return f.ticket, nil
}
func (f *fakeGLPI) GetFollowups(context.Context, int64) ([]model.Followup, error) {
	f.followupReads++
	if f.injectFollowupOnSecondCheck && f.followupReads >= 2 {
		return []model.Followup{{ID: 99, UserID: 123, Date: time.Now().Format(time.RFC3339)}}, nil
	}
	return f.followups, nil
}
func (f *fakeGLPI) SetPriority(_ context.Context, _ int64, priority int64) error {
	f.setPriority++
	f.priorityValue = priority
	f.ticket.Priority = priority
	f.ticket.DateMod = "priority-v2"
	return nil
}
func (f *fakeGLPI) SetCategory(_ context.Context, _ int64, id int64) error {
	f.setCategory++
	f.ticket.CategoryID = id
	f.ticket.DateMod = "v2"
	return nil
}
func (f *fakeGLPI) AddFollowup(_ context.Context, _ int64, text string, html bool) error {
	f.addReply++
	f.replyText = text
	f.replyHTML = html
	f.ticket.DateMod = "v3"
	return nil
}
func (f *fakeGLPI) SetAssignedGroups(_ context.Context, _ int64, ids []int64, _ string) error {
	f.assignedGroups = append([]int64(nil), ids...)
	f.ticket.AssignedGroups = append([]int64(nil), ids...)
	return nil
}
func (f *fakeGLPI) SetAssignedUsers(_ context.Context, _ int64, ids []int64, _ string) error {
	f.assignedUsers = append([]int64(nil), ids...)
	f.ticket.AssignedUsers = append([]int64(nil), ids...)
	return nil
}
func (f *fakeGLPI) AddPrivateFollowup(_ context.Context, _ int64, content string, _ bool) error {
	if f.privateNoteErr != nil {
		return f.privateNoteErr
	}
	f.privateNotes = append(f.privateNotes, content)
	return nil
}
func (f *fakeGLPI) LinkITILObject(_ context.Context, _, targetID int64, _, _ string) error {
	f.linkedTargets = append(f.linkedTargets, targetID)
	return nil
}
func (f *fakeGLPI) GetCategories(context.Context) ([]model.Category, error) { return f.cats, nil }

type fakeContextCollector struct{ snapshot model.ContextSnapshot }

func (f fakeContextCollector) Collect(context.Context, model.Ticket) model.ContextSnapshot {
	return f.snapshot
}

type fakeAI struct {
	d                    model.Decision
	status               model.StatusDecision
	categoryHitCount     *int
	statusCandidateCount *int
	replyHitCount        *int
	order                *[]string
	replyCategoryID      *int64
	replyOutcomeCount    *int
	priority             model.PriorityDecision
	priorityBlock        bool
	escalation           model.EscalationDecision
}

func (f fakeAI) Ping(context.Context) error { return nil }
func (f fakeAI) AnalyseCategory(_ context.Context, _ model.Ticket, _ []model.Category, categoryHits []model.KnowledgeHit, _ model.ContextSnapshot) (model.Decision, error) {
	if f.categoryHitCount != nil {
		*f.categoryHitCount = len(categoryHits)
	}
	if f.order != nil {
		*f.order = append(*f.order, "category")
	}
	return f.d, nil
}
func (f fakeAI) AnalyseStatus(_ context.Context, _ model.Ticket, _ model.Category, candidates []model.ServiceIssueCandidate) (model.StatusDecision, error) {
	if f.statusCandidateCount != nil {
		*f.statusCandidateCount = len(candidates)
	}
	if f.order != nil {
		*f.order = append(*f.order, "status")
	}
	return f.status, nil
}

func (f fakeAI) AnalysePriority(ctx context.Context, _ model.Ticket, _ model.Category, _ model.ContextSnapshot) (model.PriorityDecision, error) {
	if f.priorityBlock {
		<-ctx.Done()
		return model.PriorityDecision{}, ctx.Err()
	}
	return f.priority, nil
}

func (f fakeAI) AnalyseEscalation(_ context.Context, _ model.Ticket, _ []model.Followup, _ model.ContextSnapshot, _ model.EscalationEvidence, _ model.EscalationConstraints) (model.EscalationDecision, error) {
	return f.escalation, nil
}

func (f fakeAI) AnalyseReply(_ context.Context, _ model.Ticket, category model.Category, replyHits []model.KnowledgeHit, ctxData model.ContextSnapshot) (model.Decision, error) {
	if f.replyHitCount != nil {
		*f.replyHitCount = len(replyHits)
	}
	if f.replyCategoryID != nil {
		*f.replyCategoryID = category.ID
	}
	if f.replyOutcomeCount != nil {
		*f.replyOutcomeCount = len(ctxData.ValidatedOutcomes)
	}
	if f.order != nil {
		*f.order = append(*f.order, "reply")
	}
	return f.d, nil
}

func newTestService(t *testing.T, g *fakeGLPI, d model.Decision, autoReply bool) *Service {
	t.Helper()
	dir := t.TempDir()
	kDir := dir + "/k"
	if err := os.MkdirAll(kDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"id":"KB1","title":"Known","text":"vpn gateway","answer":"Bitte starten Sie den VPN-Client neu.","auto_reply":true,"min_score":0,"categories":[2],"keywords":["vpn","gateway"],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(kDir+"/kb.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := knowledge.Load(context.Background(), kDir, dir, nil, false, []string{"internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DryRun: false, AutoCategory: true, AutoReply: autoReply, CategoryConfidence: .9, ReplyConfidence: .9, KnowledgeMinScore: 0, KnowledgeTopK: 1, CategoryPromptLimit: 20, Workers: 1, GLPIAllowedStatusIDs: []int64{1}, KnowledgeAllowedSources: []string{"internal-kb"}, KnowledgeCategorySources: []string{"internal-kb"}, KnowledgeAutoReplySources: []string{"internal-kb"}, CommunicationLanguage: "de-DE", CommunicationStyle: "formal", CommunicationSalutation: "Guten Tag,", CommunicationClosing: "Mit freundlichen Grüßen", CommunicationSignature: "IT-Service", AIContentLabelEnabled: true}
	return New(cfg, g, fakeAI{d: d}, k, nil, st, queue.New(8), metrics.New(), nil)
}

func TestPollDiagnosticsShowAlreadyProcessedTickets(t *testing.T) {
	ticket := model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 1, Priority: 3}
	g := &fakeGLPI{ticket: ticket, recent: []model.Ticket{ticket}, cats: []model.Category{{ID: 1}}}
	svc := newTestService(t, g, model.Decision{}, false)
	if err := svc.state.Append(model.RunRecord{RunID: "previous", TicketID: ticket.ID, SourceVersion: sourceVersion(ticket), Trigger: "poll", Outcome: "processed", FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc.poll(context.Background())
	status := svc.metrics.PollStatus()
	if status.Fetched != 1 || status.Seen != 1 || status.Unseen != 0 || status.Enqueued != 0 || svc.q.Len() != 0 {
		t.Fatalf("unexpected poll status: %+v queue=%d", status, svc.q.Len())
	}
}

func TestPollDiagnosticsShowUnseenTicketEnqueued(t *testing.T) {
	ticket := model.Ticket{ID: 2, Name: "new", Content: "ticket", DateMod: "v1", StatusID: 1, Priority: 3}
	g := &fakeGLPI{ticket: ticket, recent: []model.Ticket{ticket}, cats: []model.Category{{ID: 1}}}
	svc := newTestService(t, g, model.Decision{}, false)
	svc.poll(context.Background())
	status := svc.metrics.PollStatus()
	if status.Fetched != 1 || status.Seen != 0 || status.Unseen != 1 || status.Enqueued != 1 || svc.q.Len() != 1 {
		t.Fatalf("unexpected poll status: %+v queue=%d", status, svc.q.Len())
	}
}

func TestForcedManualRecheckBypassesProcessedVersion(t *testing.T) {
	ticket := model.Ticket{ID: 3, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 1, Priority: 3}
	g := &fakeGLPI{ticket: ticket, cats: []model.Category{{ID: 1}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 1, 1
	svc := newTestService(t, g, d, false)
	if err := svc.state.Append(model.RunRecord{RunID: "previous", TicketID: ticket.ID, SourceVersion: sourceVersion(ticket), Trigger: "poll", Outcome: "processed", FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessWork(context.Background(), queue.WorkItem{TicketID: ticket.ID, Trigger: "manual_recheck", Priority: queue.PriorityManual, Force: true}); err != nil {
		t.Fatal(err)
	}
	runs := svc.state.Recent(1)
	if len(runs) != 1 || runs[0].Outcome != "processed" || runs[0].Trigger != "manual_recheck" {
		t.Fatalf("unexpected forced run: %+v", runs)
	}
}

func TestPriorityTimeoutDoesNotBlockTicketPipeline(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 1, Priority: 3}, cats: []model.Category{{ID: 1}, {ID: 2}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	svc := newTestService(t, g, d, false)
	svc.cfg.PriorityEnabled = true
	svc.cfg.PriorityAnalysisTimeout = 20 * time.Millisecond
	svc.cfg.OllamaTimeout = time.Minute
	svc.ai = fakeAI{d: d, priorityBlock: true}
	started := time.Now()
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("priority stage blocked pipeline for %s", elapsed)
	}
	r := svc.state.Recent(1)[0]
	if r.Outcome != "processed" || r.PriorityDecision != "priority_ai_failed" {
		t.Fatalf("unexpected run: %+v", r)
	}
	if g.setCategory != 1 {
		t.Fatalf("category pipeline did not continue, writes=%d", g.setCategory)
	}
}

func TestExistingFollowupBlocksReplyButNotCategory(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 1}, followups: []model.Followup{{ID: 5}}, cats: []model.Category{{ID: 1}, {ID: 2}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, 1, "KB1"
	svc := newTestService(t, g, d, true)
	replyHitCount := -1
	svc.ai = fakeAI{d: d, replyHitCount: &replyHitCount}
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if g.setCategory != 1 {
		t.Fatalf("category writes=%d", g.setCategory)
	}
	if g.addReply != 0 {
		t.Fatalf("reply writes=%d", g.addReply)
	}
	if replyHitCount != -1 {
		t.Fatalf("reply analysis unexpectedly executed with %d candidates", replyHitCount)
	}
	runs := svc.state.Recent(1)
	if len(runs) != 1 || runs[0].ReplyDecision != "reply_existing_followup" || runs[0].Outcome != "processed" {
		t.Fatalf("unexpected run audit: %+v", runs)
	}
}

func TestRaceFollowupBlocksReply(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 2}, cats: []model.Category{{ID: 2}}, injectFollowupOnSecondCheck: true}
	var d model.Decision
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, 1, "KB1"
	svc := newTestService(t, g, d, true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Process(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if g.addReply != 0 {
		t.Fatalf("reply writes=%d", g.addReply)
	}
}

func TestDisallowedStatusSkipsWithoutWrites(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 6, CategoryID: 1}, cats: []model.Category{{ID: 1}, {ID: 2}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	svc := newTestService(t, g, d, true)
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if g.setCategory != 0 || g.addReply != 0 {
		t.Fatalf("unexpected writes: category=%d reply=%d", g.setCategory, g.addReply)
	}
}

func TestRunAuditExplainsCategoryBelowThreshold(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "konto", Content: "anmeldung", DateMod: "v1", StatusID: 1, CategoryID: 1}, cats: []model.Category{{ID: 1, Name: "Allgemein"}, {ID: 2, Name: "Active Directory"}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, .82
	d.Reason = "Das Problem deutet auf Active Directory hin."
	svc := newTestService(t, g, d, false)
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if g.setCategory != 0 {
		t.Fatalf("unexpected category write: %d", g.setCategory)
	}
	runs := svc.state.Recent(1)
	if len(runs) != 1 {
		t.Fatalf("runs=%d", len(runs))
	}
	r := runs[0]
	if r.AIRecommendedCategoryID != 2 || r.AIRecommendedCategoryName != "Active Directory" || r.AICategoryConfidence != .82 || r.CategoryThreshold != .9 {
		t.Fatalf("missing AI category audit: %+v", r)
	}
	if r.CategoryDecision != "category_confidence_below_threshold" || r.CategoryWouldChange || r.CategoryChanged {
		t.Fatalf("unexpected category decision audit: %+v", r)
	}
	if r.AIReason == "" || r.PolicyReason == "" {
		t.Fatalf("missing reason audit: %+v", r)
	}
}

func TestSemanticHintsImproveActiveDirectoryCategory(t *testing.T) {
	h := strings.Join(semanticCategoryHints(model.Category{ID: 2, Name: "Active Directory"}), " ")
	if !strings.Contains(strings.ToLower(h), "konto gesperrt") || !strings.Contains(strings.ToLower(h), "anmeldung") {
		t.Fatalf("expected identity hints, got %q", h)
	}
}

func TestRunStoresStructuredPolicyChecks(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "konto", Content: "anmeldung", DateMod: "v1", StatusID: 1, CategoryID: 1}, cats: []model.Category{{ID: 1, Name: "Allgemein"}, {ID: 2, Name: "Active Directory"}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, .82
	d.Reply.Allowed = false
	svc := newTestService(t, g, d, false)
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	r := svc.state.Recent(1)[0]
	if len(r.CategoryChecks) < 4 {
		t.Fatalf("category checks=%d", len(r.CategoryChecks))
	}
	if len(r.ReplyChecks) < 8 {
		t.Fatalf("reply checks=%d", len(r.ReplyChecks))
	}
	found := false
	for _, c := range r.CategoryChecks {
		if c.Code == "category_confidence" && c.Status == "fail" && c.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing blocking confidence rule: %+v", r.CategoryChecks)
	}
}

func TestDiagnoseKnowledgeExplainsCandidate(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "VPN gateway", Content: "gateway nicht erreichbar", DateMod: "v1", StatusID: 1, CategoryID: 2}, cats: []model.Category{{ID: 2, Name: "VPN"}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, 1, "KB1"
	svc := newTestService(t, g, d, true)
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	r := svc.state.Recent(1)[0]
	diag, err := svc.DiagnoseKnowledge(context.Background(), r.RunID, "KB1", "reply")
	if err != nil {
		t.Fatal(err)
	}
	if diag.KnowledgeID != "KB1" || diag.RetrievalRank != 1 {
		t.Fatalf("unexpected diagnostic: %+v", diag)
	}
	if len(diag.Checks) == 0 {
		t.Fatal("expected diagnostic checks")
	}
	if diag.CurrentTicketChanged {
		t.Fatal("ticket should not be marked changed")
	}
}

func TestCategoryHintsUseOnlyConfiguredCategorySources(t *testing.T) {
	dir := t.TempDir()
	kDir := dir + "/knowledge"
	if err := os.MkdirAll(kDir, 0o755); err != nil {
		t.Fatal(err)
	}
	categoryDoc := `{"id":"CAT","title":"Category selector","text":"category evidence","categories":[2],"keywords":["category-only-hint"],"source":"internal-category","language":"de-DE","communication_style":"formal"}`
	replyDoc := `{"id":"REPLY","title":"Reply article","text":"reply evidence","answer":"answer","categories":[1],"keywords":["reply-only-hint"],"source":"internal-kb","language":"de-DE","communication_style":"formal"}`
	if err := os.WriteFile(kDir+"/cat.json", []byte(categoryDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kDir+"/reply.json", []byte(replyDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := knowledge.Load(context.Background(), kDir, dir, nil, false, []string{"internal-category", "internal-kb"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{cfg: config.Config{KnowledgeCategorySources: []string{"internal-category"}}, knowledge: k}
	cats := svc.enrichCategories([]model.Category{{ID: 1, Name: "One"}, {ID: 2, Name: "Two"}})
	if strings.Contains(strings.Join(cats[0].Hints, " "), "reply-only-hint") {
		t.Fatalf("normal reply source influenced category hints: %+v", cats[0].Hints)
	}
	if !strings.Contains(strings.Join(cats[1].Hints, " "), "category-only-hint") {
		t.Fatalf("category source hint missing: %+v", cats[1].Hints)
	}
}

func TestTwoStageAnalysisUsesCategoryBeforeReply(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "vpn", Content: "gateway", DateMod: "v1", StatusID: 1, CategoryID: 1}, cats: []model.Category{{ID: 1, Name: "Allgemein"}, {ID: 2, Name: "VPN"}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, 1, "KB1"
	d.Reason = "passt"
	svc := newTestService(t, g, d, true)
	var order []string
	var replyCategoryID int64
	categoryHits, replyHits := -1, -1
	svc.ai = fakeAI{d: d, order: &order, replyCategoryID: &replyCategoryID, categoryHitCount: &categoryHits, replyHitCount: &replyHits}
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "category,reply" {
		t.Fatalf("analysis order = %v", order)
	}
	if replyCategoryID != 2 {
		t.Fatalf("reply basis category = %d, want 2", replyCategoryID)
	}
	if categoryHits != 1 || replyHits != 1 {
		t.Fatalf("candidate counts category=%d reply=%d", categoryHits, replyHits)
	}
	r := svc.state.Recent(1)[0]
	if !r.CategoryAnalysisExecuted || !r.ReplyAnalysisExecuted {
		t.Fatalf("missing stage audit: %+v", r)
	}
	if r.ReplyBasisCategoryID != 2 || len(r.CategoryKnowledgeCandidates) == 0 || len(r.ReplyKnowledgeCandidates) == 0 {
		t.Fatalf("missing separated knowledge audit: %+v", r)
	}
}

func TestStatusIncidentUsesOnlyPredefinedTemplate(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 1, Name: "Outlook nicht erreichbar", Content: "Keine Verbindung zu Exchange", DateMod: "v1", StatusID: 1, CategoryID: 2}, cats: []model.Category{{ID: 2, Name: "Outlook"}}}
	var d model.Decision
	d.Category.ID, d.Category.Confidence = 2, 1
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, 1, "KB1"
	svc := newTestService(t, g, d, true)
	svc.cfg.ContextEnabled = true
	svc.cfg.ContextStatusReplyEnabled = true
	svc.cfg.ContextStatusReplyMinRelevance = .5
	svc.cfg.ContextStatusReplyMinAIConfidence = .8
	svc.cfg.ContextStatusReplyMinFinalScore = .5
	svc.cfg.ContextIncidentReplyText = "Für {{service_name}} liegt derzeit eine bekannte Störung vor."
	svc.cfg.ContextMaintenanceReplyText = "Für {{service_name}} läuft derzeit eine Wartung."
	svc.context = fakeContextCollector{snapshot: model.ContextSnapshot{ServiceIssues: []model.ServiceIssueContext{{Source: "uptime-kuma", Kind: "monitor", MonitorID: 7, MonitorName: "Exchange Online", Status: "down", Relevance: .9}}}}
	var order []string
	replyHits := -1
	svc.ai = fakeAI{d: d, status: model.StatusDecision{Matched: true, CandidateID: "uptime-1", Confidence: .95, Reason: "Exchange passt eindeutig."}, order: &order, replyHitCount: &replyHits}
	if err := svc.Process(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "category,status" {
		t.Fatalf("analysis order=%v", order)
	}
	if replyHits != -1 {
		t.Fatalf("normal reply analysis unexpectedly ran with %d hits", replyHits)
	}
	if g.addReply != 1 || !strings.Contains(g.replyText, "bekannte Störung") || !strings.Contains(g.replyText, "Exchange Online") {
		t.Fatalf("unexpected followup html=%v text=%q", g.replyHTML, g.replyText)
	}
	if strings.Contains(g.replyText, "VPN-Client") {
		t.Fatalf("normal KB answer leaked into status reply: %q", g.replyText)
	}
	r := svc.state.Recent(1)[0]
	if !r.StatusReplySelected || r.StatusReplyType != "incident" || r.StatusReplyDecision != "reply_status_incident_accepted" || r.ReplyAnalysisSkipReason != "status_reply_selected" {
		t.Fatalf("unexpected status audit: %+v", r)
	}
	if r.StatusReplyFinalScore < .85 || !r.ReplyWritten {
		t.Fatalf("unexpected score/write audit: %+v", r)
	}
}

func TestStatusMaintenanceUsesMaintenanceTemplate(t *testing.T) {
	cfg := config.Config{ContextStatusReplyEnabled: true, ContextStatusReplyMinRelevance: .5, ContextStatusReplyMinAIConfidence: .8, ContextStatusReplyMinFinalScore: .5, ContextIncidentReplyText: "Störung {{service_name}}", ContextMaintenanceReplyText: "Wartung {{service_name}}", CommunicationSalutation: "Guten Tag,", CommunicationClosing: "Viele Grüße", CommunicationSignature: "IT", AIContentLabelEnabled: false}
	policy := NewPolicy(false, true, .9, .9, .7, .3, .45, .35, .2, []string{"internal-kb"}, []string{"internal-kb"}, "de-DE", "formal", cfg.CommunicationSalutation, cfg.CommunicationClosing, cfg.CommunicationSignature, false, true, true, .2)
	candidates := statusIssueCandidates([]model.ServiceIssueContext{{Kind: "maintenance", MonitorName: "Dokumentenmanagement", Status: "maintenance", Relevance: .8}})
	res := evaluateStatusReply(cfg, policy, model.ContextSnapshot{}, candidates, model.StatusDecision{Matched: true, CandidateID: "uptime-1", Confidence: .9})
	if !res.Accepted || res.Type != "maintenance" || !strings.Contains(res.ReplyText, "Wartung Dokumentenmanagement") || strings.Contains(res.ReplyText, "Störung") {
		t.Fatalf("unexpected maintenance result: %+v", res)
	}
}

func TestPriorityAnalysisIsVisibleInRunAndShadowModeDoesNotWrite(t *testing.T) {
	g := &fakeGLPI{ticket: model.Ticket{ID: 21, Name: "Standort ausgefallen", Content: "Alle Benutzer ohne Zugriff", DateCreation: time.Now().Add(-time.Hour).Format(time.RFC3339), DateMod: "v1", StatusID: 1, CategoryID: 2, Priority: 2}, cats: []model.Category{{ID: 2, Name: "Netzwerk"}}}
	var category model.Decision
	category.Category.ID, category.Category.Confidence = 2, .99
	svc := newTestService(t, g, category, false)
	svc.cfg.PriorityEnabled = true
	svc.cfg.AutoPriority = false
	svc.cfg.PriorityConfidence = .88
	svc.cfg.PriorityMaxIncrease = 1
	svc.cfg.PriorityAllowedReasonCodes = []string{"site_affected", "core_service_unavailable"}
	svc.ai = fakeAI{d: category, priority: model.PriorityDecision{RecommendedPriority: 5, RecommendedImpact: 4, RecommendedUrgency: 5, AffectedScope: "site", TimeCriticality: "immediate", ReasonCodes: []string{"site_affected", "core_service_unavailable"}, Confidence: .94, Reason: "Ein ganzer Standort ist betroffen."}}
	if err := svc.Process(context.Background(), 21); err != nil {
		t.Fatal(err)
	}
	if g.setPriority != 0 {
		t.Fatalf("shadow mode wrote priority %d times", g.setPriority)
	}
	r := svc.state.Recent(1)[0]
	if !r.PriorityAnalysisExecuted || r.PriorityBefore != 2 || r.AIRecommendedPriority != 5 || r.AIRecommendedImpact != 4 || r.AIRecommendedUrgency != 5 || r.PriorityAffectedScope != "site" || r.PriorityTimeCriticality != "immediate" || r.PriorityProposed != 3 || !r.PriorityWouldChange || r.PriorityChanged {
		t.Fatalf("priority audit not explicit: %+v", r)
	}
	if r.PriorityDecision != "priority_accepted_shadow" {
		t.Fatalf("unexpected priority decision: %s", r.PriorityDecision)
	}
	found := false
	for _, a := range r.Analyses {
		if a.AnalysisType == "priority" {
			found = true
			if !a.Action.Proposed || !a.Action.DryRun || a.Action.Executed || len(a.Checks) == 0 {
				t.Fatalf("unexpected priority analysis: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("separate priority AnalysisRun missing")
	}
}

type fakeOutcomeRetriever struct {
	rows []learning.OutcomeEvidence
	err  error
}

func (f fakeOutcomeRetriever) SearchOutcomes(context.Context, string, int, float64) ([]learning.OutcomeEvidence, error) {
	return append([]learning.OutcomeEvidence(nil), f.rows...), f.err
}

func TestValidatedOutcomeRetrievalReachesReplyContextButNotKnowledgeAuthority(t *testing.T) {
	ticket := model.Ticket{ID: 77, Name: "vpn", Content: "gateway verbindet nicht", DateMod: "v1", StatusID: 1, CategoryID: 2, Priority: 3}
	g := &fakeGLPI{ticket: ticket, cats: []model.Category{{ID: 2, Name: "VPN"}}}
	var outcomeCount int
	d := model.Decision{}
	d.Category.ID, d.Category.Confidence = 2, .99
	d.Reply.Allowed, d.Reply.Confidence, d.Reply.KnowledgeID = true, .99, "KB1"
	svc := newTestService(t, g, d, true)
	svc.cfg.OutcomeRetrievalEnabled = true
	svc.cfg.OutcomeRetrievalSearchK = 6
	svc.cfg.OutcomeRetrievalMinSimilarity = .5
	svc.cfg.OutcomeRetrievalFailOpen = false
	svc.outcomeRetriever = fakeOutcomeRetriever{rows: []learning.OutcomeEvidence{{MemoryID: "m1", OutcomeID: "o1", Decision: "accepted", Text: "verified historical VPN solution", Similarity: .88, Source: "glpi.outcome.accepted"}}}
	svc.ai = fakeAI{d: d, replyOutcomeCount: &outcomeCount}
	if err := svc.Process(context.Background(), ticket.ID); err != nil {
		t.Fatal(err)
	}
	if outcomeCount != 1 {
		t.Fatalf("reply model saw %d validated outcomes, want 1", outcomeCount)
	}
	runs := svc.state.Recent(10)
	if len(runs) == 0 || len(runs[0].ValidatedOutcomeCandidates) != 1 {
		t.Fatalf("outcome evidence missing from audit: %#v", runs)
	}
	if runs[0].AIKnowledgeID != "KB1" {
		t.Fatalf("validated outcome must not become selectable knowledge authority: %#v", runs[0])
	}
}

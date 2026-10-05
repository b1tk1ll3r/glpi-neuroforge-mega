package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/knowledge"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/ollama"
	"github.com/example/glpi-ai-agent/internal/prioritysignals"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
)

type GLPI interface {
	Ping(context.Context) error
	ValidateContract(context.Context) error
	ListRecentTickets(context.Context, int, string) ([]model.Ticket, error)
	GetTicket(context.Context, int64) (model.Ticket, error)
	GetFollowups(context.Context, int64) ([]model.Followup, error)
	SetCategory(context.Context, int64, int64) error
	AddFollowup(context.Context, int64, string, bool) error
	GetCategories(context.Context) ([]model.Category, error)
}
type AI interface {
	Ping(context.Context) error
	AnalyseCategory(context.Context, model.Ticket, []model.Category, []model.KnowledgeHit, model.ContextSnapshot) (model.Decision, error)
	AnalyseStatus(context.Context, model.Ticket, model.Category, []model.ServiceIssueCandidate) (model.StatusDecision, error)
	AnalyseReply(context.Context, model.Ticket, model.Category, []model.KnowledgeHit, model.ContextSnapshot) (model.Decision, error)
}
type ContextCollector interface {
	Collect(context.Context, model.Ticket) model.ContextSnapshot
}
type Service struct {
	cfg              config.Config
	glpi             GLPI
	ai               AI
	knowledge        *knowledge.Store
	learning         *learning.Store
	outcomes         *learning.OutcomeStore
	outcomeSink      learning.OutcomeSink
	outcomeRetriever learning.OutcomeRetriever
	state            *state.Store
	q                *queue.Queue
	metrics          *metrics.Metrics
	policy           Policy
	context          ContextCollector
	locks            sync.Map
	catMu            sync.RWMutex
	pollLogOnce      sync.Once
	categories       []model.Category
	catAt            time.Time
}

func New(cfg config.Config, g GLPI, ai AI, k *knowledge.Store, l *learning.Store, s *state.Store, q *queue.Queue, m *metrics.Metrics, contextCollector ContextCollector) *Service {
	return &Service{cfg: cfg, glpi: g, ai: ai, knowledge: k, learning: l, state: s, q: q, metrics: m, context: contextCollector, policy: NewPolicy(cfg.AutoCategory, cfg.AutoReply, cfg.CategoryConfidence, cfg.ReplyConfidence, cfg.KnowledgeMinScore, cfg.KnowledgeRetrievalFloor, cfg.KnowledgeEvidenceRetrievalWeight, cfg.KnowledgeEvidenceAIWeight, cfg.KnowledgeEvidenceCategoryWeight, cfg.KnowledgeAllowedSources, cfg.KnowledgeAutoReplySources, cfg.CommunicationLanguage, cfg.CommunicationStyle, cfg.CommunicationSalutation, cfg.CommunicationClosing, cfg.CommunicationSignature, cfg.AIContentLabelEnabled, cfg.ContextBlockReplyOnError, cfg.ContextBlockReplyOnIncident, cfg.ContextRelevanceMinScore)}
}

func (s *Service) SetOutcomeLearning(store *learning.OutcomeStore, sink learning.OutcomeSink) {
	s.outcomes = store
	s.outcomeSink = sink
	if r, ok := sink.(learning.OutcomeRetriever); ok {
		s.outcomeRetriever = r
	}
}
func (s *Service) SetOutcomeRetriever(r learning.OutcomeRetriever) {
	s.outcomeRetriever = r
}
func (s *Service) Queue() *queue.Queue { return s.q }
func (s *Service) Start(ctx context.Context) {
	go s.healthLoop(ctx)
	go s.pollLoop(ctx)
	if s.cfg.EscalationEnabled {
		go s.escalationLoop(ctx)
	}
	for i := 0; i < s.cfg.Workers; i++ {
		go s.worker(ctx, i)
	}
}
func (s *Service) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.GLPIPollInterval)
	defer ticker.Stop()
	s.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}
func (s *Service) poll(ctx context.Context) {
	pollAt := time.Now()
	tickets, err := s.glpi.ListRecentTickets(ctx, s.cfg.GLPIPollLimit, s.cfg.GLPITicketFilter)
	s.metrics.Polls.Add(1)
	if err != nil {
		s.metrics.SetPollStatus(metrics.PollStatus{At: pollAt, Error: err.Error()})
		s.metrics.Errors.Add(1)
		slog.Error("GLPI poll failed", "error", err)
		return
	}
	seen, unseen, enqueued, rejected := 0, 0, 0, 0
	for _, t := range tickets {
		version := sourceVersion(t)
		if s.state.Seen(t.ID, version) {
			seen++
			continue
		}
		unseen++
		if s.q.EnqueueWork(queue.WorkItem{TicketID: t.ID, Trigger: "poll", Priority: queue.PriorityPoll}) {
			enqueued++
			s.metrics.QueueDepth.Store(int64(s.q.Len()))
		} else {
			rejected++
		}
	}
	status := metrics.PollStatus{At: pollAt, Fetched: len(tickets), Seen: seen, Unseen: unseen, Enqueued: enqueued, Rejected: rejected}
	s.metrics.SetPollStatus(status)
	s.pollLogOnce.Do(func() {
		slog.Info("initial GLPI ticket poll completed", "fetched", status.Fetched, "already_processed", status.Seen, "unseen", status.Unseen, "enqueued", status.Enqueued, "rejected", status.Rejected, "filter_configured", strings.TrimSpace(s.cfg.GLPITicketFilter) != "")
	})
	slog.Debug("GLPI ticket poll completed", "fetched", status.Fetched, "already_processed", status.Seen, "unseen", status.Unseen, "enqueued", status.Enqueued, "rejected", status.Rejected)
}
func (s *Service) healthLoop(ctx context.Context) {
	check := func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		gerr := s.glpi.Ping(c)
		oerr := s.ai.Ping(c)
		s.metrics.SetHealth(gerr == nil, oerr == nil)
	}
	check()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
func (s *Service) worker(ctx context.Context, n int) {
	for {
		item, ok := s.q.NextWork(ctx)
		if !ok {
			return
		}
		s.metrics.QueueDepth.Store(int64(s.q.Len()))
		if err := s.ProcessWork(ctx, item); err != nil {
			slog.Error("ticket processing failed", "worker", n, "ticket_id", item.TicketID, "trigger", item.Trigger, "error", err)
		}
		s.q.DoneWork(item)
		s.metrics.QueueDepth.Store(int64(s.q.Len()))
	}
}

// Process remains the compatibility entry point used by tests and manual callers.
func (s *Service) Process(ctx context.Context, id int64) error {
	return s.ProcessWork(ctx, queue.WorkItem{TicketID: id, Trigger: "manual", Priority: queue.PriorityManual})
}

// lockTicket serializes work per ticket and returns the unlock function.
// Entries are intentionally never deleted: deleting after Unlock lets a waiter
// and a newcomer end up holding different mutexes for the same ticket.
func (s *Service) lockTicket(id int64) func() {
	muAny, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) ProcessWork(ctx context.Context, item queue.WorkItem) error {
	if strings.EqualFold(strings.TrimSpace(item.Trigger), "scheduled_escalation") {
		return s.processEscalation(ctx, item)
	}
	id := item.TicketID
	defer s.lockTicket(id)()
	start := time.Now()
	trigger := strings.TrimSpace(item.Trigger)
	if trigger == "" {
		trigger = "poll"
	}
	run := model.RunRecord{RunID: newRunID(), TicketID: id, Trigger: trigger, StartedAt: start, DryRun: s.cfg.DryRun, Outcome: "error"}
	finish := func(err error) {
		run.FinishedAt = time.Now()
		if err != nil {
			run.Error = err.Error()
			s.metrics.Errors.Add(1)
		}
		if e := s.state.Append(run); e != nil {
			slog.Error("persist run failed", "error", e)
		}
	}
	t, err := s.glpi.GetTicket(ctx, id)
	if err != nil {
		run.Reason = "ticket_load_failed"
		finish(err)
		return err
	}
	run.TicketName = t.Name
	run.SourceVersion = sourceVersion(t)
	if s.cfg.OutcomeLearningEnabled {
		run.LearningTicketText = compactLearningText(stripHTML(t.Content), 4000)
	}
	run.CategoryBefore = t.CategoryID
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_ticket_loaded", Group: "eligibility", Label: "Ticket konnte geladen werden", Status: "pass", Actual: "ja", Expected: "ja"})
	alreadySeen := s.state.Seen(t.ID, run.SourceVersion)
	eligibleVersion := !alreadySeen || item.Force
	versionDetail := ""
	if item.Force && alreadySeen {
		versionDetail = "Manuelle Neuanalyse erzwingt einen einmaligen Lauf für die bereits bekannte Ticketversion."
	}
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_not_already_processed", Group: "eligibility", Label: "Diese Ticketversion wurde noch nicht verarbeitet", Status: passFail(eligibleVersion), Blocking: !eligibleVersion, Actual: boolText(!alreadySeen), Expected: "ja oder manuell erzwungen", Detail: versionDetail})
	if alreadySeen && !item.Force {
		run.Outcome = "skipped"
		run.Reason = "already_processed"
		s.metrics.Skipped.Add(1)
		finish(nil)
		return nil
	}
	statusAllowed := s.statusAllowed(t.StatusID)
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_status_allowed", Group: "eligibility", Label: "Ticketstatus ist zur Verarbeitung freigegeben", Status: passFail(statusAllowed), Blocking: !statusAllowed, Actual: fmt.Sprintf("Status #%d", t.StatusID), Expected: fmt.Sprintf("einer von %v", s.cfg.GLPIAllowedStatusIDs)})
	if !statusAllowed {
		run.Outcome = "skipped"
		run.Reason = "status_not_allowed"
		s.metrics.Skipped.Add(1)
		finish(nil)
		return nil
	}
	followups, err := s.glpi.GetFollowups(ctx, id)
	if err != nil {
		run.Reason = "followup_check_failed"
		finish(err)
		return err
	}
	canReply := len(followups) == 0
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_no_existing_followup", Group: "execution", Label: "Ticket hat noch keine Antwort / kein Followup", Status: passFail(canReply), Blocking: !canReply, Actual: fmt.Sprintf("%d Followups", len(followups)), Expected: "0 Followups"})
	categories, err := s.getCategories(ctx)
	if err != nil {
		run.Reason = "categories_failed"
		finish(err)
		return err
	}
	run.CategoryBeforeName = categoryName(categories, t.CategoryID)
	promptCats := shortlistCategories(t, categories, s.cfg.CategoryPromptLimit)
	auditTopK := s.cfg.KnowledgeAuditTopK
	if auditTopK <= 0 {
		auditTopK = s.cfg.KnowledgeTopK
		if auditTopK <= 0 {
			auditTopK = 10
		}
	}
	llmTopK := s.cfg.KnowledgeTopK
	if llmTopK <= 0 {
		llmTopK = 6
	}
	ticketQuery := t.Name + "\n" + stripHTML(t.Content)
	allRetrievalHits, err := s.knowledge.Search(ctx, ticketQuery, 0, categories)
	if err != nil {
		run.Reason = "knowledge_search_failed"
		finish(err)
		return err
	}
	categoryRetrievalHits := knowledge.FilterHitsBySources(allRetrievalHits, s.cfg.KnowledgeCategorySources, 0)
	retrievalHits := knowledge.FilterHitsBySources(allRetrievalHits, s.cfg.KnowledgeAllowedSources, 0)
	categoryLLMHits, categoryCutoff := selectKnowledgeCandidates(categoryRetrievalHits, llmTopK, s.cfg.KnowledgeRetrievalFloor, s.cfg.KnowledgeCandidateMaxGap)
	run.CategoryKnowledgeLLMCandidates = len(categoryLLMHits)
	run.CategoryKnowledgeCandidateCutoff = categoryCutoff
	run.KnowledgeCandidateMaxGap = s.cfg.KnowledgeCandidateMaxGap
	run.KnowledgeAuditTopK = auditTopK
	categoryCandidateIDs := knowledgeHitIDSet(categoryLLMHits)
	run.CategoryKnowledgeCandidates = auditKnowledgeCandidates(categoryRetrievalHits, s.cfg.KnowledgeMinScore, auditTopK, categoryCandidateIDs, categoryCutoff, s.cfg.KnowledgeRetrievalFloor, llmTopK)

	contextData := model.ContextSnapshot{}
	if s.context != nil && s.cfg.ContextEnabled {
		s.metrics.ContextFetches.Add(1)
		contextData = s.context.Collect(ctx, t)
		run.ContextChanges = len(contextData.Changes)
		run.ContextIncidents = len(contextData.MajorIncidents)
		run.ContextIssues = len(contextData.ServiceIssues)
		run.ContextDevices = len(contextData.UserDevices)
		run.ContextWarnings = append([]string(nil), contextData.Warnings...)
		run.ContextDetails = auditContextDetails(contextData, 5)
		if contextData.Incomplete {
			s.metrics.ContextErrors.Add(1)
		}
	}

	// Stage 1: classify the ticket using only category knowledge. The result is
	// persisted separately and becomes the deterministic basis for reply retrieval.
	categoryStarted := time.Now()
	categoryAnalysis := newAnalysis(run, "category", categoryPromptVersion, map[string]any{"ticket": t, "categories": promptCats, "knowledge_candidates": categoryLLMHits, "context": contextData}, categoryStarted)
	run.CategoryAnalysisExecuted = true
	categoryCtx, categoryTrace := ollama.WithTrace(ctx, s.cfg.OllamaRoutingMode)
	categoryDecision, err := s.ai.AnalyseCategory(categoryCtx, t, promptCats, categoryLLMHits, contextData)
	run.CategoryAnalysisDurationMS = time.Since(categoryStarted).Milliseconds()
	if err != nil {
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_category_ai", Group: "execution", Label: "Kategorieanalyse konnte ausgeführt werden", Status: "fail", Blocking: true, Actual: err.Error(), Expected: "erfolgreich"})
		finishAnalysis(&categoryAnalysis, s.cfg.OllamaModel, nil, nil, "", 0, nil, model.ActionAudit{Type: "set_category", Result: "skipped: category_ai_failed"}, err)
		attachAnalysisTrace(&categoryAnalysis, categoryTrace)
		run.Analyses = append(run.Analyses, categoryAnalysis)
		run.Reason = "category_ai_failed"
		finish(err)
		return err
	}
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_category_ai", Group: "execution", Label: "Kategorieanalyse konnte ausgeführt werden", Status: "pass", Actual: "erfolgreich", Expected: "erfolgreich"})
	run.CategoryAIReason = strings.TrimSpace(categoryDecision.Reason)
	finishAnalysis(&categoryAnalysis, s.cfg.OllamaModel, categoryDecision.Category, nil, categoryDecision.Reason, categoryDecision.Category.Confidence, nil, model.ActionAudit{Type: "set_category"}, nil)
	attachAnalysisTrace(&categoryAnalysis, categoryTrace)
	run.Analyses = append(run.Analyses, categoryAnalysis)
	categoryAnalysisIndex := len(run.Analyses) - 1

	replyBasis := effectiveReplyCategory(t, categoryDecision, categories, s.cfg.AutoCategory, s.cfg.CategoryConfidence)
	run.ReplyBasisCategoryID = replyBasis.ID
	run.ReplyBasisCategoryName = categoryDisplayName(replyBasis)

	// Independent priority stage. The model only recommends a GLPI priority and
	// controlled reason codes; deterministic Go policy decides whether a change
	// would be permitted. In the default Shadow Mode no write occurs.
	var priorityResult model.PriorityResult
	priorityAnalysisIndex := -1
	if s.cfg.PriorityEnabled {
		priorityStarted := time.Now()
		priorityEvidence := prioritysignals.Extract(t)
		priorityTimeout := s.cfg.PriorityAnalysisTimeout
		if priorityTimeout <= 0 {
			priorityTimeout = 45 * time.Second
		}
		if s.cfg.OllamaTimeout > 0 && priorityTimeout > s.cfg.OllamaTimeout {
			priorityTimeout = s.cfg.OllamaTimeout
		}
		priorityAnalysis := newAnalysis(run, "priority", priorityPromptVersion, map[string]any{"ticket": t, "effective_category": replyBasis, "context": contextData, "deterministic_evidence": priorityEvidence, "allowed_reason_codes": s.cfg.PriorityAllowedReasonCodes, "neutral_reason_codes": []string{"single_user_affected", "workaround_available", "insufficient_information"}, "threshold": s.cfg.PriorityConfidence, "max_increase": s.cfg.PriorityMaxIncrease, "analysis_timeout": priorityTimeout.String()}, priorityStarted)
		run.PriorityAnalysisExecuted = true
		run.PriorityBefore = t.Priority
		run.PriorityThreshold = s.cfg.PriorityConfidence
		if priorityClient, ok := s.ai.(priorityAI); ok {
			priorityBaseCtx, priorityTrace := ollama.WithTrace(ctx, s.cfg.OllamaRoutingMode)
			priorityCtx, cancelPriority := context.WithTimeout(priorityBaseCtx, priorityTimeout)
			priorityDecision, priorityErr := priorityClient.AnalysePriority(priorityCtx, t, replyBasis, contextData)
			cancelPriority()
			run.PriorityAnalysisDurationMS = time.Since(priorityStarted).Milliseconds()
			if priorityErr != nil {
				run.PriorityDecision = "priority_ai_failed"
				finishAnalysis(&priorityAnalysis, s.cfg.OllamaModel, nil, nil, "", 0, nil, model.ActionAudit{Type: "set_priority", Result: "skipped: priority_ai_failed"}, priorityErr)
				run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_priority_ai", Group: "execution", Label: "Prioritätsanalyse konnte ausgeführt werden", Status: "warn", Actual: priorityErr.Error(), Expected: "erfolgreich", Detail: "Kategorie- und Antwortverarbeitung laufen weiter; es wird keine Priorität geändert."})
				s.metrics.Errors.Add(1)
			} else {
				priorityResult = evaluatePriority(s.cfg, t, priorityDecision)
				run.PriorityAIReason = strings.TrimSpace(priorityDecision.Reason)
				run.AIRecommendedPriority = priorityDecision.RecommendedPriority
				run.AIRecommendedImpact = priorityDecision.RecommendedImpact
				run.AIRecommendedUrgency = priorityDecision.RecommendedUrgency
				run.PriorityAffectedScope = strings.TrimSpace(priorityDecision.AffectedScope)
				run.PriorityTimeCriticality = strings.TrimSpace(priorityDecision.TimeCriticality)
				run.AIPriorityConfidence = priorityDecision.Confidence
				run.PriorityReasonCodes = append([]string(nil), priorityResult.ReasonCodes...)
				run.PriorityChecks = append([]model.RuleCheck(nil), priorityResult.Checks...)
				run.PriorityDecision = priorityResult.Decision
				run.PriorityProposed = priorityResult.PriorityAfter
				run.PriorityWouldChange = priorityResult.ChangePriority
				action := model.ActionAudit{Type: "set_priority", Proposed: priorityResult.ChangePriority, DryRun: s.cfg.DryRun || !s.cfg.AutoPriority, Before: fmt.Sprintf("priority=%d", t.Priority), After: fmt.Sprintf("priority=%d", priorityResult.PriorityAfter), Result: priorityResult.Decision}
				finishAnalysis(&priorityAnalysis, s.cfg.OllamaModel, priorityResult, priorityResult.ReasonCodes, priorityDecision.Reason, priorityDecision.Confidence, priorityResult.Checks, action, nil)
				run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_priority_ai", Group: "execution", Label: "Prioritätsanalyse konnte ausgeführt werden", Status: "pass", Actual: "erfolgreich", Expected: "erfolgreich"})
				if priorityDecision.RecommendedPriority > 0 {
					s.metrics.PriorityRecommendations.Add(1)
				}
			}
			attachAnalysisTrace(&priorityAnalysis, priorityTrace)
		} else {
			priorityErr := fmt.Errorf("AI client does not implement priority analysis")
			run.PriorityDecision = "priority_ai_unavailable"
			finishAnalysis(&priorityAnalysis, s.cfg.OllamaModel, nil, nil, "", 0, nil, model.ActionAudit{Type: "set_priority", Result: "skipped: priority_ai_unavailable"}, priorityErr)
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_priority_ai", Group: "execution", Label: "Prioritätsanalyse konnte ausgeführt werden", Status: "warn", Actual: priorityErr.Error(), Expected: "erfolgreich"})
		}
		run.Analyses = append(run.Analyses, priorityAnalysis)
		priorityAnalysisIndex = len(run.Analyses) - 1
	} else {
		run.PriorityDecision = "priority_disabled"
	}

	// Stage 2: the model may only decide whether one active Uptime Kuma entry
	// clearly explains the ticket. It never receives or returns an end-user text.
	// A deterministic Go policy combines this confidence with the existing
	// relevance score and, if accepted, renders an operator-defined template.
	statusCandidates := statusIssueCandidates(contextData.ServiceIssues)
	run.StatusReplyMinRelevance = s.cfg.ContextStatusReplyMinRelevance
	run.StatusReplyMinAIConfidence = s.cfg.ContextStatusReplyMinAIConfidence
	run.StatusReplyMinFinalScore = s.cfg.ContextStatusReplyMinFinalScore
	var statusDecision model.StatusDecision
	var statusEval statusReplyEvaluation
	statusAnalysisStarted := time.Now()
	var statusAnalysisErr error
	var statusTrace *ollama.Trace
	switch {
	case !s.cfg.ContextStatusReplyEnabled:
		run.StatusAnalysisSkipReason = "status_reply_disabled"
	case !canReply:
		run.StatusAnalysisSkipReason = "existing_followup"
	case !s.cfg.AutoReply:
		run.StatusAnalysisSkipReason = "auto_reply_disabled"
	case contextData.Incomplete:
		run.StatusAnalysisSkipReason = "context_incomplete"
	case len(statusCandidates) == 0:
		run.StatusAnalysisSkipReason = "no_status_candidates"
	default:
		run.StatusAnalysisExecuted = true
		statusCtx, trace := ollama.WithTrace(ctx, s.cfg.OllamaRoutingMode)
		statusTrace = trace
		statusDecision, err = s.ai.AnalyseStatus(statusCtx, t, replyBasis, statusCandidates)
		run.StatusAnalysisDurationMS = time.Since(statusAnalysisStarted).Milliseconds()
		if err != nil {
			statusAnalysisErr = err
			run.StatusAnalysisSkipReason = "status_ai_failed"
			run.StatusAIReason = "Statuszuordnung fehlgeschlagen: " + err.Error()
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_status_ai", Group: "execution", Label: "Status- und Störungszuordnung konnte ausgeführt werden", Status: "warn", Actual: err.Error(), Expected: "erfolgreich", Detail: "Es wird kein Status-Template verwendet; der normale Reply-Pfad bleibt fail-closed."})
			s.metrics.Errors.Add(1)
			slog.Warn("status analysis failed; normal reply policy retained", "ticket_id", id, "error", err)
		} else {
			run.StatusAIReason = strings.TrimSpace(statusDecision.Reason)
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_status_ai", Group: "execution", Label: "Status- und Störungszuordnung konnte ausgeführt werden", Status: "pass", Actual: "erfolgreich", Expected: "erfolgreich"})
		}
	}
	statusEval = evaluateStatusReply(s.cfg, s.policy, contextData, statusCandidates, statusDecision)
	run.StatusChecks = append([]model.RuleCheck(nil), statusEval.Checks...)
	run.StatusCandidates = auditStatusCandidates(statusCandidates, statusDecision, s.cfg, statusEval)
	run.StatusReplySelected = statusEval.Accepted
	run.StatusReplyDecision = statusEval.DecisionCode
	if run.StatusAnalysisSkipReason != "" && !statusEval.Accepted {
		run.StatusReplyDecision = run.StatusAnalysisSkipReason
	}
	run.StatusReplyType = statusEval.Type
	run.StatusReplyCandidateID = strings.TrimSpace(statusDecision.CandidateID)
	run.StatusReplyAIConfidence = statusDecision.Confidence
	run.StatusReplyFinalScore = statusEval.FinalScore
	run.StatusReplyRenderedText = statusEval.RenderedText
	if statusEval.Candidate.ID != "" {
		run.StatusReplyCandidateName = statusCandidateName(statusEval.Candidate.Issue)
		run.StatusReplyCandidateStatus = statusEval.Candidate.Issue.Status
		run.StatusReplyRelevance = statusEval.Candidate.Issue.Relevance
	}
	statusAnalysis := newAnalysis(run, "status_match", statusPromptVersion, map[string]any{"ticket": t, "effective_category": replyBasis, "candidates": statusCandidates, "thresholds": map[string]float64{"relevance": s.cfg.ContextStatusReplyMinRelevance, "ai_confidence": s.cfg.ContextStatusReplyMinAIConfidence, "final_score": s.cfg.ContextStatusReplyMinFinalScore}}, statusAnalysisStarted)
	statusActionResult := statusEval.DecisionCode
	if !run.StatusAnalysisExecuted {
		statusActionResult = "skipped: " + run.StatusAnalysisSkipReason
	}
	finishAnalysis(&statusAnalysis, s.cfg.OllamaModel, statusEval, nil, statusDecision.Reason, statusDecision.Confidence, statusEval.Checks, model.ActionAudit{Type: "add_status_followup", Proposed: statusEval.Accepted, DryRun: s.cfg.DryRun, Result: statusActionResult}, statusAnalysisErr)
	attachAnalysisTrace(&statusAnalysis, statusTrace)
	run.Analyses = append(run.Analyses, statusAnalysis)
	statusAnalysisIndex := len(run.Analyses) - 1

	// Human-validated outcomes are secondary operational evidence. They never become
	// selectable auto-reply knowledge on their own; the deterministic policy still
	// requires an approved KB article. They do, however, let the reply-selection model
	// benefit from previously verified or corrected cases.
	if s.cfg.OutcomeRetrievalEnabled && s.outcomeRetriever != nil {
		started := time.Now()
		experiences, searchErr := s.outcomeRetriever.SearchOutcomes(ctx, ticketQuery, s.cfg.OutcomeRetrievalSearchK, s.cfg.OutcomeRetrievalMinSimilarity)
		run.ValidatedOutcomeSearchDurationMS = time.Since(started).Milliseconds()
		if s.metrics != nil {
			s.metrics.OutcomeSearches.Add(1)
		}
		if searchErr != nil {
			run.ValidatedOutcomeSearchError = searchErr.Error()
			if s.metrics != nil {
				s.metrics.OutcomeSearchErrors.Add(1)
			}
			if !s.cfg.OutcomeRetrievalFailOpen {
				run.Reason = "validated_outcome_search_failed"
				finish(searchErr)
				return searchErr
			}
			slog.Warn("validated outcome retrieval failed; continuing without experience evidence", "ticket_id", id, "error", searchErr)
		} else {
			for _, e := range experiences {
				ev := model.ValidatedOutcomeEvidence{MemoryID: e.MemoryID, OutcomeID: e.OutcomeID, Decision: e.Decision, Text: compactLearningText(e.Text, 5000), Similarity: e.Similarity, Source: e.Source, TicketID: e.TicketID, KnowledgeID: e.KnowledgeID}
				contextData.ValidatedOutcomes = append(contextData.ValidatedOutcomes, ev)
				run.ValidatedOutcomeCandidates = append(run.ValidatedOutcomeCandidates, ev)
			}
			if s.metrics != nil {
				s.metrics.OutcomeSearchHits.Add(uint64(len(experiences)))
			}
		}
	}

	// Stage 3 starts only after the category and optional status result are known. Reply knowledge is
	// reranked and selected against the effective category, so unrelated articles
	// are less likely to reach the answer-selection model.
	hits := s.knowledge.RerankForCategory(retrievalHits, replyBasis.ID)
	replyLLMHits, candidateCutoff := selectKnowledgeCandidates(hits, llmTopK, s.cfg.KnowledgeRetrievalFloor, s.cfg.KnowledgeCandidateMaxGap)
	if statusEval.Accepted {
		replyLLMHits = nil
		run.ReplyAnalysisSkipReason = "status_reply_selected"
	} else if !canReply {
		replyLLMHits = nil
		run.ReplyAnalysisSkipReason = "existing_followup"
	} else if !s.cfg.AutoReply {
		replyLLMHits = nil
		run.ReplyAnalysisSkipReason = "auto_reply_disabled"
	} else if len(replyLLMHits) == 0 {
		run.ReplyAnalysisSkipReason = "no_reply_knowledge_candidates"
	}
	run.KnowledgeLLMCandidates = len(replyLLMHits)
	run.KnowledgeCandidateCutoff = candidateCutoff
	replyCandidateIDs := knowledgeHitIDSet(replyLLMHits)
	run.ReplyKnowledgeCandidates = auditKnowledgeCandidates(hits, s.cfg.KnowledgeMinScore, auditTopK, replyCandidateIDs, candidateCutoff, s.cfg.KnowledgeRetrievalFloor, llmTopK)
	// Backwards-compatible alias for existing API consumers and old UI code.
	run.KnowledgeCandidates = append([]model.KnowledgeCandidateAudit(nil), run.ReplyKnowledgeCandidates...)

	var replyDecision model.Decision
	replyAnalysisStarted := time.Now()
	var replyAnalysisErr error
	var replyTrace *ollama.Trace
	switch run.ReplyAnalysisSkipReason {
	case "status_reply_selected":
		replyDecision.Reason = "Normale Antwortanalyse nicht ausgeführt: Ein vordefiniertes Status-Template wurde freigegeben."
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Normale KB-Antwortanalyse wurde benötigt", Status: "info", Actual: "übersprungen: Status-Template ausgewählt", Expected: "nur ohne freigegebenes Status-Template"})
	case "existing_followup":
		replyDecision.Reason = "Antwortanalyse nicht ausgeführt: Ticket besitzt bereits ein Followup."
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Antwortanalyse wurde benötigt", Status: "info", Actual: "übersprungen: vorhandenes Followup", Expected: "nur ohne vorhandenes Followup"})
	case "auto_reply_disabled":
		replyDecision.Reason = "Antwortanalyse nicht ausgeführt: AUTO_REPLY ist deaktiviert."
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Antwortanalyse wurde benötigt", Status: "info", Actual: "übersprungen: AUTO_REPLY=false", Expected: "AUTO_REPLY=true"})
	case "no_reply_knowledge_candidates":
		replyDecision.Reason = "Antwortanalyse nicht ausgeführt: Keine Antwort-KB erreichte die Kandidatenauswahl."
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Antwortanalyse wurde benötigt", Status: "info", Actual: "übersprungen: keine Kandidaten", Expected: "mindestens ein Antwortkandidat"})
	default:
		run.ReplyAnalysisExecuted = true
		replyCtx, trace := ollama.WithTrace(ctx, s.cfg.OllamaRoutingMode)
		replyTrace = trace
		replyDecision, err = s.ai.AnalyseReply(replyCtx, t, replyBasis, replyLLMHits, contextData)
		run.ReplyAnalysisDurationMS = time.Since(replyAnalysisStarted).Milliseconds()
		if err != nil {
			replyAnalysisErr = err
			// A failed second stage must not discard a valid category result. The
			// reply is disabled and the category continues through the Go policy.
			run.ReplyAnalysisSkipReason = "reply_ai_failed"
			replyDecision = model.Decision{}
			replyDecision.Reason = "Antwortanalyse fehlgeschlagen: " + err.Error()
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Antwortanalyse konnte ausgeführt werden", Status: "warn", Actual: err.Error(), Expected: "erfolgreich", Detail: "Die Kategorieanalyse bleibt gültig; es wird keine Antwort vorgeschlagen."})
			s.metrics.Errors.Add(1)
			slog.Warn("reply analysis failed; category result retained", "ticket_id", id, "error", err)
		} else {
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_ai", Group: "execution", Label: "Antwortanalyse konnte ausgeführt werden", Status: "pass", Actual: "erfolgreich", Expected: "erfolgreich"})
		}
	}
	run.ReplyAIReason = strings.TrimSpace(replyDecision.Reason)
	replyAnalysis := newAnalysis(run, "reply_selection", replyPromptVersion, map[string]any{"ticket": t, "effective_category": replyBasis, "knowledge_candidates": replyLLMHits, "context": contextData}, replyAnalysisStarted)
	replyActionResult := "reply_analysis_completed"
	if run.ReplyAnalysisSkipReason != "" {
		replyActionResult = "skipped: " + run.ReplyAnalysisSkipReason
	}
	finishAnalysis(&replyAnalysis, s.cfg.OllamaModel, replyDecision.Reply, nil, replyDecision.Reason, replyDecision.Reply.Confidence, nil, model.ActionAudit{Type: "add_followup", DryRun: s.cfg.DryRun, Result: replyActionResult}, replyAnalysisErr)
	attachAnalysisTrace(&replyAnalysis, replyTrace)
	run.Analyses = append(run.Analyses, replyAnalysis)
	replyAnalysisIndex := len(run.Analyses) - 1

	decision := model.Decision{}
	decision.Category = categoryDecision.Category
	decision.Reply = replyDecision.Reply
	decision.Reason = joinAIReasons(run.CategoryAIReason, run.StatusAIReason, run.ReplyAIReason)

	if len(hits) > 0 {
		run.KnowledgeTopID = hits[0].Doc.ID
		run.KnowledgeTopTitle = hits[0].Doc.Title
		run.KnowledgeScore = hits[0].Score
		run.KnowledgeSemanticScore = hits[0].SemanticScore
		run.KnowledgeTitleScore = hits[0].TitleScore
		run.KnowledgeLexicalScore = hits[0].LexicalScore
		run.KnowledgeKeywordScore = hits[0].KeywordScore
		run.KnowledgeCategoryScore = hits[0].CategoryScore
		run.KnowledgeBestChunk = hits[0].BestChunkExcerpt
		run.KnowledgeBestQueryChunk = hits[0].BestQueryExcerpt
		run.KnowledgeQueryChunks = hits[0].QueryChunkCount
		run.KnowledgeDocumentChunks = hits[0].DocumentChunkCount
		run.KnowledgeThreshold = s.cfg.KnowledgeMinScore
		if hits[0].Doc.MinScore > run.KnowledgeThreshold {
			run.KnowledgeThreshold = hits[0].Doc.MinScore
		}
	}
	result, err := s.policy.Evaluate(t, decision, categories, hits, contextData)
	if err == nil {
		result.CategoryChecks = append(result.CategoryChecks, categoryKnowledgeMappingChecks(categoryLLMHits, categories, result.CategoryRecommendationID)...)
	}
	if err != nil {
		run.Reason = "policy_rejected"
		finish(err)
		return err
	}
	if statusEval.Accepted {
		// This is not model-generated prose. The model only selected a verified
		// Uptime Kuma candidate; the exact operator-defined template is rendered
		// deterministically and takes precedence over the normal KB reply.
		result.Reply = true
		result.ReplyText = statusEval.ReplyText
		result.ReplyIsHTML = statusEval.ReplyIsHTML
		result.KnowledgeID = ""
		result.ReplyKnowledgeID = ""
		result.ReplyRecommendation = true
		result.ReplyConfidence = statusDecision.Confidence
		result.ReplyThreshold = s.cfg.ContextStatusReplyMinAIConfidence
		result.ReplyDecision = statusEval.DecisionCode
		result.ReplyChecks = append([]model.RuleCheck(nil), statusEval.Checks...)
		result.AIReason = joinAIReasons(run.CategoryAIReason, run.StatusAIReason, run.ReplyAIReason)
	}
	run.AIReason = result.AIReason
	run.Reason = result.AIReason // backwards compatible audit field
	run.AIRecommendedCategoryID = result.CategoryRecommendationID
	run.AIRecommendedCategoryName = result.CategoryRecommendationName
	run.AICategoryConfidence = result.CategoryConfidence
	run.CategoryThreshold = result.CategoryThreshold
	run.CategoryDecision = result.CategoryDecision
	run.CategoryProposed = result.CategoryID
	run.CategoryWouldChange = result.ChangeCategory
	run.AIReplyRecommended = result.ReplyRecommendation
	run.AIReplyConfidence = result.ReplyConfidence
	run.ReplyThreshold = result.ReplyThreshold
	run.AIKnowledgeID = result.ReplyKnowledgeID
	run.ReplyDecision = result.ReplyDecision
	run.ReplyProposed = result.Reply
	if result.Reply {
		run.ReplyProposedText = compactLearningText(stripHTML(result.ReplyText), 12000)
	}
	run.KnowledgeID = result.KnowledgeID
	if result.KnowledgeThreshold > 0 {
		run.KnowledgeThreshold = result.KnowledgeThreshold
	}
	run.KnowledgeEvidenceScore = result.KnowledgeEvidenceScore
	run.KnowledgeRetrievalFloor = result.KnowledgeRetrievalFloor
	run.KnowledgeCategoryAligned = result.KnowledgeCategoryAligned
	run.CategoryChecks = append([]model.RuleCheck(nil), result.CategoryChecks...)
	run.ReplyChecks = append([]model.RuleCheck(nil), result.ReplyChecks...)
	if categoryAnalysisIndex >= 0 && categoryAnalysisIndex < len(run.Analyses) {
		a := &run.Analyses[categoryAnalysisIndex]
		a.Checks = append([]model.RuleCheck(nil), result.CategoryChecks...)
		a.Action = model.ActionAudit{Type: "set_category", Proposed: result.ChangeCategory, DryRun: s.cfg.DryRun, Before: fmt.Sprintf("category=%d", t.CategoryID), After: fmt.Sprintf("category=%d", result.CategoryID), Result: result.CategoryDecision}
	}
	if replyAnalysisIndex >= 0 && replyAnalysisIndex < len(run.Analyses) {
		a := &run.Analyses[replyAnalysisIndex]
		a.Checks = append([]model.RuleCheck(nil), result.ReplyChecks...)
		a.Action.Proposed = result.Reply && canReply && !statusEval.Accepted
		a.Action.Result = result.ReplyDecision
	}
	if statusAnalysisIndex >= 0 && statusAnalysisIndex < len(run.Analyses) {
		run.Analyses[statusAnalysisIndex].Action.Proposed = statusEval.Accepted && canReply
		run.Analyses[statusAnalysisIndex].Action.Result = statusEval.DecisionCode
	}
	run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_dry_run", Group: "execution", Label: "Live-Schreibmodus aktiv", Status: map[bool]string{true: "info", false: "pass"}[s.cfg.DryRun], Blocking: false, Actual: map[bool]string{true: "DRY RUN", false: "LIVE"}[s.cfg.DryRun], Expected: "LIVE für tatsächliche Änderungen", Detail: "Im DRY RUN werden freigegebene Aktionen nur simuliert."})
	run.PolicyReason = policySummary(result.CategoryDecision, run.PriorityDecision, result.ReplyDecision)
	if !canReply {
		if replyAnalysisIndex >= 0 {
			run.Analyses[replyAnalysisIndex].Action.Proposed = false
			run.Analyses[replyAnalysisIndex].Action.Result = "reply_existing_followup"
		}
		if statusAnalysisIndex >= 0 {
			run.Analyses[statusAnalysisIndex].Action.Proposed = false
			run.Analyses[statusAnalysisIndex].Action.Result = "reply_existing_followup"
		}
		// An existing followup is the authoritative execution-level reason why
		// no reply can be proposed, regardless of the model/policy recommendation.
		run.ReplyProposed = false
		run.ReplyDecision = "reply_existing_followup"
		run.PolicyReason = policySummary(result.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
	}

	// Re-read the ticket immediately before any write. This prevents a stale
	// model decision from overwriting a human change made during inference.
	priorityWritePlanned := priorityResult.ChangePriority && s.cfg.AutoPriority
	if (result.ChangeCategory || priorityWritePlanned || (result.Reply && canReply)) && !s.cfg.DryRun {
		fresh, err := s.glpi.GetTicket(ctx, id)
		if err != nil {
			run.Reason = "prewrite_ticket_recheck_failed"
			finish(err)
			return err
		}
		if sourceVersion(fresh) != run.SourceVersion {
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_ticket_unchanged", Group: "execution", Label: "Ticket seit Analyse unverändert", Status: "fail", Blocking: true, Actual: "geändert", Expected: "unverändert"})
			run.Outcome = "skipped"
			run.Reason = "ticket_changed_before_write"
			if result.ChangeCategory {
				run.CategoryDecision = "category_ticket_changed_before_write"
			}
			if result.Reply && canReply {
				run.ReplyDecision = "reply_ticket_changed_before_write"
			}
			run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
			s.metrics.Skipped.Add(1)
			finish(nil)
			return nil
		}
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_ticket_unchanged", Group: "execution", Label: "Ticket seit Analyse unverändert", Status: "pass", Actual: "unverändert", Expected: "unverändert"})
	}

	if result.ChangeCategory && !s.cfg.DryRun {
		if err := s.glpi.SetCategory(ctx, id, result.CategoryID); err != nil {
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_category_write", Group: "execution", Label: "Kategorie konnte in GLPI geschrieben werden", Status: "fail", Blocking: true, Actual: err.Error(), Expected: "erfolgreich"})
			run.Reason = "category_write_failed"
			run.CategoryDecision = "category_write_failed"
			run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
			finish(err)
			return err
		}
		run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_category_write", Group: "execution", Label: "Kategorie konnte in GLPI geschrieben werden", Status: "pass", Actual: fmt.Sprintf("#%d", result.CategoryID), Expected: "erfolgreich"})
		run.CategoryChanged = true
		run.CategoryDecision = "category_written"
		s.metrics.CategoryChanged.Add(1)
	} else if result.ChangeCategory {
		run.CategoryDecision = "category_accepted_dry_run"
	}
	if categoryAnalysisIndex >= 0 {
		run.Analyses[categoryAnalysisIndex].Action.Executed = run.CategoryChanged
		run.Analyses[categoryAnalysisIndex].Action.Result = run.CategoryDecision
	}
	run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)

	if priorityResult.ChangePriority {
		if !s.cfg.AutoPriority {
			run.PriorityDecision = "priority_accepted_shadow"
		} else if s.cfg.DryRun {
			run.PriorityDecision = "priority_accepted_dry_run"
		} else {
			fresh, loadErr := s.glpi.GetTicket(ctx, id)
			if loadErr != nil {
				run.PriorityDecision = "priority_prewrite_recheck_failed"
				if priorityAnalysisIndex >= 0 {
					run.Analyses[priorityAnalysisIndex].Action.Error = loadErr.Error()
					run.Analyses[priorityAnalysisIndex].Action.Result = run.PriorityDecision
				}
				finish(loadErr)
				return loadErr
			}
			expectedCategory := t.CategoryID
			if run.CategoryChanged {
				expectedCategory = result.CategoryID
			}
			if !sameDecisionSource(t, fresh, expectedCategory) || fresh.Priority != t.Priority {
				run.PriorityDecision = "priority_ticket_changed_before_write"
				run.PriorityWouldChange = false
				if priorityAnalysisIndex >= 0 {
					run.Analyses[priorityAnalysisIndex].Action.Proposed = false
					run.Analyses[priorityAnalysisIndex].Action.Result = run.PriorityDecision
				}
			} else if writer, ok := s.glpi.(priorityWriter); !ok {
				writeErr := fmt.Errorf("GLPI connector does not implement priority writes")
				run.PriorityDecision = "priority_write_unavailable"
				if priorityAnalysisIndex >= 0 {
					run.Analyses[priorityAnalysisIndex].Action.Error = writeErr.Error()
					run.Analyses[priorityAnalysisIndex].Action.Result = run.PriorityDecision
				}
				finish(writeErr)
				return writeErr
			} else if writeErr := writer.SetPriority(ctx, id, priorityResult.PriorityAfter); writeErr != nil {
				run.PriorityDecision = "priority_write_failed"
				if priorityAnalysisIndex >= 0 {
					run.Analyses[priorityAnalysisIndex].Action.Error = writeErr.Error()
					run.Analyses[priorityAnalysisIndex].Action.Result = run.PriorityDecision
				}
				finish(writeErr)
				return writeErr
			} else {
				run.PriorityChanged = true
				run.PriorityDecision = "priority_written"
				s.metrics.PriorityChanges.Add(1)
				if priorityAnalysisIndex >= 0 {
					run.Analyses[priorityAnalysisIndex].Action.Executed = true
					run.Analyses[priorityAnalysisIndex].Action.DryRun = false
					run.Analyses[priorityAnalysisIndex].Action.Result = "priority_written"
				}
			}
		}
		if priorityAnalysisIndex >= 0 {
			run.Analyses[priorityAnalysisIndex].Action.Result = run.PriorityDecision
		}
	}

	if result.Reply && canReply {
		// If category was just changed by this process, date_mod will legitimately
		// differ. Compare the decision-relevant ticket fields instead and require
		// the category we expect before posting a reply.
		if !s.cfg.DryRun {
			fresh, err := s.glpi.GetTicket(ctx, id)
			if err != nil {
				run.Reason = "prereply_ticket_recheck_failed"
				finish(err)
				return err
			}
			expectedCategory := t.CategoryID
			if result.ChangeCategory {
				expectedCategory = result.CategoryID
			}
			if !sameDecisionSource(t, fresh, expectedCategory) {
				run.ReplyProposed = false
				run.Reason = "ticket_changed_before_reply"
				run.ReplyDecision = "reply_ticket_changed_before_write"
				run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
				run.Outcome = "skipped"
				s.metrics.Skipped.Add(1)
				finish(nil)
				return nil
			}
		}
		followups, err = s.glpi.GetFollowups(ctx, id)
		if err != nil {
			run.Reason = "followup_recheck_failed"
			finish(err)
			return err
		}
		if len(followups) > 0 {
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_followup_recheck", Group: "execution", Label: "Unmittelbar vor Antwort ist weiterhin kein Followup vorhanden", Status: "fail", Blocking: true, Actual: fmt.Sprintf("%d Followups", len(followups)), Expected: "0 Followups"})
			run.ReplyProposed = false
			run.Reason = "followup_appeared_before_write"
			run.ReplyDecision = "reply_followup_appeared_before_write"
			if statusEval.Accepted && statusAnalysisIndex >= 0 {
				run.Analyses[statusAnalysisIndex].Action.Proposed = false
				run.Analyses[statusAnalysisIndex].Action.Result = run.ReplyDecision
			} else if replyAnalysisIndex >= 0 {
				run.Analyses[replyAnalysisIndex].Action.Proposed = false
				run.Analyses[replyAnalysisIndex].Action.Result = run.ReplyDecision
			}
		} else if !s.cfg.DryRun {
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_followup_recheck", Group: "execution", Label: "Unmittelbar vor Antwort ist weiterhin kein Followup vorhanden", Status: "pass", Actual: "0 Followups", Expected: "0 Followups"})
			if err := s.glpi.AddFollowup(ctx, id, result.ReplyText, result.ReplyIsHTML); err != nil {
				run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_write", Group: "execution", Label: "Antwort konnte in GLPI geschrieben werden", Status: "fail", Blocking: true, Actual: err.Error(), Expected: "erfolgreich"})
				run.Reason = "reply_write_failed"
				run.ReplyDecision = "reply_write_failed"
				run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
				finish(err)
				return err
			}
			run.ExecutionChecks = append(run.ExecutionChecks, model.RuleCheck{Code: "execution_reply_write", Group: "execution", Label: "Antwort konnte in GLPI geschrieben werden", Status: "pass", Actual: "erfolgreich", Expected: "erfolgreich"})
			run.ReplyWritten = true
			run.ReplyDecision = "reply_written"
			if statusEval.Accepted && statusAnalysisIndex >= 0 {
				run.Analyses[statusAnalysisIndex].Action.Executed = true
				run.Analyses[statusAnalysisIndex].Action.DryRun = false
				run.Analyses[statusAnalysisIndex].Action.Result = run.ReplyDecision
			} else if replyAnalysisIndex >= 0 {
				run.Analyses[replyAnalysisIndex].Action.Executed = true
				run.Analyses[replyAnalysisIndex].Action.DryRun = false
				run.Analyses[replyAnalysisIndex].Action.Result = run.ReplyDecision
			}
			s.metrics.Replies.Add(1)
		} else {
			run.ReplyDecision = "reply_accepted_dry_run"
			if statusEval.Accepted && statusAnalysisIndex >= 0 {
				run.Analyses[statusAnalysisIndex].Action.Result = run.ReplyDecision
			} else if replyAnalysisIndex >= 0 {
				run.Analyses[replyAnalysisIndex].Action.Result = run.ReplyDecision
			}
		}
		run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
	}

	// Persist the final GLPI version after our own write so the next poll does
	// not immediately process the same self-induced modification again.
	if !s.cfg.DryRun && (run.CategoryChanged || run.PriorityChanged || run.ReplyWritten) {
		if finalTicket, e := s.glpi.GetTicket(ctx, id); e == nil {
			run.SourceVersion = sourceVersion(finalTicket)
		}
	}
	run.PolicyReason = policySummary(run.CategoryDecision, run.PriorityDecision, run.ReplyDecision)
	run.Outcome = "processed"
	s.metrics.Processed.Add(1)
	finish(nil)
	return nil
}

// DiagnoseRun returns the persisted, historical decision record. The rule
// checks stored on the run are the authoritative explanation of the policy at
// execution time.
func (s *Service) DiagnoseRun(ctx context.Context, runID string) (model.RunRecord, error) {
	_ = ctx
	r, ok := s.state.FindRun(strings.TrimSpace(runID))
	if !ok {
		return model.RunRecord{}, fmt.Errorf("run %q not found", runID)
	}
	return r, nil
}

// DiagnoseKnowledge recalculates one arbitrary knowledge article against the
// current ticket/index. This is intentionally marked as a current re-evaluation
// when the GLPI ticket changed since the historical run.
func (s *Service) DiagnoseKnowledge(ctx context.Context, runID, knowledgeID, purpose string) (model.KnowledgeDiagnostic, error) {
	run, ok := s.state.FindRun(strings.TrimSpace(runID))
	if !ok {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("run %q not found", runID)
	}
	doc, ok := s.knowledge.ByID(strings.TrimSpace(knowledgeID))
	if !ok {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("knowledge %q not found", knowledgeID)
	}
	purpose = strings.ToLower(strings.TrimSpace(purpose))
	if purpose == "" {
		purpose = "reply"
	}
	if purpose != "category" && purpose != "reply" {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("unknown diagnostic purpose %q", purpose)
	}
	t, err := s.glpi.GetTicket(ctx, run.TicketID)
	if err != nil {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("load current ticket: %w", err)
	}
	cats, err := s.getCategories(ctx)
	if err != nil {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("load categories: %w", err)
	}
	query := t.Name + "\n" + stripHTML(t.Content)
	indexedHits, err := s.knowledge.Search(ctx, query, 0, cats)
	if err != nil {
		return model.KnowledgeDiagnostic{}, err
	}

	sources := s.cfg.KnowledgeAllowedSources
	if purpose == "category" {
		sources = s.cfg.KnowledgeCategorySources
	}
	filteredHits := knowledge.FilterHitsBySources(indexedHits, sources, 0)
	basisID := run.ReplyBasisCategoryID
	if basisID == 0 {
		basisID = run.AIRecommendedCategoryID
	}
	if purpose == "reply" && basisID != 0 {
		filteredHits = s.knowledge.RerankForCategory(filteredHits, basisID)
	}
	maxCandidates := s.cfg.KnowledgeTopK
	if maxCandidates <= 0 {
		maxCandidates = 6
	}
	llmHits, cutoff := selectKnowledgeCandidates(filteredHits, maxCandidates, s.cfg.KnowledgeRetrievalFloor, s.cfg.KnowledgeCandidateMaxGap)
	llmSet := knowledgeHitIDSet(llmHits)

	var hit *model.KnowledgeHit
	initialRank := 0
	initialScore := 0.0
	for i := range filteredHits {
		if filteredHits[i].Doc.ID == doc.ID {
			hit = &filteredHits[i]
			initialRank = i + 1
			initialScore = filteredHits[i].Score
			break
		}
	}
	if hit == nil {
		for i := range indexedHits {
			if indexedHits[i].Doc.ID == doc.ID {
				hit = &indexedHits[i]
				initialScore = indexedHits[i].Score
				break
			}
		}
	}
	if hit == nil {
		return model.KnowledgeDiagnostic{}, fmt.Errorf("knowledge %q is not in active index", knowledgeID)
	}

	_, sent := llmSet[doc.ID]
	sourceOK := sourceConfigured(doc.Source, sources)
	reason := candidateSelectionReason(initialRank, initialScore, sent, cutoff, s.cfg.KnowledgeRetrievalFloor, maxCandidates)
	if !sourceOK {
		reason = "source_not_allowed_for_purpose"
	}
	required := s.cfg.KnowledgeMinScore
	if doc.MinScore > required {
		required = doc.MinScore
	}
	checks := []model.RuleCheck{
		{Code: "candidate_in_active_index", Group: "retrieval", Label: "Artikel ist im aktiven Knowledge-Index", Status: "pass", Actual: "ja", Expected: "ja"},
		{Code: "candidate_source_for_purpose", Group: "retrieval", Label: "Quelle ist für diese Analyse freigegeben", Status: passFail(sourceOK), Blocking: !sourceOK, Actual: doc.Source, Expected: strings.Join(sources, ", ")},
		{Code: "candidate_retrieval_floor", Group: "retrieval", Label: "Retrieval-Score erreicht Floor", Status: passFail(sourceOK && initialScore >= s.cfg.KnowledgeRetrievalFloor), Blocking: sourceOK && initialScore < s.cfg.KnowledgeRetrievalFloor, Actual: percentText(initialScore), Expected: ">= " + percentText(s.cfg.KnowledgeRetrievalFloor)},
		{Code: "candidate_dynamic_cutoff", Group: "retrieval", Label: "Artikel liegt innerhalb des dynamischen Top-K-Abstands", Status: passFail(sourceOK && initialScore >= cutoff), Blocking: sourceOK && initialScore < cutoff, Actual: percentText(initialScore), Expected: ">= " + percentText(cutoff), Detail: fmt.Sprintf("Bester Treffer minus %.1f Prozentpunkte, mindestens Retrieval-Floor.", s.cfg.KnowledgeCandidateMaxGap*100)},
		{Code: "candidate_sent_to_ai", Group: "retrieval", Label: "Artikel wurde an die passende KI-Stufe übergeben", Status: passFail(sent), Blocking: sourceOK && !sent, Actual: boolText(sent), Expected: "ja", Detail: reason},
	}

	evidenceScore := 0.0
	aiSelected := false
	if purpose == "category" {
		matches := len(doc.Categories) == 0 || containsCategory(doc.Categories, run.AIRecommendedCategoryID)
		checks = append(checks, model.RuleCheck{Code: "candidate_category_support", Group: "category", Label: "Artikel unterstützt die empfohlene Kategorie", Status: passFail(matches), Actual: boolText(matches), Expected: fmt.Sprintf("Kategorie #%d", run.AIRecommendedCategoryID), Detail: "Unbeschränkte Artikel gelten als allgemeiner Klassifikationshinweis."})
	} else {
		decision := model.Decision{}
		decision.Category.ID = run.AIRecommendedCategoryID
		decision.Category.Confidence = run.AICategoryConfidence
		decision.Reply.Allowed = run.AIReplyRecommended
		decision.Reply.Confidence = run.AIReplyConfidence
		decision.Reply.KnowledgeID = doc.ID
		decision.Reason = run.ReplyAIReason
		ctxData := model.ContextSnapshot{}
		if s.context != nil && s.cfg.ContextEnabled {
			ctxData = s.context.Collect(ctx, t)
		}
		res, _ := s.policy.Evaluate(t, decision, cats, []model.KnowledgeHit{*hit}, ctxData)
		evidenceScore = res.KnowledgeEvidenceScore
		checks = append(checks, res.ReplyChecks...)
		aiSelected = run.AIKnowledgeID == doc.ID
	}

	return model.KnowledgeDiagnostic{
		RunID: run.RunID, Purpose: purpose, TicketID: run.TicketID, KnowledgeID: doc.ID, Title: doc.Title, Source: doc.Source,
		CurrentTicketChanged: sourceVersion(t) != run.SourceVersion, RetrievalRank: initialRank, RetrievalScore: initialScore,
		SemanticScore: hit.SemanticScore, TitleScore: hit.TitleScore, LexicalScore: hit.LexicalScore, KeywordScore: hit.KeywordScore, CategoryScore: hit.CategoryScore,
		CandidateCutoff: cutoff, SentToAI: sent, SelectionReason: reason, AISelected: aiSelected,
		EvidenceScore: evidenceScore, RequiredScore: required, BestChunkExcerpt: hit.BestChunkExcerpt, BestQueryExcerpt: hit.BestQueryExcerpt,
		ExternalCategories: append([]string(nil), doc.ExternalCategories...), UnmappedCategories: append([]string(nil), doc.UnmappedExternalCategories...), Checks: checks, Document: doc,
	}, nil
}

func sourceConfigured(source string, sources []string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	for _, allowed := range sources {
		if source == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func containsCategory(categories []int64, id int64) bool {
	for _, categoryID := range categories {
		if categoryID == id {
			return true
		}
	}
	return false
}

func candidateSelectionReason(rank int, score float64, sent bool, cutoff, floor float64, maxCandidates int) string {
	if sent {
		return "sent_to_ai"
	}
	if score < floor {
		return "below_retrieval_floor"
	}
	if score < cutoff {
		return "outside_candidate_gap"
	}
	if maxCandidates > 0 && rank > maxCandidates {
		return "max_candidates_reached"
	}
	return "not_selected"
}

func auditKnowledgeCandidates(hits []model.KnowledgeHit, globalMin float64, limit int, sentToAI map[string]struct{}, cutoff, retrievalFloor float64, maxCandidates int) []model.KnowledgeCandidateAudit {
	if limit <= 0 || limit > len(hits) {
		limit = len(hits)
	}
	out := make([]model.KnowledgeCandidateAudit, 0, limit)
	for idx, h := range hits[:limit] {
		required := globalMin
		if h.Doc.MinScore > required {
			required = h.Doc.MinScore
		}
		_, wasSent := sentToAI[h.Doc.ID]
		reason := candidateSelectionReason(idx+1, h.Score, wasSent, cutoff, retrievalFloor, maxCandidates)
		out = append(out, model.KnowledgeCandidateAudit{
			ID: h.Doc.ID, Title: h.Doc.Title, Source: h.Doc.Source, Score: h.Score,
			SemanticScore: h.SemanticScore, TitleScore: h.TitleScore, LexicalScore: h.LexicalScore, KeywordScore: h.KeywordScore,
			CategoryScore: h.CategoryScore, RequiredScore: required, AutoReply: h.Doc.AutoReply,
			AutoReplyDecision: h.Doc.AutoReplyDecision, AutoReplyDetail: h.Doc.AutoReplyDetail,
			BestChunkExcerpt: h.BestChunkExcerpt, BestQueryExcerpt: h.BestQueryExcerpt,
			QueryChunkCount: h.QueryChunkCount, DocumentChunkCount: h.DocumentChunkCount, SentToAI: wasSent, RetrievalRank: idx + 1, SelectionReason: reason,
		})
	}
	return out
}

func selectKnowledgeCandidates(hits []model.KnowledgeHit, maxCandidates int, retrievalFloor, maxGap float64) ([]model.KnowledgeHit, float64) {
	if len(hits) == 0 || maxCandidates <= 0 {
		return nil, retrievalFloor
	}
	best := hits[0].Score
	if best < retrievalFloor {
		return nil, retrievalFloor
	}
	cutoff := best - maxGap
	if cutoff < retrievalFloor {
		cutoff = retrievalFloor
	}
	capacity := maxCandidates
	if len(hits) < capacity {
		capacity = len(hits)
	}
	out := make([]model.KnowledgeHit, 0, capacity)
	for _, h := range hits {
		if h.Score < cutoff || h.Score < retrievalFloor {
			break
		}
		out = append(out, h)
		if len(out) >= maxCandidates {
			break
		}
	}
	return out, cutoff
}

func knowledgeHitIDSet(hits []model.KnowledgeHit) map[string]struct{} {
	out := make(map[string]struct{}, len(hits))
	for _, h := range hits {
		out[h.Doc.ID] = struct{}{}
	}
	return out
}

func effectiveReplyCategory(t model.Ticket, d model.Decision, categories []model.Category, autoCategory bool, threshold float64) model.Category {
	effectiveID := t.CategoryID
	known := make(map[int64]model.Category, len(categories))
	for _, category := range categories {
		known[category.ID] = category
	}
	if d.Category.ID == t.CategoryID {
		effectiveID = t.CategoryID
	} else if autoCategory && d.Category.ID != 0 && d.Category.Confidence >= threshold {
		if _, ok := known[d.Category.ID]; ok {
			effectiveID = d.Category.ID
		}
	}
	if category, ok := known[effectiveID]; ok {
		return category
	}
	return model.Category{ID: effectiveID, Name: fmt.Sprintf("Kategorie #%d", effectiveID)}
}

func policySummary(decisions ...string) string {
	parts := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		decision = strings.TrimSpace(decision)
		if decision != "" {
			parts = append(parts, decision)
		}
	}
	return strings.Join(parts, "; ")
}

func joinAIReasons(reasons ...string) string {
	labels := []string{"Kategorie", "Status", "Antwort"}
	parts := make([]string, 0, len(reasons))
	for i, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		label := "Analyse"
		if i < len(labels) {
			label = labels[i]
		}
		parts = append(parts, label+": "+reason)
	}
	return strings.Join(parts, " | ")
}

func auditContextDetails(c model.ContextSnapshot, limit int) []model.ContextAuditItem {
	if limit <= 0 {
		limit = 5
	}
	out := make([]model.ContextAuditItem, 0, limit*4)
	for i, x := range c.Changes {
		if i >= limit {
			break
		}
		out = append(out, model.ContextAuditItem{Kind: "change", ID: x.ID, Name: x.Name, Relevance: x.Relevance, Detail: strings.TrimSpace(x.PlannedBegin + " – " + x.PlannedEnd)})
	}
	for i, x := range c.MajorIncidents {
		if i >= limit {
			break
		}
		out = append(out, model.ContextAuditItem{Kind: "incident", ID: x.ID, Name: x.Name, Relevance: x.Relevance, Status: fmt.Sprint(x.StatusID), Detail: auditExcerpt(x.Content, 320)})
	}
	for i, x := range c.ServiceIssues {
		if i >= limit {
			break
		}
		name := x.MonitorName
		if name == "" {
			name = x.IncidentTitle
		}
		out = append(out, model.ContextAuditItem{Kind: "uptime", ID: x.MonitorID, Name: name, Relevance: x.Relevance, Status: x.Status, Detail: auditExcerpt(x.Message, 320)})
	}
	for i, x := range c.UserDevices {
		if i >= limit {
			break
		}
		name := x.Name
		if name == "" {
			name = fmt.Sprintf("%s #%d", x.ItemType, x.ID)
		}
		parts := make([]string, 0, 3)
		for _, v := range []string{x.Serial, x.InventoryNumber, x.Location} {
			if strings.TrimSpace(v) != "" {
				parts = append(parts, strings.TrimSpace(v))
			}
		}
		detail := strings.Join(parts, " · ")
		out = append(out, model.ContextAuditItem{Kind: "device", ID: x.ID, Name: name, Status: x.Status, Detail: detail})
	}
	return out
}

func auditExcerpt(v string, max int) string {
	v = strings.Join(strings.Fields(v), " ")
	if max <= 0 || len(v) <= max {
		return v
	}
	return strings.TrimSpace(v[:max]) + "…"
}

func (s *Service) getCategories(ctx context.Context) ([]model.Category, error) {
	s.catMu.RLock()
	if len(s.categories) > 0 && time.Since(s.catAt) < 10*time.Minute {
		out := append([]model.Category(nil), s.categories...)
		s.catMu.RUnlock()
		return s.enrichCategories(out), nil
	}
	s.catMu.RUnlock()
	cats, err := s.glpi.GetCategories(ctx)
	if err != nil {
		return nil, err
	}
	s.catMu.Lock()
	s.categories = append([]model.Category(nil), cats...)
	s.catAt = time.Now()
	s.catMu.Unlock()
	return s.enrichCategories(cats), nil
}

func (s *Service) enrichCategories(cats []model.Category) []model.Category {
	out := append([]model.Category(nil), cats...)
	byID := make(map[int64]*model.Category, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
		out[i].Hints = append(out[i].Hints, semanticCategoryHints(out[i])...)
	}
	categorySources := sourceSet(s.cfg.KnowledgeCategorySources)
	for _, doc := range s.knowledge.List() {
		if _, allowed := categorySources[strings.ToLower(strings.TrimSpace(doc.Source))]; !allowed {
			continue
		}
		for _, id := range doc.Categories {
			if c := byID[id]; c != nil {
				c.Hints = appendUnique(c.Hints, doc.Title)
				for _, k := range doc.Keywords {
					c.Hints = appendUnique(c.Hints, k)
				}
			}
		}
	}
	if s.cfg.LearningEnabled && s.learning != nil {
		for i := range out {
			out[i].Examples = s.learning.ExamplesFor(out[i].ID, s.cfg.LearningExamplesPerCategory)
		}
	}
	return out
}

// Categories exposes the same enriched category catalogue that is supplied to
// Ollama. It is used by the authenticated dashboard for human feedback.
func (s *Service) Categories(ctx context.Context) ([]model.Category, error) {
	return s.getCategories(ctx)
}

func (s *Service) RecordCategoryFeedback(ctx context.Context, runID string, categoryID int64) (model.LearningExample, error) {
	if !s.cfg.LearningEnabled || s.learning == nil {
		return model.LearningExample{}, fmt.Errorf("learning is disabled")
	}
	run, ok := s.state.FindRun(strings.TrimSpace(runID))
	if !ok {
		return model.LearningExample{}, fmt.Errorf("run not found")
	}
	cats, err := s.getCategories(ctx)
	if err != nil {
		return model.LearningExample{}, err
	}
	name := categoryName(cats, categoryID)
	if categoryID <= 0 || name == "" {
		return model.LearningExample{}, fmt.Errorf("unknown category id %d", categoryID)
	}
	t, err := s.glpi.GetTicket(ctx, run.TicketID)
	if err != nil {
		return model.LearningExample{}, err
	}
	if sourceVersion(t) != run.SourceVersion {
		return model.LearningExample{}, fmt.Errorf("ticket changed since this run; process the current ticket state before teaching it")
	}
	ex := model.LearningExample{RunID: run.RunID, TicketID: t.ID, Subject: strings.TrimSpace(t.Name), Text: compactLearningText(stripHTML(t.Content), 1200), CategoryID: categoryID, CategoryName: name, AIRecommendedCategoryID: run.AIRecommendedCategoryID, AIConfidence: run.AICategoryConfidence, Correction: run.AIRecommendedCategoryID != categoryID, Source: "human-confirmed"}
	return s.learning.Add(ex)
}
func (s *Service) LearningExamples() []model.LearningExample {
	if s.learning == nil {
		return []model.LearningExample{}
	}
	return s.learning.List()
}
func (s *Service) DeleteLearning(id string) error {
	if s.learning == nil {
		return fmt.Errorf("learning is disabled")
	}
	return s.learning.Delete(id)
}
func (s *Service) LearningCount() int {
	if s.learning == nil {
		return 0
	}
	return s.learning.Count()
}

func (s *Service) RecordTicketOutcome(ctx context.Context, runID, decision, correctedReply, note, actor string) (learning.TicketOutcome, error) {
	if !s.cfg.OutcomeLearningEnabled || s.outcomes == nil || s.outcomeSink == nil {
		return learning.TicketOutcome{}, fmt.Errorf("outcome learning is disabled")
	}
	run, ok := s.state.FindRun(strings.TrimSpace(runID))
	if !ok {
		return learning.TicketOutcome{}, fmt.Errorf("run not found")
	}
	// High-trust learning must refer to the same ticket state the AI actually
	// evaluated. If GLPI changed after the run, require a fresh run before a
	// technician can promote its answer into trusted knowledge.
	if strings.TrimSpace(run.SourceVersion) != "" && s.glpi != nil {
		fresh, err := s.glpi.GetTicket(ctx, run.TicketID)
		if err != nil {
			return learning.TicketOutcome{}, fmt.Errorf("verify current ticket before outcome learning: %w", err)
		}
		if sourceVersion(fresh) != run.SourceVersion {
			return learning.TicketOutcome{}, fmt.Errorf("ticket changed since this run; process the current ticket state before validating the AI outcome")
		}
	}
	if !run.ReplyProposed || strings.TrimSpace(run.ReplyProposedText) == "" {
		return learning.TicketOutcome{}, fmt.Errorf("run has no reply proposal to validate")
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	confirmed := strings.TrimSpace(correctedReply)
	if decision == "accepted" {
		confirmed = strings.TrimSpace(run.ReplyProposedText)
	} else if decision == "corrected" {
		if confirmed == "" {
			return learning.TicketOutcome{}, fmt.Errorf("corrected outcome requires corrected_reply")
		}
	} else {
		return learning.TicketOutcome{}, fmt.Errorf("decision must be accepted or corrected")
	}
	input := strings.TrimSpace(run.LearningTicketText)
	if input == "" {
		return learning.TicketOutcome{}, fmt.Errorf("run predates outcome-gated learning and has no learning input snapshot")
	}
	categoryID := run.ReplyBasisCategoryID
	categoryName := strings.TrimSpace(run.ReplyBasisCategoryName)
	if categoryID <= 0 {
		categoryID = run.AIRecommendedCategoryID
		categoryName = strings.TrimSpace(run.AIRecommendedCategoryName)
	}
	if categoryID <= 0 {
		categoryID = run.CategoryBefore
		categoryName = strings.TrimSpace(run.CategoryBeforeName)
	}
	knowledgeID := strings.TrimSpace(run.AIKnowledgeID)
	if knowledgeID == "" {
		knowledgeID = strings.TrimSpace(run.KnowledgeID)
	}
	x := learning.TicketOutcome{RunID: run.RunID, TicketID: run.TicketID, Decision: decision, TicketInput: input, ProposedReply: strings.TrimSpace(run.ReplyProposedText), ConfirmedReply: compactLearningText(stripHTML(confirmed), 12000), CategoryID: categoryID, CategoryName: categoryName, KnowledgeID: knowledgeID, Actor: strings.TrimSpace(actor), Note: compactLearningText(note, 4000), SyncStatus: "pending"}
	if x.Actor == "" {
		x.Actor = "technician"
	}
	stored, err := s.outcomes.Add(x)
	if err != nil {
		return learning.TicketOutcome{}, err
	}
	// Exact repeated confirmations are idempotent. If the same human
	// decision has already been learned, return the existing audit record
	// without sending a duplicate trusted memory to NeuroForge. Failed
	// records intentionally continue below so they can be retried.
	if stored.SyncStatus == "learned" && strings.TrimSpace(stored.NeuroForgeID) != "" {
		if s.metrics != nil {
			s.metrics.OutcomeLearningIdempotent.Add(1)
		}
		return stored, nil
	}
	memoryID, syncErr := s.outcomeSink.LearnOutcome(ctx, stored)
	if syncErr != nil {
		if s.metrics != nil {
			s.metrics.OutcomeLearningFailed.Add(1)
		}
		failed, _ := s.outcomes.UpdateSync(stored.ID, "failed", "", syncErr.Error())
		if s.cfg.OutcomeLearningFailOpen {
			slog.Warn("validated ticket outcome persisted but NeuroForge learning failed", "run_id", run.RunID, "ticket_id", run.TicketID, "error", syncErr)
			return failed, nil
		}
		return failed, fmt.Errorf("validated outcome persisted, but NeuroForge learning failed: %w", syncErr)
	}
	learned, err := s.outcomes.UpdateSync(stored.ID, "learned", memoryID, "")
	if err == nil {
		if s.metrics != nil {
			s.metrics.OutcomeLearningLearned.Add(1)
		}
		if stored.Decision == "accepted" {
			if s.metrics != nil {
				s.metrics.OutcomeLearningAccepted.Add(1)
			}
		} else if stored.Decision == "corrected" {
			if s.metrics != nil {
				s.metrics.OutcomeLearningCorrected.Add(1)
			}
		}
	}
	if err != nil {
		return stored, err
	}
	return learned, nil
}

func (s *Service) SearchValidatedOutcomes(ctx context.Context, text string, k int) ([]model.ValidatedOutcomeEvidence, error) {
	if !s.cfg.OutcomeRetrievalEnabled || s.outcomeRetriever == nil {
		return nil, nil
	}
	if k <= 0 || k > s.cfg.OutcomeRetrievalSearchK {
		k = s.cfg.OutcomeRetrievalSearchK
	}
	rows, err := s.outcomeRetriever.SearchOutcomes(ctx, text, k, s.cfg.OutcomeRetrievalMinSimilarity)
	if err != nil {
		return nil, err
	}
	out := make([]model.ValidatedOutcomeEvidence, 0, len(rows))
	for _, e := range rows {
		out = append(out, model.ValidatedOutcomeEvidence{MemoryID: e.MemoryID, OutcomeID: e.OutcomeID, Decision: e.Decision, Text: compactLearningText(e.Text, 5000), Similarity: e.Similarity, Source: e.Source, TicketID: e.TicketID, KnowledgeID: e.KnowledgeID})
	}
	return out, nil
}

func (s *Service) TicketOutcomes() []learning.TicketOutcome {
	if s.outcomes == nil {
		return nil
	}
	return s.outcomes.List()
}

func appendUnique(in []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return in
	}
	for _, x := range in {
		if strings.EqualFold(strings.TrimSpace(x), v) {
			return in
		}
	}
	return append(in, v)
}
func compactLearningText(v string, max int) string {
	v = strings.Join(strings.Fields(v), " ")
	r := []rune(v)
	if len(r) <= max {
		return v
	}
	return string(r[:max]) + "…"
}
func semanticCategoryHints(c model.Category) []string {
	name := strings.ToLower(c.Name + " " + c.CompleteName)
	var h []string
	add := func(vals ...string) {
		for _, v := range vals {
			h = appendUnique(h, v)
		}
	}
	if strings.Contains(name, "active directory") || strings.Contains(name, "entra") || strings.Contains(name, "identity") || strings.Contains(name, "benutzerkonto") || strings.Contains(name, "account") {
		add("Benutzerkonto", "Anmeldung / Login", "Konto gesperrt", "Passwort", "Domänenkonto", "Gruppen und Berechtigungen", "Authentifizierung")
	}
	if strings.Contains(name, "druck") || strings.Contains(name, "printer") {
		add("Drucker", "Drucken nicht möglich", "Druckwarteschlange", "Netzwerkdrucker", "Toner", "Papierstau")
	}
	if strings.Contains(name, "vpn") {
		add("VPN-Verbindung", "Remote Access", "Gateway", "GlobalProtect", "Tunnel", "Verbindungsaufbau")
	}
	if strings.Contains(name, "mail") || strings.Contains(name, "outlook") || strings.Contains(name, "exchange") {
		add("E-Mail", "Outlook", "Postfach", "E-Mail Versand und Empfang", "Exchange")
	}
	if strings.Contains(name, "netz") || strings.Contains(name, "network") || strings.Contains(name, "wlan") || strings.Contains(name, "wifi") {
		add("Netzwerk", "LAN", "WLAN", "Keine Verbindung", "DNS", "IP-Adresse")
	}
	if strings.Contains(name, "hardware") || strings.Contains(name, "client") || strings.Contains(name, "arbeitsplatz") {
		add("Arbeitsplatzgerät", "Notebook", "PC", "Dockingstation", "Peripherie")
	}
	return h
}

func categoryName(categories []model.Category, id int64) string {
	if id == 0 {
		return "Nicht gesetzt"
	}
	for _, c := range categories {
		if c.ID == id {
			return categoryDisplayName(c)
		}
	}
	return ""
}

func (s *Service) statusAllowed(id int64) bool {
	for _, allowed := range s.cfg.GLPIAllowedStatusIDs {
		if id == allowed {
			return true
		}
	}
	return false
}

func sourceVersion(t model.Ticket) string {
	// Do not rely on date_mod alone: two changes can happen within the same
	// timestamp resolution and some API projections may omit it. Requesters and
	// linked items are decision-relevant because they feed the context collector.
	payload := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%s\x00%v\x00%v\x00%v\x00%v", t.ID, t.DateCreation, t.DateMod, t.Name, t.Content, t.StatusID, t.CategoryID, t.Priority, t.Impact, t.Urgency, t.EntityID, t.LocationID, t.TimeToResolve, t.RequesterIDs, t.AssignedGroups, t.AssignedUsers, t.Items)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

func sameDecisionSource(original, fresh model.Ticket, expectedCategory int64) bool {
	if fresh.Name != original.Name || fresh.Content != original.Content || fresh.StatusID != original.StatusID || fresh.CategoryID != expectedCategory || fresh.Impact != original.Impact || fresh.Urgency != original.Urgency || fresh.EntityID != original.EntityID || fresh.LocationID != original.LocationID || fresh.TimeToResolve != original.TimeToResolve {
		return false
	}
	if fmt.Sprint(fresh.RequesterIDs) != fmt.Sprint(original.RequesterIDs) || fmt.Sprint(fresh.AssignedGroups) != fmt.Sprint(original.AssignedGroups) || fmt.Sprint(fresh.AssignedUsers) != fmt.Sprint(original.AssignedUsers) || fmt.Sprint(fresh.Items) != fmt.Sprint(original.Items) {
		return false
	}
	return true
}
func newRunID() string { b := make([]byte, 8); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func categoryKnowledgeMappingChecks(hits []model.KnowledgeHit, categories []model.Category, selectedID int64) []model.RuleCheck {
	if selectedID <= 0 || len(hits) == 0 {
		return nil
	}
	byID := make(map[int64]model.Category, len(categories))
	for _, c := range categories {
		byID[c.ID] = c
	}
	selected, ok := byID[selectedID]
	if !ok {
		return nil
	}
	selectedName := selected.CompleteName
	if strings.TrimSpace(selectedName) == "" {
		selectedName = selected.Name
	}
	selectedLeaf := normalizeCategoryLeaf(selectedName)
	if selectedLeaf == "" {
		return nil
	}
	var mismatches []string
	for _, hit := range hits {
		mapped := false
		for _, id := range hit.Doc.Categories {
			if id == selectedID {
				mapped = true
				break
			}
		}
		if !mapped || len(hit.Doc.ExternalCategories) == 0 {
			continue
		}
		matches := false
		for _, label := range hit.Doc.ExternalCategories {
			if normalizeCategoryLeaf(label) == selectedLeaf {
				matches = true
				break
			}
		}
		if !matches {
			mismatches = append(mismatches, fmt.Sprintf("%s: %s → #%d %s", hit.Doc.ID, strings.Join(hit.Doc.ExternalCategories, " | "), selectedID, selectedName))
		}
	}
	if len(mismatches) == 0 {
		return nil
	}
	return []model.RuleCheck{{
		Code:     "category_external_mapping_review",
		Group:    "category",
		Label:    "Externe Knowledge-Kategorie passt namentlich zum GLPI-Ziel",
		Status:   "warn",
		Blocking: false,
		Actual:   strings.Join(mismatches, "; "),
		Expected: "Mapping fachlich geprüft",
		Detail:   "Nicht blockierend: Externe Taxonomien dürfen bewusst zusammengeführt werden. Die Abweichung sollte aber geprüft werden, weil sie Hints und KI-Begründung beeinflusst.",
	}}
}

func normalizeCategoryLeaf(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndex(v, ">"); i >= 0 {
		v = v[i+1:]
	}
	if i := strings.LastIndex(v, "/"); i >= 0 {
		v = v[i+1:]
	}
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", " und ", " ", "-", " ", "_", " ").Replace(v)
	return strings.Join(strings.Fields(v), "")
}

func stripHTML(s string) string {
	r := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n")
	s = r.Replace(s)
	var b strings.Builder
	inside := false
	for _, ch := range s {
		if ch == '<' {
			inside = true
			continue
		}
		if ch == '>' {
			inside = false
			continue
		}
		if !inside {
			b.WriteRune(ch)
		}
	}
	return strings.TrimSpace(b.String())
}
func shortlistCategories(t model.Ticket, cats []model.Category, limit int) []model.Category {
	if limit <= 0 || len(cats) <= limit {
		return cats
	}
	q := strings.Fields(strings.ToLower(t.Name + " " + stripHTML(t.Content)))
	type scored struct {
		c model.Category
		s int
	}
	ss := make([]scored, 0, len(cats))
	for _, c := range cats {
		name := strings.ToLower(c.Name + " " + c.CompleteName + " " + strings.Join(c.Hints, " ") + " " + strings.Join(c.Examples, " "))
		score := 0
		for _, w := range q {
			if len(w) >= 3 && strings.Contains(name, w) {
				score++
			}
		}
		if c.ID == t.CategoryID {
			score += 100
		}
		ss = append(ss, scored{c, score})
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].s > ss[j].s })
	out := make([]model.Category, 0, limit)
	for i := 0; i < limit && i < len(ss); i++ {
		out = append(out, ss[i].c)
	}
	return out
}

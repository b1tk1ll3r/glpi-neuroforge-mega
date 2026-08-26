package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/ollama"
	"github.com/example/glpi-ai-agent/internal/queue"
)

func (s *Service) escalationLoop(ctx context.Context) {
	s.scanEscalations(ctx)
	ticker := time.NewTicker(s.cfg.EscalationScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanEscalations(ctx)
		}
	}
}

func (s *Service) scanEscalations(ctx context.Context) {
	lister, ok := s.glpi.(escalationLister)
	if !ok {
		slog.Error("escalation scanner unavailable", "reason", "GLPI connector does not implement ListEscalationCandidates")
		return
	}
	filter := strings.TrimSpace(s.cfg.GLPIEscalationFilter)
	if filter == "" {
		filter = s.cfg.GLPITicketFilter
	}
	tickets, err := lister.ListEscalationCandidates(ctx, s.cfg.GLPIEscalationLimit, filter)
	if err != nil {
		s.metrics.Errors.Add(1)
		slog.Error("GLPI escalation scan failed", "error", err)
		return
	}
	now := time.Now()
	for _, ticket := range tickets {
		created, ok := parseGLPITime(ticket.DateCreation)
		if !ok || now.Sub(created) < s.cfg.EscalationMinAge {
			continue
		}
		if s.q.EnqueueWork(queue.WorkItem{TicketID: ticket.ID, Trigger: "scheduled_escalation", Priority: queue.PriorityScheduled}) {
			s.metrics.QueueDepth.Store(int64(s.q.Len()))
		}
	}
}

func (s *Service) processEscalation(ctx context.Context, item queue.WorkItem) error {
	id := item.TicketID
	muAny, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer func() {
		mu.Unlock()
		s.locks.Delete(id)
	}()

	start := time.Now()
	run := model.RunRecord{RunID: newRunID(), TicketID: id, Trigger: "scheduled_escalation", StartedAt: start, DryRun: s.cfg.DryRun, Outcome: "error"}
	finish := func(err error) {
		run.FinishedAt = time.Now()
		if err != nil {
			run.Error = err.Error()
			s.metrics.Errors.Add(1)
		}
		if e := s.state.Append(run); e != nil {
			slog.Error("persist escalation run failed", "error", e)
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
	run.CategoryBefore = t.CategoryID
	run.PriorityBefore = t.Priority
	if parent, ok := s.state.LatestTicketRun(id, "scheduled_escalation"); ok {
		run.CausedByRunID = parent.RunID
	}
	followups, err := s.glpi.GetFollowups(ctx, id)
	if err != nil {
		run.Reason = "followup_check_failed"
		finish(err)
		return err
	}
	contextData := model.ContextSnapshot{}
	if s.context != nil && s.cfg.ContextEnabled {
		contextData = s.context.Collect(ctx, t)
	}
	evidence := buildEscalationEvidence(s.cfg, t, followups, contextData, time.Now())
	constraints := s.escalationConstraints(contextData)
	started := time.Now()
	analysis := newAnalysis(run, "escalation", escalationPromptVersion, map[string]any{
		"ticket": t, "followups": followups, "context": contextData,
		"evidence": evidence, "constraints": constraints,
	}, started)
	ai, ok := s.ai.(escalationAI)
	if !ok {
		err = fmt.Errorf("AI client does not implement escalation analysis")
		finishAnalysis(&analysis, s.cfg.OllamaModel, nil, nil, "", 0, nil, model.ActionAudit{Type: "escalation_plan", Result: "skipped: escalation_ai_unavailable"}, err)
		run.Analyses = append(run.Analyses, analysis)
		run.Reason = "escalation_ai_unavailable"
		finish(err)
		return err
	}
	analysisBaseCtx, escalationTrace := ollama.WithTrace(ctx, s.cfg.OllamaRoutingMode)
	analysisCtx := analysisBaseCtx
	cancel := func() {}
	if s.cfg.EscalationAnalysisTimeout > 0 {
		analysisCtx, cancel = context.WithTimeout(analysisBaseCtx, s.cfg.EscalationAnalysisTimeout)
	}
	decision, err := ai.AnalyseEscalation(analysisCtx, t, followups, contextData, evidence, constraints)
	cancel()
	if err != nil {
		finishAnalysis(&analysis, s.cfg.OllamaModel, nil, nil, "", 0, nil, model.ActionAudit{Type: "escalation_plan", Result: "skipped: escalation_ai_failed"}, err)
		attachAnalysisTrace(&analysis, escalationTrace)
		run.Analyses = append(run.Analyses, analysis)
		run.Reason = "escalation_ai_failed"
		finish(err)
		return err
	}
	result := evaluateEscalation(s.cfg, s.state, t, followups, contextData, decision, time.Now())
	action := model.ActionAudit{Type: "escalation_plan", Proposed: result.Accepted, DryRun: s.cfg.DryRun || !s.cfg.AutoEscalation, Before: escalationTicketState(t), Result: result.Decision}
	if result.Accepted && s.cfg.AutoEscalation && !s.cfg.DryRun {
		fresh, loadErr := s.glpi.GetTicket(ctx, id)
		if loadErr != nil {
			err = loadErr
		} else if sourceVersion(fresh) != run.SourceVersion {
			result.Accepted = false
			result.Decision = "escalation_ticket_changed_before_write"
			action.Proposed = false
			action.Result = result.Decision
		} else {
			freshFollowups, followupErr := s.glpi.GetFollowups(ctx, id)
			if followupErr != nil {
				err = followupErr
				result.Accepted = false
				result.Decision = "escalation_prewrite_followup_check_failed"
				action.Proposed = false
				action.Result = result.Decision
			} else {
				freshContext := contextData
				if s.context != nil && s.cfg.ContextEnabled {
					freshContext = s.context.Collect(ctx, fresh)
				}
				freshResult := evaluateEscalation(s.cfg, s.state, fresh, freshFollowups, freshContext, decision, time.Now())
				freshResult.Checks = append(freshResult.Checks, model.RuleCheck{Code: "escalation_prewrite_revalidated", Group: "execution", Label: "Ticket, Followups und Kontext wurden vor dem Schreiben erneut geprüft", Status: passFail(freshResult.Accepted), Actual: freshResult.Decision, Expected: "escalation_accepted", Blocking: !freshResult.Accepted})
				result = freshResult
				contextData = freshContext
				if !result.Accepted {
					action.Proposed = false
					action.Result = result.Decision
				} else {
					action, err = s.executeEscalationPlan(ctx, fresh, decision, result, contextData)
				}
			}
		}
	} else if result.Accepted {
		action, err = s.executeEscalationPlan(ctx, t, decision, result, contextData)
	}
	finishAnalysis(&analysis, s.cfg.OllamaModel, result, decision.ReasonCodes, decision.Reason, decision.Confidence, result.Checks, action, err)
	attachAnalysisTrace(&analysis, escalationTrace)
	run.Analyses = append(run.Analyses, analysis)
	run.Reason = decision.Reason
	run.AIReason = decision.Reason
	run.PolicyReason = result.Decision
	run.Outcome = "processed"
	s.metrics.EscalationRuns.Add(1)
	finish(err)
	return err
}

func (s *Service) escalationConstraints(contextData model.ContextSnapshot) model.EscalationConstraints {
	actions := make([]string, 0, len(s.cfg.EscalationAllowedActions))
	targets := make([]string, 0, 8)
	for _, raw := range s.cfg.EscalationAllowedActions {
		action := strings.ToLower(strings.TrimSpace(raw))
		switch action {
		case "none", "raise_priority":
			actions = append(actions, action)
		case "assign_second_level":
			if s.cfg.EscalationSecondLevelGroupID > 0 {
				actions = append(actions, action)
				targets = append(targets, fmt.Sprintf("second_level_group:%d", s.cfg.EscalationSecondLevelGroupID))
			}
		case "assign_security_team":
			if s.cfg.EscalationSecurityGroupID > 0 {
				actions = append(actions, action)
				targets = append(targets, fmt.Sprintf("security_group:%d", s.cfg.EscalationSecurityGroupID))
			}
		case "notify_service_owner":
			if s.cfg.EscalationServiceOwnerGroupID > 0 || s.cfg.EscalationServiceOwnerUserID > 0 || s.cfg.EscalationWebhookURL != "" {
				actions = append(actions, action)
				targets = append(targets, "service_owner:"+actorTarget(s.cfg.EscalationServiceOwnerGroupID, s.cfg.EscalationServiceOwnerUserID, s.cfg.EscalationWebhookURL != ""))
			}
		case "link_major_incident":
			if incident, ok := selectMajorIncident(contextData, s.cfg.EscalationMajorIncidentMinScore); ok && s.cfg.GLPIEscalationITILLinkPath != "" && s.cfg.GLPIEscalationITILLinkBody != "" {
				actions = append(actions, action)
				targets = append(targets, fmt.Sprintf("major_incident:%d", incident.ID))
			}
		case "request_manager_review":
			if s.cfg.EscalationManagerReviewGroupID > 0 || s.cfg.EscalationManagerReviewUserID > 0 || s.cfg.EscalationWebhookURL != "" {
				actions = append(actions, action)
				targets = append(targets, "manager_review:"+actorTarget(s.cfg.EscalationManagerReviewGroupID, s.cfg.EscalationManagerReviewUserID, s.cfg.EscalationWebhookURL != ""))
			}
		}
	}
	if len(actions) == 0 {
		actions = []string{"none"}
	}
	ownerLevel := s.cfg.EscalationServiceOwnerMinLevel
	if ownerLevel <= 0 {
		ownerLevel = 2
	}
	managerLevel := s.cfg.EscalationManagerReviewMinLevel
	if managerLevel <= 0 {
		managerLevel = 3
	}
	return model.EscalationConstraints{
		AllowedActions: actions, AllowedReasonCodes: append([]string(nil), s.cfg.EscalationAllowedReasonCodes...),
		MaxLevel: s.cfg.EscalationMaxLevel, MinimumAge: s.cfg.EscalationMinAge.String(),
		ServiceOwnerMinLevel: ownerLevel, ManagerReviewMinLevel: managerLevel,
		MajorIncidentMinRelevance: s.cfg.EscalationMajorIncidentMinScore, ConfiguredTargets: targets,
	}
}

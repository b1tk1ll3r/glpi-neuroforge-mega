package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

type assignedGroupWriter interface {
	SetAssignedGroups(ctx context.Context, id int64, groupIDs []int64, field string) error
}

type assignedUserWriter interface {
	SetAssignedUsers(ctx context.Context, id int64, userIDs []int64, field string) error
}

type privateFollowupWriter interface {
	AddPrivateFollowup(ctx context.Context, id int64, content string, richHTML bool) error
}

type itilLinkWriter interface {
	LinkITILObject(ctx context.Context, ticketID, targetTicketID int64, pathTemplate, bodyTemplate string) error
}

func (s *Service) executeEscalationPlan(ctx context.Context, ticket model.Ticket, decision model.EscalationDecision, result model.EscalationResult, contextData model.ContextSnapshot) (model.ActionAudit, error) {
	dryRun := s.cfg.DryRun || !s.cfg.AutoEscalation
	audit := model.ActionAudit{
		Type:     "escalation_plan",
		Proposed: result.Accepted,
		DryRun:   dryRun,
		Before:   escalationTicketState(ticket),
		Result:   result.Decision,
	}
	if !result.Accepted {
		return audit, nil
	}

	current := ticket
	var errs []error
	proposed, executed := 0, 0
	for _, actionResult := range result.Actions {
		if !actionResult.Accepted {
			continue
		}
		proposed++
		step := model.ActionStepAudit{
			Step:     actionResult.Action,
			Target:   actionResult.Target,
			Proposed: true,
			DryRun:   dryRun,
			Before:   escalationTicketState(current),
			Result:   actionResult.Decision,
		}
		if dryRun {
			s.applyEscalationStateProjection(&current, actionResult.Action, contextData)
			step.After = escalationTicketState(current)
			step.Result = actionResult.IdempotencyKey + "; simulated"
			audit.Steps = append(audit.Steps, step)
			continue
		}

		warnings, actionErr := s.executeEscalationAction(ctx, &current, decision, actionResult, contextData)
		if len(warnings) > 0 {
			step.Error = errors.Join(warnings...).Error()
		}
		if actionErr != nil {
			if step.Error != "" {
				step.Error += "; " + actionErr.Error()
			} else {
				step.Error = actionErr.Error()
			}
			step.Result = actionResult.IdempotencyKey + "; failed"
			errs = append(errs, fmt.Errorf("%s: %w", actionResult.Action, actionErr))
		} else {
			step.Executed = true
			step.Result = actionResult.IdempotencyKey + "; executed"
			if len(warnings) > 0 {
				step.Result += "; warning"
			}
			executed++
			s.metrics.Escalations.Add(1)
		}
		step.After = escalationTicketState(current)
		audit.Steps = append(audit.Steps, step)
	}

	audit.After = escalationTicketState(current)
	audit.Executed = proposed > 0 && executed == proposed
	audit.Result = fmt.Sprintf("%s; proposed=%d; executed=%d", result.Decision, proposed, executed)
	if len(errs) > 0 {
		audit.Error = errors.Join(errs...).Error()
		return audit, errors.Join(errs...)
	}
	return audit, nil
}

func (s *Service) executeEscalationAction(ctx context.Context, ticket *model.Ticket, decision model.EscalationDecision, actionResult model.EscalationActionResult, contextData model.ContextSnapshot) ([]error, error) {
	var warnings []error
	addNote := func() {
		if err := s.addEscalationNote(ctx, *ticket, decision, actionResult.Action, contextData); err != nil {
			warnings = append(warnings, fmt.Errorf("private escalation note: %w", err))
		}
	}

	switch actionResult.Action {
	case "raise_priority":
		writer, ok := s.glpi.(priorityWriter)
		if !ok {
			return warnings, errors.New("GLPI connector does not implement priority writes")
		}
		target := ticket.Priority + 1
		if target > 6 {
			target = 6
		}
		if ticket.Priority < 1 || ticket.Priority >= 6 {
			return warnings, fmt.Errorf("priority %d cannot be raised", ticket.Priority)
		}
		if err := writer.SetPriority(ctx, ticket.ID, target); err != nil {
			return warnings, err
		}
		ticket.Priority = target
		addNote()
		return warnings, nil

	case "assign_second_level":
		if err := s.assignEscalationActors(ctx, ticket, s.cfg.EscalationSecondLevelGroupID, 0); err != nil {
			return warnings, err
		}
		addNote()
		return warnings, nil

	case "assign_security_team":
		if err := s.assignEscalationActors(ctx, ticket, s.cfg.EscalationSecurityGroupID, 0); err != nil {
			return warnings, err
		}
		addNote()
		return warnings, nil

	case "notify_service_owner":
		if err := s.assignEscalationActors(ctx, ticket, s.cfg.EscalationServiceOwnerGroupID, s.cfg.EscalationServiceOwnerUserID); err != nil {
			return warnings, err
		}
		// Send the externally visible notification before the optional note. If the
		// webhook fails, the stable idempotency key allows a retry without creating
		// a duplicate private followup on every attempt.
		if err := s.sendEscalationWebhook(ctx, *ticket, decision, actionResult); err != nil {
			return warnings, err
		}
		addNote()
		return warnings, nil

	case "link_major_incident":
		incident, ok := selectMajorIncident(contextData, s.cfg.EscalationMajorIncidentMinScore)
		if !ok {
			return warnings, errors.New("no eligible major incident target")
		}
		writer, ok := s.glpi.(itilLinkWriter)
		if !ok {
			return warnings, errors.New("GLPI connector does not implement ITIL links")
		}
		if err := writer.LinkITILObject(ctx, ticket.ID, incident.ID, s.cfg.GLPIEscalationITILLinkPath, s.cfg.GLPIEscalationITILLinkBody); err != nil {
			return warnings, err
		}
		addNote()
		return warnings, nil

	case "request_manager_review":
		if err := s.assignEscalationActors(ctx, ticket, s.cfg.EscalationManagerReviewGroupID, s.cfg.EscalationManagerReviewUserID); err != nil {
			return warnings, err
		}
		if err := s.sendEscalationWebhook(ctx, *ticket, decision, actionResult); err != nil {
			return warnings, err
		}
		addNote()
		return warnings, nil
	default:
		return warnings, fmt.Errorf("unsupported escalation action %q", actionResult.Action)
	}
}

func (s *Service) assignEscalationActors(ctx context.Context, ticket *model.Ticket, groupID, userID int64) error {
	if groupID > 0 && !containsInt64(ticket.AssignedGroups, groupID) {
		writer, ok := s.glpi.(assignedGroupWriter)
		if !ok {
			return errors.New("GLPI connector does not implement group assignments")
		}
		groups := appendUniqueInt64(ticket.AssignedGroups, groupID)
		if err := writer.SetAssignedGroups(ctx, ticket.ID, groups, s.cfg.GLPIEscalationGroupPatchField); err != nil {
			return err
		}
		ticket.AssignedGroups = groups
	}
	if userID > 0 && !containsInt64(ticket.AssignedUsers, userID) {
		writer, ok := s.glpi.(assignedUserWriter)
		if !ok {
			return errors.New("GLPI connector does not implement user assignments")
		}
		users := appendUniqueInt64(ticket.AssignedUsers, userID)
		if err := writer.SetAssignedUsers(ctx, ticket.ID, users, s.cfg.GLPIEscalationUserPatchField); err != nil {
			return err
		}
		ticket.AssignedUsers = users
	}
	return nil
}

func appendUniqueInt64(values []int64, value int64) []int64 {
	out := append([]int64(nil), values...)
	if value <= 0 || containsInt64(out, value) {
		return out
	}
	return append(out, value)
}

func (s *Service) addEscalationNote(ctx context.Context, ticket model.Ticket, decision model.EscalationDecision, action string, contextData model.ContextSnapshot) error {
	if !s.cfg.EscalationAddPrivateFollowup {
		return nil
	}
	writer, ok := s.glpi.(privateFollowupWriter)
	if !ok {
		return errors.New("GLPI connector does not implement private followups")
	}
	template := s.escalationNoteTemplate(action)
	if strings.TrimSpace(template) == "" {
		return nil
	}
	text := renderEscalationTemplate(template, ticket, decision, action, contextData)
	return writer.AddPrivateFollowup(ctx, ticket.ID, text, false)
}

func (s *Service) escalationNoteTemplate(action string) string {
	switch action {
	case "assign_second_level":
		return s.cfg.EscalationSecondLevelNote
	case "assign_security_team":
		return s.cfg.EscalationSecurityNote
	case "notify_service_owner":
		return s.cfg.EscalationServiceOwnerNote
	case "link_major_incident":
		return s.cfg.EscalationMajorIncidentNote
	case "request_manager_review":
		return s.cfg.EscalationManagerReviewNote
	case "raise_priority":
		return "Automatische Eskalation Stufe {{level}}: Ticketpriorität wurde um eine Stufe erhöht. Gründe: {{reason_codes}}. KI-Begründung: {{reason}}"
	default:
		return ""
	}
}

func renderEscalationTemplate(template string, ticket model.Ticket, decision model.EscalationDecision, action string, contextData model.ContextSnapshot) string {
	incident, _ := selectMajorIncident(contextData, 0)
	replacer := strings.NewReplacer(
		"{{ticket_id}}", strconv.FormatInt(ticket.ID, 10),
		"{{ticket_name}}", ticket.Name,
		"{{level}}", strconv.Itoa(decision.Level),
		"{{action}}", action,
		"{{reason}}", decision.Reason,
		"{{reason_codes}}", strings.Join(decision.ReasonCodes, ", "),
		"{{major_incident_id}}", strconv.FormatInt(incident.ID, 10),
		"{{major_incident_name}}", incident.Name,
		"{{major_incident_score}}", fmt.Sprintf("%.1f %%", incident.Relevance*100),
	)
	return strings.TrimSpace(replacer.Replace(template))
}

func (s *Service) sendEscalationWebhook(ctx context.Context, ticket model.Ticket, decision model.EscalationDecision, actionResult model.EscalationActionResult) error {
	url := strings.TrimSpace(s.cfg.EscalationWebhookURL)
	if url == "" {
		return nil
	}
	payload := map[string]any{
		"event":           "glpi_ai_escalation",
		"ticket_id":       ticket.ID,
		"ticket_name":     ticket.Name,
		"entity_id":       ticket.EntityID,
		"priority":        ticket.Priority,
		"level":           decision.Level,
		"action":          actionResult.Action,
		"target":          actionResult.Target,
		"reason_codes":    decision.ReasonCodes,
		"reason":          decision.Reason,
		"confidence":      decision.Confidence,
		"idempotency_key": actionResult.IdempotencyKey,
		"created_at":      time.Now().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", actionResult.IdempotencyKey)
	if token := strings.TrimSpace(s.cfg.EscalationWebhookBearerToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	timeout := s.cfg.EscalationWebhookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Escalation targets are administrator-configured. Refusing redirects keeps
			// credentials and payloads pinned to that exact endpoint.
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("escalation webhook HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func (s *Service) applyEscalationStateProjection(ticket *model.Ticket, action string, contextData model.ContextSnapshot) {
	switch action {
	case "raise_priority":
		if ticket.Priority >= 1 && ticket.Priority < 6 {
			ticket.Priority++
		}
	case "assign_second_level":
		ticket.AssignedGroups = appendUniqueInt64(ticket.AssignedGroups, s.cfg.EscalationSecondLevelGroupID)
	case "assign_security_team":
		ticket.AssignedGroups = appendUniqueInt64(ticket.AssignedGroups, s.cfg.EscalationSecurityGroupID)
	case "notify_service_owner":
		ticket.AssignedGroups = appendUniqueInt64(ticket.AssignedGroups, s.cfg.EscalationServiceOwnerGroupID)
		ticket.AssignedUsers = appendUniqueInt64(ticket.AssignedUsers, s.cfg.EscalationServiceOwnerUserID)
	case "request_manager_review":
		ticket.AssignedGroups = appendUniqueInt64(ticket.AssignedGroups, s.cfg.EscalationManagerReviewGroupID)
		ticket.AssignedUsers = appendUniqueInt64(ticket.AssignedUsers, s.cfg.EscalationManagerReviewUserID)
	}
}

func escalationTicketState(ticket model.Ticket) string {
	return fmt.Sprintf("priority=%d; groups=%v; users=%v", ticket.Priority, ticket.AssignedGroups, ticket.AssignedUsers)
}

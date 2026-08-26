package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/ollama"
	"github.com/example/glpi-ai-agent/internal/state"
)

const (
	categoryPromptVersion   = "category-v2"
	priorityPromptVersion   = "priority-v4"
	statusPromptVersion     = "status-v1"
	replyPromptVersion      = "reply-v2"
	escalationPromptVersion = "escalation-v2"
)

type priorityAI interface {
	AnalysePriority(ctx context.Context, t model.Ticket, category model.Category, contextData model.ContextSnapshot) (model.PriorityDecision, error)
}

type escalationAI interface {
	AnalyseEscalation(ctx context.Context, t model.Ticket, followups []model.Followup, contextData model.ContextSnapshot, evidence model.EscalationEvidence, constraints model.EscalationConstraints) (model.EscalationDecision, error)
}

type priorityWriter interface {
	SetPriority(ctx context.Context, id, priority int64) error
}

type escalationLister interface {
	ListEscalationCandidates(ctx context.Context, limit int, filter string) ([]model.Ticket, error)
}

func newAnalysis(run model.RunRecord, analysisType, promptVersion string, snapshot any, started time.Time) model.AnalysisRun {
	raw := mustJSON(snapshot)
	h := sha256.Sum256(raw)
	return model.AnalysisRun{
		AnalysisID:    newRunID(),
		ParentRunID:   run.RunID,
		TicketID:      run.TicketID,
		AnalysisType:  analysisType,
		Trigger:       run.Trigger,
		SourceVersion: run.SourceVersion,
		PromptVersion: promptVersion,
		InputHash:     hex.EncodeToString(h[:]),
		InputSnapshot: raw,
		StartedAt:     started,
		Outcome:       "processed",
	}
}

func finishAnalysis(a *model.AnalysisRun, modelName string, decision any, reasonCodes []string, explanation string, confidence float64, checks []model.RuleCheck, action model.ActionAudit, err error) {
	a.Model = strings.TrimSpace(modelName)
	a.FinishedAt = time.Now()
	a.DurationMS = a.FinishedAt.Sub(a.StartedAt).Milliseconds()
	if a.DurationMS < 0 {
		a.DurationMS = 0
	}
	if decision != nil {
		a.Decision = mustJSON(decision)
	}
	a.ReasonCodes = model.NormalizeReasonCodes(reasonCodes)
	a.Explanation = strings.TrimSpace(explanation)
	a.Confidence = confidence
	a.Checks = append([]model.RuleCheck(nil), checks...)
	a.Action = action
	if err != nil {
		a.Outcome = "error"
		a.Error = err.Error()
	} else if strings.HasPrefix(strings.ToLower(action.Result), "skipped") {
		a.Outcome = "skipped"
	}
}

func attachAnalysisTrace(a *model.AnalysisRun, trace *ollama.Trace) {
	if a == nil || trace == nil {
		return
	}
	snapshot := trace.Snapshot()
	if len(snapshot.Attempts) == 0 {
		return
	}
	a.Provider = snapshot
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`{"snapshot_error":%q}`, err.Error()))
	}
	return b
}

func evaluatePriority(cfg config.Config, t model.Ticket, d model.PriorityDecision) model.PriorityResult {
	reasonCodes := model.NormalizeReasonCodes(d.ReasonCodes)
	result := model.PriorityResult{
		PriorityBefore:      t.Priority,
		PriorityAfter:       t.Priority,
		RecommendedPriority: d.RecommendedPriority,
		RecommendedImpact:   d.RecommendedImpact,
		RecommendedUrgency:  d.RecommendedUrgency,
		AffectedScope:       strings.TrimSpace(d.AffectedScope),
		TimeCriticality:     strings.TrimSpace(d.TimeCriticality),
		Confidence:          d.Confidence,
		ReasonCodes:         append([]string(nil), reasonCodes...),
	}
	add := func(code, label, status, actual, expected, detail string, blocking bool) {
		result.Checks = append(result.Checks, model.RuleCheck{Code: code, Group: "priority", Label: label, Status: status, Actual: actual, Expected: expected, Detail: detail, Blocking: blocking})
	}
	add("priority_enabled", "KI-Prioritätsanalyse aktiviert", passFail(cfg.PriorityEnabled), boolText(cfg.PriorityEnabled), "true", "Separater KI-Lauf; Schreibzugriff benötigt zusätzlich AUTO_PRIORITY.", !cfg.PriorityEnabled)
	if !cfg.PriorityEnabled {
		result.Decision = "priority_disabled"
		return result
	}

	validRecommendation := d.RecommendedPriority >= 1 && d.RecommendedPriority <= 6
	add("priority_recommendation_valid", "KI hat eine gültige GLPI-Priorität empfohlen", passFail(validRecommendation), fmt.Sprintf("#%d", d.RecommendedPriority), "1 bis 6", "", !validRecommendation)
	currentKnown := t.Priority >= 1 && t.Priority <= 6
	add("priority_current_known", "Aktuelle GLPI-Priorität ist verfügbar", passFail(currentKnown), fmt.Sprintf("#%d", t.Priority), "1 bis 6", "Ohne aktuellen Ausgangswert wird keine automatische Änderung vorgenommen.", !currentKnown)

	increaseRequested := validRecommendation && currentKnown && d.RecommendedPriority > t.Priority
	decreaseRequested := validRecommendation && currentKnown && d.RecommendedPriority < t.Priority
	hasInsufficientInformation := model.HasReasonCode(reasonCodes, "insufficient_information")

	confidenceOK := d.Confidence >= cfg.PriorityConfidence
	if increaseRequested {
		add("priority_confidence", "KI-Confidence erreicht Schwellwert", passFail(confidenceOK), percentText(d.Confidence), ">= "+percentText(cfg.PriorityConfidence), "Die Confidence ist nur für eine tatsächliche Erhöhung ein Schreib-Gate.", !confidenceOK)
	} else {
		add("priority_confidence", "KI-Confidence erreicht Schwellwert", "na", percentText(d.Confidence), "nur bei empfohlener Erhöhung relevant", "Ohne Erhöhung wird die Confidence angezeigt, blockiert aber keine neutrale Keine-Änderung-Entscheidung.", false)
	}

	allowed := stringSet(cfg.PriorityAllowedReasonCodes)
	neutral := stringSet([]string{"single_user_affected", "workaround_available", "insufficient_information"})
	actionReasonCount := 0
	var disallowed []string
	for _, reason := range reasonCodes {
		if _, ok := allowed[reason]; ok {
			actionReasonCount++
			continue
		}
		if _, ok := neutral[reason]; ok {
			continue
		}
		disallowed = append(disallowed, reason)
	}
	reasonsOK := actionReasonCount > 0 && len(disallowed) == 0 && !hasInsufficientInformation
	if increaseRequested {
		detail := "Für eine Erhöhung ist mindestens ein freigegebener Aktionsgrund erforderlich; neutrale Grundcodes allein reichen nicht aus."
		if hasInsufficientInformation {
			detail = "insufficient_information ist ein bewusster Enthaltungsgrund und sperrt jede automatische Erhöhung."
		} else if len(disallowed) > 0 {
			detail = "Nicht freigegeben: " + strings.Join(disallowed, ", ")
		}
		add("priority_reasons_allowed", "Freigegebener Grund für eine Erhöhung vorhanden", passFail(reasonsOK), strings.Join(reasonCodes, ", "), strings.Join(cfg.PriorityAllowedReasonCodes, ", "), detail, !reasonsOK)
	} else {
		add("priority_reasons_allowed", "Freigegebener Grund für eine Erhöhung vorhanden", "na", strings.Join(reasonCodes, ", "), "nur bei empfohlener Erhöhung relevant", "Die Allowlist steuert ausschließlich Prioritätserhöhungen. Eine unveränderte Empfehlung benötigt keinen freigegebenen Eskalationsgrund.", false)
	}

	noDecrease := !decreaseRequested
	add("priority_no_decrease", "KI empfiehlt keine Herabstufung", passFail(noDecrease), fmt.Sprintf("#%d → #%d", t.Priority, d.RecommendedPriority), "empfohlen >= aktuell", "Automatische Herabstufungen sind grundsätzlich gesperrt.", !noDecrease)

	switch {
	case !validRecommendation:
		result.Decision = "priority_invalid_recommendation"
	case !currentKnown:
		result.Decision = "priority_current_unknown"
	case decreaseRequested:
		result.Decision = "priority_decrease_blocked"
	case d.RecommendedPriority == t.Priority:
		result.Accepted = true
		if hasInsufficientInformation {
			result.Decision = "priority_no_change_insufficient_information"
		} else {
			result.Decision = "priority_already_matches"
		}
	case hasInsufficientInformation:
		result.Decision = "priority_insufficient_information"
	case !confidenceOK:
		result.Decision = "priority_confidence_below_threshold"
	case !reasonsOK:
		result.Decision = "priority_reason_not_allowed"
	default:
		result.Accepted = true
		result.ChangePriority = true
		target := d.RecommendedPriority
		if max := t.Priority + cfg.PriorityMaxIncrease; cfg.PriorityMaxIncrease > 0 && target > max {
			target = max
		}
		if target > 6 {
			target = 6
		}
		result.PriorityAfter = target
		result.Decision = "priority_accepted"
	}
	if result.ChangePriority {
		add("priority_max_increase", "Erhöhung bleibt innerhalb der maximalen Schrittweite", "pass", fmt.Sprintf("#%d → #%d", t.Priority, result.PriorityAfter), fmt.Sprintf("maximal +%d", cfg.PriorityMaxIncrease), fmt.Sprintf("Die KI empfahl #%d; die Policy begrenzt den Zielwert deterministisch.", d.RecommendedPriority), false)
	} else {
		add("priority_max_increase", "Erhöhung bleibt innerhalb der maximalen Schrittweite", "na", "–", fmt.Sprintf("maximal +%d", cfg.PriorityMaxIncrease), "Keine Erhöhung freigegeben.", false)
	}
	return result
}

func evaluateEscalation(cfg config.Config, st *state.Store, t model.Ticket, followups []model.Followup, contextData model.ContextSnapshot, d model.EscalationDecision, now time.Time) model.EscalationResult {
	d.ReasonCodes = model.NormalizeReasonCodes(d.ReasonCodes)
	actions := normalizeEscalationActions(d)
	evidence := buildEscalationEvidence(cfg, t, followups, contextData, now)
	result := model.EscalationResult{Level: d.Level, ReasonCodes: append([]string(nil), d.ReasonCodes...)}
	add := func(code, label, status, actual, expected, detail string, blocking bool) {
		result.Checks = append(result.Checks, model.RuleCheck{Code: code, Group: "escalation", Label: label, Status: status, Actual: actual, Expected: expected, Detail: detail, Blocking: blocking})
	}

	created, createdOK := parseGLPITime(t.DateCreation)
	age := time.Duration(0)
	if createdOK {
		age = now.Sub(created)
	}
	ageOK := createdOK && age >= cfg.EscalationMinAge
	add("escalation_min_age", "Ticket hat das Mindestalter erreicht", passFail(ageOK), durationText(age, createdOK), ">= "+cfg.EscalationMinAge.String(), "Der Scheduler bestimmt nur Kandidaten; die KI entscheidet nicht über den Prüfzeitpunkt.", !ageOK)

	inactivityRequired := cfg.EscalationMinInactivity
	if inactivityRequired <= 0 {
		inactivityRequired = cfg.EscalationMinAge
	}
	requiresInactivity := model.HasReasonCode(d.ReasonCodes, "no_human_response")
	activityDatesOK := !evidence.HumanActivityIncomplete
	if requiresInactivity {
		add("escalation_activity_timestamps", "Zeitpunkte menschlicher Followups sind auswertbar", passFail(activityDatesOK), boolText(activityDatesOK), "true", "Nicht auswertbare menschliche Followups blockieren no_human_response fail-closed.", !activityDatesOK)
	} else {
		add("escalation_activity_timestamps", "Zeitpunkte menschlicher Followups sind auswertbar", "na", boolText(activityDatesOK), "nur für no_human_response erforderlich", "Andere belegte Eskalationsgründe wie SLA, Security oder Major Incident bleiben unabhängig auswertbar.", false)
	}
	inactiveOK := evidence.NoHumanResponse && activityDatesOK
	activityActual := "keine menschliche Aktivität seit Erstellung"
	if evidence.LastHumanActivity != "" {
		activityActual = evidence.LastHumanActivity + " · inaktiv seit " + evidence.InactiveFor
	}
	if requiresInactivity {
		add("escalation_inactivity", "Ticket ist lange genug ohne menschliche Aktivität", passFail(inactiveOK), activityActual, ">= "+inactivityRequired.String(), "Agent-Followups werden anhand GLPI_AGENT_USER_ID ausgenommen. Eine ältere menschliche Bearbeitung verhindert eine spätere Eskalation nicht dauerhaft.", !inactiveOK)
	} else {
		add("escalation_inactivity", "Ticket ist lange genug ohne menschliche Aktivität", "na", activityActual, "nur für Grund no_human_response erforderlich", "Der aktuelle Eskalationsgrund ist nicht von Inaktivität abhängig.", false)
	}

	add("escalation_model_recommends", "KI empfiehlt eine Eskalation", passFail(d.Escalate), boolText(d.Escalate), "true", "", !d.Escalate)
	levelOK := d.Level >= 1 && d.Level <= cfg.EscalationMaxLevel
	if d.Escalate {
		add("escalation_level", "Eskalationsstufe ist freigegeben", passFail(levelOK), fmt.Sprintf("Stufe %d", d.Level), fmt.Sprintf("1 bis %d", cfg.EscalationMaxLevel), "", !levelOK)
	} else {
		add("escalation_level", "Eskalationsstufe ist freigegeben", "na", fmt.Sprintf("Stufe %d", d.Level), "nur bei Eskalation relevant", "", false)
	}
	confidenceOK := d.Confidence >= cfg.EscalationConfidence
	if d.Escalate {
		add("escalation_confidence", "KI-Confidence erreicht Schwellwert", passFail(confidenceOK), percentText(d.Confidence), ">= "+percentText(cfg.EscalationConfidence), "", !confidenceOK)
	} else {
		add("escalation_confidence", "KI-Confidence erreicht Schwellwert", "na", percentText(d.Confidence), "nur bei Eskalation relevant", "", false)
	}
	reasonPresent := len(d.ReasonCodes) > 0
	if d.Escalate {
		add("escalation_reason_present", "Mindestens ein strukturierter Eskalationsgrund ist vorhanden", passFail(reasonPresent), strings.Join(d.ReasonCodes, ", "), ">= 1 Grundcode", "Eine Eskalation ohne kontrollierten Grundcode wird nie ausgeführt.", !reasonPresent)
	} else {
		add("escalation_reason_present", "Mindestens ein strukturierter Eskalationsgrund ist vorhanden", "na", strings.Join(d.ReasonCodes, ", "), "nur bei Eskalation relevant", "", false)
	}
	reasonAllowed := reasonPresent && allAllowed(d.ReasonCodes, cfg.EscalationAllowedReasonCodes)
	if d.Escalate {
		add("escalation_reasons_allowed", "Alle KI-Gründe sind freigegeben", passFail(reasonAllowed), strings.Join(d.ReasonCodes, ", "), strings.Join(cfg.EscalationAllowedReasonCodes, ", "), "", !reasonAllowed)
	} else {
		add("escalation_reasons_allowed", "Alle KI-Gründe sind freigegeben", "na", strings.Join(d.ReasonCodes, ", "), "nur bei Eskalation relevant", "", false)
	}
	mismatches := escalationReasonEvidenceMismatches(d.ReasonCodes, evidence)
	reasonEvidenceOK := len(mismatches) == 0
	if d.Escalate {
		detail := "Deterministische Grundcodes müssen durch Ticket-, SLA- oder Kontextdaten belegt sein."
		if !reasonEvidenceOK {
			detail += " Nicht belegt: " + strings.Join(mismatches, ", ")
		}
		add("escalation_reasons_evidenced", "Deterministische KI-Gründe sind durch Daten belegt", passFail(reasonEvidenceOK), strings.Join(d.ReasonCodes, ", "), "keine unbelegten Grundcodes", detail, !reasonEvidenceOK)
	} else {
		add("escalation_reasons_evidenced", "Deterministische KI-Gründe sind durch Daten belegt", "na", strings.Join(d.ReasonCodes, ", "), "nur bei Eskalation relevant", "", false)
	}

	commonDecision := "escalation_accepted"
	switch {
	case !cfg.EscalationEnabled:
		commonDecision = "escalation_disabled"
	case !ageOK:
		commonDecision = "escalation_too_young"
	case requiresInactivity && !activityDatesOK:
		commonDecision = "escalation_human_activity_time_unknown"
	case requiresInactivity && !inactiveOK:
		commonDecision = "escalation_recent_human_activity"
	case !d.Escalate:
		commonDecision = "escalation_not_recommended"
	case !levelOK:
		commonDecision = "escalation_level_not_allowed"
	case !confidenceOK:
		commonDecision = "escalation_confidence_below_threshold"
	case !reasonPresent:
		commonDecision = "escalation_reason_missing"
	case !reasonAllowed:
		commonDecision = "escalation_reason_not_allowed"
	case !reasonEvidenceOK:
		commonDecision = "escalation_reason_not_evidenced"
	}

	acceptedActions := 0
	for _, actionName := range actions {
		actionResult := evaluateEscalationAction(cfg, st, t, contextData, d, evidence, actionName, commonDecision)
		result.Actions = append(result.Actions, actionResult)
		if actionResult.Accepted {
			acceptedActions++
			result.Accepted = true
			if result.Action == "" {
				result.Action = actionResult.Action
				result.IdempotencyKey = actionResult.IdempotencyKey
			}
		}
	}
	if len(actions) == 0 && d.Escalate && commonDecision == "escalation_accepted" {
		result.Checks = append(result.Checks, model.RuleCheck{Code: "escalation_actions_present", Group: "escalation", Label: "Mindestens eine Aktion wurde empfohlen", Status: "fail", Actual: "keine", Expected: "1 bis 3 Aktionen", Blocking: true})
		commonDecision = "escalation_no_action_recommended"
	}
	if commonDecision != "escalation_accepted" {
		result.Decision = commonDecision
	} else if result.Accepted && acceptedActions < len(actions) {
		result.Decision = "escalation_partially_accepted"
	} else if result.Accepted {
		result.Decision = "escalation_accepted"
	} else {
		result.Decision = "escalation_no_action_accepted"
	}
	return result
}

func normalizeEscalationActions(d model.EscalationDecision) []string {
	raw := append([]string(nil), d.RecommendedActions...)
	if len(raw) == 0 && strings.TrimSpace(d.RecommendedAction) != "" {
		raw = append(raw, d.RecommendedAction)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, value := range raw {
		action := strings.ToLower(strings.TrimSpace(value))
		if action == "" || action == "none" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		out = append(out, action)
		if len(out) == 3 {
			break
		}
	}
	order := map[string]int{
		"assign_security_team":   10,
		"link_major_incident":    20,
		"assign_second_level":    30,
		"raise_priority":         40,
		"notify_service_owner":   50,
		"request_manager_review": 60,
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, lok := order[out[i]]
		right, rok := order[out[j]]
		if !lok {
			left = 100
		}
		if !rok {
			right = 100
		}
		if left == right {
			return out[i] < out[j]
		}
		return left < right
	})
	return out
}

func buildEscalationEvidence(cfg config.Config, t model.Ticket, followups []model.Followup, contextData model.ContextSnapshot, now time.Time) model.EscalationEvidence {
	e := model.EscalationEvidence{Unassigned: len(t.AssignedGroups) == 0 && len(t.AssignedUsers) == 0}
	if created, ok := parseGLPITime(t.DateCreation); ok {
		e.TicketAge = now.Sub(created).Round(time.Second).String()
	}
	inactivityRequired := cfg.EscalationMinInactivity
	if inactivityRequired <= 0 {
		inactivityRequired = cfg.EscalationMinAge
	}
	e.InactivityRequired = inactivityRequired.String()
	lastActivity := time.Time{}
	if created, ok := parseGLPITime(t.DateCreation); ok {
		lastActivity = created
	}
	for _, followup := range followups {
		if cfg.GLPIAgentUserID > 0 && followup.UserID == cfg.GLPIAgentUserID {
			continue
		}
		if _, ok := parseGLPITime(followup.Date); !ok {
			e.HumanActivityIncomplete = true
			break
		}
	}
	if human := lastHumanFollowup(followups, cfg.GLPIAgentUserID); human != nil {
		if parsed, ok := parseGLPITime(human.Date); ok && parsed.After(lastActivity) {
			lastActivity = parsed
			e.LastHumanActivity = parsed.Format(time.RFC3339)
		}
	}
	if !lastActivity.IsZero() {
		inactiveFor := now.Sub(lastActivity)
		e.InactiveFor = inactiveFor.Round(time.Second).String()
		e.NoHumanResponse = !e.HumanActivityIncomplete && inactiveFor >= inactivityRequired
	}
	if deadline, ok := parseGLPITime(t.TimeToResolve); ok {
		e.SLADeadline = deadline.Format(time.RFC3339)
		remaining := deadline.Sub(now)
		e.SLARemaining = remaining.Round(time.Second).String()
		e.SLABreached = remaining <= 0
		e.SLAAtRisk = !e.SLABreached && cfg.EscalationSLARiskWindow > 0 && remaining <= cfg.EscalationSLARiskWindow
	}
	if incident, ok := selectMajorIncident(contextData, cfg.EscalationMajorIncidentMinScore); ok {
		e.MajorIncidentID = incident.ID
		e.MajorIncidentName = incident.Name
		e.MajorIncidentScore = incident.Relevance
	}
	return e
}

func selectMajorIncident(contextData model.ContextSnapshot, minScore float64) (model.MajorIncidentContext, bool) {
	var best model.MajorIncidentContext
	for _, incident := range contextData.MajorIncidents {
		if incident.ID <= 0 || incident.Relevance < minScore {
			continue
		}
		if best.ID == 0 || incident.Relevance > best.Relevance {
			best = incident
		}
	}
	return best, best.ID > 0
}

func evaluateEscalationAction(cfg config.Config, st *state.Store, t model.Ticket, contextData model.ContextSnapshot, d model.EscalationDecision, evidence model.EscalationEvidence, actionName, commonDecision string) model.EscalationActionResult {
	result := model.EscalationActionResult{Action: actionName}
	add := func(code, label, status, actual, expected, detail string, blocking bool) {
		result.Checks = append(result.Checks, model.RuleCheck{Code: code, Group: "escalation_action", Label: label, Status: status, Actual: actual, Expected: expected, Detail: detail, Blocking: blocking})
	}
	allowed := containsFold(cfg.EscalationAllowedActions, actionName)
	add("escalation_action_allowed", "Aktion ist freigegeben", passFail(allowed), actionName, strings.Join(cfg.EscalationAllowedActions, ", "), "Jede Eskalationsaktion muss separat in ESCALATION_ALLOWED_ACTIONS freigegeben werden.", !allowed)

	targetReady := true
	prerequisiteOK := true
	alreadyApplied := false
	minLevelOK := true
	detail := ""
	switch actionName {
	case "raise_priority":
		result.Target = fmt.Sprintf("priority:%d", minInt64(6, t.Priority+1))
		targetReady = t.Priority >= 1 && t.Priority < 6
		alreadyApplied = t.Priority >= 6
		detail = "Priorität wird deterministisch um genau eine Stufe erhöht."
	case "assign_second_level":
		result.Target = fmt.Sprintf("group:%d", cfg.EscalationSecondLevelGroupID)
		targetReady = cfg.EscalationSecondLevelGroupID > 0
		alreadyApplied = containsInt64(t.AssignedGroups, cfg.EscalationSecondLevelGroupID)
		prerequisiteOK = evidence.NoHumanResponse || evidence.Unassigned || evidence.SLAAtRisk || evidence.SLABreached || hasAnyReason(d.ReasonCodes, "business_deadline", "no_workaround")
		detail = "Die konfigurierte Second-Level-Gruppe wird zu den vorhandenen Zuweisungen hinzugefügt."
	case "assign_security_team":
		result.Target = fmt.Sprintf("group:%d", cfg.EscalationSecurityGroupID)
		targetReady = cfg.EscalationSecurityGroupID > 0
		alreadyApplied = containsInt64(t.AssignedGroups, cfg.EscalationSecurityGroupID)
		prerequisiteOK = model.HasReasonCode(d.ReasonCodes, "security_incident_suspected")
		detail = "Die Security-Gruppe ist nur bei ausdrücklich erkanntem Sicherheitsverdacht zulässig."
	case "notify_service_owner":
		result.Target = actorTarget(cfg.EscalationServiceOwnerGroupID, cfg.EscalationServiceOwnerUserID, cfg.EscalationWebhookURL != "")
		targetReady = cfg.EscalationServiceOwnerGroupID > 0 || cfg.EscalationServiceOwnerUserID > 0 || cfg.EscalationWebhookURL != ""
		minLevel := cfg.EscalationServiceOwnerMinLevel
		if minLevel <= 0 {
			minLevel = 2
		}
		minLevelOK = d.Level >= minLevel
		detail = fmt.Sprintf("Service-Owner-Einbindung ist ab Stufe %d zulässig.", minLevel)
	case "link_major_incident":
		incident, ok := selectMajorIncident(contextData, cfg.EscalationMajorIncidentMinScore)
		if ok {
			result.Target = fmt.Sprintf("ticket:%d", incident.ID)
		}
		targetReady = ok && strings.TrimSpace(cfg.GLPIEscalationITILLinkPath) != "" && strings.TrimSpace(cfg.GLPIEscalationITILLinkBody) != ""
		prerequisiteOK = model.HasReasonCode(d.ReasonCodes, "major_incident_candidate")
		detail = "Das Ziel wird deterministisch als relevantester Major-Incident-Kandidat oberhalb des Schwellwerts gewählt."
	case "request_manager_review":
		result.Target = actorTarget(cfg.EscalationManagerReviewGroupID, cfg.EscalationManagerReviewUserID, cfg.EscalationWebhookURL != "")
		targetReady = cfg.EscalationManagerReviewGroupID > 0 || cfg.EscalationManagerReviewUserID > 0 || cfg.EscalationWebhookURL != ""
		minLevel := cfg.EscalationManagerReviewMinLevel
		if minLevel <= 0 {
			minLevel = 3
		}
		minLevelOK = d.Level >= minLevel
		detail = fmt.Sprintf("Management-Review ist ab Stufe %d zulässig.", minLevel)
	default:
		targetReady = false
		prerequisiteOK = false
		detail = "Unbekannte Aktion."
	}
	add("escalation_action_target", "Konfiguriertes Aktionsziel ist verfügbar", passFail(targetReady), emptyDash(result.Target), "gültiges Ziel", detail, !targetReady)
	add("escalation_action_prerequisite", "Fachliche Voraussetzung der Aktion ist erfüllt", passFail(prerequisiteOK), strings.Join(d.ReasonCodes, ", "), "aktionsspezifischer Grund", detail, !prerequisiteOK)
	add("escalation_action_level", "Eskalationsstufe erlaubt diese Aktion", passFail(minLevelOK), fmt.Sprintf("Stufe %d", d.Level), "aktionsspezifisches Minimum", detail, !minLevelOK)
	add("escalation_action_not_already_applied", "Ziel ist noch nicht am Ticket gesetzt", passFail(!alreadyApplied), boolText(!alreadyApplied), "true", result.Target, alreadyApplied)

	key := fmt.Sprintf("ticket=%d;level=%d;action=%s", t.ID, d.Level, actionName)
	// A priority target changes after a successful increase. Keeping it out of
	// the key prevents repeated +1 writes for the same escalation level.
	if result.Target != "" && actionName != "raise_priority" {
		key += ";target=" + result.Target
	}
	result.IdempotencyKey = key
	duplicate := st != nil && st.HasEscalationKey(key)
	if actionName == "raise_priority" && st != nil && st.HasEscalationKey(fmt.Sprintf("ticket=%d;level=%d", t.ID, d.Level)) {
		duplicate = true
	}
	add("escalation_action_not_duplicate", "Diese Aktion wurde für Stufe und Ziel noch nicht ausgeführt", passFail(!duplicate), boolText(!duplicate), "true", key, duplicate)

	switch {
	case commonDecision != "escalation_accepted":
		result.Decision = commonDecision
	case !allowed:
		result.Decision = "escalation_action_not_allowed"
	case !targetReady:
		result.Decision = "escalation_action_target_missing"
	case !prerequisiteOK:
		result.Decision = "escalation_action_prerequisite_missing"
	case !minLevelOK:
		result.Decision = "escalation_action_level_too_low"
	case alreadyApplied:
		result.Decision = "escalation_action_already_applied"
	case duplicate:
		result.Decision = "escalation_action_duplicate"
	default:
		result.Accepted = true
		result.Decision = "escalation_action_accepted"
	}
	return result
}

func escalationReasonEvidenceMismatches(codes []string, evidence model.EscalationEvidence) []string {
	var mismatches []string
	for _, code := range model.NormalizeReasonCodes(codes) {
		consistent := true
		switch code {
		case "no_human_response":
			consistent = evidence.NoHumanResponse
		case "unassigned":
			consistent = evidence.Unassigned
		case "sla_at_risk":
			consistent = evidence.SLAAtRisk
		case "sla_breached":
			consistent = evidence.SLABreached
		case "major_incident_candidate":
			consistent = evidence.MajorIncidentID > 0
		}
		if !consistent {
			mismatches = append(mismatches, code)
		}
	}
	return mismatches
}

func hasAnyReason(codes []string, wanted ...string) bool {
	for _, code := range wanted {
		if model.HasReasonCode(codes, code) {
			return true
		}
	}
	return false
}

func containsInt64(values []int64, wanted int64) bool {
	if wanted <= 0 {
		return false
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func actorTarget(groupID, userID int64, webhook bool) string {
	parts := make([]string, 0, 3)
	if groupID > 0 {
		parts = append(parts, fmt.Sprintf("group:%d", groupID))
	}
	if userID > 0 {
		parts = append(parts, fmt.Sprintf("user:%d", userID))
	}
	if webhook {
		parts = append(parts, "webhook")
	}
	return strings.Join(parts, ",")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "–"
	}
	return value
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func lastHumanFollowup(followups []model.Followup, agentUserID int64) *model.Followup {
	var latest *model.Followup
	var latestAt time.Time
	for i := range followups {
		f := &followups[i]
		if agentUserID > 0 && f.UserID == agentUserID {
			continue
		}
		at, ok := parseGLPITime(f.Date)
		if !ok {
			continue
		}
		if latest == nil || at.After(latestAt) {
			copy := *f
			latest = &copy
			latestAt = at
		}
	}
	return latest
}

func parseGLPITime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func durationText(v time.Duration, ok bool) string {
	if !ok {
		return "Erstellungszeit unbekannt"
	}
	if v < 0 {
		v = 0
	}
	return v.Round(time.Minute).String()
}

func allAllowed(values, allowedValues []string) bool {
	if len(values) == 0 {
		return false
	}
	allowed := stringSet(allowedValues)
	for _, value := range values {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(value))]; !ok {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func containsFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

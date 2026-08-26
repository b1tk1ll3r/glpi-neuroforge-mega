package agent

import (
	"fmt"
	"strings"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
)

type statusReplyEvaluation struct {
	Accepted     bool
	DecisionCode string
	Type         string
	Candidate    model.ServiceIssueCandidate
	AI           model.StatusDecision
	FinalScore   float64
	RenderedText string
	ReplyText    string
	ReplyIsHTML  bool
	Checks       []model.RuleCheck
}

func statusIssueCandidates(issues []model.ServiceIssueContext) []model.ServiceIssueCandidate {
	out := make([]model.ServiceIssueCandidate, 0, len(issues))
	for _, issue := range issues {
		if statusReplyType(issue) == "" {
			continue
		}
		out = append(out, model.ServiceIssueCandidate{
			ID:    fmt.Sprintf("uptime-%d", len(out)+1),
			Issue: issue,
		})
	}
	return out
}

func statusReplyType(issue model.ServiceIssueContext) string {
	if strings.EqualFold(issue.Kind, "maintenance") || strings.EqualFold(issue.Status, "maintenance") {
		return "maintenance"
	}
	if strings.EqualFold(issue.Kind, "pinned_incident") {
		return "incident"
	}
	switch strings.ToLower(strings.TrimSpace(issue.Status)) {
	case "down", "pending", "incident":
		return "incident"
	default:
		return ""
	}
}

func statusCandidateName(issue model.ServiceIssueContext) string {
	for _, value := range []string{issue.MonitorName, issue.IncidentTitle, issue.StatusPage} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Uptime-Kuma-Eintrag"
}

func auditStatusCandidates(candidates []model.ServiceIssueCandidate, decision model.StatusDecision, cfg config.Config, evaluation statusReplyEvaluation) []model.StatusCandidateAudit {
	out := make([]model.StatusCandidateAudit, 0, len(candidates))
	for _, candidate := range candidates {
		selected := decision.Matched && candidate.ID == decision.CandidateID
		finalScore := 0.0
		decisionCode := "not_selected"
		if selected {
			finalScore = clampPolicy01(candidate.Issue.Relevance) * clampPolicy01(decision.Confidence)
			decisionCode = statusScoreDecision(candidate.Issue.Relevance, decision.Confidence, finalScore, cfg)
			if evaluation.Accepted {
				decisionCode = evaluation.DecisionCode
			}
		}
		out = append(out, model.StatusCandidateAudit{
			ID: candidate.ID, Name: statusCandidateName(candidate.Issue), Kind: statusReplyType(candidate.Issue),
			Status: candidate.Issue.Status, Relevance: candidate.Issue.Relevance, AISelected: selected,
			AIConfidence: map[bool]float64{true: decision.Confidence, false: 0}[selected], FinalScore: finalScore, Decision: decisionCode,
		})
	}
	return out
}

func evaluateStatusReply(cfg config.Config, policy Policy, contextData model.ContextSnapshot, candidates []model.ServiceIssueCandidate, decision model.StatusDecision) statusReplyEvaluation {
	res := statusReplyEvaluation{AI: decision, DecisionCode: "status_reply_not_selected"}
	if !cfg.ContextStatusReplyEnabled {
		res.DecisionCode = "status_reply_disabled"
		res.Checks = append(res.Checks,
			check("status_reply", "status_reply_enabled", "Statusbezogene vordefinierte Antworten aktiviert", "info", false, "nein", "optionale Funktion", "Die Funktion ist bewusst deaktiviert; dies ist kein Fehler und blockiert andere KI-Läufe nicht."),
			check("status_reply", "status_reply_context_complete", "Uptime-Kuma-Kontext vollständig verfügbar", passFail(!contextData.Incomplete), contextData.Incomplete, boolText(!contextData.Incomplete), "true", strings.Join(contextData.Warnings, "; ")),
			check("status_reply", "status_reply_candidates_present", "Aktive Störungs- oder Wartungskandidaten vorhanden", "na", false, fmt.Sprintf("%d Kandidaten", len(candidates)), "nur bei aktivierter Funktion relevant", "Keine Bewertung, weil Status-Templates deaktiviert sind."),
			check("status_reply", "status_reply_ai_match", "KI hat genau einen Uptime-Kuma-Eintrag zugeordnet", "na", false, "–", "nur bei aktivierter Funktion relevant", "Kein Status-KI-Aufruf erforderlich."),
			check("status_reply", "status_reply_candidate_known", "Ausgewählter Kandidat stammt aus Uptime Kuma", "na", false, "–", "nur bei aktivierter Funktion relevant", "Kein Kandidat ausgewählt."),
		)
		res.Checks = appendStatusScoreNA(res.Checks)
		return res
	}
	res.Checks = append(res.Checks,
		check("status_reply", "status_reply_enabled", "Statusbezogene vordefinierte Antworten aktiviert", passFail(cfg.ContextStatusReplyEnabled), !cfg.ContextStatusReplyEnabled, boolText(cfg.ContextStatusReplyEnabled), "true", "Die KI bewertet nur die Zuordnung; der Benutzertext ist fest vorgegeben."),
		check("status_reply", "status_reply_context_complete", "Uptime-Kuma-Kontext vollständig verfügbar", passFail(!contextData.Incomplete), contextData.Incomplete, boolText(!contextData.Incomplete), "true", strings.Join(contextData.Warnings, "; ")),
		check("status_reply", "status_reply_candidates_present", "Aktive Störungs- oder Wartungskandidaten vorhanden", passFail(len(candidates) > 0), len(candidates) == 0, fmt.Sprintf("%d Kandidaten", len(candidates)), ">= 1 Kandidat", ""),
		check("status_reply", "status_reply_ai_match", "KI hat genau einen Uptime-Kuma-Eintrag zugeordnet", passFail(decision.Matched), !decision.Matched, boolText(decision.Matched), "ja", strings.TrimSpace(decision.Reason)),
	)

	var selected *model.ServiceIssueCandidate
	for i := range candidates {
		if candidates[i].ID == strings.TrimSpace(decision.CandidateID) {
			selected = &candidates[i]
			break
		}
	}
	candidateKnown := !decision.Matched || selected != nil
	res.Checks = append(res.Checks, check("status_reply", "status_reply_candidate_known", "Ausgewählter Kandidat stammt aus Uptime Kuma", passFail(candidateKnown), !candidateKnown, strings.TrimSpace(decision.CandidateID), "bereitgestellte Kandidaten-ID", ""))
	if selected == nil {
		res.Checks = appendStatusScoreNA(res.Checks)
		return res
	}

	res.Candidate = *selected
	res.Type = statusReplyType(selected.Issue)
	res.FinalScore = clampPolicy01(selected.Issue.Relevance) * clampPolicy01(decision.Confidence)
	relevanceOK := selected.Issue.Relevance >= cfg.ContextStatusReplyMinRelevance
	confidenceOK := decision.Confidence >= cfg.ContextStatusReplyMinAIConfidence
	finalOK := res.FinalScore >= cfg.ContextStatusReplyMinFinalScore
	template := cfg.ContextIncidentReplyText
	if res.Type == "maintenance" {
		template = cfg.ContextMaintenanceReplyText
	}
	templateOK := strings.TrimSpace(template) != ""
	res.Checks = append(res.Checks,
		check("status_reply", "status_reply_relevance", "Deterministische Ticket-Relevanz erreicht Schwellwert", passFail(relevanceOK), !relevanceOK, percentText(selected.Issue.Relevance), ">= "+percentText(cfg.ContextStatusReplyMinRelevance), ""),
		check("status_reply", "status_reply_ai_confidence", "KI-Zuordnung erreicht Schwellwert", passFail(confidenceOK), !confidenceOK, percentText(decision.Confidence), ">= "+percentText(cfg.ContextStatusReplyMinAIConfidence), ""),
		check("status_reply", "status_reply_final_score", "Kombinierter Score erreicht Schwellwert", passFail(finalOK), !finalOK, percentText(res.FinalScore), ">= "+percentText(cfg.ContextStatusReplyMinFinalScore), "Relevanz × KI-Confidence"),
		check("status_reply", "status_reply_template_present", "Vordefinierter Text für den Status ist vorhanden", passFail(templateOK), !templateOK, boolText(templateOK), "ja", "Es wird kein von der KI formulierter Text verwendet."),
	)

	if !cfg.ContextStatusReplyEnabled || contextData.Incomplete || !decision.Matched || !candidateKnown || !relevanceOK || !confidenceOK || !finalOK || !templateOK || res.Type == "" {
		res.DecisionCode = statusScoreDecision(selected.Issue.Relevance, decision.Confidence, res.FinalScore, cfg)
		return res
	}

	res.RenderedText = renderStatusTemplate(template, selected.Issue)
	if policy.AIContentLabelEnabled {
		res.ReplyText = policy.formatRichReply(policy.plainTextToHTML(res.RenderedText))
		res.ReplyIsHTML = true
	} else {
		res.ReplyText = policy.formatReply(res.RenderedText)
	}
	res.Accepted = true
	if res.Type == "maintenance" {
		res.DecisionCode = "reply_status_maintenance_accepted"
	} else {
		res.DecisionCode = "reply_status_incident_accepted"
	}
	return res
}

func appendStatusScoreNA(checks []model.RuleCheck) []model.RuleCheck {
	for _, spec := range []struct{ code, label string }{
		{"status_reply_relevance", "Deterministische Ticket-Relevanz erreicht Schwellwert"},
		{"status_reply_ai_confidence", "KI-Zuordnung erreicht Schwellwert"},
		{"status_reply_final_score", "Kombinierter Score erreicht Schwellwert"},
		{"status_reply_template_present", "Vordefinierter Text für den Status ist vorhanden"},
	} {
		checks = append(checks, check("status_reply", spec.code, spec.label, "na", false, "–", "ausgewählter Kandidat erforderlich", ""))
	}
	return checks
}

func statusScoreDecision(relevance, confidence, final float64, cfg config.Config) string {
	switch {
	case relevance < cfg.ContextStatusReplyMinRelevance:
		return "status_reply_relevance_below_threshold"
	case confidence < cfg.ContextStatusReplyMinAIConfidence:
		return "status_reply_confidence_below_threshold"
	case final < cfg.ContextStatusReplyMinFinalScore:
		return "status_reply_final_score_below_threshold"
	default:
		return "status_reply_not_selected"
	}
}

func renderStatusTemplate(template string, issue model.ServiceIssueContext) string {
	values := map[string]string{
		"{{service_name}}":     statusCandidateName(issue),
		"{{status}}":           strings.TrimSpace(issue.Status),
		"{{status_page}}":      strings.TrimSpace(issue.StatusPage),
		"{{message}}":          strings.TrimSpace(issue.Message),
		"{{incident_title}}":   strings.TrimSpace(issue.IncidentTitle),
		"{{incident_content}}": strings.TrimSpace(issue.IncidentContent),
		"{{last_heartbeat}}":   strings.TrimSpace(issue.LastHeartbeat),
	}
	out := template
	for placeholder, value := range values {
		out = strings.ReplaceAll(out, placeholder, value)
	}
	return strings.TrimSpace(out)
}

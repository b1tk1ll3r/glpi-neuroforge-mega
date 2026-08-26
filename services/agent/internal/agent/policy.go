package agent

import (
	"fmt"
	"html"
	"strings"

	"github.com/example/glpi-ai-agent/internal/model"
)

const AIContentLabelHTML = `<a href="https://ai.trustednet.eu/declaration?preset=full&component=text&extent=full&activities=generation&review=expert&lang=de&assurance=selfDeclared&subject=https%3A%2F%2Fglpi.hilden.de&substantialHumanReview=true&editorialResponsibilityConfirmed=true&responsible=Stadt+Hilden&responsibleUrl=https%3A%2F%2Fwww.hilden.de%2Fde%2Fservice%2Fimpressum-datenschutz&customDescription=Die+KI+hat+den+Inhalt+auf+Basis+des+Ticket-Inhaltes+ausgew%C3%A4hlt.+Der+Inhalt+wurde+sachlich+und+fachlich+von+einem+Menschen+erzeugt.&badgeLabel=KI-Beitrag&badgeMessage=Die+KI+hat+die+Antwort+ausgew%C3%A4hlt%2C+jedoch+nicht+verfasst.&leftColor=%23036715&rightColor=%23535454"><img src="https://ai.trustednet.eu/v1/badge.svg?preset=full&component=text&extent=full&activities=generation&review=expert&lang=de&assurance=selfDeclared&subject=https%3A%2F%2Fglpi.hilden.de&substantialHumanReview=true&editorialResponsibilityConfirmed=true&responsible=Stadt+Hilden&responsibleUrl=https%3A%2F%2Fwww.hilden.de%2Fde%2Fservice%2Fimpressum-datenschutz&customDescription=Die+KI+hat+den+Inhalt+auf+Basis+des+Ticket-Inhaltes+ausgew%C3%A4hlt.+Der+Inhalt+wurde+sachlich+und+fachlich+von+einem+Menschen+erzeugt.&badgeLabel=KI-Beitrag&badgeMessage=Die+KI+hat+die+Antwort+ausgew%C3%A4hlt%2C+jedoch+nicht+verfasst.&leftColor=%23036715&rightColor=%23535454&theme=color&link=https%3A%2F%2Fai.trustednet.eu%2Fdeclaration%3Fpreset%3Dfull%26component%3Dtext%26extent%3Dfull%26activities%3Dgeneration%26review%3Dexpert%26lang%3Dde%26assurance%3DselfDeclared%26subject%3Dhttps%253A%252F%252Fglpi.hilden.de%26substantialHumanReview%3Dtrue%26editorialResponsibilityConfirmed%3Dtrue%26responsible%3DStadt%2BHilden%26responsibleUrl%3Dhttps%253A%252F%252Fwww.hilden.de%252Fde%252Fservice%252Fimpressum-datenschutz%26customDescription%3DDie%2BKI%2Bhat%2Bden%2BInhalt%2Bauf%2BBasis%2Bdes%2BTicket-Inhaltes%2Bausgew%25C3%25A4hlt.%2BDer%2BInhalt%2Bwurde%2Bsachlich%2Bund%2Bfachlich%2Bvon%2Beinem%2BMenschen%2Berzeugt.%26badgeLabel%3DKI-Beitrag%26badgeMessage%3DDie%2BKI%2Bhat%2Bdie%2BAntwort%2Bausgew%25C3%25A4hlt%252C%2Bjedoch%2Bnicht%2Bverfasst.%26leftColor%3D%2523036715%26rightColor%3D%2523535454" alt="UCNG"></a>`

type Policy struct {
	AutoCategory, AutoReply                                                                      bool
	CategoryConfidence, ReplyConfidence, KnowledgeMinScore                                       float64
	KnowledgeRetrievalFloor                                                                      float64
	KnowledgeEvidenceRetrievalWeight, KnowledgeEvidenceAIWeight, KnowledgeEvidenceCategoryWeight float64
	AllowedSources, AutoReplySources                                                             map[string]struct{}
	CommunicationLanguage, CommunicationStyle                                                    string
	CommunicationSalutation, CommunicationClosing                                                string
	CommunicationSignature                                                                       string
	BlockReplyOnContextError, BlockReplyOnIncident                                               bool
	ContextRelevanceMinScore                                                                     float64
	AIContentLabelEnabled                                                                        bool
}

func NewPolicy(autoCategory, autoReply bool, categoryConfidence, replyConfidence, knowledgeMinScore, knowledgeRetrievalFloor, evidenceRetrievalWeight, evidenceAIWeight, evidenceCategoryWeight float64, allowedSources, autoReplySources []string, language, style, salutation, closing, signature string, aiContentLabelEnabled, blockReplyOnContextError, blockReplyOnIncident bool, contextRelevanceMinScore float64) Policy {
	if evidenceRetrievalWeight+evidenceAIWeight+evidenceCategoryWeight <= 0 {
		evidenceRetrievalWeight, evidenceAIWeight, evidenceCategoryWeight = .45, .35, .20
	}

	return Policy{
		AutoCategory:                     autoCategory,
		AutoReply:                        autoReply,
		CategoryConfidence:               categoryConfidence,
		ReplyConfidence:                  replyConfidence,
		KnowledgeMinScore:                knowledgeMinScore,
		KnowledgeRetrievalFloor:          knowledgeRetrievalFloor,
		KnowledgeEvidenceRetrievalWeight: evidenceRetrievalWeight,
		KnowledgeEvidenceAIWeight:        evidenceAIWeight,
		KnowledgeEvidenceCategoryWeight:  evidenceCategoryWeight,
		AllowedSources:                   sourceSet(allowedSources),
		AutoReplySources:                 sourceSet(autoReplySources),
		CommunicationLanguage:            strings.TrimSpace(language),
		CommunicationStyle:               strings.ToLower(strings.TrimSpace(style)),
		CommunicationSalutation:          strings.TrimSpace(salutation),
		CommunicationClosing:             strings.TrimSpace(closing),
		CommunicationSignature:           strings.TrimSpace(signature),
		AIContentLabelEnabled:            aiContentLabelEnabled,
		BlockReplyOnContextError:         blockReplyOnContextError,
		BlockReplyOnIncident:             blockReplyOnIncident,
		ContextRelevanceMinScore:         contextRelevanceMinScore,
	}
}

func (p Policy) Evaluate(t model.Ticket, d model.Decision, categories []model.Category, hits []model.KnowledgeHit, contextData model.ContextSnapshot) (model.PolicyResult, error) {
	res := model.PolicyResult{
		CategoryRecommendationID: d.Category.ID,
		CategoryConfidence:       d.Category.Confidence,
		CategoryThreshold:        p.CategoryConfidence,
		ReplyRecommendation:      d.Reply.Allowed,
		ReplyConfidence:          d.Reply.Confidence,
		ReplyThreshold:           p.ReplyConfidence,
		ReplyKnowledgeID:         strings.TrimSpace(d.Reply.KnowledgeID),
		AIReason:                 strings.TrimSpace(d.Reason),
	}

	known := make(map[int64]model.Category, len(categories))
	for _, c := range categories {
		known[c.ID] = c
	}
	if c, ok := known[d.Category.ID]; ok {
		res.CategoryRecommendationName = categoryDisplayName(c)
	}

	// Category rules. "already correct" is not a failure; it means that no
	// write action is necessary even though the recommendation is valid.
	res.CategoryChecks = append(res.CategoryChecks,
		check("category", "category_auto_enabled", "Automatische Kategorisierung aktiviert", boolStatus(p.AutoCategory), !p.AutoCategory, boolText(p.AutoCategory), "true", "Globale Schreibfreigabe für Kategorien."),
	)
	if d.Category.ID == 0 {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_recommendation_present", "KI hat eine Kategorie empfohlen", "fail", true, "#0", "gültige GLPI-Kategorie", "Kategorie 0 bedeutet: keine fachlich vertretbare Empfehlung."))
	} else {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_recommendation_present", "KI hat eine Kategorie empfohlen", "pass", false, fmt.Sprintf("#%d", d.Category.ID), "gültige GLPI-Kategorie", ""))
	}
	_, categoryKnown := known[d.Category.ID]
	if d.Category.ID == 0 {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_known", "Empfohlene Kategorie existiert in GLPI", "na", false, "–", "bekannte Kategorie", "Keine Kategorie empfohlen."))
	} else if categoryKnown {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_known", "Empfohlene Kategorie existiert in GLPI", "pass", false, fmt.Sprintf("#%d %s", d.Category.ID, res.CategoryRecommendationName), "bekannte Kategorie", ""))
	} else {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_known", "Empfohlene Kategorie existiert in GLPI", "fail", true, fmt.Sprintf("#%d", d.Category.ID), "bekannte Kategorie", "Die KI darf nur IDs aus der bereitgestellten GLPI-Kategorieliste verwenden."))
	}
	if d.Category.ID != 0 && d.Category.ID == t.CategoryID {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_change_required", "Kategorieänderung ist notwendig", "info", false, fmt.Sprintf("bereits #%d", t.CategoryID), "andere Kategorie", "Die aktuelle Kategorie entspricht bereits der KI-Empfehlung."))
	} else if d.Category.ID != 0 {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_change_required", "Kategorieänderung ist notwendig", "pass", false, fmt.Sprintf("#%d → #%d", t.CategoryID, d.Category.ID), "abweichende Kategorie", ""))
	} else {
		res.CategoryChecks = append(res.CategoryChecks, check("category", "category_change_required", "Kategorieänderung ist notwendig", "na", false, "–", "abweichende Kategorie", "Keine Empfehlung vorhanden."))
	}
	catConfidenceOK := d.Category.Confidence >= p.CategoryConfidence
	res.CategoryChecks = append(res.CategoryChecks, check("category", "category_confidence", "KI-Confidence erreicht Schwellwert", passFail(catConfidenceOK), !catConfidenceOK, percentText(d.Category.Confidence), ">= "+percentText(p.CategoryConfidence), ""))

	switch {
	case !p.AutoCategory:
		res.CategoryDecision = "category_auto_disabled"
	case d.Category.ID == 0:
		res.CategoryDecision = "category_no_recommendation"
	case d.Category.ID == t.CategoryID:
		res.CategoryDecision = "category_already_correct"
	case !categoryKnown:
		res.CategoryDecision = "category_unknown"
	case !catConfidenceOK:
		res.CategoryDecision = "category_confidence_below_threshold"
	default:
		res.ChangeCategory = true
		res.CategoryID = d.Category.ID
		res.CategoryDecision = "category_accepted"
	}

	// Resolve the selected knowledge article once. All gates below are evaluated
	// even when an earlier gate failed, so diagnostics can show the complete rule
	// picture instead of only the first short-circuit reason.
	var selected *model.KnowledgeHit
	for i := range hits {
		if hits[i].Doc.ID == res.ReplyKnowledgeID {
			selected = &hits[i]
			break
		}
	}

	relevantIncident := contextData.HasRelevantIncident(p.ContextRelevanceMinScore)
	res.ReplyChecks = append(res.ReplyChecks,
		check("reply", "reply_auto_enabled", "Auto-Reply global aktiviert", boolStatus(p.AutoReply), !p.AutoReply, boolText(p.AutoReply), "true", ""),
		check("reply", "reply_candidates_available", "Mindestens ein Knowledge-Kandidat vorhanden", passFail(len(hits) > 0), len(hits) == 0, fmt.Sprintf("%d Kandidaten", len(hits)), "> 0", ""),
		check("reply", "reply_model_recommended", "KI empfiehlt eine Antwort", passFail(d.Reply.Allowed), !d.Reply.Allowed, boolText(d.Reply.Allowed), "true", ""),
		check("reply", "reply_confidence", "KI-Reply-Confidence erreicht Schwellwert", passFail(d.Reply.Confidence >= p.ReplyConfidence), d.Reply.Confidence < p.ReplyConfidence, percentText(d.Reply.Confidence), ">= "+percentText(p.ReplyConfidence), ""),
	)
	if res.ReplyKnowledgeID == "" {
		res.ReplyChecks = append(res.ReplyChecks, check("reply", "reply_knowledge_selected", "KI hat einen Knowledge-Artikel ausgewählt", "fail", true, "keine ID", "ID eines bereitgestellten Kandidaten", ""))
	} else {
		res.ReplyChecks = append(res.ReplyChecks, check("reply", "reply_knowledge_selected", "KI hat einen Knowledge-Artikel ausgewählt", "pass", false, res.ReplyKnowledgeID, "ID eines bereitgestellten Kandidaten", ""))
	}
	if p.BlockReplyOnContextError {
		res.ReplyChecks = append(res.ReplyChecks, check("context", "reply_context_complete", "Kontextquellen vollständig", passFail(!contextData.Incomplete), contextData.Incomplete, boolText(!contextData.Incomplete), "true", strings.Join(contextData.Warnings, "; ")))
	} else {
		res.ReplyChecks = append(res.ReplyChecks, check("context", "reply_context_complete", "Kontextquellen vollständig", "na", false, boolText(!contextData.Incomplete), "nicht blockierend", "CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS ist deaktiviert."))
	}
	if p.BlockReplyOnIncident {
		res.ReplyChecks = append(res.ReplyChecks, check("context", "reply_no_relevant_incident", "Keine relevante zentrale Störung", passFail(!relevantIncident), relevantIncident, boolText(!relevantIncident), "true", "Major Incidents und Uptime-Kuma-Störungen werden berücksichtigt."))
	} else {
		res.ReplyChecks = append(res.ReplyChecks, check("context", "reply_no_relevant_incident", "Keine relevante zentrale Störung", "na", false, boolText(!relevantIncident), "nicht blockierend", "Incident-Blockierung ist deaktiviert."))
	}
	if res.ReplyKnowledgeID == "" {
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_knowledge_exists", "Ausgewählter Knowledge-Artikel ist verfügbar", "na", false, "–", "vorhandener Artikel", "Keine Knowledge-ID ausgewählt."))
	} else if selected == nil {
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_knowledge_exists", "Ausgewählter Knowledge-Artikel ist verfügbar", "fail", true, res.ReplyKnowledgeID, "Kandidat im übergebenen Set", "Die KI hat eine ID ausgewählt, die nicht im Kandidatenset vorhanden ist."))
	} else {
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_knowledge_exists", "Ausgewählter Knowledge-Artikel ist verfügbar", "pass", false, selected.Doc.ID, "Kandidat im übergebenen Set", selected.Doc.Title))
	}

	selectedAutoReplyAllowed := false
	if selected != nil {
		effectiveCategoryID := t.CategoryID
		if res.ChangeCategory {
			effectiveCategoryID = res.CategoryID
		}
		sourceAllowed := p.sourceAllowed(selected.Doc.Source)
		sourceReplyAllowed := p.sourceAllowedForReply(selected.Doc.Source)
		langOK := strings.EqualFold(strings.TrimSpace(selected.Doc.Language), p.CommunicationLanguage)
		styleOK := strings.EqualFold(strings.TrimSpace(selected.Doc.CommunicationStyle), p.CommunicationStyle)
		res.ReplyChecks = append(res.ReplyChecks,
			check("knowledge", "reply_source_allowed", "Knowledge-Quelle ist für Retrieval erlaubt", passFail(sourceAllowed), !sourceAllowed, selected.Doc.Source, "KNOWLEDGE_ALLOWED_SOURCES", ""),
			check("knowledge", "reply_source_auto_allowed", "Knowledge-Quelle ist für Auto-Reply erlaubt", passFail(sourceReplyAllowed), !sourceReplyAllowed, selected.Doc.Source, "KNOWLEDGE_AUTO_REPLY_SOURCES", ""),
			check("communication", "reply_language_match", "Sprache des Artikels passt zur Kommunikationspolicy", passFail(langOK), !langOK, selected.Doc.Language, p.CommunicationLanguage, ""),
			check("communication", "reply_style_match", "Stil des Artikels passt zur Kommunikationspolicy", passFail(styleOK), !styleOK, selected.Doc.CommunicationStyle, p.CommunicationStyle, ""),
		)
		autoDetail := ""
		if strings.TrimSpace(selected.Doc.AutoReplyDecision) != "" {
			autoDetail = selected.Doc.AutoReplyDecision
		}
		if strings.TrimSpace(selected.Doc.AutoReplyDetail) != "" {
			if autoDetail != "" {
				autoDetail += ": "
			}
			autoDetail += selected.Doc.AutoReplyDetail
		}
		if len(selected.Doc.UnmappedExternalCategories) > 0 {
			if autoDetail != "" {
				autoDetail += "; "
			}
			autoDetail += "Nicht zugeordnete externe Kategorien: " + strings.Join(selected.Doc.UnmappedExternalCategories, ", ")
		}
		selectedAutoReplyAllowed = knowledgeAutoReplyAllowed(selected.Doc)
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_knowledge_auto_reply", "Artikel darf für Auto-Reply verwendet werden", passFail(selectedAutoReplyAllowed), !selectedAutoReplyAllowed, boolText(selectedAutoReplyAllowed), "true", autoDetail))

		threshold := p.KnowledgeMinScore
		if selected.Doc.MinScore > threshold {
			threshold = selected.Doc.MinScore
		}
		res.KnowledgeThreshold = threshold
		res.KnowledgeRetrievalScore = selected.Score
		res.KnowledgeRetrievalFloor = p.KnowledgeRetrievalFloor
		retrievalOK := selected.Score >= p.KnowledgeRetrievalFloor
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_retrieval_floor", "Retrieval-Score erreicht Mindestfloor", passFail(retrievalOK), !retrievalOK, percentText(selected.Score), ">= "+percentText(p.KnowledgeRetrievalFloor), "Der Retrieval-Floor entscheidet, ob ein Artikel überhaupt als plausibler Kandidat gilt."))

		catIDForEvidence := effectiveCategoryID
		categoryEvidence, categoryAvailable := 0.0, false
		if len(selected.Doc.Categories) > 0 && catIDForEvidence != 0 {
			categoryAvailable = true
			for _, id := range selected.Doc.Categories {
				if id == catIDForEvidence {
					categoryEvidence = 1
					res.KnowledgeCategoryAligned = true
					break
				}
			}
		}
		res.KnowledgeEvidenceScore = evidenceScore(selected.Score, d.Reply.Confidence, categoryEvidence, categoryAvailable, p.KnowledgeEvidenceRetrievalWeight, p.KnowledgeEvidenceAIWeight, p.KnowledgeEvidenceCategoryWeight)
		evidenceOK := res.KnowledgeEvidenceScore >= threshold
		catDetail := "Kategorie nicht als Evidenz verfügbar; Gewichte werden normalisiert."
		if categoryAvailable {
			catDetail = "Kategorie-Evidenz: " + percentText(categoryEvidence)
		}
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_evidence_threshold", "Finale Knowledge-Evidenz erreicht Schwellwert", passFail(evidenceOK), !evidenceOK, percentText(res.KnowledgeEvidenceScore), ">= "+percentText(threshold), catDetail))

		answerPresent := strings.TrimSpace(selected.Doc.Answer) != "" || strings.TrimSpace(selected.Doc.AnswerHTML) != ""
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_answer_present", "Freigegebener Antwortinhalt vorhanden", passFail(answerPresent), !answerPresent, boolText(answerPresent), "true", ""))

		categoryAllowed := knowledgeCategoryAllowed(selected.Doc, t.CategoryID, res.ChangeCategory, res.CategoryID)
		categoryDetail := "Keine gemappte ITIL-Kategorie am Artikel verfügbar; die fachliche Eignung wird über Retrieval, KI-Auswahl und Evidenz geprüft."
		if len(selected.Doc.Categories) > 0 {
			categoryDetail = fmt.Sprintf("Gemappte Artikel-ITIL-Kategorien: %v; effektive Ticketkategorie: #%d", selected.Doc.Categories, effectiveCategoryID)
		}
		res.ReplyChecks = append(res.ReplyChecks, check("knowledge", "reply_category_scope", "Artikel passt zur effektiven Ticketkategorie", passFail(categoryAllowed), !categoryAllowed, boolText(categoryAllowed), "true", categoryDetail))
	} else {
		for _, spec := range []struct{ code, label string }{
			{"reply_source_allowed", "Knowledge-Quelle ist für Retrieval erlaubt"},
			{"reply_source_auto_allowed", "Knowledge-Quelle ist für Auto-Reply erlaubt"},
			{"reply_language_match", "Sprache des Artikels passt zur Kommunikationspolicy"},
			{"reply_style_match", "Stil des Artikels passt zur Kommunikationspolicy"},
			{"reply_knowledge_auto_reply", "Artikel darf für Auto-Reply verwendet werden"},
			{"reply_retrieval_floor", "Retrieval-Score erreicht Mindestfloor"},
			{"reply_evidence_threshold", "Finale Knowledge-Evidenz erreicht Schwellwert"},
			{"reply_answer_present", "Freigegebener Antwortinhalt vorhanden"},
			{"reply_category_scope", "Artikel passt zur effektiven Ticketkategorie"},
		} {
			res.ReplyChecks = append(res.ReplyChecks, check("knowledge", spec.code, spec.label, "na", false, "–", "Knowledge-Artikel erforderlich", "Kein gültiger Knowledge-Artikel ausgewählt."))
		}
	}

	// Keep the historical decision codes stable. The first failed gate in this
	// ordered list is the actual blocking reason.
	switch {
	case !p.AutoReply:
		res.ReplyDecision = "reply_auto_disabled"
	case len(hits) == 0:
		res.ReplyDecision = "reply_no_knowledge_candidates"
	case !d.Reply.Allowed:
		res.ReplyDecision = "reply_model_not_recommended"
	case d.Reply.Confidence < p.ReplyConfidence:
		res.ReplyDecision = "reply_confidence_below_threshold"
	case res.ReplyKnowledgeID == "":
		res.ReplyDecision = "reply_no_knowledge_selected"
	case p.BlockReplyOnContextError && contextData.Incomplete:
		res.ReplyDecision = "reply_context_incomplete"
	case p.BlockReplyOnIncident && relevantIncident:
		res.ReplyDecision = "reply_relevant_incident"
	case selected == nil:
		res.ReplyDecision = "reply_knowledge_not_found"
	case !p.sourceAllowed(selected.Doc.Source):
		res.ReplyDecision = "reply_source_not_allowed"
	case !p.sourceAllowedForReply(selected.Doc.Source):
		res.ReplyDecision = "reply_source_not_allowed_for_auto_reply"
	case !strings.EqualFold(strings.TrimSpace(selected.Doc.Language), p.CommunicationLanguage):
		res.ReplyDecision = "reply_language_mismatch"
	case !strings.EqualFold(strings.TrimSpace(selected.Doc.CommunicationStyle), p.CommunicationStyle):
		res.ReplyDecision = "reply_style_mismatch"
	case !selectedAutoReplyAllowed:
		res.ReplyDecision = "reply_knowledge_auto_reply_not_approved"
	case selected.Score < p.KnowledgeRetrievalFloor:
		res.ReplyDecision = "reply_knowledge_retrieval_below_floor"
	case res.KnowledgeEvidenceScore < res.KnowledgeThreshold:
		res.ReplyDecision = "reply_knowledge_evidence_below_threshold"
	case strings.TrimSpace(selected.Doc.Answer) == "" && strings.TrimSpace(selected.Doc.AnswerHTML) == "":
		res.ReplyDecision = "reply_knowledge_answer_empty"
	case !knowledgeCategoryAllowed(selected.Doc, t.CategoryID, res.ChangeCategory, res.CategoryID):
		res.ReplyDecision = "reply_category_not_allowed"
	default:
		res.Reply = true
		if p.AIContentLabelEnabled {
			if strings.TrimSpace(selected.Doc.AnswerHTML) != "" {
				res.ReplyText = p.formatRichReply(selected.Doc.AnswerHTML)
			} else {
				res.ReplyText = p.formatRichReply(p.plainTextToHTML(selected.Doc.Answer))
			}
			res.ReplyIsHTML = true
		} else if strings.TrimSpace(selected.Doc.AnswerHTML) != "" {
			res.ReplyText = p.formatRichReply(selected.Doc.AnswerHTML)
			res.ReplyIsHTML = true
		} else {
			res.ReplyText = p.formatReply(selected.Doc.Answer)
		}
		res.KnowledgeID = selected.Doc.ID
		res.ReplyDecision = "reply_accepted"
	}
	return res, nil
}

func check(group, code, label, status string, blocking bool, actual, expected, detail string) model.RuleCheck {
	return model.RuleCheck{Group: group, Code: code, Label: label, Status: status, Blocking: blocking && status == "fail", Actual: actual, Expected: expected, Detail: detail}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func boolStatus(ok bool) string { return passFail(ok) }
func boolText(v bool) string {
	if v {
		return "ja"
	}
	return "nein"
}
func percentText(v float64) string { return fmt.Sprintf("%.1f %%", clampPolicy01(v)*100) }

func knowledgeCategoryAllowed(d model.KnowledgeDoc, currentCategory int64, change bool, target int64) bool {
	catID := currentCategory
	if change {
		catID = target
	}
	if len(d.Categories) == 0 {
		return true
	}
	return containsPolicyInt64(d.Categories, catID)
}

func knowledgeAutoReplyAllowed(d model.KnowledgeDoc) bool {
	return d.AutoReply
}

func containsPolicyInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func categoryDisplayName(c model.Category) string {
	if strings.TrimSpace(c.CompleteName) != "" {
		return strings.TrimSpace(c.CompleteName)
	}
	return strings.TrimSpace(c.Name)
}

func (p Policy) sourceAllowed(source string) bool {
	_, ok := p.AllowedSources[strings.ToLower(strings.TrimSpace(source))]
	return ok
}

func (p Policy) sourceAllowedForReply(source string) bool {
	_, ok := p.AutoReplySources[strings.ToLower(strings.TrimSpace(source))]
	return ok
}

func (p Policy) formatReply(body string) string {
	parts := make([]string, 0, 4)
	if p.CommunicationSalutation != "" {
		parts = append(parts, p.CommunicationSalutation)
	}
	parts = append(parts, strings.TrimSpace(body))
	footer := strings.TrimSpace(strings.Join(nonEmpty(p.CommunicationClosing, p.CommunicationSignature), "\n"))
	if footer != "" {
		parts = append(parts, footer)
	}
	return strings.Join(parts, "\n\n")
}

func (p Policy) formatRichReply(bodyHTML string) string {
	parts := make([]string, 0, 4)
	if p.AIContentLabelEnabled {
		// Keep this block byte-for-byte unchanged. It is the externally defined
		// declaration that must be the first content in every AI-selected reply.
		parts = append(parts, AIContentLabelHTML)
	}
	if strings.TrimSpace(p.CommunicationSalutation) != "" {
		parts = append(parts, "<p>"+html.EscapeString(strings.TrimSpace(p.CommunicationSalutation))+"</p>")
	}
	parts = append(parts, strings.TrimSpace(bodyHTML))
	footer := nonEmpty(p.CommunicationClosing, p.CommunicationSignature)
	if len(footer) > 0 {
		escaped := make([]string, 0, len(footer))
		for _, line := range footer {
			escaped = append(escaped, html.EscapeString(line))
		}
		parts = append(parts, "<p>"+strings.Join(escaped, "<br>")+"</p>")
	}
	return strings.Join(parts, "\n")
}

func (p Policy) plainTextToHTML(body string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\r", "\n")
	if normalized == "" {
		return ""
	}
	paragraphs := strings.Split(normalized, "\n\n")
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		escaped := html.EscapeString(paragraph)
		escaped = strings.ReplaceAll(escaped, "\n", "<br>")
		out = append(out, "<p>"+escaped+"</p>")
	}
	return strings.Join(out, "\n")
}

func sourceSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func evidenceScore(retrieval, ai, category float64, categoryAvailable bool, retrievalWeight, aiWeight, categoryWeight float64) float64 {
	sum, weights := 0.0, 0.0
	if retrievalWeight > 0 {
		sum += clampPolicy01(retrieval) * retrievalWeight
		weights += retrievalWeight
	}
	if aiWeight > 0 {
		sum += clampPolicy01(ai) * aiWeight
		weights += aiWeight
	}
	if categoryAvailable && categoryWeight > 0 {
		sum += clampPolicy01(category) * categoryWeight
		weights += categoryWeight
	}
	if weights == 0 {
		return 0
	}
	return clampPolicy01(sum / weights)
}

func clampPolicy01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

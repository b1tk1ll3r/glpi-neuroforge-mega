package agent

import (
	"strings"
	"testing"

	"github.com/example/glpi-ai-agent/internal/model"
)

func productionTestPolicy() Policy {
	return NewPolicy(true, true, .9, .97, .88, .30, .45, .35, .20, []string{"internal-kb", "vendor-docs"}, []string{"internal-kb"}, "de-DE", "formal", "Guten Tag,", "Mit freundlichen Grüßen", "IT-Service", true, true, true, .2)
}

func approvedHit(source, language, style string) []model.KnowledgeHit {
	return []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "KB1", Answer: "Bitte starten Sie den VPN-Client neu.", AutoReply: true, MinScore: .9, Categories: []int64{2}, Source: source, Language: language, CommunicationStyle: style}, Score: .95}}
}

func replyDecision() model.Decision {
	var d model.Decision
	d.Reply.Allowed = true
	d.Reply.Confidence = .99
	d.Reply.KnowledgeID = "KB1"
	d.Category.ID = 2
	d.Category.Confidence = .99
	return d
}

func TestPolicyAutoReplyUsesApprovedKnowledge(t *testing.T) {
	r, err := productionTestPolicy().Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("internal-kb", "de-DE", "formal"), model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || !r.ChangeCategory {
		t.Fatalf("unexpected result: %+v", r)
	}
	if !r.ReplyIsHTML {
		t.Fatalf("AI-labelled reply must be HTML: %+v", r)
	}
	if !strings.HasPrefix(r.ReplyText, AIContentLabelHTML) {
		t.Fatalf("AI content label is not the exact first content: %s", r.ReplyText)
	}
	for _, expected := range []string{"Guten Tag,", "Bitte starten Sie", "Mit freundlichen Grüßen", "IT-Service"} {
		if !strings.Contains(r.ReplyText, expected) {
			t.Fatalf("reply missing %q: %q", expected, r.ReplyText)
		}
	}
}

func TestPolicyPreservesGLPIKnowledgeRichText(t *testing.T) {
	p := productionTestPolicy()
	d := replyDecision()
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{
		ID: "KB1", Title: "Rich", Answer: "Wichtiger Hinweis Erstens Zweitens",
		AnswerHTML: `<h2>Wichtiger Hinweis</h2><p><strong>Bitte beachten:</strong></p><ul><li>Erstens</li><li>Zweitens</li></ul><p><a href="https://example.invalid/help">Dokumentation</a></p>`,
		AutoReply:  true, MinScore: .9, Categories: []int64{2}, Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal",
	}, Score: .95, CategoryScore: 1}}
	r, err := p.Evaluate(model.Ticket{CategoryID: 2}, d, []model.Category{{ID: 2}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || !r.ReplyIsHTML {
		t.Fatalf("expected rich reply, got %+v", r)
	}
	for _, want := range []string{"<h2>Wichtiger Hinweis</h2>", "<strong>Bitte beachten:</strong>", "<ul>", "<li>Erstens</li>", `<a href="https://example.invalid/help">`} {
		if !strings.Contains(r.ReplyText, want) {
			t.Fatalf("rich reply lost %q: %s", want, r.ReplyText)
		}
	}
	if !strings.Contains(r.ReplyText, "<p>Guten Tag,</p>") || !strings.Contains(r.ReplyText, "Mit freundlichen Grüßen<br>IT-Service") {
		t.Fatalf("rich wrapper missing: %s", r.ReplyText)
	}
}

func TestPolicyRejectsSourceNotAllowedForAutoReply(t *testing.T) {
	r, err := productionTestPolicy().Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("vendor-docs", "de-DE", "formal"), model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Reply {
		t.Fatalf("vendor-docs must not auto-reply: %+v", r)
	}
}

func TestPolicyRejectsWrongLanguageOrStyle(t *testing.T) {
	p := productionTestPolicy()
	for _, tc := range []struct{ language, style string }{{"en-US", "formal"}, {"de-DE", "informal"}} {
		r, err := p.Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("internal-kb", tc.language, tc.style), model.ContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Reply {
			t.Fatalf("unexpected reply for %s/%s", tc.language, tc.style)
		}
	}
}

func TestPolicyRejectsUnknownCategoryWithoutFailingRun(t *testing.T) {
	var d model.Decision
	d.Category.ID = 99
	d.Category.Confidence = 1
	p := NewPolicy(true, false, .9, .9, .8, .30, .45, .35, .20, []string{"internal-kb"}, nil, "de-DE", "formal", "", "", "", true, true, true, .2)
	r, err := p.Evaluate(model.Ticket{CategoryID: 1}, d, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ChangeCategory || r.CategoryDecision != "category_unknown" {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestPolicyCategoryDecisionIsDeterministic(t *testing.T) {
	p := NewPolicy(true, false, .9, .9, .8, .30, .45, .35, .20, []string{"internal-kb"}, nil, "de-DE", "formal", "", "", "", true, true, true, .2)
	var d model.Decision
	d.Category.ID = 2
	d.Category.Confidence = .89
	r, err := p.Evaluate(model.Ticket{CategoryID: 1}, d, []model.Category{{ID: 1, Name: "Alt"}, {ID: 2, Name: "Active Directory"}}, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ChangeCategory || r.CategoryDecision != "category_confidence_below_threshold" || r.CategoryRecommendationName != "Active Directory" {
		t.Fatalf("unexpected result: %+v", r)
	}
	d.Category.Confidence = .91
	r, err = p.Evaluate(model.Ticket{CategoryID: 1}, d, []model.Category{{ID: 1, Name: "Alt"}, {ID: 2, Name: "Active Directory"}}, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.ChangeCategory || r.CategoryID != 2 || r.CategoryDecision != "category_accepted" {
		t.Fatalf("unexpected accepted result: %+v", r)
	}
}

func TestPolicyBlocksAutoReplyOnRelevantIncident(t *testing.T) {
	ctx := model.ContextSnapshot{MajorIncidents: []model.MajorIncidentContext{{ID: 77, Name: "VPN Ausfall", Relevance: .8}}}
	r, err := productionTestPolicy().Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("internal-kb", "de-DE", "formal"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Reply || r.ReplyDecision != "reply_relevant_incident" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestPolicyBlocksAutoReplyOnIncompleteContext(t *testing.T) {
	ctx := model.ContextSnapshot{Incomplete: true, Warnings: []string{"uptime_kuma: timeout"}}
	r, err := productionTestPolicy().Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("internal-kb", "de-DE", "formal"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Reply || r.ReplyDecision != "reply_context_incomplete" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestPolicyUsesTwoStageEvidenceForShortButUnambiguousTicket(t *testing.T) {
	p := NewPolicy(true, true, .70, .70, .70, .30, .45, .35, .20,
		[]string{"internal-kb"}, []string{"internal-kb"}, "de-DE", "formal", "", "", "", true, true, true, .2)
	d := replyDecision()
	d.Reply.Confidence = .95
	hits := []model.KnowledgeHit{{
		Doc:           model.KnowledgeDoc{ID: "KB1", Answer: "Bitte prüfen Sie die Anmeldung.", AutoReply: true, Categories: []int64{2}, Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"},
		Score:         .4309932200645022,
		CategoryScore: 1,
	}}
	r, err := p.Evaluate(model.Ticket{CategoryID: 2}, d, []model.Category{{ID: 2, Name: "Active Directory"}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || r.ReplyDecision != "reply_accepted" {
		t.Fatalf("expected two-stage evidence to accept the selected KB, got %+v", r)
	}
	if r.KnowledgeEvidenceScore < .70 || r.KnowledgeRetrievalScore < .30 || !r.KnowledgeCategoryAligned {
		t.Fatalf("unexpected evidence diagnostics: %+v", r)
	}
}

func TestPolicyStillRejectsWeakRetrievalEvenWithHighAIConfidence(t *testing.T) {
	p := NewPolicy(true, true, .70, .70, .70, .30, .45, .35, .20,
		[]string{"internal-kb"}, []string{"internal-kb"}, "de-DE", "formal", "", "", "", true, true, true, .2)
	d := replyDecision()
	d.Reply.Confidence = .99
	hits := []model.KnowledgeHit{{
		Doc:   model.KnowledgeDoc{ID: "KB1", Answer: "VPN neu starten.", AutoReply: true, Categories: []int64{99}, Source: "internal-kb", Language: "de-DE", CommunicationStyle: "formal"},
		Score: .22,
	}}
	r, err := p.Evaluate(model.Ticket{CategoryID: 2}, d, []model.Category{{ID: 2, Name: "Active Directory"}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Reply || r.ReplyDecision != "reply_knowledge_retrieval_below_floor" {
		t.Fatalf("weak retrieval must remain blocked: %+v", r)
	}
}

func TestPolicyAIContentLabelEscapesPlainTextKnowledge(t *testing.T) {
	p := productionTestPolicy()
	d := replyDecision()
	hits := approvedHit("internal-kb", "de-DE", "formal")
	hits[0].Doc.Answer = "Bitte <script>alert('x')</script> prüfen.\\nZweite Zeile."
	r, err := p.Evaluate(model.Ticket{CategoryID: 2}, d, []model.Category{{ID: 2}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || !r.ReplyIsHTML || !strings.HasPrefix(r.ReplyText, AIContentLabelHTML) {
		t.Fatalf("unexpected labelled reply: %+v", r)
	}
	if strings.Contains(r.ReplyText, "<script>") || !strings.Contains(r.ReplyText, "&lt;script&gt;") {
		t.Fatalf("plain-text KB was not safely escaped: %s", r.ReplyText)
	}
}

func TestPolicyCanDisableAIContentLabel(t *testing.T) {
	p := NewPolicy(true, true, .9, .97, .88, .30, .45, .35, .20,
		[]string{"internal-kb"}, []string{"internal-kb"}, "de-DE", "formal", "Guten Tag,", "Mit freundlichen Grüßen", "IT-Service", false, true, true, .2)
	r, err := p.Evaluate(model.Ticket{CategoryID: 1}, replyDecision(), []model.Category{{ID: 1}, {ID: 2}}, approvedHit("internal-kb", "de-DE", "formal"), model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ReplyIsHTML || strings.Contains(r.ReplyText, AIContentLabelHTML) {
		t.Fatalf("disabled AI content label must preserve plain reply behaviour: %+v", r)
	}
}

func TestPolicyAllowsExplicitlyApprovedUncategorizedGLPIArticle(t *testing.T) {
	p := NewPolicy(true, true, .70, .70, .70, .30, .45, .35, .20,
		[]string{"glpi-kb"}, []string{"glpi-kb"}, "de-DE", "formal", "", "", "", false, false, false, .2)
	d := replyDecision()
	d.Category.ID = 67
	d.Category.Confidence = .99
	d.Reply.Confidence = .99
	hits := []model.KnowledgeHit{{
		Doc: model.KnowledgeDoc{
			ID: "GLPI-KB-21", Answer: "Drucker neu verbinden.", AutoReply: true,
			AutoReplyDecision: "glpi_kb_uncategorized_article_approved",
			Source:            "glpi-kb", Language: "de-DE", CommunicationStyle: "formal",
		},
		Score: .95,
	}}
	d.Reply.KnowledgeID = "GLPI-KB-21"
	r, err := p.Evaluate(model.Ticket{CategoryID: 67}, d, []model.Category{{ID: 67}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || r.ReplyDecision != "reply_accepted" {
		t.Fatalf("expected explicitly approved uncategorized GLPI article to be accepted: %+v", r)
	}
}

func TestPolicyDoesNotApplyLegacyITILAutoReplyGateToUncategorizedArticle(t *testing.T) {
	p := NewPolicy(true, true, .70, .70, .70, .30, .45, .35, .20,
		[]string{"glpi-kb"}, []string{"glpi-kb"}, "de-DE", "formal", "", "", "", false, false, false, .2)
	d := replyDecision()
	d.Category.ID = 66
	d.Category.Confidence = .99
	d.Reply.Confidence = .99
	d.Reply.KnowledgeID = "GLPI-KB-21"
	hits := []model.KnowledgeHit{{
		Doc: model.KnowledgeDoc{
			ID: "GLPI-KB-21", Answer: "Drucker neu verbinden.", AutoReply: true,
			AutoReplyDecision: "glpi_kb_uncategorized_article_approved",
			Source:            "glpi-kb", Language: "de-DE", CommunicationStyle: "formal",
		},
		Score: .95,
	}}
	r, err := p.Evaluate(model.Ticket{CategoryID: 66}, d, []model.Category{{ID: 66}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Reply || r.ReplyDecision != "reply_accepted" {
		t.Fatalf("legacy ITIL auto-reply gate must not block explicitly approved uncategorized article: %+v", r)
	}
}

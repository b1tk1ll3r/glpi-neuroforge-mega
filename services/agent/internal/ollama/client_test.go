package ollama

import (
	"context"
	"encoding/json"
	"github.com/example/glpi-ai-agent/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnalyseStructured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		format, _ := body["format"].(map[string]any)
		if format == nil {
			t.Error("missing schema")
		} else if props, _ := format["properties"].(map[string]any); props != nil {
			if category, _ := props["category"].(map[string]any); category != nil {
				if categoryProps, _ := category["properties"].(map[string]any); categoryProps != nil {
					if _, exists := categoryProps["change"]; exists {
						t.Error("category schema must not let the model decide change=true/false")
					}
				}
			}
		}
		options, _ := body["options"].(map[string]any)
		if options["num_predict"] != float64(256) {
			t.Errorf("unexpected num_predict: %v", options["num_predict"])
		}
		if options["num_ctx"] != float64(131072) {
			t.Errorf("unexpected num_ctx: %v", options["num_ctx"])
		}
		if body["keep_alive"] != "10m0s" {
			t.Errorf("unexpected keep_alive: %v", body["keep_alive"])
		}
		if body["think"] != false {
			t.Errorf("unexpected think: %v", body["think"])
		}
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":1,"confidence":0.9},"reply":{"allowed":false,"confidence":0.1,"knowledge_id":""},"reason":"ok"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, 10*time.Minute, false, 1, 1)
	c.SetNumCtx(131072)
	d, err := c.Analyse(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Reason != "ok" {
		t.Fatalf("unexpected %+v", d)
	}
}

func TestAnalyseRetriesInvalidJSON(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := `{"category":`
		if calls > 1 {
			content = `{"category":{"id":2,"confidence":0.95},"reply":{"allowed":false,"confidence":0.1,"knowledge_id":""},"reason":"ok"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 1)
	d, err := c.Analyse(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 2, Name: "Active Directory"}}, nil, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || d.Category.ID != 2 {
		t.Fatalf("calls=%d decision=%+v", calls, d)
	}
}

func TestStructuredRetryDoesNotGrowPrompt(t *testing.T) {
	calls := 0
	var firstMessages string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages, _ := json.Marshal(body["messages"])
		if calls == 1 {
			firstMessages = string(messages)
		} else if string(messages) != firstMessages {
			t.Fatalf("retry grew or changed messages:\nfirst=%s\nretry=%s", firstMessages, messages)
		}
		options, _ := body["options"].(map[string]any)
		if options["num_ctx"] != float64(8192) {
			t.Fatalf("retry num_ctx=%v, want 8192", options["num_ctx"])
		}
		content := `{"category":`
		if calls > 1 {
			content = `{"category":{"id":2,"confidence":0.95},"reason":"ok"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 1)
	c.SetNumCtx(8192)
	if _, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 2, Name: "VPN"}}, nil, model.ContextSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
}

func TestEmbedDisablesSilentTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["truncate"] != false {
			t.Fatalf("truncate=%v, want false", body["truncate"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, time.Minute, false, 1, 0)
	v, err := c.Embed(context.Background(), []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 {
		t.Fatalf("embeddings=%d", len(v))
	}
}

func TestAnalyseCategoryOnlyIgnoresInvalidReplyDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if !strings.Contains(string(formatJSON), `"allowed":{"enum":[false],"type":"boolean"}`) &&
			!strings.Contains(string(formatJSON), `"allowed":{"type":"boolean","enum":[false]}`) {
			t.Fatalf("reply=false constraint missing from schema: %s", formatJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":2,"confidence":0.96},"reply":{"allowed":true,"confidence":0.99,"knowledge_id":""},"reason":"Outlook"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	d, err := c.Analyse(context.Background(), model.Ticket{ID: 14}, []model.Category{{ID: 2, Name: "Outlook"}}, nil, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Category.ID != 2 || d.Category.Confidence != 0.96 {
		t.Fatalf("category decision lost: %+v", d)
	}
	if d.Reply.Allowed || d.Reply.Confidence != 0 || d.Reply.KnowledgeID != "" {
		t.Fatalf("reply decision was not normalized: %+v", d.Reply)
	}
}

func TestAnalyseRetriesAllowedReplyWithoutKnowledgeID(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := `{"category":{"id":2,"confidence":0.95},"reply":{"allowed":true,"confidence":0.95,"knowledge_id":""},"reason":"passt"}`
		if calls > 1 {
			content = `{"category":{"id":2,"confidence":0.95},"reply":{"allowed":true,"confidence":0.95,"knowledge_id":"GLPI-KB-1"},"reason":"passt"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 1)
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "GLPI-KB-1", Title: "Benutzeranmeldung"}}}
	d, err := c.Analyse(context.Background(), model.Ticket{ID: 11}, []model.Category{{ID: 2, Name: "Active Directory"}}, nil, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || d.Reply.KnowledgeID != "GLPI-KB-1" {
		t.Fatalf("calls=%d decision=%+v", calls, d)
	}
}

func TestAnalyseDoesNotExposeRichAnswerHTMLToModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages, _ := body["messages"].([]any)
		for _, raw := range messages {
			m, _ := raw.(map[string]any)
			content, _ := m["content"].(string)
			if strings.Contains(content, "RICH_SECRET_MARKUP") {
				t.Fatalf("rich answer HTML leaked into Ollama prompt: %s", content)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":2,"confidence":0.95},"reply":{"allowed":false,"confidence":0.1,"knowledge_id":""},"reason":"ok"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	c.SetNumCtx(8192)
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "GLPI-KB-1", Title: "Login", Text: "plain", Answer: "plain", AnswerHTML: `<p>RICH_SECRET_MARKUP</p>`}}}
	if _, err := c.Analyse(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 2}}, nil, hits, model.ContextSnapshot{}); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyseSeparatesCategoryKnowledgeFromReplyCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if strings.Contains(string(formatJSON), "CAT-ONLY") {
			t.Fatalf("category-only ID leaked into reply knowledge enum: %s", formatJSON)
		}
		if !strings.Contains(string(formatJSON), "REPLY-OK") {
			t.Fatalf("reply ID missing from schema: %s", formatJSON)
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		prompt := string(messagesJSON)
		if !strings.Contains(prompt, "CAT-ONLY") || !strings.Contains(prompt, "classification evidence") {
			t.Fatalf("category evidence missing from prompt: %s", prompt)
		}
		if strings.Contains(prompt, "CATEGORY_SECRET_ANSWER") {
			t.Fatalf("category-only answer leaked into prompt: %s", prompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":2,"confidence":0.95},"reply":{"allowed":true,"confidence":0.95,"knowledge_id":"REPLY-OK"},"reason":"ok"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	categoryHits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "CAT-ONLY", Text: "classification evidence", Answer: "CATEGORY_SECRET_ANSWER"}}}
	replyHits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "REPLY-OK", Text: "reply evidence", Answer: "safe answer"}}}
	if _, err := c.Analyse(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 2}}, categoryHits, replyHits, model.ContextSnapshot{}); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyseCategoryUsesDedicatedSchemaAndSanitizedKnowledge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if strings.Contains(string(formatJSON), `"reply"`) {
			t.Fatalf("reply schema leaked into category stage: %s", formatJSON)
		}
		if !strings.Contains(string(formatJSON), `"maxLength":320`) {
			t.Fatalf("category reason length guard missing: %s", formatJSON)
		}
		options, _ := body["options"].(map[string]any)
		if options["num_ctx"] != float64(8192) {
			t.Fatalf("category num_ctx=%v, want 8192", options["num_ctx"])
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		if strings.Contains(string(messagesJSON), "SECRET_ANSWER") {
			t.Fatalf("category answer leaked into prompt: %s", messagesJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":2,"confidence":0.97},"reason":"VPN"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	c.SetNumCtx(8192)
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "CAT", Text: "vpn evidence", Answer: "SECRET_ANSWER"}}}
	d, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 2, Name: "VPN"}}, hits, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Category.ID != 2 || d.Reason != "VPN" {
		t.Fatalf("unexpected category decision: %+v", d)
	}
}

func TestAnalyseReplyUsesDedicatedSchemaAndEffectiveCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if strings.Contains(string(formatJSON), `"category"`) {
			t.Fatalf("category schema leaked into reply stage: %s", formatJSON)
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		prompt := string(messagesJSON)
		if !strings.Contains(prompt, "Outlook") || !strings.Contains(prompt, "REPLY-1") {
			t.Fatalf("effective category or reply candidate missing: %s", messagesJSON)
		}
		if !strings.Contains(prompt, "OUT-1") || !strings.Contains(prompt, "sekundaere Evidenz") {
			t.Fatalf("validated outcome or secondary-evidence guard missing: %s", messagesJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"reply":{"allowed":true,"confidence":0.96,"knowledge_id":"REPLY-1"},"reason":"passt"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{ID: "REPLY-1", Text: "signature", Answer: "answer"}}}
	ctxData := model.ContextSnapshot{ValidatedOutcomes: []model.ValidatedOutcomeEvidence{{OutcomeID: "OUT-1", Decision: "corrected", Text: "human verified resolution", Similarity: 0.88}}}
	d, err := c.AnalyseReply(context.Background(), model.Ticket{ID: 1}, model.Category{ID: 9, Name: "Outlook"}, hits, ctxData)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Reply.Allowed || d.Reply.KnowledgeID != "REPLY-1" {
		t.Fatalf("unexpected reply decision: %+v", d)
	}
}

func TestAnalyseStatusDoesNotGenerateReplyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		prompt := string(messagesJSON)
		if !strings.Contains(prompt, "Erzeuge niemals einen Antworttext") || !strings.Contains(prompt, "uptime-1") {
			t.Fatalf("status-only guard or candidate missing: %s", prompt)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if strings.Contains(string(formatJSON), "reply_text") || strings.Contains(string(formatJSON), "message_text") {
			t.Fatalf("reply text field leaked into schema: %s", formatJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"matched":true,"candidate_id":"uptime-1","confidence":0.93,"reason":"passt"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, time.Minute, false, 1, 0)
	candidates := []model.ServiceIssueCandidate{{ID: "uptime-1", Issue: model.ServiceIssueContext{MonitorName: "Exchange", Status: "down", Relevance: .8}}}
	d, err := c.AnalyseStatus(context.Background(), model.Ticket{ID: 1, Name: "Outlook"}, model.Category{ID: 9, Name: "Outlook"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Matched || d.CandidateID != "uptime-1" || d.Confidence != .93 {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestAnalysePriorityUsesDedicatedReasonCodeSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		format := string(formatJSON)
		if !strings.Contains(format, "recommended_priority") || !strings.Contains(format, "core_service_unavailable") || strings.Contains(format, "knowledge_id") {
			t.Fatalf("unexpected priority schema: %s", format)
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(messagesJSON), "deterministische Go-Policy") {
			t.Fatalf("policy boundary missing from priority prompt: %s", messagesJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"recommended_priority":5,"recommended_impact":4,"recommended_urgency":5,"affected_scope":"site","time_criticality":"immediate","reason_codes":["core_service_unavailable","site_affected","core_service_unavailable"],"confidence":0.93,"reason":"Ein zentraler Dienst ist am Standort nicht verfügbar."}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	d, err := c.AnalysePriority(context.Background(), model.Ticket{ID: 1, Priority: 2}, model.Category{ID: 3, Name: "Netzwerk"}, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if d.RecommendedPriority != 5 || d.Confidence != .93 || len(d.ReasonCodes) != 2 || d.ReasonCodes[0] != "core_service_unavailable" || d.ReasonCodes[1] != "site_affected" {
		t.Fatalf("unexpected priority decision: %+v", d)
	}
}

func TestAnalysePriorityReconcilesContradictoryInsufficientInformationWithoutRetry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messagesJSON, _ := json.Marshal(body["messages"])
		prompt := string(messagesJSON)
		if !strings.Contains(prompt, "multiple_users_affected") || !strings.Contains(prompt, "workaround_available") || !strings.Contains(prompt, "meine Kollegen und ich") {
			t.Fatalf("deterministic evidence missing from prompt: %s", prompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"recommended_priority":3,"recommended_impact":3,"recommended_urgency":3,"affected_scope":"unknown","time_criticality":"unknown","reason_codes":["insufficient_information"],"confidence":0.2,"reason":"insufficient_information"}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 1)
	d, err := c.AnalysePriority(context.Background(), model.Ticket{ID: 20, Priority: 3, Name: "Probleme mit Flurdrucker", Content: `<p>Meine Kollegen und ich können nicht mehr auf den Flur-Kopierern drucken.</p><p>Die Bürodrucker laufen noch.</p>`}, model.Category{ID: 67, Name: "Arbeitsplatzdrucker"}, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d want 1", attempts)
	}
	if d.AffectedScope != "multiple_users" || !model.HasReasonCode(d.ReasonCodes, "multiple_users_affected") || !model.HasReasonCode(d.ReasonCodes, "workaround_available") || model.HasReasonCode(d.ReasonCodes, "insufficient_information") || d.RecommendedPriority != 3 {
		t.Fatalf("unexpected reconciled decision: %+v", d)
	}
	if d.Confidence != 0.2 {
		t.Fatalf("confidence was increased: %v", d.Confidence)
	}
}

func TestAnalyseEscalationNormalizesNegativeDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		formatJSON, _ := json.Marshal(body["format"])
		if !strings.Contains(string(formatJSON), "recommended_actions") || !strings.Contains(string(formatJSON), "no_human_response") {
			t.Fatalf("unexpected escalation schema: %s", formatJSON)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"escalate":false,"level":3,"recommended_actions":["raise_priority"],"reason_codes":["already_being_handled"],"confidence":0.91,"reason":"Ein Techniker arbeitet bereits am Ticket."}`}})
	}))
	defer srv.Close()
	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 768, time.Minute, false, 1, 0)
	d, err := c.AnalyseEscalation(
		context.Background(),
		model.Ticket{ID: 1},
		[]model.Followup{{ID: 4, UserID: 7}},
		model.ContextSnapshot{},
		model.EscalationEvidence{NoHumanResponse: false},
		model.EscalationConstraints{AllowedActions: []string{"none", "raise_priority"}, AllowedReasonCodes: []string{"no_human_response"}, MaxLevel: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if d.Escalate || d.Level != 0 || d.RecommendedAction != "none" || len(d.RecommendedActions) != 1 || d.RecommendedActions[0] != "none" {
		t.Fatalf("negative escalation was not normalized: %+v", d)
	}
}

func TestEmbedSplitsBatchWhenOllamaContextWindowRejectsAggregateInput(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input    []string `json:"input"`
			Truncate bool     `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Truncate {
			t.Errorf("adaptive embedding must preserve truncate=false")
		}
		if len(body.Input) > 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "estimated request size 5933 tokens exceeds model context length 2048"})
			return
		}
		value := map[string]float64{"a": 1, "b": 2, "c": 3}[body.Input[0]]
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{value, 1}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, time.Minute, false, 1, 0)
	vectors, err := c.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 || vectors[0][0] != 1 || vectors[1][0] != 2 || vectors[2][0] != 3 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
	if calls != 5 {
		t.Fatalf("calls=%d, want 5 adaptive requests", calls)
	}
}

func TestEmbedSplitsOversizedSingleInputWithoutSilentTruncation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input    []string `json:"input"`
			Truncate bool     `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Truncate {
			t.Errorf("adaptive embedding must preserve truncate=false")
		}
		if len(body.Input) != 1 {
			t.Errorf("input count=%d, want 1", len(body.Input))
		}
		n := len([]rune(body.Input[0]))
		if n > 8 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "input exceeds model context window"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{float64(n), 1}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, time.Minute, false, 1, 0)
	vectors, err := c.Embed(context.Background(), []string{"abcdefghijklmnopqrstuvwxyz123456"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("unexpected vectors: %#v", vectors)
	}
	if vectors[0][0] != 8 || vectors[0][1] != 1 {
		t.Fatalf("unexpected weighted aggregate vector: %#v", vectors[0])
	}
	if calls != 7 {
		t.Fatalf("calls=%d, want 7 adaptive requests", calls)
	}
}

func TestEmbedDoesNotSplitUnrelatedBadRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "model does not support embeddings"})
	}))
	defer srv.Close()

	c := New(srv.URL, "m", "e", "de-DE", "formal", time.Second, 256, time.Minute, false, 1, 0)
	_, err := c.Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected embedding error")
	}
	if calls != 1 {
		t.Fatalf("calls=%d, unrelated error must not trigger adaptive splitting", calls)
	}
}

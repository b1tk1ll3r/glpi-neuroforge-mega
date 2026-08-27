package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func TestGoalProgressUsesResearchEvidenceTarget(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-1", Title: "NVIDIA", Target: "100 hochwertige, quellengebundene Wissenseinträge"}
	for r := 0; r < 3; r++ {
		sourceID := string(rune('a' + r))
		if err := s.UpsertSource(&core.KnowledgeSource{ID: sourceID, Type: "web", Title: "NVIDIA vendor documentation", URI: "https://example.test/nvidia/" + sourceID, Status: "ready"}); err != nil {
			t.Fatal(err)
		}
		run, err := s.StartResearchRun(g.ID, g.Title)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			memoryID := fmt.Sprintf("m-%d-%d", r, i)
			if err := s.AddMemory(&core.Memory{ID: memoryID, Kind: "evidence", MemoryType: core.MemorySemantic, Text: "NVIDIA RTX evidence", Vector: []float32{1, 0}, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", GoalID: g.ID, SourceID: sourceID}}); err != nil {
				t.Fatal(err)
			}
			_, _ = s.AddResearchEvent(run.ID, core.ResearchEvent{Type: "evidence.learned", SourceID: sourceID, MemoryID: memoryID})
		}
		_, _ = s.FinishResearchRun(run.ID, "completed", "")
	}
	e := &Engine{store: s}
	e.refreshGoalResearchProgress(g, 0)
	if g.ResearchEvidence != 30 {
		t.Fatalf("evidence=%d", g.ResearchEvidence)
	}
	if g.Progress < .299 || g.Progress > .301 {
		t.Fatalf("progress=%f reason=%s", g.Progress, g.ProgressReason)
	}
}

func TestGoalEvidenceFiltersOwnLearningMemories(t *testing.T) {
	hits := []store.SearchHit{
		{Memory: core.Memory{Kind: "goal-learning", Provenance: core.MemoryProvenance{Source: "goal-cycle"}}},
		{Memory: core.Memory{Kind: "evidence", Provenance: core.MemoryProvenance{Source: "web.page"}}},
	}
	got := filterGoalEvidenceHits(hits)
	if len(got) != 1 || got[0].Memory.Provenance.Source != "web.page" {
		t.Fatalf("unexpected hits: %#v", got)
	}
}

func TestGoalResearchPublishesIdempotentHumanReviewDraft(t *testing.T) {
	var requests int
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("bad auth")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["integration_key"] != "neuroforge-goal:goal-1" {
			t.Fatalf("bad integration key: %#v", body)
		}
		if body["answer"] == "" {
			t.Fatalf("empty answer")
		}
		meta, ok := body["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("missing metadata: %#v", body["metadata"])
		}
		if meta["research_evidence"] != float64(1) || meta["research_sources"] != float64(1) {
			t.Fatalf("draft counters must reflect selected evidence: %#v", meta)
		}
		if meta["research_goal_evidence"] != float64(13) || meta["research_goal_sources"] != float64(5) {
			t.Fatalf("goal totals must remain auditable: %#v", meta)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"staging": map[string]any{"key": "KB-AI-STAGING-1", "meta": map[string]any{"integration_action": "created"}}})
	}))
	defer kb.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := &core.KnowledgeSource{ID: "src-1", Type: "web", Title: "NVIDIA Vendor", URI: "https://example.test/nvidia/doc", Trust: .8, Status: "ready"}
	if err := s.UpsertSource(src); err != nil {
		t.Fatal(err)
	}
	mem := &core.Memory{ID: "mem-1", Kind: "evidence", MemoryType: core.MemorySemantic, Text: "RTX driver installation requires a supported operating system and current vendor package.", Vector: []float32{1, 0}, Confidence: .7, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", GoalID: "goal-1", SourceID: src.ID}}
	if err := s.AddMemory(mem); err != nil {
		t.Fatal(err)
	}
	run, err := s.StartResearchRun("goal-1", "NVIDIA")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.AddResearchEvent(run.ID, core.ResearchEvent{Type: "evidence.learned", SourceID: src.ID, MemoryID: mem.ID})
	_, _ = s.FinishResearchRun(run.ID, "completed", "")

	e := &Engine{store: s, http: kb.Client()}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "secret", MinEvidence: 1, MinSources: 1, SynthesisMode: "evidence"})
	g := &core.Goal{ID: "goal-1", Title: "NVIDIA", ResearchEvidence: 13, ResearchSources: 5, ResearchSourceIDs: []string{src.ID}}
	e.maybePublishGoalDraft(context.Background(), g, ResearchResult{RunID: run.ID})
	if requests != 1 || g.StagingDraftsCreated != 1 || g.LastStagingDraftID == "" || g.LastStagingError != "" {
		t.Fatalf("goal=%#v requests=%d", g, requests)
	}
}

func TestGoalResearchQueryNeverUsesSchedulerNextActionAsSearchSubject(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Autonomy.UseLLM = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: s}
	g := &core.Goal{Title: "NVIDIA", Description: "Sammle Informationen zu den neuen RTX Grafikkarten.", Target: "100 quellengebundene Wissenseinträge", NextAction: "Review the strongest negative evidence and create a corrective task before the next cycle."}
	qs, _ := e.goalResearchQueries(context.Background(), g, 2, nil)
	if len(qs) != 2 {
		t.Fatalf("queries=%#v", qs)
	}
	for _, q := range qs {
		if !strings.Contains(strings.ToLower(q), "nvidia") || strings.Contains(strings.ToLower(q), "negative evidence") || strings.Contains(strings.ToLower(q), "next cycle") {
			t.Fatalf("bad research query: %q", q)
		}
	}
	if qs[0] != "NVIDIA" || qs[1] != "NVIDIA site:docs.nvidia.com" {
		t.Fatalf("deterministic queries are not compact/authority-focused: %#v", qs)
	}
}

func TestDeterministicResearchQueryDoesNotSendFullGoalPromptToSearch(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Autonomy.UseLLM = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: s}
	g := &core.Goal{
		Title:       "FortiClient SSLVPN 7200",
		Description: "Erstelle einen deutschsprachigen Support-Wissensartikel zum FortiClient SSL-VPN Fehler 7200. Recherchiere Ursache, typische Auslöser, sichere Diagnose-Schritte und geeignete Lösungswege. Bevorzuge offizielle Fortinet-Dokumentation und technisch belastbare Quellen.",
		Target:      "1 hochwertiger Wissensartikel",
	}
	qs, _ := e.goalResearchQueries(context.Background(), g, 2, nil)
	if len(qs) != 2 {
		t.Fatalf("queries=%#v", qs)
	}
	if qs[0] != "FortiClient SSLVPN 7200" {
		t.Fatalf("first query must be exact compact title, got %q", qs[0])
	}
	for _, q := range qs {
		if strings.Contains(strings.ToLower(q), "wissensartikel") || strings.Contains(strings.ToLower(q), "hochwertiger") || len(q) > 100 {
			t.Fatalf("query contains goal-instruction noise: %q", q)
		}
	}
}

func TestResearchQueryUsefulRejectsMetaProcessInstructions(t *testing.T) {
	g := &core.Goal{Title: "NVIDIA", Description: "Neue RTX Grafikkarten"}
	if researchQueryUseful(g, "NVIDIA Review the strongest negative evidence and create a corrective task before the next cycle") {
		t.Fatal("meta scheduler text must not be accepted as a research query")
	}
	if !researchQueryUseful(g, "NVIDIA RTX Blackwell architecture specifications") {
		t.Fatal("subject-matter query should be accepted")
	}
}

func TestGoalProgressSurvivesTrimmedAuditFromDurableRelevantEvidence(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-old", Title: "NVIDIA", Target: "2 quellengebundene Wissenseinträge", ResearchEvidence: 100, ResearchSources: 12}
	for i := 0; i < 2; i++ {
		sid := fmt.Sprintf("src-%d", i)
		if err := s.UpsertSource(&core.KnowledgeSource{ID: sid, Type: "web", Title: "NVIDIA documentation", URI: "https://example.test/nvidia", Status: "ready"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMemory(&core.Memory{ID: fmt.Sprintf("mem-%d", i), Kind: "evidence", MemoryType: core.MemorySemantic, Text: "NVIDIA Blackwell architecture evidence", Vector: []float32{1, 0}, Status: core.MemoryActive, Tags: []string{"goal:" + g.ID}, Provenance: core.MemoryProvenance{Source: "web.page", SourceID: sid}}); err != nil {
			t.Fatal(err)
		}
	}
	e := &Engine{store: s}
	e.refreshGoalResearchProgress(g, .5)
	if g.Progress != 1 || g.ResearchEvidence != 2 || g.ResearchSources != 2 {
		t.Fatalf("durable relevant evidence not reconciled: %#v", g)
	}
}

func TestResearchProgressAndStagingRunWhenGoalSummaryLearningDisabled(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{
			"title": "Vendor evidence", "url": "https://example.com/vendor", "content": "A supported driver package resolves the documented device issue.", "engine": "test", "score": 0.9,
		}}})
	}))
	defer searx.Close()
	stagingCalls := 0
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stagingCalls++
		if r.Header.Get("Authorization") != "Bearer staging-token-123456789012345678901234" {
			t.Fatalf("bad staging auth")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"staging": map[string]any{"key": "KB-STAGING-GOAL", "meta": map[string]any{"integration_action": "created"}}})
	}))
	defer kb.Close()

	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0, 0}}, "prompt_eval_count": 1})
			return
		}
		http.NotFound(w, r)
	})
	cfg := s.Config()
	cfg.Research.Enabled = true
	cfg.Research.SearXNG.Enabled = true
	cfg.Research.SearXNG.BaseURL = searx.URL
	cfg.Research.Goal.Enabled = true
	cfg.Research.WebFetch.Enabled = false
	cfg.Brain.LearningPolicy.Enabled = true
	cfg.Brain.LearningPolicy.LearnGoalCycles = false
	cfg.Autonomy.UseLLM = false
	cfg.Brain.ExternalRelinkWorker = false
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "staging-token-123456789012345678901234", MinEvidence: 1, MinSources: 1, MaxEvidence: 4, SynthesisMode: "evidence"})
	goal := core.Goal{Title: "Driver research", Description: "collect sourced driver evidence", Target: "1 quellengebundener Wissenseintrag", Status: core.GoalActive, Priority: 80, ResearchEnabled: true}
	if err := s.UpsertGoal(&goal); err != nil {
		t.Fatal(err)
	}

	cycle, err := e.RunGoalCycle(context.Background(), goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.MemoryID != "" {
		t.Fatalf("goal-summary memory should be disabled, got %q", cycle.MemoryID)
	}
	updated, ok := s.GetGoal(goal.ID)
	if !ok {
		t.Fatal("goal missing")
	}
	if updated.ResearchEvidence < 1 || updated.Progress <= 0 {
		t.Fatalf("research progress not updated: %#v", updated)
	}
	if stagingCalls < 1 || updated.LastStagingDraftID != "KB-STAGING-GOAL" {
		t.Fatalf("staging not published: calls=%d goal=%#v", stagingCalls, updated)
	}
	if strings.Contains(strings.ToLower(updated.LastError), "learning policy") {
		t.Fatalf("legacy learning-policy error survived: %q", updated.LastError)
	}
}

func TestResearchMaterialRelevanceRejectsOffTopicWebRTCForNVIDIA(t *testing.T) {
	g := &core.Goal{Title: "NVIDIA", Description: "Sammle Informationen zu den neuen RTX Grafikkarten."}
	if researchMaterialRelevant(g, "Codecs used by WebRTC - MDN", "VP8 AVC codec browser media") {
		t.Fatal("off-topic MDN WebRTC evidence must not pass NVIDIA goal relevance")
	}
	if !researchMaterialRelevant(g, "NVIDIA GeForce RTX 5090", "Blackwell architecture and GPU documentation") {
		t.Fatal("NVIDIA evidence should pass goal relevance")
	}
}

func TestGoalArticleTargetUsesCurrentValidatedStagingDraft(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-articles", Title: "NVIDIA", Target: "20 hochwertige Wissensartikel", StagingDraftsCreated: 1, LastStagingDraftID: "KB-1", StagingDraftValidated: true, StagingQualityGateVersion: stagingQualityGateVersion}
	e := &Engine{store: s}
	e.refreshGoalResearchProgress(g, 0)
	if g.Progress < .049 || g.Progress > .051 || !strings.Contains(g.ProgressReason, "1/20 validierte Staging-Artikel") {
		t.Fatalf("article target must count only a current validated draft, got progress=%f reason=%q", g.Progress, g.ProgressReason)
	}
}

func TestResearchMaterialRelevanceRequiresExactErrorCodeAndSubjectAnchor(t *testing.T) {
	g := &core.Goal{Title: "FortiClient SSLVPN 7200"}

	for _, tc := range []struct {
		name     string
		parts    []string
		relevant bool
	}{
		{
			name:     "official fortinet title with exact code",
			parts:    []string{"Troubleshooting Tip: FortiClient error 'Credentials or SSLVPN configuration is wrong. (-7200)'", "FortiClient SSL VPN authentication troubleshooting"},
			relevant: true,
		},
		{
			name:     "split ssl vpn plus exact code",
			parts:    []string{"Technical Tip: Credential or SSL VPN configuration is wrong (-7200)", "SSL VPN authentication rule troubleshooting"},
			relevant: true,
		},
		{
			name:     "docker id only contains 7200 as substring",
			parts:    []string{"Docker image", "https://hub.docker.com/r/cffork8s/72007bf3-214d-4f19-a618-f74b145ca3a5", "generic container image"},
			relevant: false,
		},
		{
			name:     "generic sslvpn without error code",
			parts:    []string{"baselibrary/sslvpn", "VPN_TYPE=fortinet NET_ADMIN /dev/ppp"},
			relevant: false,
		},
		{
			name:     "exact code without product subject",
			parts:    []string{"Build 7200 released", "unrelated software package"},
			relevant: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := researchMaterialRelevant(g, tc.parts...); got != tc.relevant {
				t.Fatalf("researchMaterialRelevant=%v want %v for %#v", got, tc.relevant, tc.parts)
			}
		})
	}
}

func TestDecodeStagingSynthesisJSONAcceptsMarkdownFence(t *testing.T) {
	var got struct {
		Title, Text, Answer  string
		Categories, Keywords []string
	}
	raw := "```json\n{\"title\":\"DISM 0x800f081f\",\"text\":\"source backed\",\"answer\":\"Use a matching repair source after verifying the component store.\",\"categories\":[\"Windows\"],\"keywords\":[\"0x800f081f\"]}\n```"
	if err := decodeStagingSynthesisJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "DISM 0x800f081f" || len(got.Categories) != 1 {
		t.Fatalf("unexpected decode: %#v", got)
	}
}

func TestDecodeStagingSynthesisJSONRepairsFencedMembersWithoutOuterBraces(t *testing.T) {
	var got struct {
		Title, Text, Answer  string
		Categories, Keywords []string
	}
	raw := "```json\n\"title\":\"DISM 0x800f081f\",\n\"text\":\"source backed\",\n\"answer\":\"Use a matching repair source after verifying the component store.\",\n\"categories\":[\"Windows\"],\n\"keywords\":[\"0x800f081f\"]\n```"
	if err := decodeStagingSynthesisJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Answer == "" || got.Keywords[0] != "0x800f081f" {
		t.Fatalf("unexpected decode: %#v", got)
	}
}

func TestDecodeStagingSynthesisJSONRepairsInvalidBackslashesInStrings(t *testing.T) {
	var got stagingSynthesisContent
	raw := `{"title":"BitLocker Recovery","text":"Prüfen Sie C:\Windows\System32 und HKLM\SOFTWARE\Microsoft.","answer":"Öffnen Sie C:\Windows\System32 nur nach Prüfung der Recovery-Dokumentation.","categories":["Windows"],"keywords":["BitLocker"]}`
	if err := decodeStagingSynthesisJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, `C:\Windows\System32`) || !strings.Contains(got.Text, `HKLM\SOFTWARE\Microsoft`) {
		t.Fatalf("invalid backslashes were not preserved literally: %#v", got)
	}
}

func TestDecodeStagingSynthesisJSONRepairsBackslashBeforeMarkdownBacktick(t *testing.T) {
	var got stagingSynthesisContent
	raw := "{\"title\":\"BitLocker Recovery\",\"text\":\"Nutzen Sie \\`manage-bde\\` nur nach Prüfung.\",\"answer\":\"Prüfen Sie zuerst die Microsoft-Dokumentation zum Recovery-Schlüssel.\",\"categories\":[\"Windows\"],\"keywords\":[\"BitLocker\"]}"
	if err := decodeStagingSynthesisJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "\\`manage-bde\\`") {
		t.Fatalf("literal escaped Markdown marker was not preserved: %q", got.Text)
	}
}

func TestDecodeStagingSynthesisJSONRejectsSurroundingProse(t *testing.T) {
	var got stagingSynthesisContent
	raw := `Here is the JSON: {"title":"BitLocker","text":"source backed","answer":"A sufficiently long source-backed BitLocker recovery answer for review.","categories":["Windows"],"keywords":["BitLocker"]}`
	if err := decodeStagingSynthesisJSON(raw, &got); err == nil {
		t.Fatal("expected surrounding prose to fail strict structured-output decoding")
	}
}

func TestDecodeStagingSynthesisJSONRejectsUnknownFields(t *testing.T) {
	var got stagingSynthesisContent
	raw := `{"title":"BitLocker","text":"source backed","answer":"A sufficiently long source-backed BitLocker recovery answer for review.","categories":["Windows"],"keywords":["BitLocker"],"auto_reply":true}`
	if err := decodeStagingSynthesisJSON(raw, &got); err == nil {
		t.Fatal("expected strict schema rejection for unknown auto_reply field")
	}
}

func TestStagingSynthesisRetriesMalformedStructuredOutputOnce(t *testing.T) {
	chatCalls := 0
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		chatCalls++
		content := "```json\ntitle: DISM 0x800f081f\nanswer: malformed\n```"
		if chatCalls == 2 {
			content = `{"title":"Windows 11 DISM Fehler 0x800f081f","text":"Der Fehler 0x800f081f kann bei DISM auftreten. Die Reparaturquelle muss zur installierten Windows-Version passen.","answer":"Prüfen Sie zuerst die Windows-Version und verwenden Sie anschließend eine passende Reparaturquelle für DISM 0x800f081f.","categories":["Windows","DISM"],"keywords":["Windows 11","DISM","0x800f081f"]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}, "prompt_eval_count": 2, "eval_count": 2})
	})
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, SynthesisMode: "llm", MinArticleChars: 1, TargetArticleChars: 1, MaxArticleChars: 10000, MinAnswerChars: 1, MaxAnswerChars: 10000})
	cfg := s.Config()
	cfg.Autonomy.Provider = "ollama"
	cfg.Autonomy.Model = cfg.Ollama[0].ChatModel
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	goal := &core.Goal{ID: "goal-dism", Title: "Windows 11 DISM Fehler 0x800f081f", Description: "Support-Wissensartikel zu DISM 0x800f081f"}
	evidence := []draftEvidence{{Memory: core.Memory{Text: "Windows 11 DISM reports error 0x800f081f when required repair content cannot be found.", Confidence: .8, Provenance: core.MemoryProvenance{Source: "web.page"}}, Source: &core.KnowledgeSource{Title: "Microsoft DISM documentation", URI: "https://learn.microsoft.com/windows-hardware/manufacture/desktop/repair-a-windows-image"}}}
	got, err := e.synthesizeGoalDraft(context.Background(), goal, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if chatCalls != 2 {
		t.Fatalf("chat calls=%d want 2", chatCalls)
	}
	if !strings.Contains(got.Title, "0x800f081f") || got.Answer == "" {
		t.Fatalf("unexpected draft: %#v", got)
	}
}

func TestStagingArticleDepthExpandsShortDraftAndUsesConfiguredBudget(t *testing.T) {
	chatCalls := 0
	var seenNumPredict []float64
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		chatCalls++
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["format"] != "json" {
			t.Fatalf("structured staging call did not request JSON mode: %#v", req["format"])
		}
		if opts, _ := req["options"].(map[string]any); opts != nil {
			if n, ok := opts["num_predict"].(float64); ok {
				seenNumPredict = append(seenNumPredict, n)
			}
		}
		text := "Kurzer FortiClient SSLVPN Fehler 7200 Entwurf."
		answer := "Prüfen Sie die FortiClient- und FortiGate-Konfiguration für den Fehler 7200."
		if chatCalls == 2 {
			text = strings.Repeat("FortiClient SSLVPN Fehler 7200 wird anhand der bereitgestellten Fortinet-Evidence diagnostiziert. Die beschriebenen Prüfungen bleiben auf quellenbelegte Konfiguration, Authentifizierung und Systemzustand begrenzt. ", 8)
			answer = "Prüfen Sie beim FortiClient SSLVPN Fehler 7200 zunächst die quellenbelegten Authentifizierungs- und SSL-VPN-Einstellungen, anschließend den FortiGate-Systemzustand und dokumentieren Sie die Diagnoseergebnisse für die weitere Eingrenzung."
		}
		content, _ := json.Marshal(map[string]any{
			"title":      "FortiClient SSLVPN Fehler 7200",
			"text":       text,
			"answer":     answer,
			"categories": []string{"VPN"},
			"keywords":   []string{"FortiClient", "SSLVPN", "7200"},
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": string(content)}, "prompt_eval_count": 10, "eval_count": 11})
	})
	cfg := s.Config()
	cfg.Autonomy.Provider = "ollama"
	cfg.Autonomy.Model = cfg.Ollama[0].ChatModel
	cfg.Ollama[0].NumPredict = 0
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.ConfigureStagingPublisher(StagingPublisherConfig{
		Enabled: true, SynthesisMode: "llm", VerifyClaims: false,
		SynthesisMaxOutputTokens: 2300, EvidencePromptMaxChars: 2000,
		MinArticleChars: 500, TargetArticleChars: 900, MaxArticleChars: 3000,
		MinAnswerChars: 80, MaxAnswerChars: 600,
	})
	goal := &core.Goal{ID: "goal-forti", Title: "FortiClient SSLVPN 7200", Description: "Supportartikel zum Fehler 7200"}
	evidence := []draftEvidence{{
		Memory: core.Memory{ID: "m1", Text: strings.Repeat("FortiClient SSLVPN error 7200 evidence from Fortinet. ", 80), Confidence: .9, Provenance: core.MemoryProvenance{Source: "web.page", SourceID: "f1"}},
		Source: &core.KnowledgeSource{ID: "f1", Title: "Fortinet Technical Tip 7200", URI: "https://community.fortinet.com/fortigate/7200", Trust: .9},
	}}
	got, err := e.synthesizeGoalDraft(context.Background(), goal, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if chatCalls != 2 {
		t.Fatalf("chat calls=%d want 2 (synthesis + depth expansion)", chatCalls)
	}
	for _, n := range seenNumPredict {
		if n != 2300 {
			t.Fatalf("num_predict=%v want 2300", n)
		}
	}
	if got.Quality == nil || got.Quality.Article == nil || !got.Quality.Article.ExpansionApplied {
		t.Fatalf("missing article-depth audit: %#v", got.Quality)
	}
	if got.Quality.Article.TextChars < 500 || got.Quality.Article.AnswerChars < 80 {
		t.Fatalf("article bounds not enforced: %#v", got.Quality.Article)
	}
	if got.Quality.Article.EvidencePromptChars > 2600 {
		t.Fatalf("evidence prompt budget unexpectedly large: %#v", got.Quality.Article)
	}
}

func TestPromptEvidenceTextsDistributesContextBudgetAcrossEvidence(t *testing.T) {
	evidence := make([]draftEvidence, 4)
	for i := range evidence {
		evidence[i].Memory.Text = strings.Repeat(fmt.Sprintf("E%d evidence ", i+1), 300)
	}
	cfg := StagingPublisherConfig{EvidencePromptMaxChars: 1200}
	texts := promptEvidenceTexts(cfg, evidence)
	if len(texts) != 4 {
		t.Fatalf("texts=%d want 4", len(texts))
	}
	total := 0
	for i, text := range texts {
		n := len([]rune(text))
		total += n
		if n == 0 {
			t.Fatalf("evidence %d lost all prompt context", i+1)
		}
	}
	if total > 1200 {
		t.Fatalf("prompt evidence chars=%d want <=1200", total)
	}
}

func TestSourceAuthorityTreatsPrimaryDocsAndVendorCommunityDifferently(t *testing.T) {
	cfg := StagingPublisherConfig{}
	primary := sourceAuthorityFor(cfg, &core.KnowledgeSource{URI: "https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/repair-a-windows-image"})
	if !primary.Authoritative || primary.AuthorityScore < .9 {
		t.Fatalf("primary Microsoft docs should be authoritative: %#v", primary)
	}
	qna := sourceAuthorityFor(cfg, &core.KnowledgeSource{URI: "https://learn.microsoft.com/de-de/answers/questions/123/dism"})
	if qna.Authoritative || qna.Authority != "vendor-community" {
		t.Fatalf("Microsoft Q&A must not count as primary documentation: %#v", qna)
	}
	fortinet := sourceAuthorityFor(cfg, &core.KnowledgeSource{URI: "https://community.fortinet.com/fortigate-3/technical-tip-credential-or-ssl-vpn-configuration-is-wrong-7200-219912"})
	if !fortinet.Authoritative {
		t.Fatalf("first-party Fortinet technical-tip content should count as authoritative: %#v", fortinet)
	}
	fortinetForum := sourceAuthorityFor(cfg, &core.KnowledgeSource{URI: "https://community.fortinet.com/support-forum-92/solved-credential-or-ssl-vpn-configuration-is-wrong-7200-7654"})
	if fortinetForum.Authoritative || fortinetForum.Authority != "vendor-community" {
		t.Fatalf("Fortinet support-forum content must not count as authoritative: %#v", fortinetForum)
	}
}

func TestCollectGoalDraftEvidencePrefersAuthoritativeSource(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-dism", Title: "Windows 11 DISM Fehler 0x800f081f"}
	sources := []*core.KnowledgeSource{
		{ID: "blog", Type: "web", Title: "Blog 0x800f081f DISM Windows 11", URI: "https://example.test/dism-0x800f081f", Status: "ready", Trust: .85},
		{ID: "ms", Type: "web", Title: "Microsoft DISM 0x800f081f Windows 11", URI: "https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/repair-a-windows-image", Status: "ready", Trust: .85},
	}
	for i, src := range sources {
		if err := s.UpsertSource(src); err != nil {
			t.Fatal(err)
		}
		m := &core.Memory{ID: fmt.Sprintf("m%d", i), Kind: "evidence", MemoryType: core.MemorySemantic, Text: "Windows 11 DISM error 0x800f081f repair source evidence.", Confidence: .8, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", GoalID: g.ID, SourceID: src.ID}}
		if err := s.AddMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	e := &Engine{store: s}
	got := e.collectGoalDraftEvidence(g, StagingPublisherConfig{MaxEvidence: 1})
	if len(got) != 1 || got[0].Source == nil || got[0].Source.ID != "ms" {
		t.Fatalf("authoritative source was not preferred: %#v", got)
	}
}

func TestStagingAuthorityGateBlocksBlogOnlyDraft(t *testing.T) {
	calls := 0
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"staging": map[string]any{"key": "never", "meta": map[string]any{"integration_action": "created"}}})
	}))
	defer kb.Close()
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := &core.KnowledgeSource{ID: "blog", Type: "web", Title: "DISM 0x800f081f Windows 11 blog", URI: "https://example.test/windows-dism-0x800f081f", Status: "ready", Trust: .85}
	if err := s.UpsertSource(src); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "m1", Kind: "evidence", MemoryType: core.MemorySemantic, Text: "Windows 11 DISM error 0x800f081f evidence.", Confidence: .8, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", GoalID: "g1", SourceID: src.ID}}
	if err := s.AddMemory(m); err != nil {
		t.Fatal(err)
	}
	run, _ := s.StartResearchRun("g1", "Windows 11 DISM Fehler 0x800f081f")
	_, _ = s.AddResearchEvent(run.ID, core.ResearchEvent{Type: "evidence.learned", SourceID: src.ID, MemoryID: m.ID})
	e := &Engine{store: s, http: kb.Client()}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "secret", MinEvidence: 1, MinSources: 1, MaxEvidence: 4, SynthesisMode: "evidence", RequireAuthoritativeSource: true, MinAuthoritativeSources: 1})
	g := &core.Goal{ID: "g1", Title: "Windows 11 DISM Fehler 0x800f081f", ResearchEvidence: 1, ResearchSources: 1}
	e.maybePublishGoalDraft(context.Background(), g, ResearchResult{RunID: run.ID})
	if calls != 0 || !strings.Contains(g.LastStagingError, "source authority") {
		t.Fatalf("blog-only draft must fail closed: calls=%d error=%q", calls, g.LastStagingError)
	}
}

func TestCriticalIdentifierGuardRejectsInventedVersion(t *testing.T) {
	draft := stagingDraftPayload{Title: "DISM 0x800f081f", Text: "Unter Windows v99.9 tritt der Fehler 0x800f081f auf.", Answer: "Prüfen Sie DISM bei Fehler 0x800f081f und verwenden Sie /RestoreHealth."}
	evidence := []draftEvidence{{Memory: core.Memory{Text: "DISM error 0x800f081f can be repaired with /RestoreHealth."}, Source: &core.KnowledgeSource{Title: "Microsoft", URI: "https://learn.microsoft.com/doc"}}}
	if err := validateDraftCriticalIdentifiers(draft, evidence); err == nil || !strings.Contains(err.Error(), "v99.9") {
		t.Fatalf("invented version must be rejected, got %v", err)
	}
}

func TestCriticalIdentifierGuardIgnoresSlashCompoundsAndURLPaths(t *testing.T) {
	draft := stagingDraftPayload{
		Title:  "BitLocker Wiederherstellung",
		Text:   "Nach einer TPM-, BIOS-/UEFI- oder Hardwareänderung kann die Wiederherstellung erforderlich sein. Prüfen Sie die Web-/Portal-Konfiguration und dokumentieren Sie Interaktionsbereiche/-Tags.",
		Answer: "Öffnen Sie die Herstellerdokumentation unter https://example.test/docs/portal-konfiguration/uefi-recovery.",
	}
	evidence := []draftEvidence{{Memory: core.Memory{Text: "BitLocker recovery can be triggered after TPM, BIOS, UEFI, or hardware changes."}, Source: &core.KnowledgeSource{Title: "Microsoft", URI: "https://learn.microsoft.com/windows/security/operating-system-security/data-protection/bitlocker/recovery-overview"}}}
	if err := validateDraftCriticalIdentifiers(draft, evidence); err != nil {
		t.Fatalf("slash compounds and URL paths must not be treated as CLI identifiers: %v", err)
	}
}

func TestCriticalIdentifierGuardRejectsInventedSlashSwitchInCode(t *testing.T) {
	draft := stagingDraftPayload{
		Title:  "DISM Reparatur",
		Text:   "Verwenden Sie nur dokumentierte Reparaturoptionen.",
		Answer: "Führen Sie `DISM /Online /Cleanup-Image /MagicRepair` aus.",
	}
	evidence := []draftEvidence{{Memory: core.Memory{Text: "Run DISM /Online /Cleanup-Image /RestoreHealth to repair the image."}, Source: &core.KnowledgeSource{Title: "Microsoft", URI: "https://learn.microsoft.com/windows-hardware/manufacture/desktop/repair-a-windows-image"}}}
	err := validateDraftCriticalIdentifiers(draft, evidence)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "/magicrepair") {
		t.Fatalf("invented slash switch in code must be rejected, got %v", err)
	}
}

func TestCriticalIdentifierGuardAcceptsSourcedSlashSwitchInCode(t *testing.T) {
	draft := stagingDraftPayload{
		Title:  "DISM Reparatur",
		Text:   "Verwenden Sie nur dokumentierte Reparaturoptionen.",
		Answer: "Führen Sie `DISM /Online /Cleanup-Image /RestoreHealth` aus.",
	}
	evidence := []draftEvidence{{Memory: core.Memory{Text: "Run DISM /Online /Cleanup-Image /RestoreHealth to repair the image."}, Source: &core.KnowledgeSource{Title: "Microsoft", URI: "https://learn.microsoft.com/windows-hardware/manufacture/desktop/repair-a-windows-image"}}}
	if err := validateDraftCriticalIdentifiers(draft, evidence); err != nil {
		t.Fatalf("sourced slash switches in code must pass: %v", err)
	}
}

func TestClaimVerificationRepairsUnsupportedDISMOrder(t *testing.T) {
	chatCalls := 0
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		chatCalls++
		var content string
		switch chatCalls {
		case 1:
			content = `{"title":"Windows 11 DISM Fehler 0x800f081f","text":"Der Fehler 0x800f081f betrifft die Windows-Reparaturquelle.","answer":"Führen Sie zuerst sfc /scannow und anschließend DISM /Online /Cleanup-Image /RestoreHealth aus.","categories":["Windows"],"keywords":["DISM","0x800f081f"]}`
		case 2:
			content = `{"verdict":"fail","statements":[{"id":"S1","status":"unsupported","evidence_ids":["E1"],"reason":"The evidence specifies DISM before SFC."},{"id":"S2","status":"supported","evidence_ids":["E1"],"reason":"The error/source statement is supported."}],"contradictions":[]}`
		case 3:
			content = `{"title":"Windows 11 DISM Fehler 0x800f081f","text":"Der Fehler 0x800f081f betrifft die Windows-Reparaturquelle.","answer":"Führen Sie zuerst DISM /Online /Cleanup-Image /RestoreHealth und anschließend sfc /scannow aus.","categories":["Windows"],"keywords":["DISM","0x800f081f"]}`
		case 4:
			content = `{"verdict":"pass","statements":[{"id":"S1","status":"supported","evidence_ids":["E1"],"reason":"Authoritative evidence specifies this order."},{"id":"S2","status":"supported","evidence_ids":["E1"],"reason":"Supported."}],"contradictions":[]}`
		default:
			t.Fatalf("unexpected chat call %d", chatCalls)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}, "prompt_eval_count": 2, "eval_count": 2})
	})
	cfg := s.Config()
	cfg.Autonomy.Provider = "ollama"
	cfg.Autonomy.Model = cfg.Ollama[0].ChatModel
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, SynthesisMode: "llm", VerifyClaims: true, MinClaimCoverage: 1, RequireAuthoritativeActions: true, VerificationRepair: true, MinArticleChars: 1, TargetArticleChars: 1, MaxArticleChars: 10000, MinAnswerChars: 1, MaxAnswerChars: 10000})
	goal := &core.Goal{ID: "goal-dism", Title: "Windows 11 DISM Fehler 0x800f081f", Description: "Reparaturreihenfolge fuer DISM Fehler 0x800f081f"}
	evidence := []draftEvidence{{
		Memory: core.Memory{ID: "m1", Text: "For Windows error 0x800f081f, run DISM /Online /Cleanup-Image /RestoreHealth first. After DISM completes, run sfc /scannow.", Confidence: .9, Provenance: core.MemoryProvenance{Source: "web.page", SourceID: "ms"}},
		Source: &core.KnowledgeSource{ID: "ms", Title: "Microsoft system repair documentation", URI: "https://support.microsoft.com/windows/system-file-checker", Trust: .9},
	}}
	got, err := e.synthesizeGoalDraft(context.Background(), goal, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if chatCalls != 4 {
		t.Fatalf("chat calls=%d want 4", chatCalls)
	}
	if !strings.Contains(got.Answer, "zuerst DISM") || got.Quality == nil || got.Quality.Verification == nil || !got.Quality.Verification.RepairApplied {
		t.Fatalf("draft was not grounded/reverified: %#v", got)
	}
}

func TestClaimVerificationBatchesAllLongArticleStatements(t *testing.T) {
	chatCalls := 0
	s, e := policyTestEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		chatCalls++
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Messages) == 0 {
			t.Fatal("missing messages")
		}
		input := req.Messages[len(req.Messages)-1].Content
		var statements []map[string]any
		inStatements := false
		for _, line := range strings.Split(input, "\n") {
			if strings.HasPrefix(line, "DRAFT STATEMENTS") {
				inStatements = true
				continue
			}
			if strings.HasPrefix(line, "SOURCE EVIDENCE:") {
				break
			}
			if !inStatements || !strings.HasPrefix(line, "S") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			id := fields[0]
			statements = append(statements, map[string]any{"id": id, "status": "supported", "evidence_ids": []string{"E1"}, "reason": "supported by authoritative evidence"})
		}
		content, _ := json.Marshal(map[string]any{"verdict": "pass", "statements": statements, "contradictions": []string{}})
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": string(content)}, "prompt_eval_count": 2, "eval_count": 2})
	})
	cfg := s.Config()
	cfg.Autonomy.Provider = "ollama"
	cfg.Autonomy.Model = cfg.Ollama[0].ChatModel
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.ConfigureStagingPublisher(StagingPublisherConfig{MaxVerificationStatements: 16, MinClaimCoverage: 1, RequireAuthoritativeActions: true, EvidencePromptMaxChars: 4000})
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("Diagnosehinweis %02d zum FortiClient SSLVPN Fehler 7200 ist durch die bereitgestellte Fortinet-Evidence belegt.", i+1))
	}
	draft := stagingDraftPayload{
		Title:  "FortiClient SSLVPN 7200",
		Answer: "Prüfen Sie den Fehler 7200 anhand der dokumentierten Fortinet-Diagnoseschritte und validieren Sie die Konfiguration vor Änderungen.",
		Text:   strings.Join(lines, "\n"),
	}
	evidence := []draftEvidence{{
		Memory: core.Memory{ID: "m1", Text: strings.Repeat("FortiClient SSLVPN Fehler 7200 Diagnose und Konfiguration. ", 100), Confidence: .9, Provenance: core.MemoryProvenance{SourceID: "f1"}},
		Source: &core.KnowledgeSource{ID: "f1", URI: "https://community.fortinet.com/fortigate-3/technical-tip-credential-or-ssl-vpn-configuration-is-wrong-7200-219912", Trust: .9},
	}}
	report, err := e.verifyDraftClaims(context.Background(), &core.Goal{Title: "FortiClient SSLVPN 7200"}, evidence, draft)
	if err != nil {
		t.Fatal(err)
	}
	if chatCalls != 3 {
		t.Fatalf("verification calls=%d want 3 batches", chatCalls)
	}
	if len(report.Statements) != 41 || report.Coverage != 1 {
		t.Fatalf("incomplete batched verification: statements=%d coverage=%v", len(report.Statements), report.Coverage)
	}
}

func TestIndependentCorroborationsDoNotCountSameVendorOriginTwice(t *testing.T) {
	sources := map[string]*core.KnowledgeSource{
		"primary": {ID: "primary", URI: "https://learn.microsoft.com/doc/a"},
		"same":    {ID: "same", URI: "https://support.microsoft.com/doc/b"},
		"other":   {ID: "other", URI: "https://example.org/independent"},
	}
	evidence := []draftEvidence{{Memory: core.Memory{ID: "m1", Provenance: core.MemoryProvenance{SourceID: "primary"}, EvidenceSourceIDs: []string{"primary", "same", "other"}}, Source: sources["primary"]}}
	got := countDraftIndependentCorroborations(evidence, func(id string) (*core.KnowledgeSource, bool) { x, ok := sources[id]; return x, ok })
	if got != 1 {
		t.Fatalf("corroborations=%d want 1 independent origin", got)
	}
}

func TestAuthoritativeDomainConfigurationRejectsOverbroadValues(t *testing.T) {
	e := &Engine{}
	e.ConfigureStagingPublisher(StagingPublisherConfig{AuthoritativeDomains: []string{"com", "https://evil.example", "*.docs.example.com", "support.example.org"}})
	cfg := e.stagingConfig()
	if len(cfg.AuthoritativeDomains) != 2 || cfg.AuthoritativeDomains[0] != "docs.example.com" || cfg.AuthoritativeDomains[1] != "support.example.org" {
		t.Fatalf("unsafe authority domains were not sanitized: %#v", cfg.AuthoritativeDomains)
	}
}

func TestStagingRetriesOldQualityFailureOnceAfterGateUpgrade(t *testing.T) {
	requests := 0
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"staging": map[string]any{"key": "KB-AI-STAGING-OLD", "meta": map[string]any{"integration_action": "updated"}}})
	}))
	defer kb.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := &core.KnowledgeSource{ID: "src-1", Type: "web", Title: "Fortinet 7200", URI: "https://community.fortinet.com/fortigate/7200", Trust: .9, Status: "ready"}
	if err := s.UpsertSource(src); err != nil {
		t.Fatal(err)
	}
	mem := &core.Memory{ID: "mem-1", Kind: "evidence", MemoryType: core.MemorySemantic, Text: "FortiClient SSLVPN error 7200 troubleshooting evidence.", Confidence: .8, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", GoalID: "goal-1", SourceID: src.ID}}
	if err := s.AddMemory(mem); err != nil {
		t.Fatal(err)
	}
	run, err := s.StartResearchRun("goal-1", "FortiClient SSLVPN 7200")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.FinishResearchRun(run.ID, "completed", "")

	e := &Engine{store: s, http: kb.Client()}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "secret", MinEvidence: 1, MinSources: 1, SynthesisMode: "evidence"})
	g := &core.Goal{
		ID: "goal-1", Title: "FortiClient SSLVPN 7200", ResearchEvidence: 1, ResearchSources: 1,
		LastStagingDraftID: "KB-AI-STAGING-OLD",
		LastStagingError:   "staging synthesis introduced source-unverified identifiers: /portal-konfiguration",
	}

	e.maybePublishGoalDraft(context.Background(), g, ResearchResult{RunID: run.ID})
	if requests != 1 || g.LastStagingError != "" || !g.StagingDraftValidated || g.StagingQualityGateVersion != stagingQualityGateVersion {
		t.Fatalf("old quality failure was not revalidated: requests=%d goal=%#v", requests, g)
	}

	// The same unchanged, already validated evidence must not be synthesized again.
	e.maybePublishGoalDraft(context.Background(), g, ResearchResult{RunID: run.ID})
	if requests != 1 {
		t.Fatalf("validated unchanged draft was republished: requests=%d", requests)
	}
}

func TestArticleProgressRequiresCurrentValidatedDraft(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := &Engine{store: s}
	g := &core.Goal{ID: "g1", Title: "Test", Target: "1 hochwertiger Wissensartikel", LastStagingDraftID: "KB-OLD", StagingDraftsCreated: 1}

	e.refreshGoalResearchProgress(g, 0)
	if g.Progress != 0 || !strings.Contains(g.ProgressReason, "0/1 validierte Staging-Artikel") {
		t.Fatalf("unvalidated legacy draft must not satisfy article target: %#v", g)
	}

	g.StagingDraftValidated = true
	g.StagingQualityGateVersion = stagingQualityGateVersion
	e.refreshGoalResearchProgress(g, 0)
	if g.Progress != 1 || !strings.Contains(g.ProgressReason, "1/1 validierte Staging-Artikel") {
		t.Fatalf("current validated draft should satisfy target: %#v", g)
	}
}

func TestStagingBelowThresholdInvalidatesLegacyDraftAndReplacesStaleError(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := &Engine{store: s}
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: "http://knowledge.invalid", Token: "secret", MinEvidence: 4, MinSources: 2})
	g := &core.Goal{
		ID: "g1", LastStagingDraftID: "KB-OLD", StagingDraftValidated: true,
		StagingQualityGateVersion: "staging-v2",
		LastStagingError:          "staging synthesis introduced source-unverified identifiers: /portal-konfiguration",
	}
	e.maybePublishGoalDraft(context.Background(), g, ResearchResult{})
	if g.StagingDraftValidated || g.StagingQualityGateVersion != stagingQualityGateVersion || !strings.Contains(g.LastStagingError, "quality gate not satisfied") {
		t.Fatalf("legacy draft state was not invalidated/reconciled: %#v", g)
	}
}

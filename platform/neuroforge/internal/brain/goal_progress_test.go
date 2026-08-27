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
	if qs[0] != "NVIDIA" || !strings.Contains(strings.ToLower(qs[1]), "rtx") {
		t.Fatalf("deterministic queries are not compact/topic-focused: %#v", qs)
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

func TestGoalArticleTargetUsesCreatedStagingArticles(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-articles", Title: "NVIDIA", Target: "20 hochwertige Wissensartikel", StagingDraftsCreated: 1}
	e := &Engine{store: s}
	e.refreshGoalResearchProgress(g, 0)
	if g.Progress < .049 || g.Progress > .051 || !strings.Contains(g.ProgressReason, "1/20 Staging-Artikel") {
		t.Fatalf("article target must count articles, got progress=%f reason=%q", g.Progress, g.ProgressReason)
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
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, SynthesisMode: "llm"})
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

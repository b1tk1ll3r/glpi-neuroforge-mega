package brain

import (
	"context"
	"encoding/json"
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
		run, err := s.StartResearchRun(g.ID, g.Title)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			_, _ = s.AddResearchEvent(run.ID, core.ResearchEvent{Type: "evidence.learned", SourceID: string(rune('a' + r)), MemoryID: "m"})
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"staging": map[string]any{"key": "KB-AI-STAGING-1", "meta": map[string]any{"integration_action": "created"}}})
	}))
	defer kb.Close()

	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := &core.KnowledgeSource{ID: "src-1", Type: "web", Title: "Vendor", URI: "https://example.test/doc", Trust: .8, Status: "ready"}
	if err := s.UpsertSource(src); err != nil {
		t.Fatal(err)
	}
	mem := &core.Memory{ID: "mem-1", Kind: "evidence", MemoryType: core.MemorySemantic, Text: "RTX driver installation requires a supported operating system and current vendor package.", Vector: []float32{1, 0}, Confidence: .7, Status: core.MemoryActive, Provenance: core.MemoryProvenance{Source: "web.page", SourceID: src.ID}}
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
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "secret", MinEvidence: 1, MinSources: 1})
	g := &core.Goal{ID: "goal-1", Title: "NVIDIA", ResearchEvidence: 1, ResearchSources: 1, ResearchSourceIDs: []string{src.ID}}
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
	if len(qs) != 1 {
		t.Fatalf("queries=%#v", qs)
	}
	q := qs[0]
	if !strings.Contains(strings.ToLower(q), "nvidia") || strings.Contains(strings.ToLower(q), "negative evidence") || strings.Contains(strings.ToLower(q), "next cycle") {
		t.Fatalf("bad research query: %q", q)
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

func TestGoalProgressDoesNotRegressWhenResearchAuditRunsAreTrimmed(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g := &core.Goal{ID: "goal-old", Target: "100 quellengebundene Wissenseinträge", ResearchEvidence: 100, ResearchSources: 12, ResearchCorroborations: 4}
	e := &Engine{store: s}
	e.refreshGoalResearchProgress(g, .5)
	if g.Progress != 1 {
		t.Fatalf("progress regressed despite persistent cumulative counters: %f", g.Progress)
	}
	if g.ResearchEvidence != 100 || g.ResearchSources != 12 {
		t.Fatalf("counters regressed: %#v", g)
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
	e.ConfigureStagingPublisher(StagingPublisherConfig{Enabled: true, URL: kb.URL, Token: "staging-token-123456789012345678901234", MinEvidence: 1, MinSources: 1, MaxEvidence: 4})
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

package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	r := model.RunRecord{TicketID: 7, SourceVersion: "v1", FinishedAt: time.Now(), Outcome: "processed"}
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	if !s.Seen(7, "v1") {
		t.Fatal("not seen")
	}
	s2, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Seen(7, "v1") {
		t.Fatal("not persisted")
	}
}

func TestErrorRunIsRetriedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	r := model.RunRecord{TicketID: 8, SourceVersion: "v1", FinishedAt: time.Now(), Outcome: "error", Error: "temporary outage"}
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	if s.Seen(8, "v1") {
		t.Fatal("failed run must remain retryable")
	}
	s2, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Seen(8, "v1") {
		t.Fatal("failed run became seen after restart")
	}
}

func TestAnalysisIndexAndEscalationIdempotencySurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	a := model.AnalysisRun{
		AnalysisID:   "analysis-1",
		ParentRunID:  "run-1",
		TicketID:     17,
		AnalysisType: "escalation",
		Action: model.ActionAudit{
			Type:     "raise_priority",
			Executed: true,
			Result:   "ticket=17;level=2; priority_written",
		},
	}
	r := model.RunRecord{RunID: "run-1", TicketID: 17, Trigger: "scheduled_escalation", SourceVersion: "v1", FinishedAt: time.Now(), Outcome: "processed", Analyses: []model.AnalysisRun{a}}
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	if s.Seen(17, "v1") {
		t.Fatal("scheduled escalation must not consume the normal ticket version")
	}
	got, ok := s.FindAnalysis("analysis-1")
	if !ok || got.ParentRunID != "run-1" {
		t.Fatalf("analysis index result=%+v ok=%v", got, ok)
	}
	if !s.HasEscalationKey("ticket=17;level=2") {
		t.Fatal("executed escalation key not found")
	}

	s2, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.FindAnalysis("analysis-1"); !ok {
		t.Fatal("analysis index was not rebuilt after restart")
	}
	if !s2.HasEscalationKey("ticket=17;level=2") {
		t.Fatal("escalation idempotency was not rebuilt after restart")
	}
}

func TestLatestTicketRunCanExcludeScheduledRuns(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for _, r := range []model.RunRecord{
		{RunID: "normal", TicketID: 5, Trigger: "poll", SourceVersion: "v1", FinishedAt: base, Outcome: "processed"},
		{RunID: "scheduled", TicketID: 5, Trigger: "scheduled_escalation", SourceVersion: "v1", FinishedAt: base.Add(time.Second), Outcome: "processed"},
	} {
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := s.LatestTicketRun(5, "scheduled_escalation")
	if !ok || got.RunID != "normal" {
		t.Fatalf("latest non-scheduled run=%+v ok=%v", got, ok)
	}
}

func TestMultiActionEscalationKeysSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	a := model.AnalysisRun{
		AnalysisID:   "analysis-plan",
		ParentRunID:  "run-plan",
		TicketID:     23,
		AnalysisType: "escalation",
		Action: model.ActionAudit{
			Type: "escalation_plan",
			Steps: []model.ActionStepAudit{
				{Step: "assign_second_level", Executed: true, Result: "ticket=23;level=2;action=assign_second_level;target=group:42; assigned"},
				{Step: "notify_service_owner", Executed: true, Result: "ticket=23;level=2;action=notify_service_owner;target=user:9; notified"},
				{Step: "request_manager_review", Executed: false, Result: "ticket=23;level=2;action=request_manager_review;target=group:77; failed"},
			},
		},
	}
	r := model.RunRecord{RunID: "run-plan", TicketID: 23, Trigger: "scheduled_escalation", FinishedAt: time.Now(), Outcome: "processed", Analyses: []model.AnalysisRun{a}}
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"ticket=23;level=2;action=assign_second_level;target=group:42",
		"ticket=23;level=2;action=notify_service_owner;target=user:9",
	} {
		if !s.HasEscalationKey(key) {
			t.Fatalf("executed action key %q not found", key)
		}
	}
	if s.HasEscalationKey("ticket=23;level=2;action=request_manager_review;target=group:77") {
		t.Fatal("failed action must not become idempotent")
	}

	s2, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.HasEscalationKey("ticket=23;level=2;action=assign_second_level;target=group:42") || !s2.HasEscalationKey("ticket=23;level=2;action=notify_service_owner;target=user:9") {
		t.Fatal("multi-action keys were not persisted across restart")
	}
}

func TestEscalationKeyFromResultKeepsActionAndTarget(t *testing.T) {
	got := escalationKeyFromResult("ticket=4; level=3; action=link_major_incident; target=incident:99; linked")
	want := "ticket=4;level=3;action=link_major_incident;target=incident:99"
	if got != want {
		t.Fatalf("key=%q want %q", got, want)
	}
}

func TestLoadToleratesOversizedAndCorruptLines(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for _, r := range []model.RunRecord{
		{RunID: "a", TicketID: 1, SourceVersion: "v1", Outcome: "processed", FinishedAt: time.Unix(1, 0)},
		{RunID: "b", TicketID: 2, SourceVersion: "v1", Outcome: "processed", FinishedAt: time.Unix(2, 0), Error: strings.Repeat("x", 3*1024*1024)},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	lines = append(lines, "{not json", `{"run_id":"c","ticket_id":3,"source_version":"v1","outcome":"processed"}`)
	if err := os.WriteFile(filepath.Join(dir, "runs.jsonl"), []byte(strings.Join(lines, "\n")), 0o640); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, 10)
	if err != nil {
		t.Fatalf("open with oversized line: %v", err)
	}
	if got := len(s.Recent(10)); got != 3 {
		t.Fatalf("runs=%d want 3", got)
	}
	if _, ok := s.FindRun("b"); !ok {
		t.Fatal("oversized record not loaded")
	}
	if !s.Seen(3, "v1") {
		t.Fatal("unterminated last record not loaded")
	}
}

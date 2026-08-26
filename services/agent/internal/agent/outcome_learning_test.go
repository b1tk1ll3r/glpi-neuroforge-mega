package agent

import (
	"context"
	"testing"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/state"
)

type fakeOutcomeSink struct {
	got   learning.TicketOutcome
	id    string
	err   error
	calls int
}

func (f *fakeOutcomeSink) LearnOutcome(_ context.Context, x learning.TicketOutcome) (string, error) {
	f.got = x
	f.calls++
	return f.id, f.err
}

func TestRecordTicketOutcomeAcceptedAndCorrected(t *testing.T) {
	st, err := state.Open(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	run := model.RunRecord{RunID: "run-1", TicketID: 42, ReplyProposed: true, ReplyProposedText: "Bitte VPN neu starten.", LearningTicketText: "VPN verbindet nicht.", ReplyBasisCategoryID: 5, ReplyBasisCategoryName: "VPN", AIKnowledgeID: "kb-vpn"}
	if err := st.Append(run); err != nil {
		t.Fatal(err)
	}
	os, err := learning.OpenOutcomes(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	sink := &fakeOutcomeSink{id: "mem-1"}
	svc := &Service{cfg: config.Config{OutcomeLearningEnabled: true}, state: st, outcomes: os, outcomeSink: sink}

	got, err := svc.RecordTicketOutcome(context.Background(), "run-1", "accepted", "", "checked", "tech")
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncStatus != "learned" || got.NeuroForgeID != "mem-1" || got.ConfirmedReply != "Bitte VPN neu starten." || sink.got.KnowledgeID != "kb-vpn" {
		t.Fatalf("unexpected accepted outcome %#v sink=%#v", got, sink.got)
	}

	sink.id = "mem-2"
	got, err = svc.RecordTicketOutcome(context.Background(), "run-1", "corrected", "VPN-Profil neu importieren.", "technician correction", "tech")
	if err != nil {
		t.Fatal(err)
	}
	items := os.List()
	if got.Decision != "corrected" || got.ConfirmedReply != "VPN-Profil neu importieren." || got.NeuroForgeID != "mem-2" || len(items) != 2 || got.SupersedesID == "" {
		t.Fatalf("unexpected corrected outcome %#v list=%#v", got, items)
	}
	// Repeating the same correction must not create or learn a duplicate.
	beforeCalls := sink.calls
	retry, err := svc.RecordTicketOutcome(context.Background(), "run-1", "corrected", "VPN-Profil neu importieren.", "technician correction", "tech")
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != got.ID || sink.calls != beforeCalls || len(os.List()) != 2 {
		t.Fatalf("expected idempotent retry: retry=%#v calls=%d list=%#v", retry, sink.calls, os.List())
	}
}

func TestRecordTicketOutcomeRejectsStaleTicketState(t *testing.T) {
	st, err := state.Open(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	original := model.Ticket{ID: 42, Name: "VPN", Content: "VPN verbindet nicht.", DateMod: "v1", StatusID: 1}
	run := model.RunRecord{RunID: "run-stale", TicketID: 42, SourceVersion: sourceVersion(original), ReplyProposed: true, ReplyProposedText: "VPN neu starten.", LearningTicketText: "VPN verbindet nicht."}
	if err := st.Append(run); err != nil {
		t.Fatal(err)
	}
	os, err := learning.OpenOutcomes(t.TempDir(), 20)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGLPI{ticket: original}
	g.ticket.Content = "Ticket wurde zwischenzeitlich aktualisiert."
	sink := &fakeOutcomeSink{id: "mem-stale"}
	svc := &Service{cfg: config.Config{OutcomeLearningEnabled: true}, glpi: g, state: st, outcomes: os, outcomeSink: sink}

	_, err = svc.RecordTicketOutcome(context.Background(), "run-stale", "accepted", "", "", "tech")
	if err == nil {
		t.Fatal("expected stale ticket state to block trusted outcome learning")
	}
	if sink.calls != 0 || len(os.List()) != 0 {
		t.Fatalf("stale run must not be persisted or learned: calls=%d outcomes=%#v", sink.calls, os.List())
	}
}

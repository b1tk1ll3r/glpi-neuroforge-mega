package learning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOutcomeStorePreservesRevisionHistoryAndPersistsSync(t *testing.T) {
	s, err := OpenOutcomes(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(TicketOutcome{RunID: "r1", TicketID: 1, Decision: "accepted", TicketInput: "problem", ProposedReply: "fix", ConfirmedReply: "fix", Actor: "tech"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Add(TicketOutcome{RunID: "r1", TicketID: 1, Decision: "corrected", TicketInput: "problem", ProposedReply: "fix", ConfirmedReply: "better", Actor: "tech"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 2 || a.ID == b.ID || b.SupersedesID != a.ID {
		t.Fatalf("expected immutable revision history: %#v", s.List())
	}
	retry, err := s.Add(TicketOutcome{RunID: "r1", TicketID: 1, Decision: "corrected", TicketInput: "problem", ProposedReply: "fix", ConfirmedReply: "better", Actor: "tech"})
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != b.ID || len(s.List()) != 2 {
		t.Fatalf("exact repeated human decision must be idempotent: retry=%#v list=%#v", retry, s.List())
	}
	got, err := s.UpdateSync(b.ID, "learned", "mem-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.SyncStatus != "learned" || got.NeuroForgeID != "mem-1" {
		t.Fatalf("unexpected sync: %#v", got)
	}
}

func TestNeuroForgeOutcomeSink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integrations/outcomes" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		if v["decision"] != "accepted" {
			t.Fatalf("unexpected payload %#v", v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"memory": map[string]any{"id": "mem-7"}})
	}))
	defer srv.Close()
	sink, err := NewNeuroForgeOutcomeSink(srv.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id, err := sink.LearnOutcome(context.Background(), TicketOutcome{ID: "o", RunID: "r", TicketID: 1, Decision: "accepted", TicketInput: "p", ProposedReply: "a", ConfirmedReply: "a", Actor: "tech"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "mem-7" {
		t.Fatalf("id=%q", id)
	}
}

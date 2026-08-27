package store

import (
	"errors"
	"testing"

	"neuroforge/internal/core"
)

func TestUpsertGoalRejectsDuplicateActiveGoalFingerprint(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := &core.Goal{Title: "FortiClient SSLVPN 7200", Description: "Create one support article", Target: "1 hochwertiger Wissensartikel", Status: core.GoalActive}
	if err := s.UpsertGoal(first); err != nil {
		t.Fatal(err)
	}
	duplicate := &core.Goal{Title: "  forticlient   sslvpn 7200 ", Description: " Create one support article ", Target: "1 hochwertiger Wissensartikel", Status: core.GoalActive}
	if err := s.UpsertGoal(duplicate); !errors.Is(err, ErrDuplicateGoal) {
		t.Fatalf("duplicate goal error=%v, want %v", err, ErrDuplicateGoal)
	}
}

func TestUpsertGoalAllowsSameFingerprintAfterCompletion(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := &core.Goal{Title: "FortiClient SSLVPN 7200", Description: "Create one support article", Target: "1 hochwertiger Wissensartikel", Status: core.GoalCompleted}
	if err := s.UpsertGoal(first); err != nil {
		t.Fatal(err)
	}
	second := &core.Goal{Title: first.Title, Description: first.Description, Target: first.Target, Status: core.GoalActive}
	if err := s.UpsertGoal(second); err != nil {
		t.Fatalf("completed historical goal must not block a new run: %v", err)
	}
}

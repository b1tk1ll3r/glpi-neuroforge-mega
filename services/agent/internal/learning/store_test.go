package learning

import (
	"github.com/example/glpi-ai-agent/internal/model"
	"testing"
)

func TestAddReplaceAndDelete(t *testing.T) {
	s, err := Open(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(model.LearningExample{RunID: "r1", Subject: "Login geht nicht", CategoryID: 2, CategoryName: "AD"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("count=%d", s.Count())
	}
	_, err = s.Add(model.LearningExample{RunID: "r1", Subject: "Login geht nicht", CategoryID: 3, CategoryName: "Identity"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 || len(s.ExamplesFor(3, 5)) != 1 {
		t.Fatal("replacement failed")
	}
	if err := s.Delete(a.ID); err == nil {
		t.Fatal("old replaced id should not exist")
	}
}

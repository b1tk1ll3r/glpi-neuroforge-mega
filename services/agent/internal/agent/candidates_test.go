package agent

import (
	"math"
	"testing"

	"github.com/example/glpi-ai-agent/internal/model"
)

func hit(id string, score float64) model.KnowledgeHit {
	return model.KnowledgeHit{Doc: model.KnowledgeDoc{ID: id}, Score: score}
}

func TestSelectKnowledgeCandidatesDynamicGap(t *testing.T) {
	hits := []model.KnowledgeHit{
		hit("a", 0.82), hit("b", 0.79), hit("c", 0.76), hit("d", 0.43), hit("e", 0.39),
	}
	got, cutoff := selectKnowledgeCandidates(hits, 6, 0.30, 0.20)
	if math.Abs(cutoff-0.62) > 1e-9 {
		t.Fatalf("cutoff=%v want 0.62", cutoff)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0].Doc.ID != "a" || got[2].Doc.ID != "c" {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestSelectKnowledgeCandidatesRespectsMaxAndFloor(t *testing.T) {
	hits := []model.KnowledgeHit{
		hit("a", 0.66), hit("b", 0.64), hit("c", 0.63), hit("d", 0.61), hit("e", 0.59), hit("f", 0.58), hit("g", 0.57),
	}
	got, cutoff := selectKnowledgeCandidates(hits, 5, 0.30, 0.20)
	if math.Abs(cutoff-0.46) > 1e-9 {
		t.Fatalf("cutoff=%v want 0.46", cutoff)
	}
	if len(got) != 5 {
		t.Fatalf("len=%d want max 5", len(got))
	}

	low := []model.KnowledgeHit{hit("x", 0.29), hit("y", 0.28)}
	got, cutoff = selectKnowledgeCandidates(low, 6, 0.30, 0.20)
	if len(got) != 0 || math.Abs(cutoff-0.30) > 1e-9 {
		t.Fatalf("below floor: len=%d cutoff=%v", len(got), cutoff)
	}
}

func TestSelectKnowledgeCandidatesUsesFloorAsCutoff(t *testing.T) {
	hits := []model.KnowledgeHit{hit("a", 0.44), hit("b", 0.35), hit("c", 0.31), hit("d", 0.29)}
	got, cutoff := selectKnowledgeCandidates(hits, 6, 0.30, 0.20)
	if math.Abs(cutoff-0.30) > 1e-9 {
		t.Fatalf("cutoff=%v want floor 0.30", cutoff)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
}

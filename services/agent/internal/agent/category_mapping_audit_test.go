package agent

import (
	"testing"

	"github.com/example/glpi-ai-agent/internal/model"
)

func TestCategoryKnowledgeMappingChecksWarnsOnDifferentTargetName(t *testing.T) {
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{
		ID:                 "KAT-NETZWERKDRUCKER-SELECT",
		Categories:         []int64{67},
		ExternalCategories: []string{"Drucken, Scannen und Kopieren > Netzwerkdrucker"},
	}}}
	cats := []model.Category{{ID: 67, Name: "Arbeitsplatzdrucker", CompleteName: "Drucken, Scannen und Kopieren > Arbeitsplatzdrucker"}}
	checks := categoryKnowledgeMappingChecks(hits, cats, 67)
	if len(checks) != 1 || checks[0].Status != "warn" || checks[0].Blocking {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}

func TestCategoryKnowledgeMappingChecksAcceptsMatchingLeaf(t *testing.T) {
	hits := []model.KnowledgeHit{{Doc: model.KnowledgeDoc{
		ID:                 "KAT-ARBEITSPLATZDRUCKER-SELECT",
		Categories:         []int64{67},
		ExternalCategories: []string{"Drucken, Scannen und Kopieren > Arbeitsplatzdrucker"},
	}}}
	cats := []model.Category{{ID: 67, Name: "Arbeitsplatzdrucker", CompleteName: "Drucken, Scannen und Kopieren > Arbeitsplatzdrucker"}}
	if checks := categoryKnowledgeMappingChecks(hits, cats, 67); len(checks) != 0 {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}

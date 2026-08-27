package brain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
	"neuroforge/internal/vector"
)

var targetNumberRE = regexp.MustCompile(`(?i)(\d{1,9})`)

func (e *Engine) refreshGoalResearchProgress(goal *core.Goal, evaluation float64) {
	runs := e.store.ResearchRunsSnapshot(goal.ID, 200)
	sourceSet := map[string]struct{}{}
	evidence, corroborations := 0, 0
	for _, run := range runs {
		evidence += run.Stats.NewEvidence
		corroborations += run.Stats.Corroborations
		for _, ev := range run.Events {
			if strings.TrimSpace(ev.SourceID) != "" {
				sourceSet[ev.SourceID] = struct{}{}
			}
		}
	}
	// Research-run telemetry is intentionally bounded. Keep persistent cumulative
	// counters monotonic so progress cannot fall backwards when old runs are
	// trimmed from the audit window. Existing source IDs are merged into the
	// bounded lineage sample.
	for _, id := range goal.ResearchSourceIDs {
		if strings.TrimSpace(id) != "" {
			sourceSet[id] = struct{}{}
		}
	}
	if evidence > goal.ResearchEvidence {
		goal.ResearchEvidence = evidence
	}
	if corroborations > goal.ResearchCorroborations {
		goal.ResearchCorroborations = corroborations
	}
	if len(sourceSet) > goal.ResearchSources {
		goal.ResearchSources = len(sourceSet)
	}
	goal.ResearchSourceIDs = goal.ResearchSourceIDs[:0]
	for id := range sourceSet {
		goal.ResearchSourceIDs = append(goal.ResearchSourceIDs, id)
		if len(goal.ResearchSourceIDs) >= 512 {
			break
		}
	}

	target := strings.ToLower(strings.TrimSpace(goal.Target))
	if m := targetNumberRE.FindStringSubmatch(target); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			current, label := goal.ResearchEvidence, "quellengebundene Evidenzen"
			// Explicit evidence/knowledge-entry wording wins over adjectives such as
			// "quellengebundene"; otherwise a target like "100 quellengebundene
			// Wissenseinträge" would incorrectly become a source-count target.
			evidenceTarget := strings.Contains(target, "wissensein") || strings.Contains(target, "evidenz") || strings.Contains(target, "claim") || strings.Contains(target, "eintr")
			if !evidenceTarget && (strings.Contains(target, "quelle") || strings.Contains(target, "source")) {
				current, label = goal.ResearchSources, "unabhängige Quellen"
			}
			if strings.Contains(target, "bestät") || strings.Contains(target, "corrobor") {
				current, label = goal.ResearchCorroborations, "Bestätigungen"
			}
			goal.Progress = vector.Clamp(float64(current)/float64(n), 0, 1)
			goal.ProgressReason = fmt.Sprintf("%d/%d %s", current, n, label)
			return
		}
	}

	sat := func(v, target int) float64 {
		if target <= 0 {
			return 0
		}
		return vector.Clamp(float64(v)/float64(target), 0, 1)
	}
	quality := vector.Clamp((evaluation+1)/2, 0, 1)
	goal.Progress = vector.Clamp(.55*sat(goal.ResearchEvidence, 20)+.25*sat(goal.ResearchSources, 8)+.15*sat(goal.ResearchCorroborations, 5)+.05*quality, 0, 1)
	goal.ProgressReason = fmt.Sprintf("%d Evidenzen · %d Quellen · %d Bestätigungen", goal.ResearchEvidence, goal.ResearchSources, goal.ResearchCorroborations)
}

func filterGoalEvidenceHits(hits []store.SearchHit) []store.SearchHit {
	out := make([]store.SearchHit, 0, len(hits))
	for _, h := range hits {
		if h.Memory.Kind == "goal-learning" || h.Memory.Provenance.Source == "goal-cycle" {
			continue
		}
		out = append(out, h)
	}
	return out
}

// ReconcileGoalProgress backfills the measurable progress fields from persisted
// research-run telemetry. It makes upgrades immediately reflect historical work
// without requiring a fresh web-research cycle first.
func (e *Engine) ReconcileGoalProgress() error {
	for _, g := range e.store.GoalsSnapshot() {
		if !g.ResearchEnabled {
			continue
		}
		before, beforeReason := g.Progress, g.ProgressReason
		e.refreshGoalResearchProgress(&g, g.LastEvaluation)
		if g.Progress != before || g.ProgressReason != beforeReason {
			if err := e.store.UpsertGoal(&g); err != nil {
				return err
			}
		}
	}
	return nil
}

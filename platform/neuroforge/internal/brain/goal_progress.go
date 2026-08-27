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
	if goal == nil {
		return
	}
	// Recompute from relevant evidence instead of keeping monotonic counters from
	// old research runs. This intentionally lets upgrades remove previously
	// counted off-topic evidence (for example an NVIDIA goal polluted by WebRTC).
	sourceSet := map[string]struct{}{}
	memorySet := map[string]struct{}{}
	corroborationSet := map[string]struct{}{}
	runs := e.store.ResearchRunsSnapshot(goal.ID, 200)
	for _, run := range runs {
		for _, ev := range run.Events {
			if ev.Type != "evidence.learned" && ev.Type != "evidence.corroborated" {
				continue
			}
			m, ok := e.store.GetMemory(ev.MemoryID)
			if !ok || m == nil {
				continue
			}
			var src *core.KnowledgeSource
			if m.Provenance.SourceID != "" {
				if x, ok := e.store.GetSource(m.Provenance.SourceID); ok {
					src = x
				}
			}
			if !goalEvidenceRelevant(goal, *m, src) {
				continue
			}
			memorySet[m.ID] = struct{}{}
			if ev.SourceID != "" {
				sourceSet[ev.SourceID] = struct{}{}
			} else if m.Provenance.SourceID != "" {
				sourceSet[m.Provenance.SourceID] = struct{}{}
			}
			if ev.Type == "evidence.corroborated" && ev.SourceID != "" {
				corroborating, ok := e.store.GetSource(ev.SourceID)
				if ok && corroborating != nil {
					primaryOrigin := ""
					if src != nil {
						primaryOrigin = sourceOriginKey(src.URI)
					}
					origin := sourceOriginKey(corroborating.URI)
					if origin != "" && origin != primaryOrigin {
						corroborationSet[m.ID+"\x00"+origin] = struct{}{}
					}
				}
			}
		}
	}
	// Durable provenance/legacy goal tags cover evidence older than the bounded
	// research-run history and make the relevance repair effective after restart.
	for _, m := range e.store.MemoriesSnapshot() {
		if !memoryBelongsToGoal(m, goal.ID) || m.Provenance.SourceID == "" {
			continue
		}
		var src *core.KnowledgeSource
		if x, ok := e.store.GetSource(m.Provenance.SourceID); ok {
			src = x
		}
		if !goalEvidenceRelevant(goal, m, src) {
			continue
		}
		memorySet[m.ID] = struct{}{}
		sourceSet[m.Provenance.SourceID] = struct{}{}
		primaryOrigin := ""
		if src != nil {
			primaryOrigin = sourceOriginKey(src.URI)
		}
		for _, sid := range m.EvidenceSourceIDs {
			if sid == "" || sid == m.Provenance.SourceID {
				continue
			}
			corroborating, ok := e.store.GetSource(sid)
			if !ok || corroborating == nil {
				continue
			}
			origin := sourceOriginKey(corroborating.URI)
			if origin != "" && origin != primaryOrigin {
				corroborationSet[m.ID+"\x00"+origin] = struct{}{}
			}
		}
	}
	goal.ResearchEvidence = len(memorySet)
	goal.ResearchSources = len(sourceSet)
	goal.ResearchCorroborations = len(corroborationSet)
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
			if strings.Contains(target, "artikel") || strings.Contains(target, "article") || strings.Contains(target, "draft") || strings.Contains(target, "entwurf") {
				current, label = 0, "validierte Staging-Artikel"
				if goal.LastStagingDraftID != "" && goal.StagingDraftValidated && goal.StagingQualityGateVersion == stagingQualityGateVersion {
					current = 1
				}
			} else {
				evidenceTarget := strings.Contains(target, "wissensein") || strings.Contains(target, "evidenz") || strings.Contains(target, "claim") || strings.Contains(target, "eintr")
				if !evidenceTarget && (strings.Contains(target, "quelle") || strings.Contains(target, "source")) {
					current, label = goal.ResearchSources, "unabhängige Quellen"
				}
				if strings.Contains(target, "bestät") || strings.Contains(target, "corrobor") {
					current, label = goal.ResearchCorroborations, "Bestätigungen"
				}
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

package brain

import (
	"sort"
	"strings"
	"unicode"

	"neuroforge/internal/core"
)

// goalAnchorTokens extracts a deliberately small set of subject anchors from the
// goal title. Generic workflow/helpdesk words are ignored so a broad page cannot
// become goal evidence merely because it contains words such as "client" or
// "documentation". These anchors are used only as a fail-closed relevance gate;
// they do not replace semantic retrieval/ranking.
func goalAnchorTokens(goal *core.Goal) []string {
	if goal == nil {
		return nil
	}
	generic := map[string]bool{
		"client": true, "clients": true, "architecture": true, "architektur": true,
		"documentation": true, "dokumentation": true, "official": true, "offizielle": true,
		"information": true, "informationen": true, "user": true, "users": true,
		"benutzer": true, "administrator": true, "administratoren": true,
		"guide": true, "guides": true, "hilfe": true, "help": true,
		"knowledge": true, "wissen": true, "article": true, "articles": true,
		"artikel": true, "research": true, "vorschlag": true,
		"new": true, "neue": true, "neuen": true, "neu": true,
		"graphics": true, "grafikkarten": true, "karte": true, "karten": true,
	}
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, goal.Title)
	seen := map[string]bool{}
	out := make([]string, 0, 6)
	for _, tok := range strings.Fields(normalized) {
		if len([]rune(tok)) < 3 || generic[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	if len(out) == 0 {
		// Fall back to non-empty title tokens. This keeps generic goals usable
		// while still requiring some direct subject overlap.
		for _, tok := range strings.Fields(normalized) {
			if len([]rune(tok)) < 3 || seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func researchMaterialRelevant(goal *core.Goal, parts ...string) bool {
	anchors := goalAnchorTokens(goal)
	if len(anchors) == 0 {
		return true
	}
	haystack := strings.ToLower(strings.Join(parts, "\n"))
	for _, tok := range anchors {
		if strings.Contains(haystack, tok) {
			return true
		}
	}
	return false
}

func goalIDFromTags(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "goal:") {
			if id := strings.TrimSpace(strings.TrimPrefix(tag, "goal:")); id != "" {
				return id
			}
		}
	}
	return ""
}

func memoryBelongsToGoal(m core.Memory, goalID string) bool {
	if strings.TrimSpace(goalID) == "" {
		return false
	}
	if m.Provenance.GoalID == goalID {
		return true
	}
	for _, tag := range m.Tags {
		if tag == "goal:"+goalID {
			return true
		}
	}
	return false
}

func goalEvidenceRelevant(goal *core.Goal, m core.Memory, src *core.KnowledgeSource) bool {
	if goal == nil || m.Status != core.MemoryActive || m.Provenance.Source == "goal-cycle" || strings.TrimSpace(m.Text) == "" {
		return false
	}
	if src != nil {
		return researchMaterialRelevant(goal, src.Title, src.URI, m.Text)
	}
	return researchMaterialRelevant(goal, m.Provenance.SourceTitle, m.Provenance.SourceURI, m.Text)
}

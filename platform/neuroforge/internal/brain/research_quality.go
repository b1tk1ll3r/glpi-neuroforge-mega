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

	// Relevance is intentionally token-aware and requires coverage of more than
	// one anchor for multi-part goals. The previous substring-any rule allowed
	// false positives such as goal code "7200" matching a Docker repository id
	// beginning with "72007...", or a generic page containing only "sslvpn".
	// Numeric anchors (error codes, versions, model numbers) must match an exact
	// normalized token and, when a lexical anchor exists, be accompanied by at
	// least one subject/product anchor.
	words := normalizedResearchWords(parts...)
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		wordSet[w] = true
	}
	// Also index compact adjacent word pairs/triples so an anchor such as
	// "sslvpn" matches source text written as "SSL VPN" without falling back to
	// unrestricted substring matching.
	compact := make(map[string]bool, len(words)*2)
	for i := range words {
		if i+1 < len(words) {
			compact[words[i]+words[i+1]] = true
		}
		if i+2 < len(words) {
			compact[words[i]+words[i+1]+words[i+2]] = true
		}
	}

	lexicalTotal, lexicalMatches := 0, 0
	numericTotal, numericMatches := 0, 0
	for _, anchor := range anchors {
		if allDigits(anchor) {
			numericTotal++
			if wordSet[anchor] {
				numericMatches++
			}
			continue
		}
		lexicalTotal++
		if wordSet[anchor] || compact[anchor] {
			lexicalMatches++
		}
	}

	if numericTotal > 0 {
		if numericMatches == 0 {
			return false
		}
		if lexicalTotal > 0 && lexicalMatches == 0 {
			return false
		}
		return true
	}
	if lexicalTotal <= 1 {
		return lexicalMatches == lexicalTotal
	}
	return lexicalMatches >= 2
}

func normalizedResearchWords(parts ...string) []string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, strings.Join(parts, "\n"))
	return strings.Fields(normalized)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
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

package brain

import (
	"strings"
	"unicode/utf8"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

func policyTrust(lp core.LearningPolicyConfig, source string) float64 {
	if lp.SourceTrust == nil {
		return 1
	}
	if v, ok := lp.SourceTrust[source]; ok {
		return vector.Clamp(v, 0, 1)
	}
	return 1
}

func policyConfidence(lp core.LearningPolicyConfig, source string, base float64) float64 {
	if base <= 0 {
		base = 1
	}
	return vector.Clamp(base*policyTrust(lp, source), 0, 1)
}

func policyTextAllowed(lp core.LearningPolicyConfig, text string) bool {
	if lp.MaxMemoryTextChars <= 0 {
		return true
	}
	return utf8.RuneCountInString(strings.TrimSpace(text)) <= lp.MaxMemoryTextChars
}

func (e *Engine) duplicateMemory(vec []float32, memoryType, kind string, threshold float64) (*core.Memory, float64) {
	if threshold <= -1 || len(vec) == 0 {
		return nil, 0
	}
	hits := e.store.SearchVector(vec, 8, threshold, 0)
	for _, h := range hits {
		if h.Memory.MemoryType == memoryType && (kind == "" || h.Memory.Kind == kind) && h.Memory.Status == core.MemoryActive && h.Similarity >= threshold {
			m := h.Memory
			return &m, h.Similarity
		}
	}
	return nil, 0
}

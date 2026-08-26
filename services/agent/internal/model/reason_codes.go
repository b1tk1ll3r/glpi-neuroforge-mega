package model

import "strings"

// NormalizeReasonCodes trims, lowercases and de-duplicates model-provided
// reason codes while preserving their first-seen order. Structured-output
// implementations do not always enforce JSON Schema's uniqueItems keyword,
// therefore policy code must not rely on the model doing so.
func NormalizeReasonCodes(codes []string) []string {
	if len(codes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(codes))
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HasReasonCode reports whether a normalized or raw reason-code slice contains
// the requested code. Comparison is case-insensitive and ignores whitespace.
func HasReasonCode(codes []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	if wanted == "" {
		return false
	}
	for _, code := range codes {
		if strings.ToLower(strings.TrimSpace(code)) == wanted {
			return true
		}
	}
	return false
}

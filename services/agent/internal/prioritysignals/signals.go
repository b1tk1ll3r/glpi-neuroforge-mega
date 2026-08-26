package prioritysignals

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/example/glpi-ai-agent/internal/model"
)

// Signal is deterministic evidence extracted from the user-provided ticket.
// It does not decide the priority. It only makes explicit statements available
// to the dedicated AI run and to consistency checks around its output.
type Signal struct {
	Code    string `json:"code"`
	Source  string `json:"source"`
	Excerpt string `json:"excerpt"`
}

// Evidence contains only signals that are stated explicitly enough to be used
// as a consistency guard. Absence of a signal is never interpreted as proof of
// the opposite condition.
type Evidence struct {
	Signals []Signal `json:"signals,omitempty"`
}

type rule struct {
	code string
	re   *regexp.Regexp
}

var rules = []rule{
	{code: "multiple_users_affected", re: regexp.MustCompile(`(?i)\b(?:meine|unsere)\s+(?:kolleg(?:e|en|innen)|mitarbeiter(?:innen)?|teammitglieder)\s+und\s+ich\b`)},
	{code: "multiple_users_affected", re: regexp.MustCompile(`(?i)\b(?:kolleg(?:e|en|innen)|mitarbeiter(?:innen)?|benutzer|nutzer|personen)\s+und\s+ich\b`)},
	{code: "multiple_users_affected", re: regexp.MustCompile(`(?i)\b(?:mehrere|viele|alle|sämtliche)\s+(?:kolleg(?:en|innen)?|mitarbeiter(?:innen)?|benutzer|nutzer|personen|arbeitsplätze|rechner|pcs|geräte)\b`)},
	{code: "multiple_users_affected", re: regexp.MustCompile(`(?i)\bwir\s+alle\b`)},
	{code: "site_affected", re: regexp.MustCompile(`(?i)\b(?:gesamter|ganzer|kompletter)\s+(?:standort|gebäude|liegenschaft|campus)\b`)},
	{code: "organization_affected", re: regexp.MustCompile(`(?i)\b(?:gesamte|ganze|komplette)\s+(?:organisation|verwaltung|behörde|firma|unternehmen)\b`)},
	{code: "workaround_available", re: regexp.MustCompile(`(?i)\b(?:workaround|ausweichmöglichkeit|alternative(?:r|n)?\s+weg|ersatzlösung)\b`)},
	{code: "workaround_available", re: regexp.MustCompile(`(?i)\b(?:andere|weitere|alternative)\s+(?:drucker|geräte|systeme|anwendungen?)\b.{0,60}\b(?:funktionier(?:t|en)|lauf(?:en|t)|geh(?:t|en))\b`)},
	{code: "workaround_available", re: regexp.MustCompile(`(?i)\b(?:büro|arbeitsplatz)[- ]?drucker\b.{0,50}\b(?:funktionier(?:t|en)|lauf(?:en|t)|geh(?:t|en)|noch\s+verfügbar)\b`)},
	{code: "no_workaround", re: regexp.MustCompile(`(?i)\b(?:kein(?:e|en)?\s+(?:workaround|alternative|ausweichmöglichkeit)|ohne\s+ausweichmöglichkeit|überhaupt\s+nicht\s+weiterarbeiten)\b`)},
	{code: "business_deadline", re: regexp.MustCompile(`(?i)\b(?:frist|deadline|abgabetermin|termin)\b.{0,60}\b(?:heute|morgen|bis\s+\d{1,2}[.\/-]\d{1,2}|dringend|läuft\s+ab)\b`)},
	{code: "security_incident_suspected", re: regexp.MustCompile(`(?i)\b(?:phishing|ransomware|schadsoftware|malware|konto\s+übernommen|unbefugter\s+zugriff|datenabfluss)\b`)},
	{code: "data_loss_possible", re: regexp.MustCompile(`(?i)\b(?:datenverlust|daten\s+verloren|dateien\s+gelöscht|nicht\s+gespeichert|überschrieben)\b`)},
}

var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)
var whitespaceRE = regexp.MustCompile(`\s+`)

// Extract returns deterministic evidence from subject and body. It intentionally
// uses conservative patterns: false negatives are acceptable; false positives
// must not become automatic priority changes.
func Extract(t model.Ticket) Evidence {
	text := normalize(t.Name + "\n" + t.Content)
	seen := map[string]struct{}{}
	out := Evidence{}
	for _, r := range rules {
		loc := r.re.FindStringIndex(text)
		if loc == nil {
			continue
		}
		if _, ok := seen[r.code]; ok {
			continue
		}
		seen[r.code] = struct{}{}
		out.Signals = append(out.Signals, Signal{Code: r.code, Source: "ticket_text", Excerpt: excerpt(text, loc[0], loc[1], 90)})
	}
	return out
}

func (e Evidence) Has(code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	for _, s := range e.Signals {
		if strings.EqualFold(strings.TrimSpace(s.Code), code) {
			return true
		}
	}
	return false
}

func (e Evidence) Codes() []string {
	out := make([]string, 0, len(e.Signals))
	for _, s := range e.Signals {
		if strings.TrimSpace(s.Code) != "" {
			out = append(out, s.Code)
		}
	}
	return out
}

func normalize(v string) string {
	v = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "</li>", "\n").Replace(v)
	v = tagRE.ReplaceAllString(v, " ")
	v = html.UnescapeString(v)
	v = whitespaceRE.ReplaceAllString(v, " ")
	return strings.TrimSpace(v)
}

func excerpt(s string, start, end, radius int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	left := start - radius
	if left < 0 {
		left = 0
	}
	right := end + radius
	if right > len(s) {
		right = len(s)
	}
	// Move to UTF-8 boundaries because regexp indices are byte offsets.
	for left > 0 && !utf8.RuneStart(s[left]) {
		left--
	}
	for right < len(s) && !utf8.RuneStart(s[right]) {
		right++
	}
	v := strings.TrimSpace(s[left:right])
	if left > 0 {
		v = "…" + v
	}
	if right < len(s) {
		v += "…"
	}
	return v
}

// Reconcile makes a model result consistent with explicit ticket evidence
// without performing another AI request. It is deliberately conservative:
// it never raises confidence and never invents a priority increase.
func Reconcile(t model.Ticket, evidence Evidence, out model.PriorityDecision) model.PriorityDecision {
	reasons := model.NormalizeReasonCodes(out.ReasonCodes)
	if len(evidence.Signals) > 0 {
		reasons = removeCode(reasons, "insufficient_information")
	}

	// Explicit, deterministic signals take precedence over contradictory model
	// metadata. They describe facts, not the final priority decision.
	for _, code := range []string{"organization_affected", "site_affected", "multiple_users_affected", "workaround_available", "no_workaround", "business_deadline", "security_incident_suspected", "data_loss_possible"} {
		if evidence.Has(code) {
			reasons = prependUnique(reasons, code)
		}
	}

	switch {
	case evidence.Has("organization_affected"):
		out.AffectedScope = "organization"
	case evidence.Has("site_affected"):
		if out.AffectedScope != "organization" {
			out.AffectedScope = "site"
		}
	case evidence.Has("multiple_users_affected"):
		if out.AffectedScope != "organization" && out.AffectedScope != "site" {
			out.AffectedScope = "multiple_users"
		}
	}

	if len(reasons) == 0 {
		reasons = []string{"insufficient_information"}
		out.RecommendedPriority = t.Priority
		if strings.TrimSpace(out.AffectedScope) == "" {
			out.AffectedScope = "unknown"
		}
	}
	if model.HasReasonCode(reasons, "insufficient_information") {
		reasons = []string{"insufficient_information"}
		out.RecommendedPriority = t.Priority
	}
	if len(reasons) > 3 {
		reasons = reasons[:3]
	}
	out.ReasonCodes = reasons

	reason := strings.TrimSpace(out.Reason)
	if reason == "" || isBareReasonCode(reason, reasons) || strings.EqualFold(reason, "insufficient_information") && len(evidence.Signals) > 0 {
		out.Reason = evidenceExplanation(evidence)
	}
	return out
}

func prependUnique(values []string, code string) []string {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return values
	}
	out := []string{code}
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" && value != code {
			out = append(out, value)
		}
	}
	return out
}

func removeCode(values []string, code string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !strings.EqualFold(strings.TrimSpace(value), code) {
			out = append(out, value)
		}
	}
	return out
}

func isBareReasonCode(reason string, codes []string) bool {
	for _, code := range codes {
		if strings.EqualFold(strings.TrimSpace(reason), strings.TrimSpace(code)) {
			return true
		}
	}
	return false
}

func evidenceExplanation(e Evidence) string {
	switch {
	case e.Has("multiple_users_affected") && e.Has("workaround_available"):
		return "Mehrere Personen sind betroffen; zugleich ist laut Ticket eine funktionierende Ausweichmöglichkeit vorhanden."
	case e.Has("multiple_users_affected") && e.Has("no_workaround"):
		return "Mehrere Personen sind betroffen und laut Ticket besteht keine Ausweichmöglichkeit."
	case e.Has("multiple_users_affected"):
		return "Laut Ticket sind mehrere Personen betroffen."
	case e.Has("site_affected"):
		return "Laut Ticket ist ein ganzer Standort betroffen."
	case e.Has("organization_affected"):
		return "Laut Ticket ist die gesamte Organisation betroffen."
	case e.Has("workaround_available"):
		return "Laut Ticket ist eine funktionierende Ausweichmöglichkeit vorhanden."
	case e.Has("no_workaround"):
		return "Laut Ticket besteht keine Ausweichmöglichkeit."
	default:
		return "Die Ticketangaben reichen nicht für eine belastbare Höherstufung aus."
	}
}

package prioritysignals

import (
	"testing"

	"github.com/example/glpi-ai-agent/internal/model"
)

func TestExtractMultipleUsersAndWorkaround(t *testing.T) {
	e := Extract(model.Ticket{Name: "Probleme mit Flurdrucker", Content: `<p>Meine Kollegen und ich können nicht mehr auf den Flur-Kopierern drucken.</p><p>Die Bürodrucker laufen noch.</p>`})
	if !e.Has("multiple_users_affected") {
		t.Fatalf("missing multiple user signal: %+v", e.Signals)
	}
	if !e.Has("workaround_available") {
		t.Fatalf("missing workaround signal: %+v", e.Signals)
	}
	if e.Has("insufficient_information") {
		t.Fatalf("extractor must not invent insufficient_information")
	}
}

func TestExtractDoesNotTreatGenericSingularAsSingleUserProof(t *testing.T) {
	e := Extract(model.Ticket{Content: `Ich kann nicht drucken.`})
	if len(e.Signals) != 0 {
		t.Fatalf("unexpected signals: %+v", e.Signals)
	}
}

func TestReconcileDoesNotRetryOrRaiseConfidence(t *testing.T) {
	ticket := model.Ticket{Priority: 3, Content: `Meine Kollegen und ich können nicht drucken. Die Bürodrucker laufen noch.`}
	e := Extract(ticket)
	out := Reconcile(ticket, e, model.PriorityDecision{
		RecommendedPriority: 3,
		AffectedScope:       "unknown",
		ReasonCodes:         []string{"insufficient_information"},
		Confidence:          0.2,
		Reason:              "insufficient_information",
	})
	if out.AffectedScope != "multiple_users" {
		t.Fatalf("scope=%q", out.AffectedScope)
	}
	if !model.HasReasonCode(out.ReasonCodes, "multiple_users_affected") || !model.HasReasonCode(out.ReasonCodes, "workaround_available") {
		t.Fatalf("reason codes=%v", out.ReasonCodes)
	}
	if model.HasReasonCode(out.ReasonCodes, "insufficient_information") {
		t.Fatalf("contradictory reason retained: %v", out.ReasonCodes)
	}
	if out.Confidence != 0.2 {
		t.Fatalf("confidence=%v", out.Confidence)
	}
	if out.Reason == "" || out.Reason == "insufficient_information" {
		t.Fatalf("reason=%q", out.Reason)
	}
}

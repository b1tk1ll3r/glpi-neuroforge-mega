package agent

import (
	"testing"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
)

func priorityTestConfig() config.Config {
	return config.Config{
		PriorityEnabled:            true,
		PriorityConfidence:         .88,
		PriorityMaxIncrease:        1,
		PriorityAllowedReasonCodes: []string{"multiple_users_affected", "site_affected", "core_service_unavailable", "business_deadline", "no_workaround"},
	}
}

func TestEvaluatePriorityTreatsInsufficientInformationAsNeutralNoChange(t *testing.T) {
	result := evaluatePriority(priorityTestConfig(), model.Ticket{Priority: 3}, model.PriorityDecision{
		RecommendedPriority: 3,
		Confidence:          .95,
		ReasonCodes:         []string{"insufficient_information", " insufficient_information ", "INSUFFICIENT_INFORMATION"},
	})

	if !result.Accepted || result.ChangePriority || result.PriorityAfter != 3 {
		t.Fatalf("neutral no-change decision was not accepted: %+v", result)
	}
	if result.Decision != "priority_no_change_insufficient_information" {
		t.Fatalf("unexpected decision: %s", result.Decision)
	}
	if len(result.ReasonCodes) != 1 || result.ReasonCodes[0] != "insufficient_information" {
		t.Fatalf("reason codes were not normalized: %#v", result.ReasonCodes)
	}
	for _, check := range result.Checks {
		if check.Code == "priority_reasons_allowed" {
			if check.Status != "na" || check.Blocking {
				t.Fatalf("neutral reason check must not be blocking: %+v", check)
			}
			return
		}
	}
	t.Fatal("priority_reasons_allowed check missing")
}

func TestEvaluatePriorityBlocksIncreaseWithInsufficientInformation(t *testing.T) {
	result := evaluatePriority(priorityTestConfig(), model.Ticket{Priority: 3}, model.PriorityDecision{
		RecommendedPriority: 4,
		Confidence:          .99,
		ReasonCodes:         []string{"insufficient_information", "business_deadline"},
	})
	if result.Accepted || result.ChangePriority {
		t.Fatalf("increase with insufficient information must be blocked: %+v", result)
	}
	if result.Decision != "priority_insufficient_information" {
		t.Fatalf("unexpected decision: %s", result.Decision)
	}
}

func TestEvaluatePriorityAllowsNeutralContextAlongsideAllowedActionReason(t *testing.T) {
	result := evaluatePriority(priorityTestConfig(), model.Ticket{Priority: 2}, model.PriorityDecision{
		RecommendedPriority: 4,
		Confidence:          .94,
		ReasonCodes:         []string{"single_user_affected", "business_deadline"},
	})
	if !result.Accepted || !result.ChangePriority || result.PriorityAfter != 3 {
		t.Fatalf("allowed increase was not accepted and capped: %+v", result)
	}
}

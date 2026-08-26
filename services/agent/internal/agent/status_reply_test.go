package agent

import (
	"testing"

	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/model"
)

func TestStatusReplyDisabledIsInformationalNotBlocking(t *testing.T) {
	cfg := config.Config{ContextStatusReplyEnabled: false}
	res := evaluateStatusReply(cfg, Policy{}, model.ContextSnapshot{}, nil, model.StatusDecision{})
	if res.DecisionCode != "status_reply_disabled" {
		t.Fatalf("decision=%s", res.DecisionCode)
	}
	for _, c := range res.Checks {
		if c.Blocking || c.Status == "fail" {
			t.Fatalf("disabled status feature must not produce blocking failures: %+v", c)
		}
	}
}

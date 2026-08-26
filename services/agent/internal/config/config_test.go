package config

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		GLPIURL:                   "https://glpi.internal.example",
		GLPIClientID:              "client-id",
		GLPIClientSecret:          "real-secret-value",
		GLPIUsername:              "svc-agent",
		GLPIPassword:              "real-password-value",
		GLPIAllowedStatusIDs:      []int64{1},
		WebAllowAnonymous:         true,
		Workers:                   1,
		QueueSize:                 1,
		CategoryConfidence:        .9,
		ReplyConfidence:           .9,
		KnowledgeMinScore:         .8,
		KnowledgeAllowedSources:   []string{"internal-kb"},
		KnowledgeAutoReplySources: []string{"internal-kb"},
		CommunicationLanguage:     "de-DE",
		CommunicationStyle:        "formal",
		OllamaTimeout:             time.Minute,
		OllamaNumPredict:          256,
		OllamaKeepAlive:           10 * time.Minute,
		OllamaThink:               false,
		OllamaMaxConcurrent:       1,
	}
}

func TestValidateRejectsPlainHTTPByDefault(t *testing.T) {
	c := validConfig()
	c.GLPIURL = "http://glpi.internal.example"
	if err := c.Validate(); err == nil {
		t.Fatal("expected plain HTTP to be rejected")
	}
	c.GLPIAllowInsecureHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatalf("explicit insecure override should validate: %v", err)
	}
}

func TestValidateRejectsPlaceholderSecrets(t *testing.T) {
	c := validConfig()
	c.GLPIClientSecret = "CHANGE_ME"
	if err := c.Validate(); err == nil {
		t.Fatal("expected placeholder secret to be rejected")
	}
}

func TestValidateRequiresAllowedStatus(t *testing.T) {
	c := validConfig()
	c.GLPIAllowedStatusIDs = nil
	if err := c.Validate(); err == nil {
		t.Fatal("expected empty status whitelist to be rejected")
	}
}

func TestValidateRejectsAutoReplySourceOutsideAllowlist(t *testing.T) {
	c := validConfig()
	c.KnowledgeAutoReplySources = []string{"vendor-docs"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected auto-reply source outside allowlist to be rejected")
	}
}

func TestValidateRejectsUnknownCommunicationStyle(t *testing.T) {
	c := validConfig()
	c.CommunicationStyle = "super-friendly"
	if err := c.Validate(); err == nil {
		t.Fatal("expected unsupported communication style to be rejected")
	}
}

func TestValidateRequiresMajorIncidentFilter(t *testing.T) {
	c := validConfig()
	c.ContextEnabled = true
	c.ContextTimeout = 1
	c.MajorIncidentsEnabled = true
	c.GLPIMajorIncidentLimit = 20
	if err := c.Validate(); err == nil {
		t.Fatal("expected major incident filter to be required")
	}
}

func TestValidateUptimeKumaMetricsRequiresAPIKey(t *testing.T) {
	c := validConfig()
	c.ContextEnabled = true
	c.ContextTimeout = 1
	c.UptimeKumaEnabled = true
	c.UptimeKumaURL = "https://uptime.internal.example"
	c.UptimeKumaMode = "metrics"
	c.UptimeKumaMaxIssues = 20
	if err := c.Validate(); err == nil {
		t.Fatal("expected Uptime Kuma API key to be required in metrics mode")
	}
	c.UptimeKumaAPIKey = "test-api-key"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected metrics mode with API key to validate: %v", err)
	}
}

func TestValidateUserDeviceFilterRequiresUserPlaceholder(t *testing.T) {
	c := validConfig()
	c.ContextEnabled = true
	c.ContextTimeout = 1
	c.UserDeviceContextEnabled = true
	c.GLPIUserDevicePaths = []string{"/Assets/Computer"}
	c.GLPIUserDeviceFilterTemplate = "user.id==42"
	c.GLPIUserDeviceLimit = 20
	if err := c.Validate(); err == nil {
		t.Fatal("expected user device filter template to require {{user_id}}")
	}
}

func TestKnowledgeWebEditRequiresAuthentication(t *testing.T) {
	c := validConfig()
	c.KnowledgeWebEditEnabled = true
	c.WebAllowAnonymous = true
	if err := c.Validate(); err == nil {
		t.Fatal("expected anonymous KB editing to be rejected")
	}
}

func TestValidateGLPIKBRequiresAllowedSource(t *testing.T) {
	c := validConfig()
	c.GLPIKBEnabled = true
	c.GLPIKBPath = "auto"
	c.GLPIKBLimit = 100
	c.GLPIKBSyncInterval = 10 * time.Minute
	c.GLPIKBSource = "glpi-kb"
	if err := c.Validate(); err == nil {
		t.Fatal("expected GLPI KB source outside allowlist to be rejected")
	}
	c.KnowledgeAllowedSources = []string{"internal-kb", "glpi-kb"}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected GLPI KB config to validate: %v", err)
	}
}

func TestValidateGLPIKBAutoReplyRequiresCategoryWhitelist(t *testing.T) {
	c := validConfig()
	c.GLPIKBEnabled = true
	c.GLPIKBPath = "auto"
	c.GLPIKBLimit = 100
	c.GLPIKBSyncInterval = 10 * time.Minute
	c.GLPIKBSource = "glpi-kb"
	c.KnowledgeAllowedSources = []string{"internal-kb", "glpi-kb"}
	c.KnowledgeAutoReplySources = []string{"internal-kb", "glpi-kb"}
	c.GLPIKBAutoReply = true
	if err := c.Validate(); err == nil {
		t.Fatal("expected an auto reply category whitelist to be required")
	}
	c.GLPIKBAutoReplyCategoryIDs = []int64{3}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected explicit category whitelist to validate: %v", err)
	}
	c.GLPIKBAutoReplyCategoryIDs = nil
	c.GLPIKBAutoReplyITILCategoryIDs = []int64{38}
	if err := c.Validate(); err == nil {
		t.Fatal("expected legacy ITIL category list not to approve GLPI KB auto reply")
	}
	c.GLPIKBAutoReplyAllowUncategorized = true
	c.GLPIKBAutoReplyUncategorizedArticleIDs = []int64{1}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected explicit uncategorized article allowlist to validate: %v", err)
	}
}

func TestValidateRejectsInvalidKnowledgeScoringConfig(t *testing.T) {
	c := validConfig()
	c.KnowledgeSemanticWeight = 1.2
	if err := c.Validate(); err == nil {
		t.Fatal("expected knowledge weight > 1 to be rejected")
	}
	c = validConfig()
	c.KnowledgeChunkWords = 100
	c.KnowledgeChunkOverlapWords = 100
	if err := c.Validate(); err == nil {
		t.Fatal("expected overlap >= chunk size to be rejected")
	}
}

func TestValidateKnowledgeCategoryModeTrimsWhitespace(t *testing.T) {
	c := validConfig()
	c.KnowledgeCategoryMode = "  UNSCOPED  "
	if err := c.Validate(); err != nil {
		t.Fatalf("expected whitespace/case normalized category mode to validate: %v", err)
	}
}

func TestEnvNormalizedLowerDefaultsBlankOrWhitespace(t *testing.T) {
	t.Setenv("KNOWLEDGE_CATEGORY_MODE", "   ")
	if got := envNormalizedLower("KNOWLEDGE_CATEGORY_MODE", "unscoped"); got != "unscoped" {
		t.Fatalf("expected unscoped default, got %q", got)
	}
	t.Setenv("KNOWLEDGE_CATEGORY_MODE", "  STRICT ")
	if got := envNormalizedLower("KNOWLEDGE_CATEGORY_MODE", "unscoped"); got != "strict" {
		t.Fatalf("expected strict, got %q", got)
	}
}

func TestKnowledgeIndexSourcesIncludesCategoryOnlySources(t *testing.T) {
	c := validConfig()
	c.KnowledgeAllowedSources = []string{"internal-kb", "glpi-kb"}
	c.KnowledgeCategorySources = []string{"internal-category", "INTERNAL-KB"}
	got := c.KnowledgeIndexSources()
	want := []string{"internal-kb", "glpi-kb", "internal-category"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("index sources=%v want=%v", got, want)
	}
}

func TestValidateAllowsCategoryOnlyGLPIKBSourceWithoutAutoReply(t *testing.T) {
	c := validConfig()
	c.GLPIKBEnabled = true
	c.GLPIKBPath = "auto"
	c.GLPIKBLimit = 100
	c.GLPIKBSyncInterval = 10 * time.Minute
	c.GLPIKBSource = "category-sync"
	c.KnowledgeCategorySources = []string{"category-sync"}
	if err := c.Validate(); err != nil {
		t.Fatalf("category-only GLPI KB source should validate: %v", err)
	}
	c.GLPIKBAutoReply = true
	c.GLPIKBAutoReplyCategoryIDs = []int64{3}
	if err := c.Validate(); err == nil {
		t.Fatal("category-only GLPI KB source must not be allowed for auto-reply")
	}
}

func TestLoadDefaultsCategorySourcesToAllowedSources(t *testing.T) {
	for key, value := range map[string]string{
		"GLPI_URL":                  "https://glpi.internal.example",
		"GLPI_CLIENT_ID":            "client-id",
		"GLPI_CLIENT_SECRET":        "real-secret-value",
		"GLPI_USERNAME":             "svc-agent",
		"GLPI_PASSWORD":             "real-password-value",
		"WEB_ALLOW_ANONYMOUS":       "true",
		"KNOWLEDGE_ALLOWED_SOURCES": "internal-kb,vendor-docs",
	} {
		t.Setenv(key, value)
	}
	old, existed := os.LookupEnv("KNOWLEDGE_CATEGORY_SOURCES")
	if err := os.Unsetenv("KNOWLEDGE_CATEGORY_SOURCES"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("KNOWLEDGE_CATEGORY_SOURCES", old)
		} else {
			_ = os.Unsetenv("KNOWLEDGE_CATEGORY_SOURCES")
		}
	})
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(c.KnowledgeCategorySources) != "[internal-kb vendor-docs]" {
		t.Fatalf("category sources=%v", c.KnowledgeCategorySources)
	}
}

func TestValidateStatusReplyRequiresTemplates(t *testing.T) {
	c := validConfig()
	c.ContextEnabled = true
	c.ContextTimeout = time.Second
	c.UptimeKumaEnabled = true
	c.UptimeKumaURL = "https://uptime.internal.example"
	c.UptimeKumaMode = "metrics"
	c.UptimeKumaAPIKey = "key"
	c.UptimeKumaMaxIssues = 20
	c.ContextStatusReplyEnabled = true
	c.ContextStatusReplyMinRelevance = .5
	c.ContextStatusReplyMinAIConfidence = .8
	c.ContextStatusReplyMinFinalScore = .5
	if err := c.Validate(); err == nil {
		t.Fatal("expected missing status templates to be rejected")
	}
	c.ContextIncidentReplyText = "Störung"
	c.ContextMaintenanceReplyText = "Wartung"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected configured status templates to validate: %v", err)
	}
}

func TestEnvTemplateExpandsNewlines(t *testing.T) {
	t.Setenv("STATUS_TEXT_TEST", `Erste Zeile\nZweite Zeile`)
	if got := envTemplate("STATUS_TEXT_TEST", ""); got != "Erste Zeile\nZweite Zeile" {
		t.Fatalf("unexpected template %q", got)
	}
}

func TestValidatePriorityAndEscalationFailClosed(t *testing.T) {
	c := validConfig()
	c.PriorityEnabled = true
	c.PriorityAllowedReasonCodes = nil
	if err := c.Validate(); err == nil {
		t.Fatal("expected empty priority reason allowlist to be rejected")
	}

	c = validConfig()
	c.PriorityEnabled = true
	c.AutoPriority = true
	c.PriorityAllowedReasonCodes = []string{"core_service_unavailable"}
	c.PriorityMaxIncrease = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected zero automatic priority increase to be rejected")
	}

	c = validConfig()
	c.EscalationEnabled = true
	c.EscalationScanInterval = time.Minute
	c.EscalationMinAge = time.Hour
	c.EscalationMaxLevel = 3
	c.GLPIEscalationLimit = 100
	c.EscalationAllowedReasonCodes = []string{"no_human_response"}
	c.EscalationAllowedActions = []string{"none"}
	c.AutoEscalation = true
	if err := c.Validate(); err == nil {
		t.Fatal("expected automatic escalation without agent ID/implemented action to be rejected")
	}
	c.GLPIAgentUserID = 42
	c.EscalationAllowedActions = []string{"none", "raise_priority"}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected safe escalation config to validate: %v", err)
	}
}

func validEscalationConfig() Config {
	c := validConfig()
	c.EscalationEnabled = true
	c.EscalationScanInterval = 15 * time.Minute
	c.EscalationMinAge = time.Hour
	c.EscalationMinInactivity = 30 * time.Minute
	c.EscalationAnalysisTimeout = 45 * time.Second
	c.EscalationConfidence = .9
	c.EscalationMaxLevel = 4
	c.EscalationSLARiskWindow = 2 * time.Hour
	c.EscalationServiceOwnerMinLevel = 2
	c.EscalationManagerReviewMinLevel = 3
	c.EscalationMajorIncidentMinScore = .5
	c.EscalationAllowedReasonCodes = []string{"no_human_response", "security_incident_suspected", "major_incident_candidate"}
	c.EscalationAllowedActions = []string{"none", "raise_priority"}
	c.GLPIEscalationLimit = 100
	c.GLPIEscalationGroupPatchField = "assigned_groups"
	c.GLPIEscalationUserPatchField = "assigned_users"
	return c
}

func TestValidateEscalationActionTargets(t *testing.T) {
	c := validEscalationConfig()
	c.AutoEscalation = true
	c.GLPIAgentUserID = 42
	c.EscalationAllowedActions = []string{"assign_second_level"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected missing second-level group to be rejected")
	}
	c.EscalationSecondLevelGroupID = 9
	if err := c.Validate(); err != nil {
		t.Fatalf("second-level target should validate: %v", err)
	}

	c = validEscalationConfig()
	c.AutoEscalation = true
	c.GLPIAgentUserID = 42
	c.EscalationAllowedActions = []string{"assign_security_team"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected missing security group to be rejected")
	}
	c.EscalationSecurityGroupID = 10
	if err := c.Validate(); err != nil {
		t.Fatalf("security target should validate: %v", err)
	}
}

func TestValidateEscalationNotificationAndLinkAdapters(t *testing.T) {
	c := validEscalationConfig()
	c.AutoEscalation = true
	c.GLPIAgentUserID = 42
	c.EscalationAllowedActions = []string{"notify_service_owner", "request_manager_review"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected missing owner/manager targets to be rejected")
	}
	c.EscalationWebhookURL = "https://hooks.internal.example/escalation"
	c.EscalationWebhookTimeout = 5 * time.Second
	if err := c.Validate(); err != nil {
		t.Fatalf("webhook-backed notification targets should validate: %v", err)
	}

	c = validEscalationConfig()
	c.AutoEscalation = true
	c.GLPIAgentUserID = 42
	c.EscalationAllowedActions = []string{"link_major_incident"}
	c.ContextEnabled = true
	c.ContextTimeout = time.Second
	c.MajorIncidentsEnabled = true
	c.GLPIMajorIncidentFilter = "status.id==1"
	c.GLPIMajorIncidentLimit = 20
	if err := c.Validate(); err == nil {
		t.Fatal("expected missing ITIL link adapter to be rejected")
	}
	c.GLPIEscalationITILLinkPath = "/ITIL/Link/{{ticket_id}}"
	c.GLPIEscalationITILLinkBody = `{"source":{{ticket_id}},"target":{{major_incident_id}}}`
	if err := c.Validate(); err != nil {
		t.Fatalf("configured ITIL link adapter should validate: %v", err)
	}
}

func TestValidateRejectsUnknownEscalationActionAndUnsafeField(t *testing.T) {
	c := validEscalationConfig()
	c.EscalationAllowedActions = []string{"run_arbitrary_command"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected unknown escalation action to be rejected")
	}
	c = validEscalationConfig()
	c.GLPIEscalationGroupPatchField = "assigned_groups;drop"
	if err := c.Validate(); err == nil {
		t.Fatal("expected unsafe actor field to be rejected")
	}
}

func TestValidateEscalationWebhookRequiresHTTPSByDefault(t *testing.T) {
	c := validEscalationConfig()
	c.EscalationWebhookURL = "http://hooks.internal.example/escalation"
	c.EscalationWebhookTimeout = time.Second
	if err := c.Validate(); err == nil {
		t.Fatal("expected plain HTTP escalation webhook to be rejected")
	}
	c.EscalationWebhookAllowInsecureHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatalf("explicit insecure webhook override should validate: %v", err)
	}
}

func TestValidateEscalationLinkAdapterRequiresSourceAndTarget(t *testing.T) {
	c := validEscalationConfig()
	c.GLPIEscalationITILLinkPath = "/ITIL/Link"
	c.GLPIEscalationITILLinkBody = `{"target":99}`
	if err := c.Validate(); err == nil {
		t.Fatal("expected fixed link adapter without placeholders to be rejected")
	}
	c.GLPIEscalationITILLinkBody = `{"source":{{ticket_id}},"target":{{major_incident_id}}}`
	if err := c.Validate(); err != nil {
		t.Fatalf("valid link adapter rejected: %v", err)
	}
}

func TestValidateOllamaPoolConfiguration(t *testing.T) {
	c := validConfig()
	c.OllamaURLs = []string{"http://ollama-1.internal:11434", "http://ollama-2.internal:11434"}
	c.OllamaNodeNames = []string{"lenovo-1", "lenovo-2"}
	c.OllamaNodeWeights = []int{1, 2}
	c.OllamaNodeMaxInflight = 1
	c.OllamaRoutingMode = "weighted"
	c.OllamaNodeHealthInterval = 10 * time.Second
	c.OllamaNodeFailureCooldown = 30 * time.Second
	c.OllamaNodeRequestTimeout = time.Minute
	c.OllamaFailoverAttempts = 2
	if err := c.Validate(); err != nil {
		t.Fatalf("valid pool rejected: %v", err)
	}

	c.OllamaNodeNames = []string{"only-one"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected mismatched node names to be rejected")
	}

	c.OllamaNodeNames = []string{"duplicate", "duplicate"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected duplicate node names to be rejected")
	}

	c = validConfig()
	c.OllamaURLs = []string{"http://user:secret@ollama-1.internal:11434"}
	c.OllamaNodeMaxInflight = 1
	c.OllamaNodeRequestTimeout = time.Minute
	c.OllamaFailoverAttempts = 1
	if err := c.Validate(); err == nil {
		t.Fatal("expected credentials in Ollama node URL to be rejected")
	}

	c = validConfig()
	c.OllamaURLs = []string{"http://ollama-1.internal:11434", "http://ollama-2.internal:11434"}
	c.OllamaNodeMaxInflight = 1
	c.OllamaNodeRequestTimeout = time.Minute
	c.OllamaRoutingMode = "unknown"
	c.OllamaFailoverAttempts = 2
	if err := c.Validate(); err == nil {
		t.Fatal("expected unknown routing mode to be rejected")
	}
}

func TestValidateGLPIKBAutoReplyUncategorizedRequiresArticleAllowlist(t *testing.T) {
	c := validConfig()
	c.GLPIKBEnabled = true
	c.GLPIKBPath = "auto"
	c.GLPIKBLimit = 100
	c.GLPIKBSyncInterval = 10 * time.Minute
	c.GLPIKBSource = "glpi-kb"
	c.KnowledgeAllowedSources = []string{"internal-kb", "glpi-kb"}
	c.KnowledgeAutoReplySources = []string{"internal-kb", "glpi-kb"}
	c.GLPIKBAutoReply = true
	c.GLPIKBAutoReplyAllowUncategorized = true
	if err := c.Validate(); err == nil {
		t.Fatal("expected uncategorized auto reply to require an explicit article allowlist")
	}
	c.GLPIKBAutoReplyUncategorizedArticleIDs = []int64{1}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected uncategorized auto reply with explicit article allowlist to validate: %v", err)
	}
}

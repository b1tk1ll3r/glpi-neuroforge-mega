package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr string
	DataDir  string
	DryRun   bool
	LogLevel string

	WebUsername       string
	WebPassword       string
	WebAllowAnonymous bool
	ControlReadToken  string
	WebhookSecret     string

	GLPIURL               string
	GLPIAPIVersion        string
	GLPIClientID          string
	GLPIClientSecret      string
	GLPIUsername          string
	GLPIPassword          string
	GLPIPollInterval      time.Duration
	GLPIPollLimit         int
	GLPITicketFilter      string
	GLPITimeout           time.Duration
	GLPIAgentUserID       int64
	GLPIAllowInsecureHTTP bool
	GLPIAllowedStatusIDs  []int64

	OllamaURL                   string // legacy single-node value
	OllamaAPIKey                string // optional Bearer token sent to every Ollama node
	OllamaURLs                  []string
	OllamaNodeNames             []string
	OllamaNodeWeights           []int
	OllamaModel                 string
	OllamaEmbeddingModel        string
	OllamaTimeout               time.Duration
	OllamaNumPredict            int
	OllamaNumCtx                int
	OllamaKeepAlive             time.Duration
	OllamaThink                 bool
	OllamaMaxConcurrent         int // legacy alias for per-node concurrency
	OllamaNodeMaxInflight       int
	OllamaRoutingMode           string
	OllamaNodeHealthInterval    time.Duration
	OllamaNodeFailureCooldown   time.Duration
	OllamaNodeRequestTimeout    time.Duration
	OllamaFailoverEnabled       bool
	OllamaFailoverAttempts      int
	OllamaRequireSameDigest     bool
	OllamaRequireEmbeddingModel bool
	OllamaJSONRetries           int

	KnowledgeDir               string
	RAGEnabled                 bool
	KnowledgeTopK              int
	KnowledgeAuditTopK         int
	KnowledgeCandidateMaxGap   float64
	CategoryPromptLimit        int
	KnowledgeAllowedSources    []string
	KnowledgeCategorySources   []string
	KnowledgeAutoReplySources  []string
	KnowledgeWebEditEnabled    bool
	KnowledgeCategoryMode      string
	KnowledgeCategoryMapFile   string
	KnowledgeIgnoreGlobs       []string
	KnowledgeSemanticWeight    float64
	KnowledgeTitleWeight       float64
	KnowledgeLexicalWeight     float64
	KnowledgeKeywordWeight     float64
	KnowledgeCategoryWeight    float64
	KnowledgeEmbeddingProfile  string
	KnowledgeChunkWords        int
	KnowledgeChunkOverlapWords int
	KnowledgeMaxChunksPerDoc   int
	KnowledgeMaxQueryChunks    int
	KnowledgeIndexMode         string
	KnowledgeEmbedBatchSize    int
	KnowledgeIndexScanInterval time.Duration
	KnowledgeVectorBackend     string
	NeuroForgeURL              string
	NeuroForgeAPIKey           string
	NeuroForgeNamespace        string
	NeuroForgeTimeout          time.Duration
	NeuroForgeSearchK          int
	NeuroForgeFailOpen         bool
	GLPIKBEnabled              bool
	GLPIKBPath                 string
	GLPIKBFilter               string
	GLPIKBLimit                int
	GLPIKBSyncInterval         time.Duration
	GLPIKBSyncTimeout          time.Duration
	GLPIKBSource               string
	GLPIKBAutoReply            bool
	GLPIKBAutoReplyCategoryIDs []int64
	// GLPIKBAutoReplyITILCategoryIDs is retained only for configuration
	// compatibility. It no longer participates in Auto-Reply approval. Ticket
	// categories remain a fachliches retrieval/policy signal, not an article
	// release mechanism.
	GLPIKBAutoReplyITILCategoryIDs []int64
	// GLPIKBAutoReplyAllowUncategorized permits synchronized GLPI KB articles
	// without a KB category to be approved only when their concrete KnowbaseItem
	// ID is listed in GLPIKBAutoReplyUncategorizedArticleIDs.
	GLPIKBAutoReplyAllowUncategorized bool
	// GLPIKBAutoReplyUncategorizedArticleIDs is an explicit allowlist of GLPI
	// KnowbaseItem IDs that may use the uncategorized runtime fallback.
	GLPIKBAutoReplyUncategorizedArticleIDs []int64

	LearningEnabled               bool
	LearningMaxExamples           int
	LearningExamplesPerCategory   int
	OutcomeLearningEnabled        bool
	OutcomeLearningFailOpen       bool
	OutcomeLearningMaxOutcomes    int
	OutcomeRetrievalEnabled       bool
	OutcomeRetrievalSearchK       int
	OutcomeRetrievalMinSimilarity float64
	OutcomeRetrievalFailOpen      bool

	CommunicationLanguage   string
	CommunicationStyle      string
	CommunicationSalutation string
	CommunicationClosing    string
	CommunicationSignature  string
	AIContentLabelEnabled   bool

	AutoCategory                       bool
	AutoReply                          bool
	PriorityEnabled                    bool
	AutoPriority                       bool
	PriorityConfidence                 float64
	PriorityAnalysisTimeout            time.Duration
	PriorityMaxIncrease                int64
	PriorityAllowedReasonCodes         []string
	EscalationEnabled                  bool
	AutoEscalation                     bool
	EscalationScanInterval             time.Duration
	EscalationMinAge                   time.Duration
	EscalationMinInactivity            time.Duration
	EscalationAnalysisTimeout          time.Duration
	EscalationConfidence               float64
	EscalationMaxLevel                 int
	EscalationSLARiskWindow            time.Duration
	EscalationServiceOwnerMinLevel     int
	EscalationManagerReviewMinLevel    int
	EscalationMajorIncidentMinScore    float64
	EscalationAllowedReasonCodes       []string
	EscalationAllowedActions           []string
	EscalationSecondLevelGroupID       int64
	EscalationSecurityGroupID          int64
	EscalationServiceOwnerGroupID      int64
	EscalationServiceOwnerUserID       int64
	EscalationManagerReviewGroupID     int64
	EscalationManagerReviewUserID      int64
	EscalationAddPrivateFollowup       bool
	EscalationSecondLevelNote          string
	EscalationSecurityNote             string
	EscalationServiceOwnerNote         string
	EscalationMajorIncidentNote        string
	EscalationManagerReviewNote        string
	EscalationWebhookURL               string
	EscalationWebhookBearerToken       string
	EscalationWebhookTimeout           time.Duration
	EscalationWebhookAllowInsecureHTTP bool
	GLPIEscalationGroupPatchField      string
	GLPIEscalationUserPatchField       string
	GLPIEscalationITILLinkPath         string
	GLPIEscalationITILLinkBody         string
	GLPIEscalationFilter               string
	GLPIEscalationLimit                int
	CategoryConfidence                 float64
	ReplyConfidence                    float64
	KnowledgeMinScore                  float64
	KnowledgeRetrievalFloor            float64
	KnowledgeEvidenceRetrievalWeight   float64
	KnowledgeEvidenceAIWeight          float64
	KnowledgeEvidenceCategoryWeight    float64

	ContextEnabled                    bool
	ContextTimeout                    time.Duration
	ContextRelevanceMinScore          float64
	ContextBlockReplyOnError          bool
	ContextBlockReplyOnIncident       bool
	ContextStatusReplyEnabled         bool
	ContextStatusReplyMinRelevance    float64
	ContextStatusReplyMinAIConfidence float64
	ContextStatusReplyMinFinalScore   float64
	ContextIncidentReplyText          string
	ContextMaintenanceReplyText       string
	ChangeCalendarEnabled             bool
	GLPIChangePath                    string
	GLPIChangeFilter                  string
	GLPIChangeLimit                   int
	ChangeLookback                    time.Duration
	ChangeLookahead                   time.Duration
	MajorIncidentsEnabled             bool
	GLPIMajorIncidentFilter           string
	GLPIMajorIncidentLimit            int
	UserDeviceContextEnabled          bool
	GLPIUserDevicePaths               []string
	GLPIUserDeviceFilterTemplate      string
	GLPIUserDeviceLimit               int
	UptimeKumaEnabled                 bool
	UptimeKumaURL                     string
	UptimeKumaMode                    string
	UptimeKumaAPIKey                  string
	UptimeKumaStatusPages             []string
	UptimeKumaTimeout                 time.Duration
	UptimeKumaMaxIssues               int
	UptimeKumaIncludeMaintenance      bool

	QueueSize int
	Workers   int
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:                               env("HTTP_ADDR", ":8080"),
		DataDir:                                env("DATA_DIR", "./data"),
		DryRun:                                 envBool("DRY_RUN", true),
		LogLevel:                               env("LOG_LEVEL", "info"),
		WebUsername:                            os.Getenv("WEB_USERNAME"),
		WebPassword:                            os.Getenv("WEB_PASSWORD"),
		WebAllowAnonymous:                      envBool("WEB_ALLOW_ANONYMOUS", false),
		ControlReadToken:                       strings.TrimSpace(os.Getenv("CONTROL_READ_TOKEN")),
		WebhookSecret:                          os.Getenv("WEBHOOK_SECRET"),
		GLPIURL:                                strings.TrimRight(os.Getenv("GLPI_URL"), "/"),
		GLPIAPIVersion:                         env("GLPI_API_VERSION", "v2.3"),
		GLPIClientID:                           os.Getenv("GLPI_CLIENT_ID"),
		GLPIClientSecret:                       os.Getenv("GLPI_CLIENT_SECRET"),
		GLPIUsername:                           os.Getenv("GLPI_USERNAME"),
		GLPIPassword:                           os.Getenv("GLPI_PASSWORD"),
		GLPIPollInterval:                       envDuration("GLPI_POLL_INTERVAL", 30*time.Second),
		GLPIPollLimit:                          envInt("GLPI_POLL_LIMIT", 50),
		GLPITicketFilter:                       os.Getenv("GLPI_TICKET_FILTER"),
		GLPITimeout:                            envDuration("GLPI_TIMEOUT", 20*time.Second),
		GLPIAgentUserID:                        envInt64("GLPI_AGENT_USER_ID", 0),
		GLPIAllowInsecureHTTP:                  envBool("GLPI_ALLOW_INSECURE_HTTP", false),
		GLPIAllowedStatusIDs:                   envInt64List("GLPI_ALLOWED_STATUS_IDS", "1"),
		OllamaURL:                              strings.TrimRight(env("OLLAMA_URL", "http://ollama:11434"), "/"),
		OllamaAPIKey:                           strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")),
		OllamaURLs:                             envStringListPreserveCase("OLLAMA_URLS", ""),
		OllamaNodeNames:                        envStringListPreserveCase("OLLAMA_NODE_NAMES", ""),
		OllamaNodeWeights:                      envIntListAllowEmpty("OLLAMA_NODE_WEIGHTS"),
		OllamaModel:                            env("OLLAMA_MODEL", "qwen3:8b"),
		OllamaEmbeddingModel:                   env("OLLAMA_EMBEDDING_MODEL", "embeddinggemma"),
		OllamaTimeout:                          envDuration("OLLAMA_TIMEOUT", 10*time.Minute),
		OllamaNumPredict:                       envInt("OLLAMA_NUM_PREDICT", 768),
		OllamaNumCtx:                           envInt("OLLAMA_NUM_CTX", 8192),
		OllamaKeepAlive:                        envDuration("OLLAMA_KEEP_ALIVE", 10*time.Minute),
		OllamaThink:                            envBool("OLLAMA_THINK", false),
		OllamaMaxConcurrent:                    envInt("OLLAMA_MAX_CONCURRENT", 1),
		OllamaNodeMaxInflight:                  envInt("OLLAMA_NODE_MAX_INFLIGHT", 0),
		OllamaRoutingMode:                      envNormalizedLower("OLLAMA_ROUTING_MODE", "least_inflight"),
		OllamaNodeHealthInterval:               envDuration("OLLAMA_NODE_HEALTH_INTERVAL", 15*time.Second),
		OllamaNodeFailureCooldown:              envDuration("OLLAMA_NODE_FAILURE_COOLDOWN", 30*time.Second),
		OllamaNodeRequestTimeout:               envDuration("OLLAMA_NODE_REQUEST_TIMEOUT", 0),
		OllamaFailoverEnabled:                  envBool("OLLAMA_FAILOVER_ENABLED", true),
		OllamaFailoverAttempts:                 envInt("OLLAMA_FAILOVER_ATTEMPTS", 0),
		OllamaRequireSameDigest:                envBool("OLLAMA_REQUIRE_SAME_MODEL_DIGEST", true),
		OllamaRequireEmbeddingModel:            envBool("OLLAMA_REQUIRE_EMBEDDING_MODEL", true),
		OllamaJSONRetries:                      envInt("OLLAMA_JSON_RETRIES", 1),
		KnowledgeDir:                           env("KNOWLEDGE_DIR", "./knowledge"),
		RAGEnabled:                             envBool("RAG_ENABLED", true),
		KnowledgeTopK:                          envInt("KNOWLEDGE_TOP_K", 6),
		KnowledgeAuditTopK:                     envInt("KNOWLEDGE_AUDIT_TOP_K", 10),
		KnowledgeCandidateMaxGap:               envFloat("KNOWLEDGE_CANDIDATE_MAX_GAP", 0.20),
		CategoryPromptLimit:                    envInt("CATEGORY_PROMPT_LIMIT", 80),
		KnowledgeAllowedSources:                envStringList("KNOWLEDGE_ALLOWED_SOURCES", "internal-kb"),
		KnowledgeCategorySources:               envStringList("KNOWLEDGE_CATEGORY_SOURCES", ""),
		KnowledgeAutoReplySources:              envStringList("KNOWLEDGE_AUTO_REPLY_SOURCES", "internal-kb"),
		KnowledgeWebEditEnabled:                envBool("KNOWLEDGE_WEB_EDIT_ENABLED", false),
		KnowledgeCategoryMode:                  envNormalizedLower("KNOWLEDGE_CATEGORY_MODE", "unscoped"),
		KnowledgeCategoryMapFile:               strings.TrimSpace(os.Getenv("KNOWLEDGE_CATEGORY_MAP_FILE")),
		KnowledgeIgnoreGlobs:                   envStringListPreserveCase("KNOWLEDGE_IGNORE_GLOBS", ""),
		KnowledgeSemanticWeight:                envFloat("KNOWLEDGE_WEIGHT_SEMANTIC", 0.45),
		KnowledgeTitleWeight:                   envFloat("KNOWLEDGE_WEIGHT_TITLE", 0.20),
		KnowledgeLexicalWeight:                 envFloat("KNOWLEDGE_WEIGHT_LEXICAL", 0.20),
		KnowledgeKeywordWeight:                 envFloat("KNOWLEDGE_WEIGHT_KEYWORDS", 0.075),
		KnowledgeCategoryWeight:                envFloat("KNOWLEDGE_WEIGHT_CATEGORY", 0.075),
		KnowledgeEmbeddingProfile:              envNormalizedLower("KNOWLEDGE_EMBEDDING_PROFILE", "auto"),
		KnowledgeChunkWords:                    envInt("KNOWLEDGE_CHUNK_WORDS", 160),
		KnowledgeChunkOverlapWords:             envInt("KNOWLEDGE_CHUNK_OVERLAP_WORDS", 30),
		KnowledgeMaxChunksPerDoc:               envInt("KNOWLEDGE_MAX_CHUNKS_PER_DOC", 24),
		KnowledgeMaxQueryChunks:                envInt("KNOWLEDGE_MAX_QUERY_CHUNKS", 64),
		KnowledgeIndexMode:                     envNormalizedLower("KNOWLEDGE_INDEX_MODE", "incremental"),
		KnowledgeEmbedBatchSize:                envInt("KNOWLEDGE_EMBED_BATCH_SIZE", 64),
		KnowledgeIndexScanInterval:             envDuration("KNOWLEDGE_INDEX_SCAN_INTERVAL", 5*time.Minute),
		KnowledgeVectorBackend:                 envNormalizedLower("KNOWLEDGE_VECTOR_BACKEND", "local"),
		NeuroForgeURL:                          strings.TrimRight(env("NEUROFORGE_URL", "http://127.0.0.1:8090"), "/"),
		NeuroForgeAPIKey:                       strings.TrimSpace(os.Getenv("NEUROFORGE_API_KEY")),
		NeuroForgeNamespace:                    env("NEUROFORGE_NAMESPACE", "glpi-agent"),
		NeuroForgeTimeout:                      envDuration("NEUROFORGE_TIMEOUT", 15*time.Second),
		NeuroForgeSearchK:                      envInt("NEUROFORGE_SEARCH_K", 128),
		NeuroForgeFailOpen:                     envBool("NEUROFORGE_FAIL_OPEN", true),
		GLPIKBEnabled:                          envBool("GLPI_KB_ENABLED", false),
		GLPIKBPath:                             env("GLPI_KB_PATH", "auto"),
		GLPIKBFilter:                           strings.TrimSpace(os.Getenv("GLPI_KB_FILTER")),
		GLPIKBLimit:                            envInt("GLPI_KB_LIMIT", 500),
		GLPIKBSyncInterval:                     envDuration("GLPI_KB_SYNC_INTERVAL", 10*time.Minute),
		GLPIKBSyncTimeout:                      envDuration("GLPI_KB_SYNC_TIMEOUT", 30*time.Minute),
		GLPIKBSource:                           strings.ToLower(env("GLPI_KB_SOURCE", "glpi-kb")),
		GLPIKBAutoReply:                        envBool("GLPI_KB_AUTO_REPLY", false),
		GLPIKBAutoReplyCategoryIDs:             envInt64ListAllowEmpty("GLPI_KB_AUTO_REPLY_CATEGORY_IDS"),
		GLPIKBAutoReplyITILCategoryIDs:         envInt64ListAllowEmpty("GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS"),
		GLPIKBAutoReplyAllowUncategorized:      envBool("GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED", false),
		GLPIKBAutoReplyUncategorizedArticleIDs: envInt64ListAllowEmpty("GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS"),
		LearningEnabled:                        envBool("LEARNING_ENABLED", true),
		LearningMaxExamples:                    envInt("LEARNING_MAX_EXAMPLES", 500),
		LearningExamplesPerCategory:            envInt("LEARNING_EXAMPLES_PER_CATEGORY", 5),
		OutcomeLearningEnabled:                 envBool("OUTCOME_LEARNING_ENABLED", true),
		OutcomeLearningFailOpen:                envBool("OUTCOME_LEARNING_FAIL_OPEN", false),
		OutcomeLearningMaxOutcomes:             envInt("OUTCOME_LEARNING_MAX_OUTCOMES", 2000),
		OutcomeRetrievalEnabled:                envBool("OUTCOME_RETRIEVAL_ENABLED", true),
		OutcomeRetrievalSearchK:                envInt("OUTCOME_RETRIEVAL_SEARCH_K", 6),
		OutcomeRetrievalMinSimilarity:          envFloat("OUTCOME_RETRIEVAL_MIN_SIMILARITY", 0.58),
		OutcomeRetrievalFailOpen:               envBool("OUTCOME_RETRIEVAL_FAIL_OPEN", true),
		CommunicationLanguage:                  env("COMMUNICATION_LANGUAGE", "de-DE"),
		CommunicationStyle:                     strings.ToLower(env("COMMUNICATION_STYLE", "formal")),
		CommunicationSalutation:                env("COMMUNICATION_SALUTATION", "Guten Tag,"),
		CommunicationClosing:                   env("COMMUNICATION_CLOSING", "Mit freundlichen Grüßen"),
		CommunicationSignature:                 env("COMMUNICATION_SIGNATURE", "IT-Service"),
		AIContentLabelEnabled:                  envBool("AI_CONTENT_LABEL_ENABLED", true),
		AutoCategory:                           envBool("AUTO_CATEGORY", true),
		AutoReply:                              envBool("AUTO_REPLY", false),
		PriorityEnabled:                        envBool("PRIORITY_ENABLED", true),
		AutoPriority:                           envBool("AUTO_PRIORITY", false),
		PriorityConfidence:                     envFloat("PRIORITY_CONFIDENCE", 0.88),
		PriorityAnalysisTimeout:                envDuration("PRIORITY_ANALYSIS_TIMEOUT", 45*time.Second),
		PriorityMaxIncrease:                    envInt64("PRIORITY_MAX_INCREASE", 1),
		PriorityAllowedReasonCodes:             envStringList("PRIORITY_ALLOWED_REASON_CODES", "multiple_users_affected,site_affected,organization_affected,core_service_unavailable,security_incident_suspected,data_loss_possible,legal_or_regulatory_risk,business_deadline,no_workaround,safety_relevant,exam_or_event_critical"),
		EscalationEnabled:                      envBool("ESCALATION_ENABLED", false),
		AutoEscalation:                         envBool("AUTO_ESCALATION", false),
		EscalationScanInterval:                 envDuration("ESCALATION_SCAN_INTERVAL", 15*time.Minute),
		EscalationMinAge:                       envDuration("ESCALATION_MIN_AGE", 4*time.Hour),
		EscalationMinInactivity:                envDuration("ESCALATION_MIN_INACTIVITY", 2*time.Hour),
		EscalationAnalysisTimeout:              envDuration("ESCALATION_ANALYSIS_TIMEOUT", 45*time.Second),
		EscalationConfidence:                   envFloat("ESCALATION_CONFIDENCE", 0.88),
		EscalationMaxLevel:                     envInt("ESCALATION_MAX_LEVEL", 3),
		EscalationSLARiskWindow:                envDuration("ESCALATION_SLA_RISK_WINDOW", 2*time.Hour),
		EscalationServiceOwnerMinLevel:         envInt("ESCALATION_SERVICE_OWNER_MIN_LEVEL", 2),
		EscalationManagerReviewMinLevel:        envInt("ESCALATION_MANAGER_REVIEW_MIN_LEVEL", 3),
		EscalationMajorIncidentMinScore:        envFloat("ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE", 0.50),
		EscalationAllowedReasonCodes:           envStringList("ESCALATION_ALLOWED_REASON_CODES", "no_human_response,sla_at_risk,sla_breached,business_deadline,no_workaround,security_incident_suspected,unassigned,major_incident_candidate"),
		EscalationAllowedActions:               envStringList("ESCALATION_ALLOWED_ACTIONS", "none,raise_priority"),
		EscalationSecondLevelGroupID:           envInt64("ESCALATION_SECOND_LEVEL_GROUP_ID", 0),
		EscalationSecurityGroupID:              envInt64("ESCALATION_SECURITY_GROUP_ID", 0),
		EscalationServiceOwnerGroupID:          envInt64("ESCALATION_SERVICE_OWNER_GROUP_ID", 0),
		EscalationServiceOwnerUserID:           envInt64("ESCALATION_SERVICE_OWNER_USER_ID", 0),
		EscalationManagerReviewGroupID:         envInt64("ESCALATION_MANAGER_REVIEW_GROUP_ID", 0),
		EscalationManagerReviewUserID:          envInt64("ESCALATION_MANAGER_REVIEW_USER_ID", 0),
		EscalationAddPrivateFollowup:           envBool("ESCALATION_ADD_PRIVATE_FOLLOWUP", true),
		EscalationSecondLevelNote:              envTemplate("ESCALATION_SECOND_LEVEL_NOTE", "Automatische Eskalation Stufe {{level}}: Übergabe an den Second-Level-Support. Gründe: {{reason_codes}}. KI-Begründung: {{reason}}"),
		EscalationSecurityNote:                 envTemplate("ESCALATION_SECURITY_NOTE", "Automatische Eskalation Stufe {{level}}: Übergabe an das Security-Team. Gründe: {{reason_codes}}. KI-Begründung: {{reason}}"),
		EscalationServiceOwnerNote:             envTemplate("ESCALATION_SERVICE_OWNER_NOTE", "Automatische Eskalation Stufe {{level}}: Service Owner wurde zur Prüfung einbezogen. Gründe: {{reason_codes}}. KI-Begründung: {{reason}}"),
		EscalationMajorIncidentNote:            envTemplate("ESCALATION_MAJOR_INCIDENT_NOTE", "Automatische Eskalation Stufe {{level}}: Verknüpfung mit Major Incident #{{major_incident_id}} ({{major_incident_name}}). Relevanz: {{major_incident_score}}. Gründe: {{reason_codes}}."),
		EscalationManagerReviewNote:            envTemplate("ESCALATION_MANAGER_REVIEW_NOTE", "Automatische Eskalation Stufe {{level}}: Management-Review angefordert. Gründe: {{reason_codes}}. KI-Begründung: {{reason}}"),
		EscalationWebhookURL:                   strings.TrimSpace(os.Getenv("ESCALATION_WEBHOOK_URL")),
		EscalationWebhookBearerToken:           strings.TrimSpace(os.Getenv("ESCALATION_WEBHOOK_BEARER_TOKEN")),
		EscalationWebhookTimeout:               envDuration("ESCALATION_WEBHOOK_TIMEOUT", 10*time.Second),
		EscalationWebhookAllowInsecureHTTP:     envBool("ESCALATION_WEBHOOK_ALLOW_INSECURE_HTTP", false),
		GLPIEscalationGroupPatchField:          env("GLPI_ESCALATION_GROUP_PATCH_FIELD", "assigned_groups"),
		GLPIEscalationUserPatchField:           env("GLPI_ESCALATION_USER_PATCH_FIELD", "assigned_users"),
		GLPIEscalationITILLinkPath:             strings.TrimSpace(os.Getenv("GLPI_ESCALATION_ITIL_LINK_PATH")),
		GLPIEscalationITILLinkBody:             envTemplate("GLPI_ESCALATION_ITIL_LINK_BODY", ""),
		GLPIEscalationFilter:                   strings.TrimSpace(os.Getenv("GLPI_ESCALATION_FILTER")),
		GLPIEscalationLimit:                    envInt("GLPI_ESCALATION_LIMIT", 100),
		CategoryConfidence:                     envFloat("CATEGORY_CONFIDENCE", 0.90),
		ReplyConfidence:                        envFloat("REPLY_CONFIDENCE", 0.97),
		KnowledgeMinScore:                      envFloat("KNOWLEDGE_MIN_SCORE", 0.70),
		KnowledgeRetrievalFloor:                envFloat("KNOWLEDGE_RETRIEVAL_FLOOR", 0.30),
		KnowledgeEvidenceRetrievalWeight:       envFloat("KNOWLEDGE_EVIDENCE_WEIGHT_RETRIEVAL", 0.45),
		KnowledgeEvidenceAIWeight:              envFloat("KNOWLEDGE_EVIDENCE_WEIGHT_AI", 0.35),
		KnowledgeEvidenceCategoryWeight:        envFloat("KNOWLEDGE_EVIDENCE_WEIGHT_CATEGORY", 0.20),

		ContextEnabled:                    envBool("CONTEXT_ENABLED", true),
		ContextTimeout:                    envDuration("CONTEXT_TIMEOUT", 12*time.Second),
		ContextRelevanceMinScore:          envFloat("CONTEXT_RELEVANCE_MIN_SCORE", 0.20),
		ContextBlockReplyOnError:          envBool("CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS", true),
		ContextBlockReplyOnIncident:       envBool("CONTEXT_BLOCK_AUTO_REPLY_ON_INCIDENT", true),
		ContextStatusReplyEnabled:         envBool("CONTEXT_STATUS_REPLY_ENABLED", false),
		ContextStatusReplyMinRelevance:    envFloat("CONTEXT_STATUS_REPLY_MIN_RELEVANCE", 0.50),
		ContextStatusReplyMinAIConfidence: envFloat("CONTEXT_STATUS_REPLY_MIN_AI_CONFIDENCE", 0.80),
		ContextStatusReplyMinFinalScore:   envFloat("CONTEXT_STATUS_REPLY_MIN_FINAL_SCORE", 0.45),
		ContextIncidentReplyText:          envTemplate("CONTEXT_INCIDENT_REPLY_TEXT", ""),
		ContextMaintenanceReplyText:       envTemplate("CONTEXT_MAINTENANCE_REPLY_TEXT", ""),
		ChangeCalendarEnabled:             envBool("CHANGE_CALENDAR_ENABLED", true),
		GLPIChangePath:                    env("GLPI_CHANGE_PATH", "/Assistance/Change"),
		GLPIChangeFilter:                  os.Getenv("GLPI_CHANGE_FILTER"),
		GLPIChangeLimit:                   envInt("GLPI_CHANGE_LIMIT", 100),
		ChangeLookback:                    envDuration("CHANGE_LOOKBACK", 48*time.Hour),
		ChangeLookahead:                   envDuration("CHANGE_LOOKAHEAD", 24*time.Hour),
		MajorIncidentsEnabled:             envBool("MAJOR_INCIDENTS_ENABLED", false),
		GLPIMajorIncidentFilter:           strings.TrimSpace(os.Getenv("GLPI_MAJOR_INCIDENT_FILTER")),
		GLPIMajorIncidentLimit:            envInt("GLPI_MAJOR_INCIDENT_LIMIT", 20),
		UserDeviceContextEnabled:          envBool("USER_DEVICE_CONTEXT_ENABLED", true),
		GLPIUserDevicePaths:               envPathList("GLPI_USER_DEVICE_PATHS", "/Assets/Computer"),
		GLPIUserDeviceFilterTemplate:      env("GLPI_USER_DEVICE_FILTER_TEMPLATE", "user.id=={{user_id}}"),
		GLPIUserDeviceLimit:               envInt("GLPI_USER_DEVICE_LIMIT", 20),
		UptimeKumaEnabled:                 envBool("UPTIME_KUMA_ENABLED", false),
		UptimeKumaURL:                     strings.TrimRight(os.Getenv("UPTIME_KUMA_URL"), "/"),
		UptimeKumaMode:                    envNormalizedLower("UPTIME_KUMA_MODE", "metrics"),
		UptimeKumaAPIKey:                  os.Getenv("UPTIME_KUMA_API_KEY"),
		UptimeKumaStatusPages:             envStringListPreserveCase("UPTIME_KUMA_STATUS_PAGES", ""),
		UptimeKumaTimeout:                 envDuration("UPTIME_KUMA_TIMEOUT", 10*time.Second),
		UptimeKumaMaxIssues:               envInt("UPTIME_KUMA_MAX_ISSUES", 20),
		UptimeKumaIncludeMaintenance:      envBool("UPTIME_KUMA_INCLUDE_MAINTENANCE", true),

		QueueSize: envInt("QUEUE_SIZE", 256),
		Workers:   envInt("WORKERS", 2),
	}
	if len(c.OllamaURLs) == 0 {
		c.OllamaURLs = []string{c.OllamaURL}
	}
	for i := range c.OllamaURLs {
		c.OllamaURLs[i] = strings.TrimRight(strings.TrimSpace(c.OllamaURLs[i]), "/")
	}
	if c.OllamaNodeMaxInflight == 0 {
		c.OllamaNodeMaxInflight = c.OllamaMaxConcurrent
	}
	if c.OllamaNodeRequestTimeout == 0 {
		c.OllamaNodeRequestTimeout = c.OllamaTimeout
	}
	if c.OllamaFailoverAttempts == 0 {
		c.OllamaFailoverAttempts = len(c.OllamaURLs)
	}
	// Backwards compatibility: without an explicit category source list, the
	// same sources used for normal knowledge retrieval also inform classification.
	if _, configured := os.LookupEnv("KNOWLEDGE_CATEGORY_SOURCES"); !configured {
		c.KnowledgeCategorySources = append([]string(nil), c.KnowledgeAllowedSources...)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	var missing []string
	for name, value := range map[string]string{
		"GLPI_URL":           c.GLPIURL,
		"GLPI_CLIENT_ID":     c.GLPIClientID,
		"GLPI_CLIENT_SECRET": c.GLPIClientSecret,
		"GLPI_USERNAME":      c.GLPIUsername,
		"GLPI_PASSWORD":      c.GLPIPassword,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if !c.WebAllowAnonymous && (c.WebUsername == "" || c.WebPassword == "") {
		return errors.New("WEB_USERNAME and WEB_PASSWORD are required unless WEB_ALLOW_ANONYMOUS=true")
	}
	if !c.WebAllowAnonymous && len(c.WebPassword) < 12 {
		return errors.New("WEB_PASSWORD must contain at least 12 characters")
	}
	if !c.WebAllowAnonymous && isPlaceholder(c.WebPassword) {
		return errors.New("WEB_PASSWORD still contains a CHANGE_ME placeholder")
	}
	if c.ControlReadToken != "" && len(c.ControlReadToken) < 24 {
		return errors.New("CONTROL_READ_TOKEN must contain at least 24 characters when enabled")
	}
	if c.ControlReadToken != "" && isPlaceholder(c.ControlReadToken) {
		return errors.New("CONTROL_READ_TOKEN still contains a CHANGE_ME placeholder")
	}
	if c.WebhookSecret != "" && len(c.WebhookSecret) < 24 {
		return errors.New("WEBHOOK_SECRET must contain at least 24 characters when enabled")
	}
	if c.WebhookSecret != "" && isPlaceholder(c.WebhookSecret) {
		return errors.New("WEBHOOK_SECRET still contains a CHANGE_ME placeholder")
	}
	for name, value := range map[string]string{"GLPI_CLIENT_ID": c.GLPIClientID, "GLPI_CLIENT_SECRET": c.GLPIClientSecret, "GLPI_PASSWORD": c.GLPIPassword} {
		if isPlaceholder(value) {
			return fmt.Errorf("%s still contains a CHANGE_ME placeholder", name)
		}
	}
	u, err := url.Parse(c.GLPIURL)
	if err != nil || u.Host == "" {
		return errors.New("GLPI_URL must be a valid absolute URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("GLPI_URL scheme must be http or https")
	}
	if u.Scheme == "http" && !c.GLPIAllowInsecureHTTP {
		return errors.New("GLPI_URL must use https unless GLPI_ALLOW_INSECURE_HTTP=true")
	}
	if c.OllamaTimeout <= 0 {
		return errors.New("OLLAMA_TIMEOUT must be > 0")
	}
	if c.OllamaNumPredict <= 0 || c.OllamaNumPredict > 4096 {
		return errors.New("OLLAMA_NUM_PREDICT must be between 1 and 4096")
	}
	if c.OllamaNumCtx != 0 && (c.OllamaNumCtx < 2048 || c.OllamaNumCtx > 1048576) {
		return errors.New("OLLAMA_NUM_CTX must be 0 or between 2048 and 1048576")
	}
	if c.OllamaKeepAlive < 0 {
		return errors.New("OLLAMA_KEEP_ALIVE must be >= 0")
	}
	if c.OllamaMaxConcurrent <= 0 || c.OllamaMaxConcurrent > 32 {
		return errors.New("OLLAMA_MAX_CONCURRENT must be between 1 and 32")
	}
	ollamaURLs := append([]string(nil), c.OllamaURLs...)
	if len(ollamaURLs) == 0 {
		legacyURL := strings.TrimSpace(c.OllamaURL)
		if legacyURL == "" {
			legacyURL = "http://ollama:11434"
		}
		ollamaURLs = []string{legacyURL}
	}
	if len(ollamaURLs) > 64 {
		return errors.New("OLLAMA_URLS supports at most 64 nodes")
	}
	seenOllamaURLs := map[string]struct{}{}
	for _, raw := range ollamaURLs {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid Ollama node URL %q", raw)
		}
		key := strings.TrimRight(u.String(), "/")
		if _, ok := seenOllamaURLs[key]; ok {
			return fmt.Errorf("duplicate Ollama node URL %q", key)
		}
		seenOllamaURLs[key] = struct{}{}
	}
	if len(c.OllamaNodeNames) > 0 && len(c.OllamaNodeNames) != len(ollamaURLs) {
		return errors.New("OLLAMA_NODE_NAMES must contain exactly one name per OLLAMA_URLS entry")
	}
	seenOllamaNames := map[string]struct{}{}
	for _, rawName := range c.OllamaNodeNames {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return errors.New("OLLAMA_NODE_NAMES entries must not be empty")
		}
		if _, ok := seenOllamaNames[name]; ok {
			return fmt.Errorf("duplicate Ollama node name %q", name)
		}
		seenOllamaNames[name] = struct{}{}
	}
	if len(c.OllamaNodeWeights) > 0 && len(c.OllamaNodeWeights) != len(ollamaURLs) {
		return errors.New("OLLAMA_NODE_WEIGHTS must contain exactly one weight per OLLAMA_URLS entry")
	}
	for _, weight := range c.OllamaNodeWeights {
		if weight < 1 || weight > 100 {
			return errors.New("OLLAMA_NODE_WEIGHTS values must be between 1 and 100")
		}
	}
	nodeMaxInflight := c.OllamaNodeMaxInflight
	if nodeMaxInflight == 0 {
		nodeMaxInflight = c.OllamaMaxConcurrent
	}
	if nodeMaxInflight < 1 || nodeMaxInflight > 32 {
		return errors.New("OLLAMA_NODE_MAX_INFLIGHT must be between 1 and 32")
	}
	routingMode := c.OllamaRoutingMode
	if routingMode == "" {
		routingMode = "least_inflight"
	}
	switch routingMode {
	case "least_inflight", "round_robin", "weighted", "fastest_recent":
	default:
		return errors.New("OLLAMA_ROUTING_MODE must be one of: least_inflight, round_robin, weighted, fastest_recent")
	}
	if c.OllamaNodeHealthInterval != 0 && c.OllamaNodeHealthInterval < time.Second {
		return errors.New("OLLAMA_NODE_HEALTH_INTERVAL must be >= 1s")
	}
	if c.OllamaNodeFailureCooldown < 0 {
		return errors.New("OLLAMA_NODE_FAILURE_COOLDOWN must be >= 0")
	}
	nodeRequestTimeout := c.OllamaNodeRequestTimeout
	if nodeRequestTimeout == 0 {
		nodeRequestTimeout = c.OllamaTimeout
	}
	if nodeRequestTimeout <= 0 {
		return errors.New("OLLAMA_NODE_REQUEST_TIMEOUT must be > 0")
	}
	failoverAttempts := c.OllamaFailoverAttempts
	if failoverAttempts == 0 {
		failoverAttempts = len(ollamaURLs)
	}
	if failoverAttempts < 1 || failoverAttempts > len(ollamaURLs) {
		return errors.New("OLLAMA_FAILOVER_ATTEMPTS must be between 1 and the number of configured Ollama nodes")
	}
	if c.OllamaJSONRetries < 0 || c.OllamaJSONRetries > 3 {
		return errors.New("OLLAMA_JSON_RETRIES must be between 0 and 3")
	}
	if c.KnowledgeWebEditEnabled && c.WebAllowAnonymous {
		return errors.New("KNOWLEDGE_WEB_EDIT_ENABLED requires authenticated dashboard access; WEB_ALLOW_ANONYMOUS must be false")
	}
	switch strings.ToLower(strings.TrimSpace(c.KnowledgeCategoryMode)) {
	case "", "unscoped", "skip", "strict":
	default:
		return fmt.Errorf("KNOWLEDGE_CATEGORY_MODE must be one of: unscoped, skip, strict (got %q)", c.KnowledgeCategoryMode)
	}
	for _, pattern := range c.KnowledgeIgnoreGlobs {
		if _, err := filepath.Match(pattern, "probe.json"); err != nil {
			return fmt.Errorf("invalid KNOWLEDGE_IGNORE_GLOBS pattern %q: %w", pattern, err)
		}
	}
	if c.KnowledgeTopK != 0 && (c.KnowledgeTopK < 1 || c.KnowledgeTopK > 20) {
		return errors.New("KNOWLEDGE_TOP_K must be between 1 and 20")
	}
	if c.KnowledgeAuditTopK != 0 {
		if c.KnowledgeAuditTopK > 50 || (c.KnowledgeTopK > 0 && c.KnowledgeAuditTopK < c.KnowledgeTopK) {
			return errors.New("KNOWLEDGE_AUDIT_TOP_K must be >= KNOWLEDGE_TOP_K and <= 50")
		}
	}
	if c.KnowledgeCandidateMaxGap < 0 || c.KnowledgeCandidateMaxGap > 1 {
		return errors.New("KNOWLEDGE_CANDIDATE_MAX_GAP must be between 0 and 1")
	}
	weights := []float64{c.KnowledgeSemanticWeight, c.KnowledgeTitleWeight, c.KnowledgeLexicalWeight, c.KnowledgeKeywordWeight, c.KnowledgeCategoryWeight}
	weightSum := 0.0
	for _, w := range weights {
		if w < 0 || w > 1 {
			return errors.New("KNOWLEDGE_WEIGHT_* values must be between 0 and 1")
		}
		weightSum += w
	}
	// All-zero values are allowed for Config values constructed directly in tests/embedders;
	// the knowledge store then applies its safe defaults. Values loaded from ENV are explicit.
	_ = weightSum
	if c.KnowledgeEmbeddingProfile != "" && c.KnowledgeEmbeddingProfile != "auto" && c.KnowledgeEmbeddingProfile != "plain" && c.KnowledgeEmbeddingProfile != "embeddinggemma" {
		return errors.New("KNOWLEDGE_EMBEDDING_PROFILE must be auto, plain, or embeddinggemma")
	}
	if c.KnowledgeChunkWords != 0 && (c.KnowledgeChunkWords < 40 || c.KnowledgeChunkWords > 1000) {
		return errors.New("KNOWLEDGE_CHUNK_WORDS must be between 40 and 1000")
	}
	if c.KnowledgeChunkOverlapWords < 0 || (c.KnowledgeChunkWords > 0 && c.KnowledgeChunkOverlapWords >= c.KnowledgeChunkWords) {
		return errors.New("KNOWLEDGE_CHUNK_OVERLAP_WORDS must be >= 0 and smaller than KNOWLEDGE_CHUNK_WORDS")
	}
	if c.KnowledgeMaxChunksPerDoc != 0 && (c.KnowledgeMaxChunksPerDoc < 1 || c.KnowledgeMaxChunksPerDoc > 100) {
		return errors.New("KNOWLEDGE_MAX_CHUNKS_PER_DOC must be between 1 and 100")
	}
	if c.KnowledgeMaxQueryChunks != 0 && (c.KnowledgeMaxQueryChunks < 1 || c.KnowledgeMaxQueryChunks > 200) {
		return errors.New("KNOWLEDGE_MAX_QUERY_CHUNKS must be between 1 and 200")
	}
	switch c.KnowledgeIndexMode {
	case "", "incremental", "rebuild", "readonly":
	default:
		return errors.New("KNOWLEDGE_INDEX_MODE must be one of: incremental, rebuild, readonly")
	}
	if c.KnowledgeEmbedBatchSize != 0 && (c.KnowledgeEmbedBatchSize < 1 || c.KnowledgeEmbedBatchSize > 256) {
		return errors.New("KNOWLEDGE_EMBED_BATCH_SIZE must be between 1 and 256")
	}
	if c.KnowledgeIndexScanInterval < 0 {
		return errors.New("KNOWLEDGE_INDEX_SCAN_INTERVAL must be >= 0")
	}
	switch c.KnowledgeVectorBackend {
	case "", "local", "dual", "neuroforge":
	default:
		return fmt.Errorf("KNOWLEDGE_VECTOR_BACKEND must be one of: local, dual, neuroforge (got %q)", c.KnowledgeVectorBackend)
	}
	if c.KnowledgeVectorBackend == "dual" || c.KnowledgeVectorBackend == "neuroforge" {
		if len(c.NeuroForgeAPIKey) < 24 {
			return errors.New("NEUROFORGE_API_KEY must contain at least 24 characters for dual/neuroforge mode")
		}
		if isPlaceholder(c.NeuroForgeAPIKey) {
			return errors.New("NEUROFORGE_API_KEY still contains a CHANGE_ME placeholder")
		}
		u, err := url.Parse(c.NeuroForgeURL)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("NEUROFORGE_URL must be an absolute http(s) URL")
		}
		if c.NeuroForgeSearchK < 1 || c.NeuroForgeSearchK > 500 {
			return errors.New("NEUROFORGE_SEARCH_K must be between 1 and 500")
		}
		if c.NeuroForgeTimeout <= 0 {
			return errors.New("NEUROFORGE_TIMEOUT must be positive")
		}
	}
	if c.LearningEnabled {
		if c.LearningMaxExamples < 1 || c.LearningMaxExamples > 10000 {
			return errors.New("LEARNING_MAX_EXAMPLES must be between 1 and 10000")
		}
		if c.LearningExamplesPerCategory < 1 || c.LearningExamplesPerCategory > 20 {
			return errors.New("LEARNING_EXAMPLES_PER_CATEGORY must be between 1 and 20")
		}
		if c.OutcomeLearningMaxOutcomes < 1 || c.OutcomeLearningMaxOutcomes > 50000 {
			return errors.New("OUTCOME_LEARNING_MAX_OUTCOMES must be between 1 and 50000")
		}
		if c.OutcomeRetrievalSearchK < 1 || c.OutcomeRetrievalSearchK > 50 {
			return errors.New("OUTCOME_RETRIEVAL_SEARCH_K must be between 1 and 50")
		}
		if c.OutcomeRetrievalMinSimilarity < -1 || c.OutcomeRetrievalMinSimilarity > 1 {
			return errors.New("OUTCOME_RETRIEVAL_MIN_SIMILARITY must be between -1 and 1")
		}
	}
	if len(c.GLPIAllowedStatusIDs) == 0 {
		return errors.New("GLPI_ALLOWED_STATUS_IDS must contain at least one positive status id")
	}
	for _, id := range c.GLPIAllowedStatusIDs {
		if id <= 0 {
			return errors.New("GLPI_ALLOWED_STATUS_IDS may contain only positive integers")
		}
	}
	if c.AutoReply && c.GLPIAgentUserID <= 0 {
		return errors.New("GLPI_AGENT_USER_ID must be set when AUTO_REPLY=true")
	}
	if strings.TrimSpace(c.CommunicationLanguage) == "" {
		return errors.New("COMMUNICATION_LANGUAGE must not be empty")
	}
	switch c.CommunicationStyle {
	case "formal", "neutral", "informal":
	default:
		return errors.New("COMMUNICATION_STYLE must be one of: formal, neutral, informal")
	}
	if len(c.KnowledgeAllowedSources) == 0 {
		return errors.New("KNOWLEDGE_ALLOWED_SOURCES must contain at least one source")
	}
	allowedSources := make(map[string]struct{}, len(c.KnowledgeAllowedSources))
	indexedSources := make(map[string]struct{}, len(c.KnowledgeAllowedSources)+len(c.KnowledgeCategorySources))
	for _, source := range c.KnowledgeAllowedSources {
		if strings.TrimSpace(source) == "" {
			return errors.New("KNOWLEDGE_ALLOWED_SOURCES may not contain empty source names")
		}
		normalized := strings.ToLower(strings.TrimSpace(source))
		allowedSources[normalized] = struct{}{}
		indexedSources[normalized] = struct{}{}
	}
	for _, source := range c.KnowledgeCategorySources {
		if strings.TrimSpace(source) == "" {
			return errors.New("KNOWLEDGE_CATEGORY_SOURCES may not contain empty source names")
		}
		indexedSources[strings.ToLower(strings.TrimSpace(source))] = struct{}{}
	}
	for _, source := range c.KnowledgeAutoReplySources {
		if _, ok := allowedSources[strings.ToLower(strings.TrimSpace(source))]; !ok {
			return fmt.Errorf("KNOWLEDGE_AUTO_REPLY_SOURCES source %q is not present in KNOWLEDGE_ALLOWED_SOURCES", source)
		}
	}
	if c.GLPIKBEnabled {
		if c.GLPIKBLimit < 1 || c.GLPIKBLimit > 5000 {
			return errors.New("GLPI_KB_LIMIT must be between 1 and 5000")
		}
		if c.GLPIKBSyncInterval < time.Minute {
			return errors.New("GLPI_KB_SYNC_INTERVAL must be at least 1m")
		}
		if c.GLPIKBSyncTimeout < 0 || (c.GLPIKBSyncTimeout > 0 && c.GLPIKBSyncTimeout < time.Minute) {
			return errors.New("GLPI_KB_SYNC_TIMEOUT must be 0 or at least 1m")
		}
		if c.GLPIKBPath != "auto" && !validAPIPath(c.GLPIKBPath) {
			return errors.New("GLPI_KB_PATH must be 'auto' or an absolute API path")
		}
		if strings.TrimSpace(c.GLPIKBSource) == "" {
			return errors.New("GLPI_KB_SOURCE must not be empty")
		}
		glpiKBSource := strings.ToLower(strings.TrimSpace(c.GLPIKBSource))
		if _, ok := indexedSources[glpiKBSource]; !ok {
			return fmt.Errorf("GLPI_KB_SOURCE %q must be present in KNOWLEDGE_ALLOWED_SOURCES or KNOWLEDGE_CATEGORY_SOURCES", c.GLPIKBSource)
		}
		if c.GLPIKBAutoReplyAllowUncategorized && !c.GLPIKBAutoReply {
			return errors.New("GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true requires GLPI_KB_AUTO_REPLY=true")
		}
		if len(c.GLPIKBAutoReplyUncategorizedArticleIDs) > 0 && !c.GLPIKBAutoReplyAllowUncategorized {
			return errors.New("GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS requires GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true")
		}
		if c.GLPIKBAutoReply {
			if _, ok := allowedSources[glpiKBSource]; !ok {
				return fmt.Errorf("GLPI_KB_SOURCE %q must be present in KNOWLEDGE_ALLOWED_SOURCES when GLPI_KB_AUTO_REPLY=true", c.GLPIKBSource)
			}
			if c.GLPIKBAutoReplyAllowUncategorized && len(c.GLPIKBAutoReplyUncategorizedArticleIDs) == 0 {
				return errors.New("GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true requires GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS")
			}
			if len(c.GLPIKBAutoReplyCategoryIDs) == 0 && (!c.GLPIKBAutoReplyAllowUncategorized || len(c.GLPIKBAutoReplyUncategorizedArticleIDs) == 0) {
				return errors.New("GLPI_KB_AUTO_REPLY=true requires GLPI_KB_AUTO_REPLY_CATEGORY_IDS and/or an explicit uncategorized article allowlist")
			}
			found := false
			for _, source := range c.KnowledgeAutoReplySources {
				if strings.EqualFold(strings.TrimSpace(source), glpiKBSource) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("GLPI_KB_SOURCE %q must be present in KNOWLEDGE_AUTO_REPLY_SOURCES when GLPI_KB_AUTO_REPLY=true", c.GLPIKBSource)
			}
		}
	}
	if c.AutoReply && len(c.KnowledgeAutoReplySources) == 0 {
		return errors.New("KNOWLEDGE_AUTO_REPLY_SOURCES must contain at least one source when AUTO_REPLY=true")
	}
	if c.Workers < 1 || c.QueueSize < 1 {
		return errors.New("WORKERS and QUEUE_SIZE must be >= 1")
	}
	if c.PriorityConfidence < 0 || c.PriorityConfidence > 1 || c.EscalationConfidence < 0 || c.EscalationConfidence > 1 || c.EscalationMajorIncidentMinScore < 0 || c.EscalationMajorIncidentMinScore > 1 {
		return errors.New("PRIORITY_CONFIDENCE, ESCALATION_CONFIDENCE and ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE must be between 0 and 1")
	}
	if c.PriorityAnalysisTimeout < 0 {
		return errors.New("PRIORITY_ANALYSIS_TIMEOUT must be >= 0")
	}
	if c.PriorityMaxIncrease < 0 || c.PriorityMaxIncrease > 5 {
		return errors.New("PRIORITY_MAX_INCREASE must be between 0 and 5")
	}
	if c.AutoPriority && !c.PriorityEnabled {
		return errors.New("AUTO_PRIORITY=true requires PRIORITY_ENABLED=true")
	}
	if c.PriorityEnabled && len(c.PriorityAllowedReasonCodes) == 0 {
		return errors.New("PRIORITY_ALLOWED_REASON_CODES must contain at least one reason when PRIORITY_ENABLED=true")
	}
	if c.AutoPriority && c.PriorityMaxIncrease < 1 {
		return errors.New("PRIORITY_MAX_INCREASE must be at least 1 when AUTO_PRIORITY=true")
	}
	if c.EscalationEnabled {
		if c.EscalationScanInterval < time.Minute {
			return errors.New("ESCALATION_SCAN_INTERVAL must be at least 1m")
		}
		if c.EscalationMinAge < time.Minute {
			return errors.New("ESCALATION_MIN_AGE must be at least 1m")
		}
		if c.EscalationMinInactivity != 0 && c.EscalationMinInactivity < time.Minute {
			return errors.New("ESCALATION_MIN_INACTIVITY must be at least 1m")
		}
		if c.EscalationAnalysisTimeout < 0 {
			return errors.New("ESCALATION_ANALYSIS_TIMEOUT must be >= 0")
		}
		if c.EscalationSLARiskWindow < 0 {
			return errors.New("ESCALATION_SLA_RISK_WINDOW must be >= 0")
		}
		if c.EscalationMaxLevel < 1 || c.EscalationMaxLevel > 4 {
			return errors.New("ESCALATION_MAX_LEVEL must be between 1 and 4")
		}
		if (c.EscalationServiceOwnerMinLevel != 0 && (c.EscalationServiceOwnerMinLevel < 1 || c.EscalationServiceOwnerMinLevel > 4)) || (c.EscalationManagerReviewMinLevel != 0 && (c.EscalationManagerReviewMinLevel < 1 || c.EscalationManagerReviewMinLevel > 4)) {
			return errors.New("ESCALATION_SERVICE_OWNER_MIN_LEVEL and ESCALATION_MANAGER_REVIEW_MIN_LEVEL must be between 1 and 4")
		}
		if c.GLPIEscalationLimit < 1 || c.GLPIEscalationLimit > 1000 {
			return errors.New("GLPI_ESCALATION_LIMIT must be between 1 and 1000")
		}
		if len(c.EscalationAllowedReasonCodes) == 0 {
			return errors.New("ESCALATION_ALLOWED_REASON_CODES must contain at least one reason when ESCALATION_ENABLED=true")
		}
		if len(c.EscalationAllowedActions) == 0 {
			return errors.New("ESCALATION_ALLOWED_ACTIONS must contain at least one action when ESCALATION_ENABLED=true")
		}
		knownActions := map[string]bool{"none": true, "raise_priority": true, "assign_second_level": true, "assign_security_team": true, "notify_service_owner": true, "link_major_incident": true, "request_manager_review": true}
		for _, raw := range c.EscalationAllowedActions {
			action := strings.ToLower(strings.TrimSpace(raw))
			if !knownActions[action] {
				return fmt.Errorf("unknown ESCALATION_ALLOWED_ACTIONS value %q", raw)
			}
		}
		if (strings.TrimSpace(c.GLPIEscalationGroupPatchField) != "" && !safeJSONField(c.GLPIEscalationGroupPatchField)) || (strings.TrimSpace(c.GLPIEscalationUserPatchField) != "" && !safeJSONField(c.GLPIEscalationUserPatchField)) {
			return errors.New("GLPI_ESCALATION_GROUP_PATCH_FIELD and GLPI_ESCALATION_USER_PATCH_FIELD must be simple JSON field names")
		}
		linkPathSet := strings.TrimSpace(c.GLPIEscalationITILLinkPath) != ""
		linkBodySet := strings.TrimSpace(c.GLPIEscalationITILLinkBody) != ""
		if linkPathSet != linkBodySet {
			return errors.New("GLPI_ESCALATION_ITIL_LINK_PATH and GLPI_ESCALATION_ITIL_LINK_BODY must be configured together")
		}
		if linkPathSet {
			if err := validateEscalationLinkAdapter(c.GLPIEscalationITILLinkPath, c.GLPIEscalationITILLinkBody); err != nil {
				return err
			}
		}
		if c.EscalationWebhookURL != "" {
			parsed, err := url.ParseRequestURI(c.EscalationWebhookURL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return errors.New("ESCALATION_WEBHOOK_URL must be an absolute http(s) URL")
			}
			if parsed.Scheme == "http" && !c.EscalationWebhookAllowInsecureHTTP {
				return errors.New("plain HTTP ESCALATION_WEBHOOK_URL requires ESCALATION_WEBHOOK_ALLOW_INSECURE_HTTP=true")
			}
			if c.EscalationWebhookTimeout <= 0 {
				return errors.New("ESCALATION_WEBHOOK_TIMEOUT must be > 0 when ESCALATION_WEBHOOK_URL is set")
			}
		}
	}
	if c.AutoEscalation && !c.EscalationEnabled {
		return errors.New("AUTO_ESCALATION=true requires ESCALATION_ENABLED=true")
	}
	if c.AutoEscalation {
		if c.GLPIAgentUserID <= 0 {
			return errors.New("GLPI_AGENT_USER_ID must be set when AUTO_ESCALATION=true")
		}
		hasExecutableAction := false
		for _, raw := range c.EscalationAllowedActions {
			if action := strings.ToLower(strings.TrimSpace(raw)); action != "" && action != "none" {
				hasExecutableAction = true
			}
		}
		if !hasExecutableAction {
			return errors.New("AUTO_ESCALATION=true requires at least one executable ESCALATION_ALLOWED_ACTIONS value")
		}
		for _, raw := range c.EscalationAllowedActions {
			switch strings.ToLower(strings.TrimSpace(raw)) {
			case "assign_second_level":
				if c.EscalationSecondLevelGroupID <= 0 {
					return errors.New("ESCALATION_SECOND_LEVEL_GROUP_ID is required for assign_second_level")
				}
			case "assign_security_team":
				if c.EscalationSecurityGroupID <= 0 {
					return errors.New("ESCALATION_SECURITY_GROUP_ID is required for assign_security_team")
				}
			case "notify_service_owner":
				if c.EscalationServiceOwnerGroupID <= 0 && c.EscalationServiceOwnerUserID <= 0 && c.EscalationWebhookURL == "" {
					return errors.New("notify_service_owner requires a configured service-owner group/user or ESCALATION_WEBHOOK_URL")
				}
			case "request_manager_review":
				if c.EscalationManagerReviewGroupID <= 0 && c.EscalationManagerReviewUserID <= 0 && c.EscalationWebhookURL == "" {
					return errors.New("request_manager_review requires a configured manager group/user or ESCALATION_WEBHOOK_URL")
				}
			case "link_major_incident":
				if !c.ContextEnabled || !c.MajorIncidentsEnabled {
					return errors.New("link_major_incident requires CONTEXT_ENABLED=true and MAJOR_INCIDENTS_ENABLED=true")
				}
				if c.GLPIEscalationITILLinkPath == "" || c.GLPIEscalationITILLinkBody == "" {
					return errors.New("link_major_incident requires GLPI_ESCALATION_ITIL_LINK_PATH and GLPI_ESCALATION_ITIL_LINK_BODY")
				}
			}
		}
	}
	if c.CategoryConfidence < 0 || c.CategoryConfidence > 1 || c.ReplyConfidence < 0 || c.ReplyConfidence > 1 || c.KnowledgeMinScore < 0 || c.KnowledgeMinScore > 1 || c.KnowledgeRetrievalFloor < 0 || c.KnowledgeRetrievalFloor > 1 || c.ContextRelevanceMinScore < 0 || c.ContextRelevanceMinScore > 1 {
		return errors.New("confidence/score thresholds must be between 0 and 1")
	}
	if c.ContextStatusReplyMinRelevance < 0 || c.ContextStatusReplyMinRelevance > 1 || c.ContextStatusReplyMinAIConfidence < 0 || c.ContextStatusReplyMinAIConfidence > 1 || c.ContextStatusReplyMinFinalScore < 0 || c.ContextStatusReplyMinFinalScore > 1 {
		return errors.New("CONTEXT_STATUS_REPLY_* score thresholds must be between 0 and 1")
	}
	if c.ContextStatusReplyEnabled {
		if !c.ContextEnabled || !c.UptimeKumaEnabled {
			return errors.New("CONTEXT_STATUS_REPLY_ENABLED requires CONTEXT_ENABLED=true and UPTIME_KUMA_ENABLED=true")
		}
		if strings.TrimSpace(c.ContextIncidentReplyText) == "" {
			return errors.New("CONTEXT_INCIDENT_REPLY_TEXT is required when CONTEXT_STATUS_REPLY_ENABLED=true")
		}
		if strings.TrimSpace(c.ContextMaintenanceReplyText) == "" {
			return errors.New("CONTEXT_MAINTENANCE_REPLY_TEXT is required when CONTEXT_STATUS_REPLY_ENABLED=true")
		}
	}
	if c.KnowledgeEvidenceRetrievalWeight < 0 || c.KnowledgeEvidenceAIWeight < 0 || c.KnowledgeEvidenceCategoryWeight < 0 {
		return errors.New("KNOWLEDGE_EVIDENCE_WEIGHT_* values must be >= 0")
	}
	if c.ContextEnabled {
		if c.ContextTimeout <= 0 {
			return errors.New("CONTEXT_TIMEOUT must be > 0")
		}
		if c.ChangeCalendarEnabled {
			if !validAPIPath(c.GLPIChangePath) {
				return errors.New("GLPI_CHANGE_PATH must be an absolute API path such as /Assistance/Change")
			}
			if c.GLPIChangeLimit < 1 || c.GLPIChangeLimit > 1000 {
				return errors.New("GLPI_CHANGE_LIMIT must be between 1 and 1000")
			}
		}
		if c.MajorIncidentsEnabled {
			if c.GLPIMajorIncidentFilter == "" {
				return errors.New("GLPI_MAJOR_INCIDENT_FILTER is required when MAJOR_INCIDENTS_ENABLED=true")
			}
			if c.GLPIMajorIncidentLimit < 1 || c.GLPIMajorIncidentLimit > 500 {
				return errors.New("GLPI_MAJOR_INCIDENT_LIMIT must be between 1 and 500")
			}
		}
		if c.UserDeviceContextEnabled {
			if len(c.GLPIUserDevicePaths) == 0 {
				return errors.New("GLPI_USER_DEVICE_PATHS must contain at least one API path when USER_DEVICE_CONTEXT_ENABLED=true")
			}
			for _, p := range c.GLPIUserDevicePaths {
				if !validAPIPath(p) {
					return fmt.Errorf("invalid GLPI user-device API path %q", p)
				}
			}
			if !strings.Contains(c.GLPIUserDeviceFilterTemplate, "{{user_id}}") {
				return errors.New("GLPI_USER_DEVICE_FILTER_TEMPLATE must contain {{user_id}}")
			}
			if c.GLPIUserDeviceLimit < 1 || c.GLPIUserDeviceLimit > 500 {
				return errors.New("GLPI_USER_DEVICE_LIMIT must be between 1 and 500")
			}
		}
		if c.UptimeKumaEnabled {
			if c.UptimeKumaURL == "" {
				return errors.New("UPTIME_KUMA_URL is required when UPTIME_KUMA_ENABLED=true")
			}
			u, err := url.Parse(c.UptimeKumaURL)
			if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
				return errors.New("UPTIME_KUMA_URL must be an absolute http/https URL")
			}
			switch c.UptimeKumaMode {
			case "metrics":
				if strings.TrimSpace(c.UptimeKumaAPIKey) == "" {
					return errors.New("UPTIME_KUMA_API_KEY is required for UPTIME_KUMA_MODE=metrics")
				}
			case "status_page":
				if len(c.UptimeKumaStatusPages) == 0 {
					return errors.New("UPTIME_KUMA_STATUS_PAGES is required for UPTIME_KUMA_MODE=status_page")
				}
			default:
				return errors.New("UPTIME_KUMA_MODE must be metrics or status_page")
			}
			if c.UptimeKumaMaxIssues < 1 || c.UptimeKumaMaxIssues > 200 {
				return errors.New("UPTIME_KUMA_MAX_ISSUES must be between 1 and 200")
			}
		}
	}
	return nil
}

func validateEscalationLinkAdapter(pathTemplate, bodyTemplate string) error {
	pathTemplate = strings.TrimSpace(pathTemplate)
	bodyTemplate = strings.TrimSpace(bodyTemplate)
	if !validAPIPath(pathTemplate) || strings.ContainsAny(pathTemplate, "?#") {
		return errors.New("GLPI_ESCALATION_ITIL_LINK_PATH must be an absolute API route without query or fragment")
	}
	combined := pathTemplate + "\n" + bodyTemplate
	hasSource := strings.Contains(combined, "{{ticket_id}}") || strings.Contains(combined, "{{source_ticket_id}}")
	hasTarget := strings.Contains(combined, "{{major_incident_id}}") || strings.Contains(combined, "{{target_ticket_id}}")
	if !hasSource || !hasTarget {
		return errors.New("GLPI escalation link adapter must reference both source ticket and major incident placeholders")
	}
	replacer := strings.NewReplacer(
		"{{ticket_id}}", "1",
		"{{source_ticket_id}}", "1",
		"{{major_incident_id}}", "2",
		"{{target_ticket_id}}", "2",
	)
	var body any
	if err := json.Unmarshal([]byte(replacer.Replace(bodyTemplate)), &body); err != nil {
		return fmt.Errorf("GLPI_ESCALATION_ITIL_LINK_BODY must be valid JSON after placeholder expansion: %w", err)
	}
	return nil
}

func validAPIPath(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "/") && !strings.Contains(v, "..") && !strings.ContainsAny(v, "\r\n")
}

func safeJSONField(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envTemplate allows readable one-line .env values while preserving an exact,
// operator-defined message. Only escaped newlines are expanded.
func envTemplate(key, def string) string {
	v := env(key, def)
	v = strings.ReplaceAll(v, `\n`, "\n")
	return strings.TrimSpace(v)
}

func envNormalizedLower(key, def string) string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return strings.ToLower(strings.TrimSpace(def))
	}
	return v
}

// KnowledgeIndexSources returns the union of normal retrieval sources and
// category-only sources. The store indexes this union, while the agent applies
// the narrower source scopes when building the two model input sets.
func (c Config) KnowledgeIndexSources() []string {
	seen := make(map[string]struct{}, len(c.KnowledgeAllowedSources)+len(c.KnowledgeCategorySources))
	out := make([]string, 0, len(c.KnowledgeAllowedSources)+len(c.KnowledgeCategorySources))
	for _, list := range [][]string{c.KnowledgeAllowedSources, c.KnowledgeCategorySources} {
		for _, source := range list {
			normalized := strings.ToLower(strings.TrimSpace(source))
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	return out
}

func isPlaceholder(v string) bool {
	return strings.Contains(strings.ToUpper(strings.TrimSpace(v)), "CHANGE_ME")
}

func envInt64List(key, def string) []int64 {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		raw = def
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 {
			return nil
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func envInt64ListAllowEmpty(key string) []int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	seen := map[int64]struct{}{}
	for _, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 {
			return nil
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func envStringList(key, def string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok {
		raw = def
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		v := strings.ToLower(strings.TrimSpace(part))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func envStringListPreserveCase(key, def string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok {
		raw = def
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func envPathList(key, def string) []string {
	vals := envStringListPreserveCase(key, def)
	for i := range vals {
		vals[i] = "/" + strings.TrimLeft(strings.TrimSpace(vals[i]), "/")
	}
	return vals
}

func envIntListAllowEmpty(key string) []int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, e := strconv.ParseBool(v)
	if e != nil {
		return def
	}
	return b
}
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		return def
	}
	return n
}
func envInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil {
		return def
	}
	return n
}
func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, e := strconv.ParseFloat(v, 64)
	if e != nil {
		return def
	}
	return n
}
func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, e := time.ParseDuration(v)
	if e != nil {
		return def
	}
	return d
}

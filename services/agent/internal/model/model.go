package model

import (
	"encoding/json"
	"time"
)

type LinkedItem struct {
	ItemType string `json:"item_type"`
	ID       int64  `json:"id"`
	Name     string `json:"name,omitempty"`
}

type Ticket struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Content        string       `json:"content"`
	DateCreation   string       `json:"date_creation,omitempty"`
	DateMod        string       `json:"date_mod"`
	StatusID       int64        `json:"status_id"`
	CategoryID     int64        `json:"category_id"`
	Priority       int64        `json:"priority,omitempty"`
	Impact         int64        `json:"impact,omitempty"`
	Urgency        int64        `json:"urgency,omitempty"`
	EntityID       int64        `json:"entity_id,omitempty"`
	LocationID     int64        `json:"location_id,omitempty"`
	TimeToResolve  string       `json:"time_to_resolve,omitempty"`
	RequesterIDs   []int64      `json:"requester_ids,omitempty"`
	AssignedGroups []int64      `json:"assigned_group_ids,omitempty"`
	AssignedUsers  []int64      `json:"assigned_user_ids,omitempty"`
	Items          []LinkedItem `json:"items,omitempty"`
}

type Followup struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	IsPrivate bool   `json:"is_private"`
	UserID    int64  `json:"user_id"`
	Date      string `json:"date"`
}

type Category struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	CompleteName string `json:"completename"`
	// KnowbaseCategoryID is the GLPI knowledge-base category associated with
	// this ITIL category, when the installed GLPI exposes that relation.
	KnowbaseCategoryID int64    `json:"knowbase_category_id,omitempty"`
	Hints              []string `json:"hints,omitempty"`
	Examples           []string `json:"confirmed_examples,omitempty"`
}

type LearningExample struct {
	ID                      string    `json:"id"`
	RunID                   string    `json:"run_id,omitempty"`
	TicketID                int64     `json:"ticket_id,omitempty"`
	Subject                 string    `json:"subject"`
	Text                    string    `json:"text"`
	CategoryID              int64     `json:"category_id"`
	CategoryName            string    `json:"category_name"`
	AIRecommendedCategoryID int64     `json:"ai_recommended_category_id,omitempty"`
	AIConfidence            float64   `json:"ai_confidence,omitempty"`
	Correction              bool      `json:"correction"`
	CreatedAt               time.Time `json:"created_at"`
	Source                  string    `json:"source"`
}

type KnowledgeDoc struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Answer string `json:"answer"`
	// AnswerHTML contains trusted rich text from a synchronized GLPI KB item.
	// It is never sent to the LLM or used for embeddings.
	AnswerHTML string `json:"answer_html,omitempty"`
	AutoReply  bool   `json:"auto_reply"`
	// AutoReplyDecision explains why a synchronized source document is or is
	// not eligible for automatic replies. Local JSON documents may leave this
	// empty because their explicit auto_reply flag is already authoritative.
	AutoReplyDecision          string       `json:"auto_reply_decision,omitempty"`
	AutoReplyDetail            string       `json:"auto_reply_detail,omitempty"`
	MinScore                   float64      `json:"min_score"`
	Categories                 []int64      `json:"categories"`
	ExternalCategories         []string     `json:"external_categories,omitempty"`
	UnmappedExternalCategories []string     `json:"unmapped_external_categories,omitempty"`
	Keywords                   []string     `json:"keywords"`
	Source                     string       `json:"source"`
	SourceURI                  string       `json:"source_uri,omitempty"`
	SourceCategoryIDs          []int64      `json:"source_category_ids,omitempty"`
	SourceModifiedAt           string       `json:"source_modified_at,omitempty"`
	Language                   string       `json:"language"`
	CommunicationStyle         string       `json:"communication_style"`
	LinkedItems                []LinkedItem `json:"linked_items,omitempty"`
}

// GLPIKnowledgeItem is the normalized read-only representation returned by
// the GLPI connector before it is converted into a KnowledgeDoc.
type GLPIKnowledgeItem struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	Content     string       `json:"content"`
	CategoryIDs []int64      `json:"category_ids,omitempty"`
	Language    string       `json:"language,omitempty"`
	ModifiedAt  string       `json:"modified_at,omitempty"`
	LinkedItems []LinkedItem `json:"linked_items,omitempty"`
}

type KnowledgeHit struct {
	Doc                KnowledgeDoc `json:"doc"`
	Score              float64      `json:"score"`
	SemanticScore      float64      `json:"semantic_score,omitempty"`
	TitleScore         float64      `json:"title_score,omitempty"`
	LexicalScore       float64      `json:"lexical_score,omitempty"`
	KeywordScore       float64      `json:"keyword_score,omitempty"`
	CategoryScore      float64      `json:"category_score,omitempty"`
	BestChunkExcerpt   string       `json:"best_chunk_excerpt,omitempty"`
	BestQueryExcerpt   string       `json:"best_query_excerpt,omitempty"`
	QueryChunkCount    int          `json:"query_chunk_count,omitempty"`
	DocumentChunkCount int          `json:"document_chunk_count,omitempty"`
}

// ChangeContext is a normalized, read-only view of a GLPI Change. Only fields
// useful for ticket triage are passed to the model.
type ChangeContext struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Content      string  `json:"content,omitempty"`
	StatusID     int64   `json:"status_id,omitempty"`
	CategoryID   int64   `json:"category_id,omitempty"`
	PlannedBegin string  `json:"planned_begin,omitempty"`
	PlannedEnd   string  `json:"planned_end,omitempty"`
	DateMod      string  `json:"date_mod,omitempty"`
	Relevance    float64 `json:"relevance"`
	Source       string  `json:"source"`
}

type MajorIncidentContext struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Content    string  `json:"content,omitempty"`
	StatusID   int64   `json:"status_id,omitempty"`
	CategoryID int64   `json:"category_id,omitempty"`
	Priority   int64   `json:"priority,omitempty"`
	Impact     int64   `json:"impact,omitempty"`
	Urgency    int64   `json:"urgency,omitempty"`
	DateMod    string  `json:"date_mod,omitempty"`
	Relevance  float64 `json:"relevance"`
	Source     string  `json:"source"`
}

type ServiceIssueCandidate struct {
	ID    string              `json:"id"`
	Issue ServiceIssueContext `json:"issue"`
}

type StatusDecision struct {
	Matched     bool    `json:"matched"`
	CandidateID string  `json:"candidate_id"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason"`
}

type ServiceIssueContext struct {
	Source          string  `json:"source"`
	StatusPage      string  `json:"status_page"`
	Kind            string  `json:"kind"` // monitor | pinned_incident | maintenance
	MonitorID       int64   `json:"monitor_id,omitempty"`
	MonitorName     string  `json:"monitor_name,omitempty"`
	Status          string  `json:"status"`
	Message         string  `json:"message,omitempty"`
	LastHeartbeat   string  `json:"last_heartbeat,omitempty"`
	Uptime24h       float64 `json:"uptime_24h,omitempty"`
	IncidentTitle   string  `json:"incident_title,omitempty"`
	IncidentContent string  `json:"incident_content,omitempty"`
	Relevance       float64 `json:"relevance"`
}

type UserDeviceContext struct {
	UserID            int64  `json:"user_id,omitempty"`
	ItemType          string `json:"item_type"`
	ID                int64  `json:"id"`
	Name              string `json:"name,omitempty"`
	Serial            string `json:"serial,omitempty"`
	InventoryNumber   string `json:"inventory_number,omitempty"`
	Status            string `json:"status,omitempty"`
	Location          string `json:"location,omitempty"`
	LastInventoryDate string `json:"last_inventory_date,omitempty"`
	Source            string `json:"source"`
}

type ValidatedOutcomeEvidence struct {
	MemoryID    string  `json:"memory_id"`
	OutcomeID   string  `json:"outcome_id,omitempty"`
	Decision    string  `json:"decision,omitempty"`
	Text        string  `json:"text"`
	Similarity  float64 `json:"similarity"`
	Source      string  `json:"source"`
	TicketID    string  `json:"ticket_id,omitempty"`
	KnowledgeID string  `json:"knowledge_id,omitempty"`
}

type ContextSnapshot struct {
	FetchedAt         time.Time                  `json:"fetched_at"`
	Changes           []ChangeContext            `json:"changes,omitempty"`
	MajorIncidents    []MajorIncidentContext     `json:"major_incidents,omitempty"`
	ServiceIssues     []ServiceIssueContext      `json:"service_issues,omitempty"`
	UserDevices       []UserDeviceContext        `json:"user_devices,omitempty"`
	Warnings          []string                   `json:"warnings,omitempty"`
	Incomplete        bool                       `json:"incomplete"`
	ValidatedOutcomes []ValidatedOutcomeEvidence `json:"validated_outcomes,omitempty"`
}

func (c ContextSnapshot) HasRelevantIncident(minScore float64) bool {
	for _, incident := range c.MajorIncidents {
		if incident.Relevance >= minScore {
			return true
		}
	}
	for _, issue := range c.ServiceIssues {
		if issue.Relevance >= minScore && (issue.Status == "down" || issue.Status == "pending" || issue.Kind == "pinned_incident") {
			return true
		}
	}
	return false
}

type Decision struct {
	Category struct {
		ID         int64   `json:"id"`
		Confidence float64 `json:"confidence"`
	} `json:"category"`
	Reply struct {
		Allowed     bool    `json:"allowed"`
		Confidence  float64 `json:"confidence"`
		KnowledgeID string  `json:"knowledge_id"`
	} `json:"reply"`
	Reason string `json:"reason"`
}

// RuleCheck is a machine-readable explanation of one deterministic decision gate.
// Status is one of: pass, fail, warn, info, na.
type RuleCheck struct {
	Code     string `json:"code"`
	Group    string `json:"group,omitempty"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Blocking bool   `json:"blocking,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Expected string `json:"expected,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// KnowledgeDiagnostic explains why one knowledge article was or was not
// considered for a specific ticket run. The score is recalculated against the
// current knowledge index; CurrentTicketChanged tells the UI when the ticket has
// changed since the historical run.
type KnowledgeDiagnostic struct {
	RunID                string       `json:"run_id"`
	Purpose              string       `json:"purpose,omitempty"`
	TicketID             int64        `json:"ticket_id"`
	KnowledgeID          string       `json:"knowledge_id"`
	Title                string       `json:"title"`
	Source               string       `json:"source"`
	CurrentTicketChanged bool         `json:"current_ticket_changed"`
	RetrievalRank        int          `json:"retrieval_rank"`
	RetrievalScore       float64      `json:"retrieval_score"`
	SemanticScore        float64      `json:"semantic_score,omitempty"`
	TitleScore           float64      `json:"title_score,omitempty"`
	LexicalScore         float64      `json:"lexical_score,omitempty"`
	KeywordScore         float64      `json:"keyword_score,omitempty"`
	CategoryScore        float64      `json:"category_score,omitempty"`
	CandidateCutoff      float64      `json:"candidate_cutoff,omitempty"`
	SentToAI             bool         `json:"sent_to_ai"`
	SelectionReason      string       `json:"selection_reason"`
	AISelected           bool         `json:"ai_selected"`
	EvidenceScore        float64      `json:"evidence_score,omitempty"`
	RequiredScore        float64      `json:"required_score,omitempty"`
	BestChunkExcerpt     string       `json:"best_chunk_excerpt,omitempty"`
	BestQueryExcerpt     string       `json:"best_query_excerpt,omitempty"`
	ExternalCategories   []string     `json:"external_categories,omitempty"`
	UnmappedCategories   []string     `json:"unmapped_categories,omitempty"`
	Checks               []RuleCheck  `json:"checks"`
	Document             KnowledgeDoc `json:"document"`
}

type PolicyResult struct {
	ChangeCategory             bool        `json:"change_category"`
	CategoryID                 int64       `json:"category_id"`
	CategoryRecommendationID   int64       `json:"category_recommendation_id"`
	CategoryRecommendationName string      `json:"category_recommendation_name,omitempty"`
	CategoryConfidence         float64     `json:"category_confidence"`
	CategoryThreshold          float64     `json:"category_threshold"`
	CategoryDecision           string      `json:"category_decision"`
	Reply                      bool        `json:"reply"`
	ReplyText                  string      `json:"reply_text,omitempty"`
	ReplyIsHTML                bool        `json:"reply_is_html,omitempty"`
	KnowledgeID                string      `json:"knowledge_id,omitempty"`
	ReplyRecommendation        bool        `json:"reply_recommendation"`
	ReplyConfidence            float64     `json:"reply_confidence"`
	ReplyThreshold             float64     `json:"reply_threshold"`
	ReplyKnowledgeID           string      `json:"reply_knowledge_id,omitempty"`
	ReplyDecision              string      `json:"reply_decision"`
	KnowledgeThreshold         float64     `json:"knowledge_threshold,omitempty"`
	KnowledgeRetrievalScore    float64     `json:"knowledge_retrieval_score,omitempty"`
	KnowledgeEvidenceScore     float64     `json:"knowledge_evidence_score,omitempty"`
	KnowledgeRetrievalFloor    float64     `json:"knowledge_retrieval_floor,omitempty"`
	KnowledgeCategoryAligned   bool        `json:"knowledge_category_aligned,omitempty"`
	AIReason                   string      `json:"ai_reason,omitempty"`
	CategoryChecks             []RuleCheck `json:"category_checks,omitempty"`
	ReplyChecks                []RuleCheck `json:"reply_checks,omitempty"`
}

// KnowledgeCandidateAudit captures the top retrieval candidates used for a run.
// It intentionally stores only normalized, non-secret diagnostic information.
type KnowledgeCandidateAudit struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Source             string  `json:"source"`
	Score              float64 `json:"score"`
	SemanticScore      float64 `json:"semantic_score,omitempty"`
	TitleScore         float64 `json:"title_score,omitempty"`
	LexicalScore       float64 `json:"lexical_score,omitempty"`
	KeywordScore       float64 `json:"keyword_score,omitempty"`
	CategoryScore      float64 `json:"category_score,omitempty"`
	RequiredScore      float64 `json:"required_score,omitempty"`
	AutoReply          bool    `json:"auto_reply"`
	AutoReplyDecision  string  `json:"auto_reply_decision,omitempty"`
	AutoReplyDetail    string  `json:"auto_reply_detail,omitempty"`
	BestChunkExcerpt   string  `json:"best_chunk_excerpt,omitempty"`
	BestQueryExcerpt   string  `json:"best_query_excerpt,omitempty"`
	QueryChunkCount    int     `json:"query_chunk_count,omitempty"`
	DocumentChunkCount int     `json:"document_chunk_count,omitempty"`
	SentToAI           bool    `json:"sent_to_ai,omitempty"`
	RetrievalRank      int     `json:"retrieval_rank,omitempty"`
	SelectionReason    string  `json:"selection_reason,omitempty"`
}

// ContextAuditItem is a compact snapshot of context that influenced a run.
type StatusCandidateAudit struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Status       string  `json:"status"`
	Relevance    float64 `json:"relevance"`
	AISelected   bool    `json:"ai_selected,omitempty"`
	AIConfidence float64 `json:"ai_confidence,omitempty"`
	FinalScore   float64 `json:"final_score,omitempty"`
	Decision     string  `json:"decision,omitempty"`
}

type ContextAuditItem struct {
	Kind      string  `json:"kind"`
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name"`
	Status    string  `json:"status,omitempty"`
	Relevance float64 `json:"relevance,omitempty"`
	Detail    string  `json:"detail,omitempty"`
}

// AnalysisRun is one independently auditable AI or deterministic analysis stage.
// OllamaRequestAttempt captures one concrete HTTP attempt against an Ollama node.
// It is attached to an AnalysisRun so routing and failover remain auditable.
type OllamaRequestAttempt struct {
	Attempt            int       `json:"attempt"`
	Stage              string    `json:"stage,omitempty"`
	Path               string    `json:"path,omitempty"`
	NodeName           string    `json:"node_name"`
	NodeURL            string    `json:"node_url"`
	ModelDigest        string    `json:"model_digest,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	DurationMS         int64     `json:"duration_ms"`
	InflightAtStart    int64     `json:"inflight_at_start,omitempty"`
	HTTPStatus         int       `json:"http_status,omitempty"`
	Outcome            string    `json:"outcome"`
	Retryable          bool      `json:"retryable,omitempty"`
	Error              string    `json:"error,omitempty"`
	TotalDurationNS    int64     `json:"total_duration_ns,omitempty"`
	LoadDurationNS     int64     `json:"load_duration_ns,omitempty"`
	PromptEvalCount    int64     `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration_ns,omitempty"`
	EvalCount          int64     `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration_ns,omitempty"`
}

// OllamaProviderTrace summarizes the routing of one logical analysis.
type OllamaProviderTrace struct {
	Provider     string                 `json:"provider,omitempty"`
	RoutingMode  string                 `json:"routing_mode,omitempty"`
	SelectedNode string                 `json:"selected_node,omitempty"`
	SelectedURL  string                 `json:"selected_url,omitempty"`
	FailoverUsed bool                   `json:"failover_used,omitempty"`
	AttemptCount int                    `json:"attempt_count,omitempty"`
	Attempts     []OllamaRequestAttempt `json:"attempts,omitempty"`
}

// OllamaNodeStatus is the read-only operational state exposed in the dashboard.
type OllamaNodeStatus struct {
	Name                  string    `json:"name"`
	URL                   string    `json:"url"`
	Weight                int       `json:"weight"`
	Healthy               bool      `json:"healthy"`
	Compatible            bool      `json:"compatible"`
	Available             bool      `json:"available"`
	InFlight              int64     `json:"inflight"`
	MaxInFlight           int       `json:"max_inflight"`
	ChatModelDigest       string    `json:"chat_model_digest,omitempty"`
	EmbeddingModelDigest  string    `json:"embedding_model_digest,omitempty"`
	LastCheck             time.Time `json:"last_check,omitempty"`
	LastSuccess           time.Time `json:"last_success,omitempty"`
	CooldownUntil         time.Time `json:"cooldown_until,omitempty"`
	ConsecutiveFailures   int       `json:"consecutive_failures,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	Requests              uint64    `json:"requests"`
	Failures              uint64    `json:"failures"`
	AverageDurationMS     float64   `json:"average_duration_ms,omitempty"`
	LastRequestDurationMS int64     `json:"last_request_duration_ms,omitempty"`
}

// Decision and InputSnapshot contain the exact structured values used at execution
// time, so later diagnostics do not depend on the current ticket or configuration.
type AnalysisRun struct {
	AnalysisID    string              `json:"analysis_id"`
	ParentRunID   string              `json:"parent_run_id"`
	TicketID      int64               `json:"ticket_id"`
	AnalysisType  string              `json:"analysis_type"`
	Trigger       string              `json:"trigger"`
	SourceVersion string              `json:"source_version"`
	Model         string              `json:"model,omitempty"`
	PromptVersion string              `json:"prompt_version,omitempty"`
	InputHash     string              `json:"input_hash,omitempty"`
	InputSnapshot json.RawMessage     `json:"input_snapshot,omitempty"`
	StartedAt     time.Time           `json:"started_at"`
	FinishedAt    time.Time           `json:"finished_at"`
	DurationMS    int64               `json:"duration_ms"`
	Outcome       string              `json:"outcome"`
	Error         string              `json:"error,omitempty"`
	Decision      json.RawMessage     `json:"decision,omitempty"`
	ReasonCodes   []string            `json:"reason_codes,omitempty"`
	Explanation   string              `json:"explanation,omitempty"`
	Confidence    float64             `json:"confidence,omitempty"`
	Checks        []RuleCheck         `json:"checks,omitempty"`
	Action        ActionAudit         `json:"action,omitempty"`
	Provider      OllamaProviderTrace `json:"provider,omitempty"`
}

type ActionStepAudit struct {
	Step     string `json:"step,omitempty"`
	Target   string `json:"target,omitempty"`
	Proposed bool   `json:"proposed,omitempty"`
	Executed bool   `json:"executed,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	Result   string `json:"result,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ActionAudit struct {
	Type     string            `json:"type,omitempty"`
	Target   string            `json:"target,omitempty"`
	Proposed bool              `json:"proposed,omitempty"`
	Executed bool              `json:"executed,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"`
	Before   string            `json:"before,omitempty"`
	After    string            `json:"after,omitempty"`
	Result   string            `json:"result,omitempty"`
	Error    string            `json:"error,omitempty"`
	Steps    []ActionStepAudit `json:"steps,omitempty"`
}

type PriorityDecision struct {
	RecommendedPriority int64    `json:"recommended_priority"`
	RecommendedImpact   int64    `json:"recommended_impact"`
	RecommendedUrgency  int64    `json:"recommended_urgency"`
	AffectedScope       string   `json:"affected_scope"`
	TimeCriticality     string   `json:"time_criticality"`
	ReasonCodes         []string `json:"reason_codes"`
	Confidence          float64  `json:"confidence"`
	Reason              string   `json:"reason"`
}

type PriorityResult struct {
	Accepted            bool        `json:"accepted"`
	ChangePriority      bool        `json:"change_priority"`
	PriorityBefore      int64       `json:"priority_before"`
	PriorityAfter       int64       `json:"priority_after"`
	RecommendedPriority int64       `json:"recommended_priority"`
	RecommendedImpact   int64       `json:"recommended_impact,omitempty"`
	RecommendedUrgency  int64       `json:"recommended_urgency,omitempty"`
	AffectedScope       string      `json:"affected_scope,omitempty"`
	TimeCriticality     string      `json:"time_criticality,omitempty"`
	Confidence          float64     `json:"confidence"`
	Decision            string      `json:"decision"`
	ReasonCodes         []string    `json:"reason_codes,omitempty"`
	Checks              []RuleCheck `json:"checks,omitempty"`
}

type EscalationDecision struct {
	Escalate           bool     `json:"escalate"`
	Level              int      `json:"level"`
	RecommendedAction  string   `json:"recommended_action,omitempty"` // legacy compatibility
	RecommendedActions []string `json:"recommended_actions,omitempty"`
	ReasonCodes        []string `json:"reason_codes"`
	Confidence         float64  `json:"confidence"`
	Reason             string   `json:"reason"`
}

type EscalationEvidence struct {
	TicketAge               string  `json:"ticket_age"`
	NoHumanResponse         bool    `json:"no_human_response"`
	HumanActivityIncomplete bool    `json:"human_activity_incomplete,omitempty"`
	LastHumanActivity       string  `json:"last_human_activity,omitempty"`
	InactiveFor             string  `json:"inactive_for,omitempty"`
	InactivityRequired      string  `json:"inactivity_required,omitempty"`
	Unassigned              bool    `json:"unassigned"`
	SLADeadline             string  `json:"sla_deadline,omitempty"`
	SLABreached             bool    `json:"sla_breached"`
	SLAAtRisk               bool    `json:"sla_at_risk"`
	SLARemaining            string  `json:"sla_remaining,omitempty"`
	MajorIncidentID         int64   `json:"major_incident_id,omitempty"`
	MajorIncidentName       string  `json:"major_incident_name,omitempty"`
	MajorIncidentScore      float64 `json:"major_incident_score,omitempty"`
}

type EscalationConstraints struct {
	AllowedActions            []string `json:"allowed_actions"`
	AllowedReasonCodes        []string `json:"allowed_reason_codes"`
	MaxLevel                  int      `json:"max_level"`
	MinimumAge                string   `json:"minimum_age"`
	ServiceOwnerMinLevel      int      `json:"service_owner_min_level"`
	ManagerReviewMinLevel     int      `json:"manager_review_min_level"`
	MajorIncidentMinRelevance float64  `json:"major_incident_min_relevance"`
	ConfiguredTargets         []string `json:"configured_targets,omitempty"`
}

type EscalationActionResult struct {
	Action         string      `json:"action"`
	Target         string      `json:"target,omitempty"`
	Accepted       bool        `json:"accepted"`
	Decision       string      `json:"decision"`
	Checks         []RuleCheck `json:"checks,omitempty"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
}

type EscalationResult struct {
	Accepted       bool                     `json:"accepted"`
	Level          int                      `json:"level"`
	Action         string                   `json:"action,omitempty"` // first accepted action for compatibility
	Actions        []EscalationActionResult `json:"actions,omitempty"`
	Decision       string                   `json:"decision"`
	ReasonCodes    []string                 `json:"reason_codes,omitempty"`
	Checks         []RuleCheck              `json:"checks,omitempty"`
	IdempotencyKey string                   `json:"idempotency_key,omitempty"` // first accepted action for compatibility
}

type RunRecord struct {
	RunID                            string                     `json:"run_id"`
	TicketID                         int64                      `json:"ticket_id"`
	TicketName                       string                     `json:"ticket_name"`
	SourceVersion                    string                     `json:"source_version"`
	Trigger                          string                     `json:"trigger,omitempty"`
	CausedByRunID                    string                     `json:"caused_by_run_id,omitempty"`
	StartedAt                        time.Time                  `json:"started_at"`
	FinishedAt                       time.Time                  `json:"finished_at"`
	Outcome                          string                     `json:"outcome"`
	Reason                           string                     `json:"reason"`
	AIReason                         string                     `json:"ai_reason,omitempty"`
	CategoryAIReason                 string                     `json:"category_ai_reason,omitempty"`
	ReplyAIReason                    string                     `json:"reply_ai_reason,omitempty"`
	CategoryAnalysisExecuted         bool                       `json:"category_analysis_executed,omitempty"`
	ReplyAnalysisExecuted            bool                       `json:"reply_analysis_executed,omitempty"`
	ReplyAnalysisSkipReason          string                     `json:"reply_analysis_skip_reason,omitempty"`
	CategoryAnalysisDurationMS       int64                      `json:"category_analysis_duration_ms,omitempty"`
	ReplyAnalysisDurationMS          int64                      `json:"reply_analysis_duration_ms,omitempty"`
	PriorityAnalysisExecuted         bool                       `json:"priority_analysis_executed,omitempty"`
	PriorityAnalysisDurationMS       int64                      `json:"priority_analysis_duration_ms,omitempty"`
	PriorityAIReason                 string                     `json:"priority_ai_reason,omitempty"`
	PriorityBefore                   int64                      `json:"priority_before,omitempty"`
	AIRecommendedPriority            int64                      `json:"ai_recommended_priority,omitempty"`
	AIRecommendedImpact              int64                      `json:"ai_recommended_impact,omitempty"`
	AIRecommendedUrgency             int64                      `json:"ai_recommended_urgency,omitempty"`
	PriorityAffectedScope            string                     `json:"priority_affected_scope,omitempty"`
	PriorityTimeCriticality          string                     `json:"priority_time_criticality,omitempty"`
	AIPriorityConfidence             float64                    `json:"ai_priority_confidence,omitempty"`
	PriorityThreshold                float64                    `json:"priority_threshold,omitempty"`
	PriorityDecision                 string                     `json:"priority_decision,omitempty"`
	PriorityProposed                 int64                      `json:"priority_proposed,omitempty"`
	PriorityWouldChange              bool                       `json:"priority_would_change,omitempty"`
	PriorityChanged                  bool                       `json:"priority_changed,omitempty"`
	PriorityReasonCodes              []string                   `json:"priority_reason_codes,omitempty"`
	PriorityChecks                   []RuleCheck                `json:"priority_checks,omitempty"`
	StatusAnalysisExecuted           bool                       `json:"status_analysis_executed,omitempty"`
	StatusAnalysisSkipReason         string                     `json:"status_analysis_skip_reason,omitempty"`
	StatusAnalysisDurationMS         int64                      `json:"status_analysis_duration_ms,omitempty"`
	StatusAIReason                   string                     `json:"status_ai_reason,omitempty"`
	StatusReplySelected              bool                       `json:"status_reply_selected,omitempty"`
	StatusReplyDecision              string                     `json:"status_reply_decision,omitempty"`
	StatusReplyType                  string                     `json:"status_reply_type,omitempty"`
	StatusReplyCandidateID           string                     `json:"status_reply_candidate_id,omitempty"`
	StatusReplyCandidateName         string                     `json:"status_reply_candidate_name,omitempty"`
	StatusReplyCandidateStatus       string                     `json:"status_reply_candidate_status,omitempty"`
	StatusReplyRelevance             float64                    `json:"status_reply_relevance,omitempty"`
	StatusReplyAIConfidence          float64                    `json:"status_reply_ai_confidence,omitempty"`
	StatusReplyFinalScore            float64                    `json:"status_reply_final_score,omitempty"`
	StatusReplyMinRelevance          float64                    `json:"status_reply_min_relevance,omitempty"`
	StatusReplyMinAIConfidence       float64                    `json:"status_reply_min_ai_confidence,omitempty"`
	StatusReplyMinFinalScore         float64                    `json:"status_reply_min_final_score,omitempty"`
	StatusReplyRenderedText          string                     `json:"status_reply_rendered_text,omitempty"`
	StatusChecks                     []RuleCheck                `json:"status_checks,omitempty"`
	StatusCandidates                 []StatusCandidateAudit     `json:"status_candidates,omitempty"`
	ReplyBasisCategoryID             int64                      `json:"reply_basis_category_id,omitempty"`
	ReplyBasisCategoryName           string                     `json:"reply_basis_category_name,omitempty"`
	PolicyReason                     string                     `json:"policy_reason,omitempty"`
	CategoryChecks                   []RuleCheck                `json:"category_checks,omitempty"`
	ReplyChecks                      []RuleCheck                `json:"reply_checks,omitempty"`
	ExecutionChecks                  []RuleCheck                `json:"execution_checks,omitempty"`
	CategoryBefore                   int64                      `json:"category_before"`
	CategoryBeforeName               string                     `json:"category_before_name,omitempty"`
	AIRecommendedCategoryID          int64                      `json:"ai_recommended_category_id,omitempty"`
	AIRecommendedCategoryName        string                     `json:"ai_recommended_category_name,omitempty"`
	AICategoryConfidence             float64                    `json:"ai_category_confidence,omitempty"`
	CategoryThreshold                float64                    `json:"category_threshold,omitempty"`
	CategoryDecision                 string                     `json:"category_decision,omitempty"`
	CategoryProposed                 int64                      `json:"category_proposed"`
	CategoryWouldChange              bool                       `json:"category_would_change"`
	CategoryChanged                  bool                       `json:"category_changed"`
	AIReplyRecommended               bool                       `json:"ai_reply_recommended,omitempty"`
	AIReplyConfidence                float64                    `json:"ai_reply_confidence,omitempty"`
	ReplyThreshold                   float64                    `json:"reply_threshold,omitempty"`
	AIKnowledgeID                    string                     `json:"ai_knowledge_id,omitempty"`
	ReplyDecision                    string                     `json:"reply_decision,omitempty"`
	ReplyProposed                    bool                       `json:"reply_proposed"`
	ReplyProposedText                string                     `json:"reply_proposed_text,omitempty"`
	LearningTicketText               string                     `json:"learning_ticket_text,omitempty"`
	ReplyWritten                     bool                       `json:"reply_written"`
	KnowledgeID                      string                     `json:"knowledge_id,omitempty"`
	KnowledgeTopID                   string                     `json:"knowledge_top_id,omitempty"`
	KnowledgeTopTitle                string                     `json:"knowledge_top_title,omitempty"`
	KnowledgeScore                   float64                    `json:"knowledge_score,omitempty"`
	KnowledgeSemanticScore           float64                    `json:"knowledge_semantic_score,omitempty"`
	KnowledgeTitleScore              float64                    `json:"knowledge_title_score,omitempty"`
	KnowledgeLexicalScore            float64                    `json:"knowledge_lexical_score,omitempty"`
	KnowledgeKeywordScore            float64                    `json:"knowledge_keyword_score,omitempty"`
	KnowledgeCategoryScore           float64                    `json:"knowledge_category_score,omitempty"`
	KnowledgeThreshold               float64                    `json:"knowledge_threshold,omitempty"`
	KnowledgeEvidenceScore           float64                    `json:"knowledge_evidence_score,omitempty"`
	KnowledgeRetrievalFloor          float64                    `json:"knowledge_retrieval_floor,omitempty"`
	KnowledgeCategoryAligned         bool                       `json:"knowledge_category_aligned,omitempty"`
	KnowledgeBestChunk               string                     `json:"knowledge_best_chunk,omitempty"`
	KnowledgeBestQueryChunk          string                     `json:"knowledge_best_query_chunk,omitempty"`
	KnowledgeQueryChunks             int                        `json:"knowledge_query_chunks,omitempty"`
	KnowledgeDocumentChunks          int                        `json:"knowledge_document_chunks,omitempty"`
	KnowledgeLLMCandidates           int                        `json:"knowledge_llm_candidates,omitempty"`
	CategoryKnowledgeLLMCandidates   int                        `json:"category_knowledge_llm_candidates,omitempty"`
	CategoryKnowledgeCandidateCutoff float64                    `json:"category_knowledge_candidate_cutoff,omitempty"`
	KnowledgeCandidateCutoff         float64                    `json:"knowledge_candidate_cutoff,omitempty"`
	KnowledgeCandidateMaxGap         float64                    `json:"knowledge_candidate_max_gap,omitempty"`
	KnowledgeAuditTopK               int                        `json:"knowledge_audit_top_k,omitempty"`
	ContextChanges                   int                        `json:"context_changes,omitempty"`
	ContextIncidents                 int                        `json:"context_incidents,omitempty"`
	ContextIssues                    int                        `json:"context_issues,omitempty"`
	ContextDevices                   int                        `json:"context_devices,omitempty"`
	ContextWarnings                  []string                   `json:"context_warnings,omitempty"`
	ValidatedOutcomeCandidates       []ValidatedOutcomeEvidence `json:"validated_outcome_candidates,omitempty"`
	ValidatedOutcomeSearchDurationMS int64                      `json:"validated_outcome_search_duration_ms,omitempty"`
	ValidatedOutcomeSearchError      string                     `json:"validated_outcome_search_error,omitempty"`
	KnowledgeCandidates              []KnowledgeCandidateAudit  `json:"knowledge_candidates,omitempty"`
	CategoryKnowledgeCandidates      []KnowledgeCandidateAudit  `json:"category_knowledge_candidates,omitempty"`
	ReplyKnowledgeCandidates         []KnowledgeCandidateAudit  `json:"reply_knowledge_candidates,omitempty"`
	ContextDetails                   []ContextAuditItem         `json:"context_details,omitempty"`
	DryRun                           bool                       `json:"dry_run"`
	Analyses                         []AnalysisRun              `json:"analyses,omitempty"`
	Error                            string                     `json:"error,omitempty"`
}

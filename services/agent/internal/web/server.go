package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/example/glpi-ai-agent/internal/config"
	knowledgepkg "github.com/example/glpi-ai-agent/internal/knowledge"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/model"
	"github.com/example/glpi-ai-agent/internal/obsidian"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
)

//go:embed templates/dashboard.html templates/diagnostics.html templates/category-mappings.html
var files embed.FS

type KnowledgeManager interface {
	List() []model.KnowledgeDoc
	ByID(string) (model.KnowledgeDoc, bool)
	Upsert(context.Context, model.KnowledgeDoc) error
	Delete(string) error
	IsManaged(string) bool
	Origin(string) string
	LoadStats() knowledgepkg.LoadStats
	InitStatus() knowledgepkg.InitStatus
	Ready() bool
	FindMetadata(string, int) []model.KnowledgeDoc
	CategoryMappings() (knowledgepkg.CategoryMappingState, error)
	SaveCategoryMappings(context.Context, map[string][]int64) error
}
type FeedbackManager interface {
	Categories(context.Context) ([]model.Category, error)
	RecordCategoryFeedback(context.Context, string, int64) (model.LearningExample, error)
	LearningExamples() []model.LearningExample
	DeleteLearning(string) error
	LearningCount() int
	RecordTicketOutcome(context.Context, string, string, string, string, string) (learning.TicketOutcome, error)
	TicketOutcomes() []learning.TicketOutcome
}

type OutcomeQualityManager interface {
	SearchValidatedOutcomes(context.Context, string, int) ([]model.ValidatedOutcomeEvidence, error)
}

type KnowledgeQualitySearch interface {
	Search(context.Context, string, int, ...[]model.Category) ([]model.KnowledgeHit, error)
}

type DiagnosticsManager interface {
	DiagnoseRun(context.Context, string) (model.RunRecord, error)
	DiagnoseKnowledge(context.Context, string, string, string) (model.KnowledgeDiagnostic, error)
}
type OllamaNodeProvider interface {
	NodeStatuses() []model.OllamaNodeStatus
	RoutingMode() string
}

type Server struct {
	cfg         config.Config
	metrics     *metrics.Metrics
	state       *state.Store
	q           *queue.Queue
	knowledge   KnowledgeManager
	feedback    FeedbackManager
	diagnostics DiagnosticsManager
	ollamaNodes OllamaNodeProvider
	tpl         *template.Template
}

func New(cfg config.Config, m *metrics.Metrics, s *state.Store, q *queue.Queue, k KnowledgeManager, f FeedbackManager, providers ...OllamaNodeProvider) (*Server, error) {
	t, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	srv := &Server{cfg: cfg, metrics: m, state: s, q: q, knowledge: k, feedback: f, tpl: t}
	if len(providers) > 0 {
		srv.ollamaNodes = providers[0]
	}
	if d, ok := f.(DiagnosticsManager); ok {
		srv.diagnostics = d
	}
	return srv, nil
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.prom)
	mux.Handle("GET /api/control/runs", s.controlReadAuth(http.HandlerFunc(s.controlRuns)))
	mux.Handle("GET /api/control/graph/runs/{id}", s.controlReadAuth(http.HandlerFunc(s.controlRunGraph)))
	mux.Handle("GET /api/control/graph/learning", s.controlReadAuth(http.HandlerFunc(s.controlLearningGraph)))
	mux.Handle("GET /", s.auth(http.HandlerFunc(s.dashboard)))
	mux.Handle("GET /diagnostics", s.auth(http.HandlerFunc(s.diagnosticsPage)))
	mux.Handle("GET /category-mappings", s.auth(http.HandlerFunc(s.categoryMappingsPage)))
	mux.Handle("GET /api/diagnostics/run/{id}", s.auth(http.HandlerFunc(s.diagnosticRun)))
	mux.Handle("GET /api/diagnostics/analysis/{id}", s.auth(http.HandlerFunc(s.diagnosticAnalysis)))
	mux.Handle("GET /api/diagnostics/run/{id}/knowledge", s.auth(http.HandlerFunc(s.diagnosticKnowledge)))
	mux.Handle("GET /api/diagnostics/knowledge", s.auth(http.HandlerFunc(s.diagnosticKnowledgeSearch)))
	mux.Handle("GET /api/status", s.auth(http.HandlerFunc(s.status)))
	mux.Handle("GET /api/runs", s.auth(http.HandlerFunc(s.runs)))
	mux.Handle("GET /api/categories", s.auth(http.HandlerFunc(s.categories)))
	mux.Handle("GET /api/category-mappings", s.auth(http.HandlerFunc(s.categoryMappingsGet)))
	mux.Handle("PUT /api/category-mappings", s.auth(s.mutation(http.HandlerFunc(s.categoryMappingsPut))))
	mux.Handle("GET /api/knowledge", s.auth(http.HandlerFunc(s.knowledgeList)))
	mux.Handle("GET /api/knowledge/export/obsidian", s.auth(http.HandlerFunc(s.knowledgeExportObsidian)))
	mux.Handle("GET /api/knowledge/{id}", s.auth(http.HandlerFunc(s.knowledgeGet)))
	mux.Handle("POST /api/knowledge", s.auth(s.mutation(http.HandlerFunc(s.knowledgeCreate))))
	mux.Handle("PUT /api/knowledge/{id}", s.auth(s.mutation(http.HandlerFunc(s.knowledgeUpdate))))
	mux.Handle("DELETE /api/knowledge/{id}", s.auth(s.mutation(http.HandlerFunc(s.knowledgeDelete))))
	mux.Handle("GET /api/learning", s.auth(http.HandlerFunc(s.learningList)))
	mux.Handle("POST /api/learning", s.auth(s.mutation(http.HandlerFunc(s.learningAdd))))
	mux.Handle("DELETE /api/learning/{id}", s.auth(s.mutation(http.HandlerFunc(s.learningDelete))))
	mux.Handle("GET /api/outcomes", s.auth(http.HandlerFunc(s.outcomeList)))
	mux.Handle("POST /api/quality/replay", s.auth(s.mutation(http.HandlerFunc(s.qualityReplay))))
	mux.Handle("POST /api/outcomes", s.auth(s.mutation(http.HandlerFunc(s.outcomeAdd))))
	mux.Handle("POST /api/tickets/{id}/reprocess", s.auth(s.mutation(http.HandlerFunc(s.reprocessTicket))))
	mux.HandleFunc("POST /webhook/glpi", s.webhook)
	return securityHeaders(requestLog(mux))
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"ok"}`)
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	g, o := s.metrics.Health()
	k := s.knowledge.Ready()
	ks := s.knowledge.InitStatus()
	w.Header().Set("Content-Type", "application/json")
	if !g || !o || !k {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(map[string]any{"glpi": g, "ollama": o, "knowledge": k, "knowledge_state": ks.State, "knowledge_phase": ks.Phase})
}
func (s *Server) prom(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.metrics.WritePrometheus(w)
	if s.ollamaNodes == nil {
		return
	}
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_healthy gauge\n")
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_available gauge\n")
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_inflight gauge\n")
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_requests_total counter\n")
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_failures_total counter\n")
	io.WriteString(w, "# TYPE glpi_agent_ollama_node_average_duration_ms gauge\n")
	for _, node := range s.ollamaNodes.NodeStatuses() {
		name := prometheusLabel(node.Name)
		fmt.Fprintf(w, "glpi_agent_ollama_node_healthy{node=\"%s\"} %d\n", name, boolMetric(node.Healthy && node.Compatible))
		fmt.Fprintf(w, "glpi_agent_ollama_node_available{node=\"%s\"} %d\n", name, boolMetric(node.Available))
		fmt.Fprintf(w, "glpi_agent_ollama_node_inflight{node=\"%s\"} %d\n", name, node.InFlight)
		fmt.Fprintf(w, "glpi_agent_ollama_node_requests_total{node=\"%s\"} %d\n", name, node.Requests)
		fmt.Fprintf(w, "glpi_agent_ollama_node_failures_total{node=\"%s\"} %d\n", name, node.Failures)
		fmt.Fprintf(w, "glpi_agent_ollama_node_average_duration_ms{node=\"%s\"} %.3f\n", name, node.AverageDurationMS)
	}
}

func boolMetric(v bool) int {
	if v {
		return 1
	}
	return 0
}

func prometheusLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tpl.ExecuteTemplate(w, "dashboard.html", map[string]any{"DryRun": s.cfg.DryRun, "AutoReply": s.cfg.AutoReply, "AutoCategory": s.cfg.AutoCategory, "CommunicationLanguage": s.cfg.CommunicationLanguage, "CommunicationStyle": s.cfg.CommunicationStyle})
}
func (s *Server) diagnosticsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/diagnostics" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tpl.ExecuteTemplate(w, "diagnostics.html", nil)
}

func (s *Server) categoryMappingsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/category-mappings" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tpl.ExecuteTemplate(w, "category-mappings.html", map[string]any{"Editable": s.cfg.KnowledgeWebEditEnabled})
}

func (s *Server) categoryMappingsGet(w http.ResponseWriter, r *http.Request) {
	state, err := s.knowledge.CategoryMappings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cats, err := s.feedback.Categories(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("load GLPI categories: %v", err), http.StatusBadGateway)
		return
	}
	type categoryView struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		CompleteName string `json:"completename"`
	}
	out := make([]categoryView, 0, len(cats))
	for _, c := range cats {
		out = append(out, categoryView{ID: c.ID, Name: c.Name, CompleteName: c.CompleteName})
	}
	respondJSON(w, map[string]any{
		"mapping":  state,
		"glpi":     out,
		"editable": s.cfg.KnowledgeWebEditEnabled && strings.TrimSpace(s.cfg.KnowledgeCategoryMapFile) != "",
	})
}

func (s *Server) categoryMappingsPut(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, 11*time.Minute) // covers the 10 minute re-evaluation below
	if !s.cfg.KnowledgeWebEditEnabled {
		http.Error(w, "knowledge editing disabled", http.StatusForbidden)
		return
	}
	if strings.TrimSpace(s.cfg.KnowledgeCategoryMapFile) == "" {
		http.Error(w, "KNOWLEDGE_CATEGORY_MAP_FILE is not configured", http.StatusConflict)
		return
	}
	type mappingInput struct {
		Label       string  `json:"label"`
		CategoryIDs []int64 `json:"category_ids"`
	}
	var body struct {
		Mappings []mappingInput `json:"mappings"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("invalid category mapping payload: %v", err), http.StatusBadRequest)
		return
	}

	cats, err := s.feedback.Categories(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("cannot validate GLPI categories: %v", err), http.StatusBadGateway)
		return
	}
	valid := make(map[int64]struct{}, len(cats))
	for _, c := range cats {
		valid[c.ID] = struct{}{}
	}
	mappings := make(map[string][]int64, len(body.Mappings))
	seen := map[string]string{}
	for _, item := range body.Mappings {
		label := strings.TrimSpace(item.Label)
		if label == "" {
			http.Error(w, "mapping label must not be empty", http.StatusBadRequest)
			return
		}
		normalized := strings.ToLower(strings.Join(strings.Fields(label), " "))
		if prev, exists := seen[normalized]; exists && prev != label {
			http.Error(w, fmt.Sprintf("duplicate mapping labels %q and %q", prev, label), http.StatusBadRequest)
			return
		}
		seen[normalized] = label
		for _, id := range item.CategoryIDs {
			if _, ok := valid[id]; !ok {
				http.Error(w, fmt.Sprintf("unknown GLPI category id %d for %q", id, label), http.StatusUnprocessableEntity)
				return
			}
		}
		mappings[label] = append([]int64(nil), item.CategoryIDs...)
	}

	// Category re-evaluation can touch tens of thousands of JSON files. It is
	// deliberately independent of the HTTP request cancellation so a reverse
	// proxy/client disconnect cannot leave a newly written mapping unapplied.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := s.knowledge.SaveCategoryMappings(ctx, mappings); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	state, err := s.knowledge.CategoryMappings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respondJSON(w, state)
}

func (s *Server) diagnosticRun(w http.ResponseWriter, r *http.Request) {
	if s.diagnostics == nil {
		http.Error(w, "diagnostics unavailable", http.StatusNotImplemented)
		return
	}
	run, err := s.diagnostics.DiagnoseRun(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	respondJSON(w, run)
}

func (s *Server) diagnosticAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.state == nil {
		http.Error(w, "analysis diagnostics unavailable", http.StatusNotImplemented)
		return
	}
	analysis, ok := s.state.FindAnalysis(strings.TrimSpace(r.PathValue("id")))
	if !ok {
		http.Error(w, "analysis not found", http.StatusNotFound)
		return
	}
	respondJSON(w, analysis)
}

func (s *Server) diagnosticKnowledge(w http.ResponseWriter, r *http.Request) {
	if s.diagnostics == nil {
		http.Error(w, "diagnostics unavailable", http.StatusNotImplemented)
		return
	}
	kbID := strings.TrimSpace(r.URL.Query().Get("knowledge_id"))
	if kbID == "" {
		http.Error(w, "knowledge_id required", http.StatusBadRequest)
		return
	}
	purpose := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("purpose")))
	if purpose == "" {
		purpose = "reply"
	}
	if purpose != "reply" && purpose != "category" {
		http.Error(w, "purpose must be category or reply", http.StatusBadRequest)
		return
	}
	d, err := s.diagnostics.DiagnoseKnowledge(r.Context(), strings.TrimSpace(r.PathValue("id")), kbID, purpose)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	respondJSON(w, d)
}

func (s *Server) diagnosticKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	type item struct {
		ID                string   `json:"id"`
		Title             string   `json:"title"`
		Source            string   `json:"source"`
		AutoReply         bool     `json:"auto_reply"`
		AutoReplyDecision string   `json:"auto_reply_decision,omitempty"`
		AutoReplyDetail   string   `json:"auto_reply_detail,omitempty"`
		Unmapped          []string `json:"unmapped_categories,omitempty"`
	}
	out := make([]item, 0, limit)
	for _, d := range s.knowledge.List() {
		hay := strings.ToLower(d.ID + " " + d.Title + " " + strings.Join(d.Keywords, " ") + " " + strings.Join(d.ExternalCategories, " "))
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		out = append(out, item{ID: d.ID, Title: d.Title, Source: d.Source, AutoReply: d.AutoReply, AutoReplyDecision: d.AutoReplyDecision, AutoReplyDetail: d.AutoReplyDetail, Unmapped: append([]string(nil), d.UnmappedExternalCategories...)})
		if len(out) >= limit {
			break
		}
	}
	respondJSON(w, out)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	g, o := s.metrics.Health()
	poll := s.metrics.PollStatus()
	kbOK, kbDocs, kbLastSync, kbLastErr := s.metrics.GLPIKBStatus()
	glpiKBAutoReplyApproved := 0
	glpiKBAutoReplyBlocked := 0
	glpiKBAutoReplyDecisions := map[string]int{}
	for _, doc := range s.knowledge.List() {
		if !strings.EqualFold(strings.TrimSpace(doc.Source), strings.TrimSpace(s.cfg.GLPIKBSource)) {
			continue
		}
		if doc.AutoReply {
			glpiKBAutoReplyApproved++
		} else {
			glpiKBAutoReplyBlocked++
		}
		decision := strings.TrimSpace(doc.AutoReplyDecision)
		if decision == "" {
			decision = "legacy_or_unspecified"
		}
		glpiKBAutoReplyDecisions[decision]++
	}
	loadStats := s.knowledge.LoadStats()
	initStatus := s.knowledge.InitStatus()
	ollamaNodes := []model.OllamaNodeStatus{}
	ollamaRoutingMode := s.cfg.OllamaRoutingMode
	if s.ollamaNodes != nil {
		ollamaNodes = s.ollamaNodes.NodeStatuses()
		ollamaRoutingMode = s.ollamaNodes.RoutingMode()
	}
	ollamaHealthyNodes := 0
	ollamaAvailableNodes := 0
	for _, node := range ollamaNodes {
		if node.Healthy && node.Compatible {
			ollamaHealthyNodes++
		}
		if node.Available {
			ollamaAvailableNodes++
		}
	}
	respondJSON(w, map[string]any{
		"uptime_seconds": int(time.Since(s.metrics.Started).Seconds()), "dry_run": s.cfg.DryRun, "auto_reply": s.cfg.AutoReply, "auto_category": s.cfg.AutoCategory,
		"priority_enabled": s.cfg.PriorityEnabled, "auto_priority": s.cfg.AutoPriority, "priority_confidence": s.cfg.PriorityConfidence, "priority_analysis_timeout": s.cfg.PriorityAnalysisTimeout.String(), "priority_max_increase": s.cfg.PriorityMaxIncrease, "priority_allowed_reason_codes": s.cfg.PriorityAllowedReasonCodes,
		"escalation_enabled": s.cfg.EscalationEnabled, "auto_escalation": s.cfg.AutoEscalation, "escalation_scan_interval": s.cfg.EscalationScanInterval.String(), "escalation_min_age": s.cfg.EscalationMinAge.String(), "escalation_min_inactivity": s.cfg.EscalationMinInactivity.String(), "escalation_analysis_timeout": s.cfg.EscalationAnalysisTimeout.String(), "escalation_confidence": s.cfg.EscalationConfidence, "escalation_max_level": s.cfg.EscalationMaxLevel, "escalation_sla_risk_window": s.cfg.EscalationSLARiskWindow.String(), "escalation_service_owner_min_level": s.cfg.EscalationServiceOwnerMinLevel, "escalation_manager_review_min_level": s.cfg.EscalationManagerReviewMinLevel, "escalation_major_incident_min_relevance": s.cfg.EscalationMajorIncidentMinScore, "escalation_allowed_reason_codes": s.cfg.EscalationAllowedReasonCodes, "escalation_allowed_actions": s.cfg.EscalationAllowedActions, "escalation_second_level_group_id": s.cfg.EscalationSecondLevelGroupID, "escalation_security_group_id": s.cfg.EscalationSecurityGroupID, "escalation_service_owner_group_id": s.cfg.EscalationServiceOwnerGroupID, "escalation_service_owner_user_id": s.cfg.EscalationServiceOwnerUserID, "escalation_manager_review_group_id": s.cfg.EscalationManagerReviewGroupID, "escalation_manager_review_user_id": s.cfg.EscalationManagerReviewUserID, "escalation_add_private_followup": s.cfg.EscalationAddPrivateFollowup, "escalation_webhook_configured": strings.TrimSpace(s.cfg.EscalationWebhookURL) != "", "escalation_webhook_timeout": s.cfg.EscalationWebhookTimeout.String(), "escalation_webhook_allow_insecure_http": s.cfg.EscalationWebhookAllowInsecureHTTP, "glpi_escalation_group_patch_field": s.cfg.GLPIEscalationGroupPatchField, "glpi_escalation_user_patch_field": s.cfg.GLPIEscalationUserPatchField, "glpi_escalation_itil_link_configured": strings.TrimSpace(s.cfg.GLPIEscalationITILLinkPath) != "" && strings.TrimSpace(s.cfg.GLPIEscalationITILLinkBody) != "", "glpi_escalation_filter_configured": strings.TrimSpace(s.cfg.GLPIEscalationFilter) != "", "glpi_escalation_limit": s.cfg.GLPIEscalationLimit,
		"processed": s.metrics.Processed.Load(), "skipped": s.metrics.Skipped.Load(), "errors": s.metrics.Errors.Load(), "category_changes": s.metrics.CategoryChanged.Load(), "replies": s.metrics.Replies.Load(), "priority_recommendations": s.metrics.PriorityRecommendations.Load(), "priority_changes": s.metrics.PriorityChanges.Load(), "escalation_runs": s.metrics.EscalationRuns.Load(), "escalations": s.metrics.Escalations.Load(), "queue_depth": s.q.Len(),
		"glpi_ok": g, "ollama_ok": o, "knowledge_docs": s.metrics.KnowledgeDocs(), "last_poll": poll.At, "polls_total": s.metrics.Polls.Load(), "poll_last_fetched": poll.Fetched, "poll_last_seen": poll.Seen, "poll_last_unseen": poll.Unseen, "poll_last_enqueued": poll.Enqueued, "poll_last_rejected": poll.Rejected, "poll_last_error": poll.Error, "processed_version_count": s.state.ProcessedVersionCount(),
		"knowledge_ready": s.knowledge.Ready(), "knowledge_init_state": initStatus.State, "knowledge_init_phase": initStatus.Phase, "knowledge_init_total_files": initStatus.TotalFiles, "knowledge_init_processed_files": initStatus.ProcessedFiles, "knowledge_init_loaded_docs": initStatus.LoadedDocs, "knowledge_init_indexed_docs": initStatus.IndexedDocs, "knowledge_init_cache_hits": initStatus.CacheHits, "knowledge_init_pending_embeddings": initStatus.PendingEmbeddings, "knowledge_init_started_at": initStatus.StartedAt, "knowledge_init_finished_at": initStatus.FinishedAt, "knowledge_init_error": initStatus.LastError,
		"knowledge_index_mode": s.cfg.KnowledgeIndexMode, "knowledge_embed_batch_size": s.cfg.KnowledgeEmbedBatchSize, "knowledge_index_scan_interval": s.cfg.KnowledgeIndexScanInterval.String(), "knowledge_snapshot_loaded": initStatus.SnapshotLoaded, "knowledge_snapshot_path": initStatus.SnapshotPath, "knowledge_snapshot_saved_at": initStatus.SnapshotSavedAt, "knowledge_last_scan_at": initStatus.LastScanAt, "knowledge_last_scan_error": initStatus.LastScanError, "knowledge_changed_files": initStatus.ChangedFiles, "knowledge_deleted_files": initStatus.DeletedFiles, "knowledge_reused_files": initStatus.ReusedFiles,
		"communication_language": s.cfg.CommunicationLanguage, "communication_style": s.cfg.CommunicationStyle, "ai_content_label_enabled": s.cfg.AIContentLabelEnabled, "knowledge_allowed_sources": s.cfg.KnowledgeAllowedSources, "knowledge_category_sources": s.cfg.KnowledgeCategorySources, "knowledge_auto_reply_sources": s.cfg.KnowledgeAutoReplySources, "knowledge_category_mode": s.cfg.KnowledgeCategoryMode, "knowledge_category_map_configured": strings.TrimSpace(s.cfg.KnowledgeCategoryMapFile) != "", "knowledge_ignore_globs": s.cfg.KnowledgeIgnoreGlobs, "knowledge_ignored_files": loadStats.IgnoredFiles, "knowledge_unmapped_category_files": loadStats.UnmappedCategoryFiles, "knowledge_unmapped_categories": loadStats.UnmappedCategories,
		"category_confidence": s.cfg.CategoryConfidence, "reply_confidence": s.cfg.ReplyConfidence, "knowledge_min_score": s.cfg.KnowledgeMinScore, "knowledge_retrieval_floor": s.cfg.KnowledgeRetrievalFloor, "knowledge_evidence_weight_retrieval": s.cfg.KnowledgeEvidenceRetrievalWeight, "knowledge_evidence_weight_ai": s.cfg.KnowledgeEvidenceAIWeight, "knowledge_evidence_weight_category": s.cfg.KnowledgeEvidenceCategoryWeight,
		"knowledge_weight_semantic": s.cfg.KnowledgeSemanticWeight, "knowledge_weight_title": s.cfg.KnowledgeTitleWeight, "knowledge_weight_lexical": s.cfg.KnowledgeLexicalWeight, "knowledge_weight_keywords": s.cfg.KnowledgeKeywordWeight, "knowledge_weight_category": s.cfg.KnowledgeCategoryWeight, "knowledge_embedding_profile": s.cfg.KnowledgeEmbeddingProfile,
		"knowledge_chunk_words": s.cfg.KnowledgeChunkWords, "knowledge_chunk_overlap_words": s.cfg.KnowledgeChunkOverlapWords, "knowledge_max_chunks_per_doc": s.cfg.KnowledgeMaxChunksPerDoc,
		"context_enabled": s.cfg.ContextEnabled, "context_fetches": s.metrics.ContextFetches.Load(), "context_errors": s.metrics.ContextErrors.Load(),
		"change_calendar_enabled": s.cfg.ChangeCalendarEnabled, "major_incidents_enabled": s.cfg.MajorIncidentsEnabled, "user_device_context_enabled": s.cfg.UserDeviceContextEnabled,
		"knowledge_edit_enabled": s.cfg.KnowledgeWebEditEnabled, "learning_enabled": s.cfg.LearningEnabled, "learning_examples": s.feedback.LearningCount(),
		"outcome_learning_enabled": s.cfg.OutcomeLearningEnabled, "outcome_learning_fail_open": s.cfg.OutcomeLearningFailOpen, "validated_outcomes": len(s.feedback.TicketOutcomes()),
		"outcome_retrieval_enabled": s.cfg.OutcomeRetrievalEnabled, "outcome_retrieval_search_k": s.cfg.OutcomeRetrievalSearchK, "outcome_retrieval_min_similarity": s.cfg.OutcomeRetrievalMinSimilarity, "outcome_retrieval_fail_open": s.cfg.OutcomeRetrievalFailOpen,
		"outcome_searches": s.metrics.OutcomeSearches.Load(), "outcome_search_hits": s.metrics.OutcomeSearchHits.Load(), "outcome_search_errors": s.metrics.OutcomeSearchErrors.Load(),
		"outcome_learning_learned": s.metrics.OutcomeLearningLearned.Load(), "outcome_learning_accepted": s.metrics.OutcomeLearningAccepted.Load(), "outcome_learning_corrected": s.metrics.OutcomeLearningCorrected.Load(), "outcome_learning_failed": s.metrics.OutcomeLearningFailed.Load(), "outcome_learning_idempotent": s.metrics.OutcomeLearningIdempotent.Load(),
		"glpi_kb_enabled": s.cfg.GLPIKBEnabled, "glpi_kb_ok": kbOK, "glpi_kb_documents": kbDocs, "glpi_kb_last_sync": kbLastSync, "glpi_kb_last_error": kbLastErr, "glpi_kb_source": s.cfg.GLPIKBSource, "glpi_kb_sync_interval": s.cfg.GLPIKBSyncInterval.String(), "glpi_kb_auto_reply_approved": glpiKBAutoReplyApproved, "glpi_kb_auto_reply_blocked": glpiKBAutoReplyBlocked, "glpi_kb_auto_reply_decisions": glpiKBAutoReplyDecisions,
		"uptime_kuma_enabled": s.cfg.UptimeKumaEnabled, "uptime_kuma_mode": s.cfg.UptimeKumaMode, "uptime_kuma_status_pages": s.cfg.UptimeKumaStatusPages, "context_fail_closed": s.cfg.ContextBlockReplyOnError, "context_incident_block": s.cfg.ContextBlockReplyOnIncident,
		"context_status_reply_enabled": s.cfg.ContextStatusReplyEnabled, "context_status_reply_min_relevance": s.cfg.ContextStatusReplyMinRelevance, "context_status_reply_min_ai_confidence": s.cfg.ContextStatusReplyMinAIConfidence, "context_status_reply_min_final_score": s.cfg.ContextStatusReplyMinFinalScore, "context_incident_reply_text_configured": strings.TrimSpace(s.cfg.ContextIncidentReplyText) != "", "context_maintenance_reply_text_configured": strings.TrimSpace(s.cfg.ContextMaintenanceReplyText) != "",
		"workers": s.cfg.Workers, "queue_size": s.cfg.QueueSize, "glpi_api_version": s.cfg.GLPIAPIVersion, "glpi_poll_interval": s.cfg.GLPIPollInterval.String(), "glpi_poll_limit": s.cfg.GLPIPollLimit, "glpi_allowed_status_ids": s.cfg.GLPIAllowedStatusIDs, "glpi_ticket_filter_configured": strings.TrimSpace(s.cfg.GLPITicketFilter) != "", "glpi_timeout": s.cfg.GLPITimeout.String(),
		"ollama_model": s.cfg.OllamaModel, "ollama_embedding_model": s.cfg.OllamaEmbeddingModel, "ollama_timeout": s.cfg.OllamaTimeout.String(), "ollama_num_predict": s.cfg.OllamaNumPredict, "ollama_num_ctx": s.cfg.OllamaNumCtx, "ollama_keep_alive": s.cfg.OllamaKeepAlive.String(), "ollama_think": s.cfg.OllamaThink, "ollama_max_concurrent": s.cfg.OllamaMaxConcurrent, "ollama_json_retries": s.cfg.OllamaJSONRetries,
		"ollama_nodes": ollamaNodes, "ollama_node_count": len(ollamaNodes), "ollama_healthy_nodes": ollamaHealthyNodes, "ollama_available_nodes": ollamaAvailableNodes, "ollama_routing_mode": ollamaRoutingMode, "ollama_node_max_inflight": s.cfg.OllamaNodeMaxInflight, "ollama_node_health_interval": s.cfg.OllamaNodeHealthInterval.String(), "ollama_node_failure_cooldown": s.cfg.OllamaNodeFailureCooldown.String(), "ollama_node_request_timeout": s.cfg.OllamaNodeRequestTimeout.String(), "ollama_failover_enabled": s.cfg.OllamaFailoverEnabled, "ollama_failover_attempts": s.cfg.OllamaFailoverAttempts, "ollama_require_same_model_digest": s.cfg.OllamaRequireSameDigest, "ollama_require_embedding_model": s.cfg.OllamaRequireEmbeddingModel,
		"rag_enabled": s.cfg.RAGEnabled, "knowledge_top_k": s.cfg.KnowledgeTopK, "knowledge_audit_top_k": s.cfg.KnowledgeAuditTopK, "knowledge_candidate_max_gap": s.cfg.KnowledgeCandidateMaxGap, "category_prompt_limit": s.cfg.CategoryPromptLimit, "knowledge_max_query_chunks": s.cfg.KnowledgeMaxQueryChunks,
		"glpi_kb_path": s.cfg.GLPIKBPath, "glpi_kb_filter_configured": strings.TrimSpace(s.cfg.GLPIKBFilter) != "", "glpi_kb_limit": s.cfg.GLPIKBLimit, "glpi_kb_auto_reply": s.cfg.GLPIKBAutoReply, "glpi_kb_auto_reply_category_ids": s.cfg.GLPIKBAutoReplyCategoryIDs, "glpi_kb_auto_reply_allow_uncategorized": s.cfg.GLPIKBAutoReplyAllowUncategorized, "glpi_kb_auto_reply_uncategorized_article_ids": s.cfg.GLPIKBAutoReplyUncategorizedArticleIDs,
		"learning_max_examples": s.cfg.LearningMaxExamples, "learning_examples_per_category": s.cfg.LearningExamplesPerCategory,
		"context_timeout": s.cfg.ContextTimeout.String(), "context_relevance_min_score": s.cfg.ContextRelevanceMinScore, "change_lookback": s.cfg.ChangeLookback.String(), "change_lookahead": s.cfg.ChangeLookahead.String(),
	})
}
func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	respondJSON(w, s.state.Recent(limit))
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.feedback.Categories(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	type categoryView struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		CompleteName string `json:"completename"`
	}
	out := make([]categoryView, 0, len(cats))
	for _, c := range cats {
		out = append(out, categoryView{ID: c.ID, Name: c.Name, CompleteName: c.CompleteName})
	}
	respondJSON(w, out)
}
func (s *Server) knowledgeList(w http.ResponseWriter, r *http.Request) {
	type view struct {
		model.KnowledgeDoc
		Managed bool   `json:"managed"`
		Origin  string `json:"origin"`
	}
	docs := s.knowledge.List()
	out := make([]view, 0, len(docs))
	for _, d := range docs {
		out = append(out, view{KnowledgeDoc: d, Managed: s.knowledge.IsManaged(d.ID), Origin: s.knowledge.Origin(d.ID)})
	}
	respondJSON(w, out)
}
func (s *Server) knowledgeExportObsidian(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, 10*time.Minute)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="glpi-neuroforge-knowledge-obsidian.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := obsidian.WriteZIP(w, s.knowledge.List(), time.Now().UTC()); err != nil {
		slog.Error("Obsidian knowledge export failed", "error", err)
	}
}

func (s *Server) knowledgeGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	d, ok := s.knowledge.ByID(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	respondJSON(w, map[string]any{"document": d, "managed": s.knowledge.IsManaged(id), "origin": s.knowledge.Origin(id)})
}

func (s *Server) decodeKnowledge(r *http.Request) (model.KnowledgeDoc, error) {
	var d model.KnowledgeDoc
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("invalid knowledge document: %w", err)
	}
	if strings.TrimSpace(d.Source) == "" {
		d.Source = "internal-kb"
	}
	if strings.TrimSpace(d.Language) == "" {
		d.Language = s.cfg.CommunicationLanguage
	}
	if strings.TrimSpace(d.CommunicationStyle) == "" {
		d.CommunicationStyle = s.cfg.CommunicationStyle
	}
	// Rich HTML is reserved for content synchronized from the trusted GLPI KB.
	// Web-managed entries remain plain text and are escaped by the GLPI client.
	d.AnswerHTML = ""
	return d, nil
}

func (s *Server) validateKnowledgeCategories(ctx context.Context, d model.KnowledgeDoc) error {
	if len(d.Categories) == 0 {
		return nil
	}
	cats, err := s.feedback.Categories(ctx)
	if err != nil {
		return fmt.Errorf("cannot validate categories: %w", err)
	}
	valid := make(map[int64]struct{}, len(cats))
	for _, c := range cats {
		valid[c.ID] = struct{}{}
	}
	for _, id := range d.Categories {
		if _, ok := valid[id]; !ok {
			return fmt.Errorf("unknown GLPI category id %d", id)
		}
	}
	return nil
}

func (s *Server) knowledgeCreate(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.KnowledgeWebEditEnabled {
		http.Error(w, "knowledge editing disabled", http.StatusForbidden)
		return
	}
	d, err := s.decodeKnowledge(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, exists := s.knowledge.ByID(strings.TrimSpace(d.ID)); exists {
		http.Error(w, "knowledge id already exists; use update instead", http.StatusConflict)
		return
	}
	if err := s.validateKnowledgeCategories(r.Context(), d); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err := s.knowledge.Upsert(r.Context(), d); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.metrics.SetKnowledgeDocs(len(s.knowledge.List()))
	respondJSONStatus(w, http.StatusCreated, d)
}

func (s *Server) knowledgeUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.KnowledgeWebEditEnabled {
		http.Error(w, "knowledge editing disabled", http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if _, exists := s.knowledge.ByID(id); !exists {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.knowledge.IsManaged(id) {
		http.Error(w, "knowledge entry is read-only", http.StatusConflict)
		return
	}
	d, err := s.decodeKnowledge(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if bodyID := strings.TrimSpace(d.ID); bodyID != "" && bodyID != id {
		http.Error(w, "knowledge id cannot be changed while editing", http.StatusConflict)
		return
	}
	d.ID = id
	if err := s.validateKnowledgeCategories(r.Context(), d); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if err := s.knowledge.Upsert(r.Context(), d); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.metrics.SetKnowledgeDocs(len(s.knowledge.List()))
	respondJSON(w, d)
}

func (s *Server) knowledgeDelete(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.KnowledgeWebEditEnabled {
		http.Error(w, "knowledge editing disabled", http.StatusForbidden)
		return
	}
	if err := s.knowledge.Delete(r.PathValue("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, err.Error(), 422)
		}
		return
	}
	s.metrics.SetKnowledgeDocs(len(s.knowledge.List()))
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) learningList(w http.ResponseWriter, r *http.Request) {
	rows := s.feedback.LearningExamples()
	if rows == nil {
		rows = []model.LearningExample{}
	}
	respondJSON(w, rows)
}
func (s *Server) learningAdd(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.LearningEnabled {
		http.Error(w, "learning disabled", http.StatusForbidden)
		return
	}
	var in struct {
		RunID      string `json:"run_id"`
		CategoryID int64  `json:"category_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		http.Error(w, "invalid feedback", 400)
		return
	}
	ex, err := s.feedback.RecordCategoryFeedback(r.Context(), in.RunID, in.CategoryID)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	respondJSON(w, ex)
}
func (s *Server) learningDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.feedback.DeleteLearning(r.PathValue("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, err.Error(), 422)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) outcomeList(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, s.feedback.TicketOutcomes())
}
func (s *Server) outcomeAdd(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.OutcomeLearningEnabled {
		http.Error(w, "outcome learning disabled", http.StatusForbidden)
		return
	}
	var in struct {
		RunID          string `json:"run_id"`
		Decision       string `json:"decision"`
		CorrectedReply string `json:"corrected_reply,omitempty"`
		Note           string `json:"note,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		http.Error(w, "invalid outcome feedback", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(s.cfg.WebUsername)
	if actor == "" {
		actor = "authenticated-technician"
	}
	x, err := s.feedback.RecordTicketOutcome(r.Context(), in.RunID, in.Decision, in.CorrectedReply, in.Note, actor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	respondJSON(w, x)
}

func (s *Server) mutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != "GLPI-AI-Agent" {
			http.Error(w, "missing request guard", http.StatusForbidden)
			return
		}
		if ct := r.Header.Get("Content-Type"); r.Method != "DELETE" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
			http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) reprocessTicket(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if !s.knowledge.Ready() {
		http.Error(w, "knowledge index is not ready", http.StatusServiceUnavailable)
		return
	}
	if !s.q.EnqueueWork(queue.WorkItem{TicketID: id, Trigger: "manual_recheck", Priority: queue.PriorityManual, Force: true}) {
		http.Error(w, "ticket is already queued or the queue is full", http.StatusConflict)
		return
	}
	s.metrics.QueueDepth.Store(int64(s.q.Len()))
	respondJSON(w, map[string]any{"accepted": true, "ticket_id": id, "trigger": "manual_recheck", "dry_run": s.cfg.DryRun})
}

var ticketRE = regexp.MustCompile(`(?i)/Ticket/(\d+)`)

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WebhookSecret == "" {
		http.Error(w, "webhook disabled", http.StatusNotFound)
		return
	}
	got := r.Header.Get("X-Webhook-Secret")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.WebhookSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	id := extractTicketID(body)
	if id <= 0 {
		http.Error(w, "no ticket id found", 422)
		return
	}
	s.metrics.WebhookEvents.Add(1)
	if !s.q.EnqueueWork(queue.WorkItem{TicketID: id, Trigger: "webhook", Priority: queue.PriorityWebhook}) {
		if s.q.Full() {
			// Let GLPI retry instead of silently dropping the event.
			w.Header().Set("Retry-After", "30")
			http.Error(w, "queue full", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted) // already pending
		return
	}
	s.metrics.QueueDepth.Store(int64(s.q.Len()))
	w.WriteHeader(http.StatusAccepted)
}
func extractTicketID(body []byte) int64 {
	var v any
	if json.Unmarshal(body, &v) == nil {
		if id := walkID(v); id > 0 {
			return id
		}
	}
	if m := ticketRE.FindSubmatch(body); len(m) == 2 {
		id, _ := strconv.ParseInt(string(m[1]), 10, 64)
		return id
	}
	return 0
}
func walkID(v any) int64 {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	for _, k := range []string{"ticket_id", "ticketId"} {
		if n := num(m[k]); n > 0 {
			return n
		}
	}
	if typ, ok := m["itemtype"].(string); ok && strings.EqualFold(typ, "Ticket") {
		if n := num(m["id"]); n > 0 {
			return n
		}
		if n := num(m["items_id"]); n > 0 {
			return n
		}
	}
	// A nested object explicitly named "ticket" may legitimately only carry an id.
	if child, ok := m["ticket"].(map[string]any); ok {
		if n := num(child["id"]); n > 0 {
			return n
		}
		if n := walkID(child); n > 0 {
			return n
		}
	}
	// Other generic wrapper objects are searched only for explicit ticket markers;
	// their own generic "id" must never be mistaken for a ticket id.
	for _, k := range []string{"item", "data", "object"} {
		if child, ok := m[k]; ok {
			if n := walkID(child); n > 0 {
				return n
			}
		}
	}
	return 0
}
func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}
func respondJSON(w http.ResponseWriter, v any) { respondJSONStatus(w, http.StatusOK, v) }
func respondJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) auth(next http.Handler) http.Handler {
	if s.cfg.WebAllowAnonymous {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.WebUsername)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.WebPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="GLPI AI Agent"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" {
			slog.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
		}
	})
}

// extendWriteDeadline lifts the server-wide WriteTimeout for handlers that
// legitimately run longer. Writers without deadline support (tests) are ignored.
func extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

func Listen(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
}
func (s *Server) String() string { return fmt.Sprintf("web(%s)", s.cfg.HTTPAddr) }

type qualityReplayCase struct {
	ID                    string   `json:"id"`
	Query                 string   `json:"query"`
	ExpectedKnowledgeID   string   `json:"expected_knowledge_id,omitempty"`
	ExpectedSolutionTerms []string `json:"expected_solution_terms,omitempty"`
	K                     int      `json:"k,omitempty"`
}

type qualityReplayRequest struct {
	Cases []qualityReplayCase `json:"cases"`
}

func (s *Server) qualityReplay(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, 5*time.Minute) // matches the scripts/quality-replay.py client timeout
	var req qualityReplayRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid replay payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Cases) == 0 || len(req.Cases) > 500 {
		http.Error(w, "cases must contain 1..500 items", http.StatusBadRequest)
		return
	}
	ks, ok := s.knowledge.(KnowledgeQualitySearch)
	if !ok {
		http.Error(w, "knowledge search is unavailable", http.StatusServiceUnavailable)
		return
	}
	om, _ := s.feedback.(OutcomeQualityManager)
	type caseResult struct {
		ID                   string  `json:"id"`
		KnowledgeRank        int     `json:"knowledge_rank,omitempty"`
		KnowledgeTopID       string  `json:"knowledge_top_id,omitempty"`
		OutcomeMatchRank     int     `json:"outcome_match_rank,omitempty"`
		OutcomeTopSimilarity float64 `json:"outcome_top_similarity,omitempty"`
		ExperienceRescue     bool    `json:"experience_rescue,omitempty"`
		Error                string  `json:"error,omitempty"`
	}
	results := make([]caseResult, 0, len(req.Cases))
	knowledgeHits, knowledgeReciprocal, knowledgeExpected := 0, 0.0, 0
	outcomeHits, outcomeReciprocal, outcomeExpected, rescues := 0, 0.0, 0, 0
	for _, c := range req.Cases {
		cr := caseResult{ID: strings.TrimSpace(c.ID)}
		if cr.ID == "" {
			cr.ID = fmt.Sprintf("case-%d", len(results)+1)
		}
		query := strings.TrimSpace(c.Query)
		if query == "" {
			cr.Error = "query is required"
			results = append(results, cr)
			continue
		}
		k := c.K
		if k <= 0 {
			k = 10
		}
		if k > 50 {
			k = 50
		}
		kh, err := ks.Search(r.Context(), query, k)
		if err != nil {
			cr.Error = "knowledge: " + err.Error()
			results = append(results, cr)
			continue
		}
		if len(kh) > 0 {
			cr.KnowledgeTopID = kh[0].Doc.ID
		}
		if want := strings.TrimSpace(c.ExpectedKnowledgeID); want != "" {
			knowledgeExpected++
			for i, h := range kh {
				if h.Doc.ID == want {
					cr.KnowledgeRank = i + 1
					knowledgeHits++
					knowledgeReciprocal += 1 / float64(i+1)
					break
				}
			}
		}
		if len(c.ExpectedSolutionTerms) > 0 && om != nil {
			outcomeExpected++
			rows, err := om.SearchValidatedOutcomes(r.Context(), query, k)
			if err != nil {
				cr.Error = strings.TrimSpace(cr.Error + " outcome: " + err.Error())
			} else {
				if len(rows) > 0 {
					cr.OutcomeTopSimilarity = rows[0].Similarity
				}
				for i, row := range rows {
					text := strings.ToLower(row.Text)
					matches := true
					for _, term := range c.ExpectedSolutionTerms {
						term = strings.ToLower(strings.TrimSpace(term))
						if term != "" && !strings.Contains(text, term) {
							matches = false
							break
						}
					}
					if matches {
						cr.OutcomeMatchRank = i + 1
						outcomeHits++
						outcomeReciprocal += 1 / float64(i+1)
						break
					}
				}
			}
			if cr.KnowledgeRank == 0 && cr.OutcomeMatchRank > 0 {
				cr.ExperienceRescue = true
				rescues++
			}
		}
		results = append(results, cr)
	}
	ratio := func(n, d int) float64 {
		if d == 0 {
			return 0
		}
		return float64(n) / float64(d)
	}
	mrr := func(sum float64, d int) float64 {
		if d == 0 {
			return 0
		}
		return sum / float64(d)
	}
	respondJSON(w, map[string]any{
		"cases": results,
		"summary": map[string]any{
			"case_count":               len(results),
			"knowledge_expected":       knowledgeExpected,
			"knowledge_recall_at_k":    ratio(knowledgeHits, knowledgeExpected),
			"knowledge_mrr":            mrr(knowledgeReciprocal, knowledgeExpected),
			"outcome_expected":         outcomeExpected,
			"outcome_recall_at_k":      ratio(outcomeHits, outcomeExpected),
			"outcome_mrr":              mrr(outcomeReciprocal, outcomeExpected),
			"experience_rescued_cases": rescues,
		},
	})
}

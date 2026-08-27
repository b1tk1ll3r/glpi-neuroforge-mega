package httpapi

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"neuroforge/internal/brain"
	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

//go:embed index.html
var webFS embed.FS

type Server struct {
	store               *store.Store
	brain               *brain.Engine
	router              *provider.Router
	cost                *cost.Manager
	mux                 *http.ServeMux
	metrics             *metricsRegistry
	inflight            atomic.Int64
	readinessOllamaLive bool
}

func New(s *store.Store, b *brain.Engine, r *provider.Router, c *cost.Manager) *Server {
	x := &Server{store: s, brain: b, router: r, cost: c, mux: http.NewServeMux(), metrics: newMetricsRegistry()}
	x.routes()
	return x
}

func (s *Server) SetReadinessOllamaLive(enabled bool) { s.readinessOllamaLive = enabled }
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.requestLimits(h)
	h = s.securityHeaders(h)
	h = s.logging(h)
	return h
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.index)
	s.mux.HandleFunc("GET /admin", s.index)
	s.mux.HandleFunc("GET /metrics", s.metricsEndpoint)
	s.mux.HandleFunc("GET /healthz", s.livez)
	s.mux.HandleFunc("GET /livez", s.livez)
	s.mux.HandleFunc("GET /readyz", s.readyz)
	s.mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) { s.json(w, 200, map[string]any{"version": "0.8.2"}) })
	s.mux.Handle("POST /api/v1/chat", s.appAuth(http.HandlerFunc(s.chat)))
	s.mux.Handle("POST /api/v1/learn", s.appAuth(http.HandlerFunc(s.learn)))
	s.mux.Handle("POST /api/v1/search", s.appAuth(http.HandlerFunc(s.search)))
	s.mux.Handle("POST /api/v1/search/vector", s.appAuth(http.HandlerFunc(s.searchVector)))
	s.mux.Handle("POST /api/v1/memory/import", s.appAuth(http.HandlerFunc(s.importMemory)))
	s.mux.Handle("POST /api/v1/feedback", s.appAuth(http.HandlerFunc(s.feedback)))
	s.mux.Handle("GET /api/v1/stats", s.controlReadAuth(http.HandlerFunc(s.stats)))
	s.mux.Handle("GET /api/v1/goals", s.appAuth(http.HandlerFunc(s.goalsList)))
	s.mux.Handle("POST /api/v1/goals", s.appAuth(http.HandlerFunc(s.goalsCreate)))
	s.mux.Handle("GET /api/v1/goals/{id}", s.appAuth(http.HandlerFunc(s.goalsGet)))
	s.mux.Handle("PUT /api/v1/goals/{id}", s.appAuth(http.HandlerFunc(s.goalsPut)))
	s.mux.Handle("DELETE /api/v1/goals/{id}", s.appAuth(http.HandlerFunc(s.goalsDelete)))
	s.mux.Handle("POST /api/v1/goals/{id}/pause", s.appAuth(http.HandlerFunc(s.goalPause)))
	s.mux.Handle("POST /api/v1/goals/{id}/resume", s.appAuth(http.HandlerFunc(s.goalResume)))
	s.mux.Handle("POST /api/v1/goals/{id}/cycle", s.appAuth(http.HandlerFunc(s.goalCycle)))
	s.mux.Handle("GET /api/v1/goals/{id}/research/live", s.appAuth(http.HandlerFunc(s.goalResearchLive)))
	s.mux.Handle("GET /api/v1/goals/{id}/research/history", s.appAuth(http.HandlerFunc(s.goalResearchHistory)))
	s.mux.Handle("GET /api/v1/learning-cycles", s.appAuth(http.HandlerFunc(s.learningCycles)))
	s.mux.Handle("GET /api/v1/conflicts", s.appAuth(http.HandlerFunc(s.conflicts)))
	s.mux.Handle("POST /api/v1/ingest/text", s.appAuth(http.HandlerFunc(s.ingestText)))
	s.mux.Handle("POST /api/v1/ingest/document", s.appAuth(http.HandlerFunc(s.ingestDocument)))
	s.mux.Handle("GET /api/v1/sources", s.appAuth(http.HandlerFunc(s.sourcesList)))
	s.mux.Handle("GET /api/v1/sources/{id}", s.appAuth(http.HandlerFunc(s.sourceGet)))
	s.mux.Handle("POST /api/v1/research", s.appAuth(http.HandlerFunc(s.researchSearch)))
	s.mux.Handle("POST /api/v1/integrations/knowledge/upsert", s.integrationAuth(http.HandlerFunc(s.integrationKnowledgeUpsert)))
	s.mux.Handle("DELETE /api/v1/integrations/knowledge/{namespace}/{document_id}", s.integrationAuth(http.HandlerFunc(s.integrationKnowledgeDelete)))
	s.mux.Handle("POST /api/v1/integrations/knowledge/search", s.integrationAuth(http.HandlerFunc(s.integrationKnowledgeSearch)))
	s.mux.Handle("POST /api/v1/integrations/events", s.integrationAuth(http.HandlerFunc(s.integrationEvent)))
	s.mux.Handle("POST /api/v1/integrations/outcomes", s.integrationAuth(http.HandlerFunc(s.integrationValidatedOutcome)))
	s.mux.Handle("POST /api/v1/integrations/outcomes/search", s.integrationAuth(http.HandlerFunc(s.integrationValidatedOutcomeSearch)))
	s.mux.Handle("GET /api/v1/integrations/graph/research", s.controlReadAuth(http.HandlerFunc(s.integrationResearchGraph)))
	s.mux.Handle("GET /api/v1/integrations/graph/brain", s.controlReadAuth(http.HandlerFunc(s.integrationBrainGraph)))

	s.mux.Handle("POST /internal/v1/cluster/request-vote", s.clusterAuth(http.HandlerFunc(s.clusterRequestVote)))
	s.mux.Handle("POST /internal/v1/cluster/heartbeat", s.clusterAuth(http.HandlerFunc(s.clusterHeartbeat)))
	s.mux.Handle("POST /internal/v1/cluster/prepare", s.clusterAuth(http.HandlerFunc(s.clusterPrepare)))
	s.mux.Handle("POST /internal/v1/cluster/commit", s.clusterAuth(http.HandlerFunc(s.clusterCommit)))
	s.mux.Handle("POST /internal/v1/cluster/abort", s.clusterAuth(http.HandlerFunc(s.clusterAbort)))
	s.mux.Handle("POST /internal/v1/cluster/propose/memory", s.clusterAuth(http.HandlerFunc(s.clusterProposeMemory)))
	s.mux.Handle("GET /internal/v1/cluster/decision/{id}", s.clusterAuth(http.HandlerFunc(s.clusterDecision)))
	s.mux.Handle("GET /internal/v1/cluster/status", s.clusterAuth(http.HandlerFunc(s.clusterStatus)))

	s.mux.Handle("POST /api/v1/worker/claim", s.workerAuth(http.HandlerFunc(s.workerClaim)))
	s.mux.Handle("POST /api/v1/worker/complete", s.workerAuth(http.HandlerFunc(s.workerComplete)))

	s.mux.Handle("GET /admin/api/status", s.adminAuth(http.HandlerFunc(s.adminStatus)))
	s.mux.Handle("GET /admin/api/config", s.adminAuth(http.HandlerFunc(s.adminGetConfig)))
	s.mux.Handle("PUT /admin/api/config", s.adminAuth(http.HandlerFunc(s.adminPutConfig)))
	s.mux.Handle("GET /admin/api/model-routing", s.adminAuth(http.HandlerFunc(s.adminGetModelRouting)))
	s.mux.Handle("PUT /admin/api/model-routing", s.adminAuth(http.HandlerFunc(s.adminPutModelRouting)))
	s.mux.Handle("GET /admin/api/secrets/status", s.adminAuth(http.HandlerFunc(s.adminSecretsStatus)))
	s.mux.Handle("GET /admin/api/secrets", s.adminAuth(http.HandlerFunc(s.adminGetSecrets)))
	s.mux.Handle("PUT /admin/api/secrets", s.adminAuth(http.HandlerFunc(s.adminPutSecrets)))
	s.mux.Handle("POST /admin/api/provider-health", s.adminAuth(http.HandlerFunc(s.adminProviderHealth)))
	s.mux.Handle("GET /admin/api/memories", s.adminAuth(http.HandlerFunc(s.adminMemories)))
	s.mux.Handle("DELETE /admin/api/memories/{id}", s.adminAuth(http.HandlerFunc(s.adminDeleteMemory)))
	s.mux.Handle("GET /admin/api/synapses", s.adminAuth(http.HandlerFunc(s.adminSynapses)))
	s.mux.Handle("GET /admin/api/usage", s.adminAuth(http.HandlerFunc(s.adminUsage)))
	s.mux.Handle("GET /admin/api/export", s.adminAuth(http.HandlerFunc(s.adminExport)))
	s.mux.Handle("POST /admin/api/consolidate", s.adminAuth(http.HandlerFunc(s.adminConsolidate)))
	s.mux.Handle("POST /admin/api/retention", s.adminAuth(http.HandlerFunc(s.adminRetention)))
	s.mux.Handle("POST /admin/api/autonomy", s.adminAuth(http.HandlerFunc(s.adminAutonomy)))
	s.mux.Handle("POST /admin/api/rebalance", s.adminAuth(http.HandlerFunc(s.adminRebalance)))
	s.mux.Handle("POST /admin/api/checkpoint", s.adminAuth(http.HandlerFunc(s.adminCheckpoint)))
	s.mux.Handle("GET /admin/api/wal", s.adminAuth(http.HandlerFunc(s.adminWAL)))
	s.mux.Handle("GET /admin/api/storage", s.adminAuth(http.HandlerFunc(s.adminStorageStatus)))
	s.mux.Handle("POST /admin/api/storage/compact", s.adminAuth(http.HandlerFunc(s.adminCompactSegments)))
	s.mux.Handle("POST /admin/api/storage/tier", s.adminAuth(http.HandlerFunc(s.adminTierStorage)))
	s.mux.Handle("POST /admin/api/index/merge", s.adminAuth(http.HandlerFunc(s.adminMergeIndex)))
	s.mux.Handle("GET /admin/api/index/disk", s.adminAuth(http.HandlerFunc(s.adminDiskANNStatus)))
	s.mux.Handle("POST /admin/api/index/disk/rebuild", s.adminAuth(http.HandlerFunc(s.adminDiskANNBuild)))
	s.mux.Handle("GET /admin/api/cluster", s.adminAuth(http.HandlerFunc(s.clusterStatus)))
	s.mux.Handle("POST /admin/api/cluster/repair", s.adminAuth(http.HandlerFunc(s.adminClusterRepair)))
	s.mux.Handle("POST /admin/api/conflicts/resolve", s.adminAuth(http.HandlerFunc(s.adminResolveConflict)))
	s.mux.Handle("GET /admin/api/knowledge/summary", s.adminAuth(http.HandlerFunc(s.adminKnowledgeSummary)))
	s.mux.Handle("GET /admin/api/knowledge/memories", s.adminAuth(http.HandlerFunc(s.adminKnowledgeMemories)))
	s.mux.Handle("GET /admin/api/knowledge/memory/{id}", s.adminAuth(http.HandlerFunc(s.adminKnowledgeMemory)))
	s.mux.Handle("GET /admin/api/knowledge/graph", s.adminAuth(http.HandlerFunc(s.adminKnowledgeGraph)))
	s.mux.Handle("GET /admin/api/knowledge/events", s.adminAuth(http.HandlerFunc(s.adminKnowledgeEvents)))
	s.mux.Handle("POST /admin/api/knowledge/search", s.adminAuth(http.HandlerFunc(s.adminKnowledgeSearch)))
	s.mux.Handle("GET /admin/api/learning-policy", s.adminAuth(http.HandlerFunc(s.adminGetLearningPolicy)))
	s.mux.Handle("PUT /admin/api/learning-policy", s.adminAuth(http.HandlerFunc(s.adminPutLearningPolicy)))
	s.mux.Handle("GET /admin/api/research", s.adminAuth(http.HandlerFunc(s.adminResearchGet)))
	s.mux.Handle("PUT /admin/api/research", s.adminAuth(http.HandlerFunc(s.adminResearchPut)))
	s.mux.Handle("POST /admin/api/research/test", s.adminAuth(http.HandlerFunc(s.adminResearchTest)))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Write(b)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		d := time.Since(start)
		s.metrics.observeHTTP(r.Method, normalizeMetricRoute(r), sw.status, sw.bytes, d)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, func() int {
			if sw.status == 0 {
				return http.StatusOK
			}
			return sw.status
		}(), d.Round(time.Millisecond))
	})
}

func secureEqual(a, b string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
func (s *Server) appAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Config()
		if cfg.API.RequireKey {
			sec := s.store.Secrets()
			// The admin dashboard is already authenticated with the stronger admin
			// credential. Allow it to call application endpoints directly so the
			// browser never needs the App API key (which is masked by default in
			// production). External applications still authenticate with Bearer.
			adminOK := secureEqual(r.Header.Get("X-Admin-Token"), sec.AdminToken)
			appOK := secureEqual(bearer(r), sec.AppAPIKey)
			if !adminOK && !appOK {
				s.err(w, 401, errors.New("invalid app API key or admin token"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) integrationAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sec := s.store.Secrets()
		adminOK := secureEqual(r.Header.Get("X-Admin-Token"), sec.AdminToken)
		integrationOK := secureEqual(bearer(r), sec.IntegrationToken)
		if !adminOK && !integrationOK {
			s.err(w, http.StatusUnauthorized, errors.New("invalid integration token or admin token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) controlReadAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sec := s.store.Secrets()
		adminOK := secureEqual(r.Header.Get("X-Admin-Token"), sec.AdminToken)
		controlOK := secureEqual(bearer(r), sec.ControlReadToken)
		appOK := secureEqual(bearer(r), sec.AppAPIKey)
		if !adminOK && !controlOK && !appOK {
			s.err(w, http.StatusUnauthorized, errors.New("invalid control/app read token or admin token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) workerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(bearer(r), s.store.Secrets().WorkerToken) {
			s.err(w, 401, errors.New("invalid worker token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("X-Admin-Token"), s.store.Secrets().AdminToken) {
			s.err(w, 401, errors.New("invalid admin token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) clusterAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sec := s.store.Secrets()
		if sec.ClusterToken == "" || !secureEqual(r.Header.Get("X-Cluster-Token"), sec.ClusterToken) {
			s.err(w, 401, errors.New("invalid cluster token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 128<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) err(w http.ResponseWriter, status int, err error) {
	s.json(w, status, map[string]any{"error": err.Error()})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var q brain.ChatRequest
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	out, err := s.brain.Chat(r.Context(), q)
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, 200, out)
}
func (s *Server) learn(w http.ResponseWriter, r *http.Request) {
	var q brain.LearnRequest
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	m, err := s.brain.Learn(r.Context(), q)
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, 201, m)
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Text string `json:"text"`
		K    int    `json:"k"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	hits, err := s.brain.Search(r.Context(), q.Text, q.K)
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, 200, hits)
}
func (s *Server) searchVector(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Vector        []float32 `json:"vector"`
		K             int       `json:"k"`
		MinSimilarity *float64  `json:"min_similarity,omitempty"`
		GraphBonus    *float64  `json:"graph_bonus,omitempty"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if len(q.Vector) == 0 {
		s.err(w, 400, errors.New("vector required"))
		return
	}
	cfg := s.store.Config()
	if q.K <= 0 {
		q.K = cfg.Brain.RecallK
	}
	min := cfg.Brain.MinSimilarity
	if q.MinSimilarity != nil {
		min = *q.MinSimilarity
	}
	bonus := cfg.Brain.GraphBonus
	if q.GraphBonus != nil {
		bonus = *q.GraphBonus
	}
	// Intentionally local-only: shard federation is one hop and must not recurse.
	hits := s.store.SearchVector(q.Vector, q.K, min, bonus)
	for i := range hits {
		if hits[i].Memory.ShardID == "" {
			hits[i].Memory.ShardID = cfg.Sharding.LocalShardID
		}
	}
	s.json(w, 200, hits)
}

func (s *Server) importMemory(w http.ResponseWriter, r *http.Request) {
	var m core.Memory
	if err := decode(r, &m); err != nil {
		s.err(w, 400, err)
		return
	}
	out, err := s.brain.ImportMemory(r.Context(), m)
	if err != nil {
		s.err(w, 400, err)
		return
	}
	s.json(w, 201, out)
}

func (s *Server) feedback(w http.ResponseWriter, r *http.Request) {
	var q brain.FeedbackRequest
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.brain.Feedback(q); err != nil {
		s.err(w, 400, err)
		return
	}
	s.json(w, 200, map[string]bool{"ok": true})
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, map[string]any{"stats": s.store.Stats(), "cost": s.cost.Totals()})
}

func (s *Server) workerClaim(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorkerID string `json:"worker_id"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if q.WorkerID == "" {
		s.err(w, 400, errors.New("worker_id required"))
		return
	}
	lease := s.store.Config().Worker.LeaseSeconds
	if lease < 10 {
		lease = 120
	}
	j, err := s.store.ClaimJob(q.WorkerID, time.Duration(lease)*time.Second)
	if err != nil {
		s.err(w, 500, err)
		return
	}
	if j == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.json(w, 200, j)
}
func (s *Server) workerComplete(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorkerID string          `json:"worker_id"`
		JobID    string          `json:"job_id"`
		Result   json.RawMessage `json:"result"`
		Error    string          `json:"error"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	j, err := s.store.CompleteJob(q.JobID, q.WorkerID, q.Result, q.Error)
	if err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.brain.ApplyJobResult(j); err != nil {
		s.err(w, 500, err)
		return
	}
	s.json(w, 200, map[string]bool{"ok": true})
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	obs := s.store.ObservabilitySnapshot()
	maint := s.store.MaintenanceStatus()
	stats := map[string]any{
		"revision": obs.Revision, "memories": obs.Memories, "synapses": obs.Synapses, "goals": obs.Goals, "learning_cycles": obs.LearningCycles,
		"pending_jobs": obs.JobsQueued + obs.JobsClaimed, "hnsw_nodes": obs.HNSWNodes, "hnsw_dimensions": obs.HNSWDimensions,
		"disk_pq_items": obs.DiskPQItems, "disk_pq_bytes": obs.DiskPQBytes, "index_mode": obs.IndexMode, "remote_shards": obs.RemoteShards, "maintenance": maint,
	}
	cfg := s.store.Config()
	sec := s.store.Secrets()
	providers := make([]map[string]any, 0, len(cfg.Ollama)+1)
	for _, o := range cfg.Ollama {
		providers = append(providers, map[string]any{"provider": "ollama", "id": o.ID, "name": o.Name, "model": o.ChatModel, "enabled": o.Enabled})
	}
	providers = append(providers, map[string]any{"provider": "openai", "model": cfg.OpenAI.ChatModel, "enabled": cfg.OpenAI.Enabled, "configured": sec.OpenAIAPIKey != ""})
	tiering := map[string]any{
		"hot_memories": obs.HotMemories, "cold_memories": obs.ColdMemories, "hot_bytes": obs.HotBytes, "tier_evictions_total": obs.TierEvictions,
		"page_cache": map[string]any{"enabled": obs.PageCacheEnabled, "max_bytes": obs.PageCacheMaxBytes, "bytes": obs.PageCacheBytes, "entries": obs.PageCacheEntries, "hits": obs.PageCacheHits, "misses": obs.PageCacheMisses, "evictions": obs.PageCacheEvicts},
	}
	cluster := map[string]any{
		"enabled": obs.ClusterEnabled, "node_id": obs.ClusterNodeID, "leader_id": obs.ClusterLeaderID, "role": obs.ClusterRole, "term": obs.ClusterTerm,
		"last_index": obs.ClusterLastIndex, "commit_index": obs.ClusterCommitIndex, "peers": obs.ClusterPeers, "voters": obs.ClusterVoters, "quorum": obs.ClusterQuorum,
		"replicated_log": obs.ClusterLog,
	}
	payload := map[string]any{
		"stats": stats, "wal": s.store.WALStatus(),
		"storage": map[string]any{"memory_segments": obs.Segments, "index_snapshot": map[string]any{"revision": obs.IndexSnapshotRevision, "deltas": obs.IndexDeltaCount, "segmented": cfg.Storage.IndexSegments.Enabled}, "tiering": tiering, "disk_ann": s.store.DiskANNStatus()},
		"cluster": cluster, "cost": s.cost.Totals(), "providers": providers, "usage": s.store.RecentUsage(25),
		"observability": obs, "runtime": currentRuntimeSnapshot(), "http": s.metrics.dashboardSnapshot(),
	}
	// Keep the original flat status fields for dashboard and external-client
	// compatibility while retaining the richer nested stats object.
	for k, v := range stats {
		payload[k] = v
	}
	s.json(w, 200, payload)
}
func (s *Server) adminGetConfig(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.Config())
}
func (s *Server) adminPutConfig(w http.ResponseWriter, r *http.Request) {
	var c core.Config
	if err := decode(r, &c); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.ValidateConfig(c); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.UpdateConfig(c); err != nil {
		s.err(w, 500, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "admin.config_changed", Summary: "Runtime configuration updated", Reason: "PUT /admin/api/config", Actor: "admin"})
	s.json(w, 200, c)
}

type modelRoutingLearning struct {
	AutoRewardEnabled    bool   `json:"auto_reward_enabled"`
	AutoRewardMode       string `json:"auto_reward_mode"`
	ConsolidationEnabled bool   `json:"consolidation_enabled"`
	ConsolidationUseLLM  bool   `json:"consolidation_use_llm"`
	AutonomyEnabled      bool   `json:"autonomy_enabled"`
	AutonomyUseLLM       bool   `json:"autonomy_use_llm"`
}

type modelRoutingSettings struct {
	Routing  core.RoutingConfig   `json:"routing"`
	Ollama   []core.OllamaServer  `json:"ollama"`
	Learning modelRoutingLearning `json:"learning"`
}

type modelRoutingUpdate struct {
	Routing  *core.RoutingConfig   `json:"routing,omitempty"`
	Ollama   *[]core.OllamaServer  `json:"ollama,omitempty"`
	Learning *modelRoutingLearning `json:"learning,omitempty"`
}

func modelRoutingFromConfig(c core.Config) modelRoutingSettings {
	var out modelRoutingSettings
	out.Routing = c.Routing
	out.Ollama = append([]core.OllamaServer(nil), c.Ollama...)
	out.Learning.AutoRewardEnabled = c.Brain.AutoReward.Enabled
	out.Learning.AutoRewardMode = c.Brain.AutoReward.Mode
	out.Learning.ConsolidationEnabled = c.Brain.Consolidation.Enabled
	out.Learning.ConsolidationUseLLM = c.Brain.Consolidation.UseLLM
	out.Learning.AutonomyEnabled = c.Autonomy.Enabled
	out.Learning.AutonomyUseLLM = c.Autonomy.UseLLM
	return out
}

func (s *Server) adminGetModelRouting(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, modelRoutingFromConfig(s.store.Config()))
}

func (s *Server) adminPutModelRouting(w http.ResponseWriter, r *http.Request) {
	var q modelRoutingUpdate
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	if q.Routing == nil && q.Ollama == nil && q.Learning == nil {
		s.err(w, 400, errors.New("routing, ollama or learning is required"))
		return
	}
	c := s.store.Config()
	if q.Routing != nil {
		c.Routing = *q.Routing
	}
	if q.Ollama != nil {
		c.Ollama = append([]core.OllamaServer(nil), (*q.Ollama)...)
	}
	if q.Learning != nil {
		c.Brain.AutoReward.Enabled = q.Learning.AutoRewardEnabled
		c.Brain.AutoReward.Mode = q.Learning.AutoRewardMode
		c.Brain.Consolidation.Enabled = q.Learning.ConsolidationEnabled
		c.Brain.Consolidation.UseLLM = q.Learning.ConsolidationUseLLM
		c.Autonomy.Enabled = q.Learning.AutonomyEnabled
		c.Autonomy.UseLLM = q.Learning.AutonomyUseLLM
	}
	if err := s.store.ValidateConfig(c); err != nil {
		s.err(w, 400, err)
		return
	}
	if err := s.store.UpdateConfig(c); err != nil {
		s.err(w, 500, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "admin.model_routing_changed", Summary: "Model routing / Ollama configuration updated", Reason: "PUT /admin/api/model-routing", Actor: "admin", Metadata: map[string]string{"chat_provider": c.Routing.ChatProvider, "embedding_provider": c.Routing.EmbeddingProvider}})
	s.json(w, 200, modelRoutingFromConfig(c))
}

func (s *Server) adminSecretsStatus(w http.ResponseWriter, r *http.Request) {
	sec := s.store.Secrets()
	s.json(w, 200, map[string]any{"openai_configured": sec.OpenAIAPIKey != "", "app_key_configured": sec.AppAPIKey != "", "integration_token_configured": sec.IntegrationToken != "", "control_read_token_configured": sec.ControlReadToken != "", "worker_token_configured": sec.WorkerToken != "", "metrics_token_configured": sec.MetricsToken != "", "shard_tokens": len(sec.ShardAPIToken), "cluster_token_configured": sec.ClusterToken != ""})
}
func maskedSecret(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "••••"
	}
	return "••••••••" + v[len(v)-4:]
}
func (s *Server) adminGetSecrets(w http.ResponseWriter, r *http.Request) {
	sec := s.store.Secrets()
	reveal := r.URL.Query().Get("reveal") == "1" && s.store.Config().Security.AllowSecretReveal
	if reveal {
		s.json(w, 200, map[string]any{"revealed": true, "app_api_key": sec.AppAPIKey, "integration_token": sec.IntegrationToken, "control_read_token": sec.ControlReadToken, "worker_token": sec.WorkerToken, "metrics_token": sec.MetricsToken, "shard_api_tokens": sec.ShardAPIToken, "cluster_token": sec.ClusterToken})
		return
	}
	maskedShards := map[string]string{}
	for k, v := range sec.ShardAPIToken {
		maskedShards[k] = maskedSecret(v)
	}
	s.json(w, 200, map[string]any{"revealed": false, "reveal_allowed": s.store.Config().Security.AllowSecretReveal, "app_api_key": maskedSecret(sec.AppAPIKey), "integration_token": maskedSecret(sec.IntegrationToken), "control_read_token": maskedSecret(sec.ControlReadToken), "worker_token": maskedSecret(sec.WorkerToken), "metrics_token": maskedSecret(sec.MetricsToken), "shard_api_tokens": maskedShards, "cluster_token": maskedSecret(sec.ClusterToken)})
}
func (s *Server) adminPutSecrets(w http.ResponseWriter, r *http.Request) {
	var q struct {
		OpenAIAPIKey     string            `json:"openai_api_key,omitempty"`
		AppAPIKey        string            `json:"app_api_key,omitempty"`
		IntegrationToken string            `json:"integration_token,omitempty"`
		ControlReadToken string            `json:"control_read_token,omitempty"`
		WorkerToken      string            `json:"worker_token,omitempty"`
		MetricsToken     string            `json:"metrics_token,omitempty"`
		ShardAPIToken    map[string]string `json:"shard_api_tokens,omitempty"`
		ClusterToken     string            `json:"cluster_token,omitempty"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	sec := s.store.Secrets()
	envLocked := func(name string) bool {
		_, ok := os.LookupEnv(name)
		return ok && strings.TrimSpace(os.Getenv(name)) != ""
	}
	for name, value := range map[string]string{
		"OPENAI_API_KEY":                q.OpenAIAPIKey,
		"NEUROFORGE_APP_API_KEY":        q.AppAPIKey,
		"NEUROFORGE_INTEGRATION_TOKEN":  q.IntegrationToken,
		"NEUROFORGE_CONTROL_READ_TOKEN": q.ControlReadToken,
		"NEUROFORGE_WORKER_TOKEN":       q.WorkerToken,
		"NEUROFORGE_METRICS_TOKEN":      q.MetricsToken,
		"NEUROFORGE_CLUSTER_TOKEN":      q.ClusterToken,
	} {
		if value != "" && envLocked(name) {
			s.err(w, http.StatusConflict, fmt.Errorf("%s is environment-managed and cannot be changed through the admin API", name))
			return
		}
	}
	if q.OpenAIAPIKey != "" {
		sec.OpenAIAPIKey = q.OpenAIAPIKey
	}
	if q.AppAPIKey != "" {
		sec.AppAPIKey = q.AppAPIKey
	}
	if q.IntegrationToken != "" {
		sec.IntegrationToken = q.IntegrationToken
	}
	if q.ControlReadToken != "" {
		sec.ControlReadToken = q.ControlReadToken
	}
	if q.WorkerToken != "" {
		sec.WorkerToken = q.WorkerToken
	}
	if q.MetricsToken != "" {
		sec.MetricsToken = q.MetricsToken
	}
	if q.ShardAPIToken != nil {
		sec.ShardAPIToken = q.ShardAPIToken
	}
	if q.ClusterToken != "" {
		sec.ClusterToken = q.ClusterToken
	}
	if err := s.store.UpdateSecrets(sec); err != nil {
		s.err(w, 500, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "admin.secrets_changed", Summary: "One or more service credentials were updated", Reason: "PUT /admin/api/secrets", Actor: "admin"})
	s.json(w, 200, map[string]bool{"ok": true})
}
func (s *Server) adminProviderHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	s.json(w, 200, s.router.Health(ctx))
}
func (s *Server) adminMemories(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	all := s.store.MemoriesSnapshot()
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	s.json(w, 200, all)
}
func (s *Server) adminDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, _ := s.store.GetMemory(id)
	if err := s.store.DeleteMemory(id); err != nil {
		s.err(w, 500, err)
		return
	}
	if m != nil {
		_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{Type: "memory.deleted", MemoryID: id, Summary: "Memory deleted by administrator", Reason: "DELETE /admin/api/memories/{id}", Actor: "admin", Metadata: map[string]string{"kind": m.Kind, "memory_type": m.MemoryType, "truth_key": m.TruthKey}})
	}
	s.json(w, 200, map[string]bool{"ok": true})
}
func (s *Server) adminSynapses(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.SynapsesSnapshot())
}
func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	s.json(w, 200, s.store.RecentUsage(limit))
}
func (s *Server) adminExport(w http.ResponseWriter, r *http.Request) {
	s.json(w, 200, s.store.ExportSafe())
}

func (s *Server) adminConsolidate(w http.ResponseWriter, r *http.Request) {
	out, err := s.brain.Consolidate(r.Context())
	if err != nil {
		// A cycle can partially succeed and still report a synthesis/provider error.
		s.json(w, 207, map[string]any{"result": out, "warning": err.Error()})
		return
	}
	s.json(w, 200, out)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.store.Config().Security.SecureHeaders {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
			w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; connect-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		}
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Header.Get("X-Request-ID") == "" {
			r.Header.Set("X-Request-ID", store.NewID("req"))
		}
		w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLimits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Config().HTTP
		if cfg.MaxBodyBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		n := s.inflight.Add(1)
		defer s.inflight.Add(-1)
		max := int64(cfg.MaxConcurrentRequests)
		if max <= 0 {
			max = 128
		}
		if n > max {
			w.Header().Set("Retry-After", "1")
			s.err(w, http.StatusServiceUnavailable, errors.New("server is at the configured concurrent request limit"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) livez(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]any{"ok": true, "status": "alive", "time": time.Now().UTC(), "version": "0.8.2"})
}

func configuredModelAvailable(models map[string]bool, configured string) bool {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return false
	}
	if models[configured] {
		return true
	}
	if !strings.Contains(configured, ":") && models[configured+":latest"] {
		return true
	}
	return false
}

func checkConfiguredOllamaModels(ctx context.Context, cfg core.Config) (bool, any) {
	type tagsResponse struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	details := map[string]any{}
	anyEnabled := false
	for _, node := range cfg.Ollama {
		if !node.Enabled || strings.TrimSpace(node.BaseURL) == "" {
			continue
		}
		anyEnabled = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(node.BaseURL, "/")+"/api/tags", nil)
		if err != nil {
			details[node.ID] = err.Error()
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			details[node.ID] = err.Error()
			continue
		}
		var tags tagsResponse
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tags)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || decodeErr != nil {
			details[node.ID] = fmt.Sprintf("HTTP %d / invalid tags response", resp.StatusCode)
			continue
		}
		models := map[string]bool{}
		for _, model := range tags.Models {
			models[strings.TrimSpace(model.Name)] = true
		}
		chatOK := configuredModelAvailable(models, node.ChatModel)
		embedOK := configuredModelAvailable(models, node.EmbeddingModel)
		details[node.ID] = map[string]any{"reachable": true, "chat_model": node.ChatModel, "chat_present": chatOK, "embedding_model": node.EmbeddingModel, "embedding_present": embedOK}
		if chatOK && embedOK {
			return true, details
		}
	}
	if !anyEnabled {
		return false, map[string]any{"error": "no enabled Ollama node configured"}
	}
	return false, details
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Config()
	sec := s.store.Secrets()
	obs := s.store.ObservabilitySnapshot()
	components := map[string]any{}
	configOK := s.store.ValidateConfig(cfg) == nil
	components["config"] = configOK
	components["app_auth"] = !cfg.API.RequireKey || sec.AppAPIKey != ""
	hasOllamaChat, hasOllamaEmbed := false, false
	for _, o := range cfg.Ollama {
		if !o.Enabled {
			continue
		}
		if strings.TrimSpace(o.ChatModel) != "" {
			hasOllamaChat = true
		}
		if strings.TrimSpace(o.EmbeddingModel) != "" {
			hasOllamaEmbed = true
		}
	}
	openAIReady := cfg.OpenAI.Enabled && sec.OpenAIAPIKey != ""
	chatReady := (cfg.Routing.ChatProvider == "openai" && openAIReady) || (cfg.Routing.ChatProvider == "ollama" && hasOllamaChat) || ((cfg.Routing.ChatProvider == "auto" || cfg.Routing.ChatProvider == "") && (hasOllamaChat || openAIReady))
	embedReady := (cfg.Routing.EmbeddingProvider == "openai" && openAIReady) || (cfg.Routing.EmbeddingProvider == "ollama" && hasOllamaEmbed) || ((cfg.Routing.EmbeddingProvider == "auto" || cfg.Routing.EmbeddingProvider == "") && (hasOllamaEmbed || openAIReady))
	components["chat_route_configured"] = chatReady
	components["embedding_route_configured"] = embedReady
	clusterReady := !cfg.Cluster.Enabled || obs.ClusterLeaderID != ""
	components["cluster"] = clusterReady
	ollamaLiveReady := true
	if s.readinessOllamaLive {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		var detail any
		ollamaLiveReady, detail = checkConfiguredOllamaModels(ctx, cfg)
		cancel()
		components["ollama_live_models"] = detail
	}
	stagingReady := true
	if s.brain != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		if err := s.brain.CheckStagingPublisher(ctx); err != nil {
			stagingReady = false
			components["kb_staging"] = err.Error()
		} else {
			components["kb_staging"] = true
		}
		cancel()
	}
	ready := configOK && chatReady && embedReady && clusterReady && ollamaLiveReady && stagingReady && (!cfg.API.RequireKey || sec.AppAPIKey != "")
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	s.json(w, status, map[string]any{"ok": ready, "status": map[bool]string{true: "ready", false: "not_ready"}[ready], "components": components, "revision": obs.Revision, "time": time.Now().UTC()})
}

package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"neuroforge/internal/core"
)

func (s *Server) adminKnowledgeSummary(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.store.KnowledgeSummary())
}

func (s *Server) adminKnowledgeMemories(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var before time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			s.err(w, 400, errors.New("before must be RFC3339"))
			return
		}
	}
	out := s.store.KnowledgeMemories(limit, before, r.URL.Query().Get("memory_type"), r.URL.Query().Get("status"), r.URL.Query().Get("kind"), r.URL.Query().Get("source"))
	s.json(w, http.StatusOK, out)
}

func (s *Server) adminKnowledgeMemory(w http.ResponseWriter, r *http.Request) {
	out, ok := s.store.KnowledgeMemoryDetail(r.PathValue("id"))
	if !ok {
		s.err(w, 404, errors.New("memory not found"))
		return
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) adminKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))
	maxNodes, _ := strconv.Atoi(r.URL.Query().Get("max_nodes"))
	s.json(w, http.StatusOK, s.store.KnowledgeGraph(r.URL.Query().Get("center"), depth, maxNodes))
}

func (s *Server) adminKnowledgeEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	s.json(w, http.StatusOK, s.store.RecentKnowledgeEvents(limit))
}

func (s *Server) adminKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Text string `json:"text"`
		K    int    `json:"k"`
	}
	if err := decode(r, &q); err != nil {
		s.err(w, 400, err)
		return
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		s.err(w, 400, errors.New("text is required"))
		return
	}
	if q.K <= 0 {
		q.K = 12
	}
	if q.K > 50 {
		q.K = 50
	}
	hits, err := s.brain.Search(r.Context(), q.Text, q.K)
	if err != nil {
		s.err(w, 502, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{
		"query":   q.Text,
		"hits":    hits,
		"formula": "score = similarity × salience_factor × type_weight × confidence_factor + graph_boost",
		"note":    "candidate_source shows whether a candidate came from HNSW, Disk-PQ, full scan, or a synapse expansion; final similarity is always computed against the original vector when available.",
	})
}

type learningPolicySettings struct {
	AutoLearn bool                      `json:"auto_learn"`
	Policy    core.LearningPolicyConfig `json:"policy"`
}

func (s *Server) adminGetLearningPolicy(w http.ResponseWriter, r *http.Request) {
	c := s.store.Config()
	s.json(w, http.StatusOK, learningPolicySettings{AutoLearn: c.Brain.AutoLearn, Policy: c.Brain.LearningPolicy})
}

func (s *Server) adminPutLearningPolicy(w http.ResponseWriter, r *http.Request) {
	var q learningPolicySettings
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	c := s.store.Config()
	c.Brain.AutoLearn = q.AutoLearn
	c.Brain.LearningPolicy = q.Policy
	if err := s.store.ValidateConfig(c); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.UpdateConfig(c); err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{
		Type: "admin.learning_policy_changed", Summary: "Learning policy updated", Reason: "PUT /admin/api/learning-policy", Actor: "admin",
		Metadata: map[string]string{"auto_learn": strconv.FormatBool(q.AutoLearn), "enabled": strconv.FormatBool(q.Policy.Enabled), "duplicate_similarity": strconv.FormatFloat(q.Policy.DuplicateSimilarity, 'f', 4, 64)},
	})
	s.json(w, http.StatusOK, q)
}

func (s *Server) adminGraphStatus(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]any{
		"graph":        s.store.GraphStats(),
		"orchestrator": s.store.OrchestratorStatus(),
		"config": map[string]any{
			"max_hops":            s.store.Config().Brain.GraphMaxHops,
			"hop_decay":           s.store.Config().Brain.GraphHopDecay,
			"max_expansion":       s.store.Config().Brain.GraphMaxExpansion,
			"min_edge_weight":     s.store.Config().Brain.GraphMinEdgeWeight,
			"backfill_enabled":    s.store.Config().Worker.GraphBackfillEnabled,
			"backfill_min_degree": s.store.Config().Worker.GraphBackfillMinDegree,
			"backfill_max_queued": s.store.Config().Worker.GraphBackfillMaxQueued,
		},
	})
}

func (s *Server) adminGraphBackfill(w http.ResponseWriter, r *http.Request) {
	planned, err := s.brain.PlanGraphBackfill()
	if err != nil {
		s.err(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusAccepted, map[string]any{"ok": true, "planned": planned, "graph": s.store.GraphStats()})
}

func (s *Server) adminOrchestratorStatus(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.store.OrchestratorStatus())
}

func (s *Server) adminOrchestratorJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	s.json(w, http.StatusOK, s.store.JobsSnapshot(limit, r.URL.Query().Get("status"), r.URL.Query().Get("type")))
}

func (s *Server) adminOrchestratorRetry(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.RetryJob(r.PathValue("id"))
	if err != nil {
		s.err(w, http.StatusConflict, err)
		return
	}
	s.json(w, http.StatusOK, job)
}

func (s *Server) adminOrchestratorCancel(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Reason string `json:"reason"`
	}
	// Empty bodies are valid for an operator cancellation; malformed non-empty
	// JSON is not.
	if r.ContentLength != 0 {
		if err := decode(r, &q); err != nil {
			s.err(w, http.StatusBadRequest, err)
			return
		}
	}
	if strings.TrimSpace(q.Reason) == "" {
		q.Reason = "canceled by administrator"
	}
	if err := s.store.CancelJob(r.PathValue("id"), q.Reason); err != nil {
		s.err(w, http.StatusNotFound, err)
		return
	}
	job, _ := s.store.Job(r.PathValue("id"))
	s.json(w, http.StatusOK, job)
}

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

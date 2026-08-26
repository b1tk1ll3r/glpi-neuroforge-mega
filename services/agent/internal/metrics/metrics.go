package metrics

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Started                   time.Time
	Processed                 atomic.Uint64
	Skipped                   atomic.Uint64
	Errors                    atomic.Uint64
	CategoryChanged           atomic.Uint64
	Replies                   atomic.Uint64
	PriorityRecommendations   atomic.Uint64
	PriorityChanges           atomic.Uint64
	EscalationRuns            atomic.Uint64
	Escalations               atomic.Uint64
	Polls                     atomic.Uint64
	WebhookEvents             atomic.Uint64
	ContextFetches            atomic.Uint64
	ContextErrors             atomic.Uint64
	QueueDepth                atomic.Int64
	OutcomeSearches           atomic.Uint64
	OutcomeSearchHits         atomic.Uint64
	OutcomeSearchErrors       atomic.Uint64
	OutcomeLearningLearned    atomic.Uint64
	OutcomeLearningAccepted   atomic.Uint64
	OutcomeLearningCorrected  atomic.Uint64
	OutcomeLearningFailed     atomic.Uint64
	OutcomeLearningIdempotent atomic.Uint64
	mu                        sync.RWMutex
	lastPoll                  time.Time
	lastPollFetched           int
	lastPollSeen              int
	lastPollUnseen            int
	lastPollEnqueued          int
	lastPollRejected          int
	lastPollError             string
	glpiOK                    bool
	ollamaOK                  bool
	knowledgeDocs             int
	glpiKBOK                  bool
	glpiKBDocs                int
	glpiKBLastSync            time.Time
	glpiKBLastError           string
}

type PollStatus struct {
	At       time.Time `json:"at"`
	Fetched  int       `json:"fetched"`
	Seen     int       `json:"seen"`
	Unseen   int       `json:"unseen"`
	Enqueued int       `json:"enqueued"`
	Rejected int       `json:"rejected"`
	Error    string    `json:"error,omitempty"`
}

func New() *Metrics                        { return &Metrics{Started: time.Now()} }
func (m *Metrics) SetLastPoll(t time.Time) { m.mu.Lock(); m.lastPoll = t; m.mu.Unlock() }
func (m *Metrics) LastPoll() time.Time     { m.mu.RLock(); defer m.mu.RUnlock(); return m.lastPoll }
func (m *Metrics) SetPollStatus(v PollStatus) {
	m.mu.Lock()
	m.lastPoll = v.At
	m.lastPollFetched = v.Fetched
	m.lastPollSeen = v.Seen
	m.lastPollUnseen = v.Unseen
	m.lastPollEnqueued = v.Enqueued
	m.lastPollRejected = v.Rejected
	m.lastPollError = v.Error
	m.mu.Unlock()
}
func (m *Metrics) PollStatus() PollStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return PollStatus{At: m.lastPoll, Fetched: m.lastPollFetched, Seen: m.lastPollSeen, Unseen: m.lastPollUnseen, Enqueued: m.lastPollEnqueued, Rejected: m.lastPollRejected, Error: m.lastPollError}
}
func (m *Metrics) SetHealth(glpi, ollama bool) {
	m.mu.Lock()
	m.glpiOK = glpi
	m.ollamaOK = ollama
	m.mu.Unlock()
}
func (m *Metrics) Health() (bool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.glpiOK, m.ollamaOK
}
func (m *Metrics) SetKnowledgeDocs(n int) { m.mu.Lock(); m.knowledgeDocs = n; m.mu.Unlock() }
func (m *Metrics) KnowledgeDocs() int     { m.mu.RLock(); defer m.mu.RUnlock(); return m.knowledgeDocs }
func (m *Metrics) SetGLPIKBStatus(ok bool, docs int, lastSync time.Time, lastErr string) {
	m.mu.Lock()
	m.glpiKBOK = ok
	m.glpiKBDocs = docs
	m.glpiKBLastSync = lastSync
	m.glpiKBLastError = lastErr
	m.mu.Unlock()
}
func (m *Metrics) GLPIKBStatus() (bool, int, time.Time, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.glpiKBOK, m.glpiKBDocs, m.glpiKBLastSync, m.glpiKBLastError
}

func (m *Metrics) WritePrometheus(w io.Writer) {
	g, o := m.Health()
	boolf := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	fmt.Fprintf(w, "# TYPE glpi_agent_processed_total counter\nglpi_agent_processed_total %d\n", m.Processed.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_skipped_total counter\nglpi_agent_skipped_total %d\n", m.Skipped.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_errors_total counter\nglpi_agent_errors_total %d\n", m.Errors.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_category_changes_total counter\nglpi_agent_category_changes_total %d\n", m.CategoryChanged.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_replies_total counter\nglpi_agent_replies_total %d\n", m.Replies.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_priority_recommendations_total counter\nglpi_agent_priority_recommendations_total %d\n", m.PriorityRecommendations.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_priority_changes_total counter\nglpi_agent_priority_changes_total %d\n", m.PriorityChanges.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_escalation_runs_total counter\nglpi_agent_escalation_runs_total %d\n", m.EscalationRuns.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_escalations_total counter\nglpi_agent_escalations_total %d\n", m.Escalations.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_context_fetches_total counter\nglpi_agent_context_fetches_total %d\n", m.ContextFetches.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_context_errors_total counter\nglpi_agent_context_errors_total %d\n", m.ContextErrors.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_queue_depth gauge\nglpi_agent_queue_depth %d\n", m.QueueDepth.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_searches_total counter\nglpi_agent_outcome_searches_total %d\n", m.OutcomeSearches.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_search_hits_total counter\nglpi_agent_outcome_search_hits_total %d\n", m.OutcomeSearchHits.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_search_errors_total counter\nglpi_agent_outcome_search_errors_total %d\n", m.OutcomeSearchErrors.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_learning_learned_total counter\nglpi_agent_outcome_learning_learned_total %d\n", m.OutcomeLearningLearned.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_learning_accepted_total counter\nglpi_agent_outcome_learning_accepted_total %d\n", m.OutcomeLearningAccepted.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_learning_corrected_total counter\nglpi_agent_outcome_learning_corrected_total %d\n", m.OutcomeLearningCorrected.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_learning_failed_total counter\nglpi_agent_outcome_learning_failed_total %d\n", m.OutcomeLearningFailed.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_outcome_learning_idempotent_total counter\nglpi_agent_outcome_learning_idempotent_total %d\n", m.OutcomeLearningIdempotent.Load())
	fmt.Fprintf(w, "# TYPE glpi_agent_glpi_up gauge\nglpi_agent_glpi_up %d\n", boolf(g))
	fmt.Fprintf(w, "# TYPE glpi_agent_ollama_up gauge\nglpi_agent_ollama_up %d\n", boolf(o))
	fmt.Fprintf(w, "# TYPE glpi_agent_knowledge_documents gauge\nglpi_agent_knowledge_documents %d\n", m.KnowledgeDocs())
	kbOK, kbDocs, _, _ := m.GLPIKBStatus()
	fmt.Fprintf(w, "# TYPE glpi_agent_glpi_kb_up gauge\nglpi_agent_glpi_kb_up %d\n", boolf(kbOK))
	fmt.Fprintf(w, "# TYPE glpi_agent_glpi_kb_documents gauge\nglpi_agent_glpi_kb_documents %d\n", kbDocs)
}

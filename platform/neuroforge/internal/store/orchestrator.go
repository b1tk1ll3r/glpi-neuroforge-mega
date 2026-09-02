package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
)

// JobSpec describes a durable unit of work. Jobs are persisted through the
// normal NeuroForge WAL/checkpoint path so a master restart does not lose
// queued, leased, retrying, or dependency-blocked work.
type JobSpec struct {
	Type                 string
	Payload              any
	Priority             int
	ResourceClass        string
	RequiredCapabilities []string
	IdempotencyKey       string
	ParentJobID          string
	DependsOn            []string
	MaxAttempts          int
	BackoffSeconds       int
	TimeoutSeconds       int
	RequiresMasterApply  bool
	MaxApplyAttempts     int
	ApplyBackoffSeconds  int
}

type WorkerHeartbeat struct {
	ID             string
	ResourceClass  string
	Capabilities   []string
	Labels         map[string]string
	MaxConcurrency int
	Version        string
	Hostname       string
	ActiveLeases   map[string]string // job_id -> lease_token
}

type OrchestratorStatus struct {
	Workers        []core.WorkerState `json:"workers"`
	JobsByStatus   map[string]int     `json:"jobs_by_status"`
	JobsByType     map[string]int     `json:"jobs_by_type"`
	JobsByResource map[string]int     `json:"jobs_by_resource"`
	OldestQueued   time.Time          `json:"oldest_queued,omitempty"`
	Queued         int                `json:"queued"`
	Claimed        int                `json:"claimed"`
	Retrying       int                `json:"retrying"`
	Applying       int                `json:"applying"`
	Blocked        int                `json:"blocked"`
	Failed         int                `json:"failed"`
	Done           int                `json:"done"`
}

func normalizeCapabilities(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, x := range in {
		x = strings.ToLower(strings.TrimSpace(x))
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func capabilitiesContain(have []string, need []string) bool {
	if len(need) == 0 {
		return true
	}
	m := make(map[string]bool, len(have))
	for _, x := range have {
		m[strings.ToLower(strings.TrimSpace(x))] = true
	}
	for _, x := range need {
		if !m[strings.ToLower(strings.TrimSpace(x))] {
			return false
		}
	}
	return true
}

func (s *Store) EnqueueJob(kind string, payload any) (*core.Job, error) {
	return s.EnqueueJobSpec(JobSpec{Type: kind, Payload: payload})
}

func (s *Store) EnqueueJobSpec(spec JobSpec) (*core.Job, error) {
	spec.Type = strings.TrimSpace(spec.Type)
	if spec.Type == "" {
		return nil, errors.New("job type is required")
	}
	b, err := json.Marshal(spec.Payload)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.state.Config.Worker
	if cfg.MaxQueuedJobs > 0 {
		pending := 0
		for _, j := range s.state.Jobs {
			if j != nil && (j.Status == "queued" || j.Status == "claimed" || j.Status == "retry_wait" || j.Status == "blocked" || j.Status == "apply_wait") {
				pending++
			}
		}
		if pending >= cfg.MaxQueuedJobs {
			return nil, fmt.Errorf("orchestrator queue full: %d >= %d", pending, cfg.MaxQueuedJobs)
		}
	}
	if key := strings.TrimSpace(spec.IdempotencyKey); key != "" {
		for _, j := range s.state.Jobs {
			if j != nil && j.IdempotencyKey == key && (j.Status == "queued" || j.Status == "claimed" || j.Status == "retry_wait" || j.Status == "blocked" || j.Status == "apply_wait") {
				cp := *j
				return &cp, nil
			}
		}
	}
	// Dependencies must already exist. Accepting dangling IDs would create jobs
	// that can remain blocked forever and makes convergence impossible to reason
	// about after operator typos or partial planning failures.
	seenDeps := make(map[string]struct{}, len(spec.DependsOn))
	cleanDeps := make([]string, 0, len(spec.DependsOn))
	for _, depID := range spec.DependsOn {
		depID = strings.TrimSpace(depID)
		if depID == "" {
			return nil, errors.New("dependency job id must not be empty")
		}
		if _, duplicate := seenDeps[depID]; duplicate {
			continue
		}
		if s.state.Jobs[depID] == nil {
			return nil, fmt.Errorf("dependency job %s not found", depID)
		}
		seenDeps[depID] = struct{}{}
		cleanDeps = append(cleanDeps, depID)
	}
	spec.DependsOn = cleanDeps
	if parentID := strings.TrimSpace(spec.ParentJobID); parentID != "" && s.state.Jobs[parentID] == nil {
		return nil, fmt.Errorf("parent job %s not found", parentID)
	}
	now := time.Now().UTC()
	maxAttempts := spec.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = cfg.DefaultMaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	backoff := spec.BackoffSeconds
	if backoff <= 0 {
		backoff = cfg.RetryBackoffSeconds
	}
	if backoff <= 0 {
		backoff = 15
	}
	maxApplyAttempts := spec.MaxApplyAttempts
	if maxApplyAttempts <= 0 {
		maxApplyAttempts = cfg.MasterApplyMaxAttempts
	}
	if maxApplyAttempts <= 0 {
		maxApplyAttempts = 5
	}
	applyBackoff := spec.ApplyBackoffSeconds
	if applyBackoff <= 0 {
		applyBackoff = cfg.MasterApplyBackoffSeconds
	}
	if applyBackoff <= 0 {
		applyBackoff = 5
	}
	priority := spec.Priority
	if priority < -1000 {
		priority = -1000
	}
	if priority > 1000 {
		priority = 1000
	}
	status := "queued"
	if len(spec.DependsOn) > 0 {
		status = "blocked"
	}
	j := &core.Job{
		ID:                   NewID("job"),
		Type:                 spec.Type,
		Payload:              b,
		Status:               status,
		Priority:             priority,
		ResourceClass:        strings.ToLower(strings.TrimSpace(spec.ResourceClass)),
		RequiredCapabilities: normalizeCapabilities(spec.RequiredCapabilities),
		IdempotencyKey:       strings.TrimSpace(spec.IdempotencyKey),
		ParentJobID:          strings.TrimSpace(spec.ParentJobID),
		DependsOn:            append([]string(nil), spec.DependsOn...),
		MaxAttempts:          maxAttempts,
		BackoffSeconds:       backoff,
		TimeoutSeconds:       spec.TimeoutSeconds,
		RequiresMasterApply:  spec.RequiresMasterApply,
		MaxApplyAttempts:     maxApplyAttempts,
		ApplyBackoffSeconds:  applyBackoff,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.state.Jobs[j.ID] = j
	cp := *j
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) dependencyStateLocked(j *core.Job) (ready bool, terminalFailure string) {
	if len(j.DependsOn) == 0 {
		return true, ""
	}
	for _, id := range j.DependsOn {
		dep := s.state.Jobs[id]
		if dep == nil {
			return false, ""
		}
		switch dep.Status {
		case "done":
			continue
		case "failed", "canceled":
			return false, "dependency " + id + " ended as " + dep.Status
		default:
			return false, ""
		}
	}
	return true, ""
}

func workerMatchesJob(w WorkerHeartbeat, j *core.Job) bool {
	resource := strings.ToLower(strings.TrimSpace(j.ResourceClass))
	workerResource := strings.ToLower(strings.TrimSpace(w.ResourceClass))
	if resource != "" && resource != "any" && resource != workerResource {
		return false
	}
	return capabilitiesContain(w.Capabilities, j.RequiredCapabilities)
}

func (s *Store) registerWorkerLocked(h WorkerHeartbeat, now time.Time) core.WorkerState {
	if s.workers == nil {
		s.workers = map[string]core.WorkerState{}
	}
	old := s.workers[h.ID]
	registered := old.RegisteredAt
	if registered.IsZero() {
		registered = now
	}
	maxc := h.MaxConcurrency
	if maxc <= 0 {
		maxc = 1
	}
	state := core.WorkerState{
		ID:             h.ID,
		ResourceClass:  strings.ToLower(strings.TrimSpace(h.ResourceClass)),
		Capabilities:   normalizeCapabilities(h.Capabilities),
		Labels:         h.Labels,
		MaxConcurrency: maxc,
		Version:        strings.TrimSpace(h.Version),
		Hostname:       strings.TrimSpace(h.Hostname),
		LastHeartbeat:  now,
		RegisteredAt:   registered,
		Status:         "online",
	}
	if state.ResourceClass == "" {
		state.ResourceClass = "cpu"
	}
	if len(state.Capabilities) == 0 {
		state.Capabilities = []string{"cpu", "vector.relink"}
	}
	for _, j := range s.state.Jobs {
		if j != nil && j.Status == "claimed" && j.ClaimedBy == h.ID && now.Before(j.LeaseUntil) {
			state.Inflight++
		}
	}
	s.workers[h.ID] = state
	return state
}

func (s *Store) RegisterWorker(h WorkerHeartbeat) (core.WorkerState, error) {
	h.ID = strings.TrimSpace(h.ID)
	if h.ID == "" {
		return core.WorkerState{}, errors.New("worker id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerWorkerLocked(h, time.Now().UTC()), nil
}

func (s *Store) HeartbeatWorker(h WorkerHeartbeat, lease time.Duration) (core.WorkerState, error) {
	h.ID = strings.TrimSpace(h.ID)
	if h.ID == "" {
		return core.WorkerState{}, errors.New("worker id is required")
	}
	if lease <= 0 {
		lease = 120 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	state := s.registerWorkerLocked(h, now)
	for jobID, token := range h.ActiveLeases {
		j := s.state.Jobs[jobID]
		if j == nil || j.Status != "claimed" || j.ClaimedBy != h.ID || j.LeaseToken == "" || j.LeaseToken != token {
			continue
		}
		// Persist a renewal only when the remaining lease is below half the
		// configured lease. This gives restart-safe leases without heartbeat WAL spam.
		if time.Until(j.LeaseUntil) <= lease/2 {
			j.LeaseUntil = now.Add(lease)
			j.UpdatedAt = now
			cp := *j
			if err := s.commitLocked("job.upsert", cp); err != nil {
				return core.WorkerState{}, err
			}
		}
	}
	return state, nil
}

func (s *Store) normalizeJobForClaimLocked(j *core.Job, now time.Time) error {
	if j == nil {
		return nil
	}
	changed := false
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = s.state.Config.Worker.DefaultMaxAttempts
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = 3
		}
		changed = true
	}
	if j.Status == "claimed" && !j.LeaseUntil.IsZero() && now.After(j.LeaseUntil) {
		j.ClaimedBy = ""
		j.LeaseToken = ""
		j.LeaseUntil = time.Time{}
		if j.Attempts >= j.MaxAttempts {
			j.Status = "failed"
			j.Error = "lease expired after maximum attempts"
			j.FinishedAt = now
		} else {
			j.Status = "retry_wait"
			j.Error = "worker lease expired"
			j.NextAttemptAt = now.Add(time.Duration(maxInt(1, j.BackoffSeconds)) * time.Second)
		}
		j.UpdatedAt = now
		changed = true
	}
	if j.Status == "retry_wait" && (j.NextAttemptAt.IsZero() || !now.Before(j.NextAttemptAt)) {
		j.Status = "queued"
		j.NextAttemptAt = time.Time{}
		j.UpdatedAt = now
		changed = true
	}
	if j.Status == "blocked" {
		ready, depFailure := s.dependencyStateLocked(j)
		if depFailure != "" {
			j.Status = "failed"
			j.Error = depFailure
			j.FinishedAt = now
			j.UpdatedAt = now
			changed = true
		} else if ready {
			j.Status = "queued"
			j.UpdatedAt = now
			changed = true
		}
	}
	if changed {
		cp := *j
		return s.commitLocked("job.upsert", cp)
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ClaimJob preserves the v0.8 worker contract for compatibility. New workers
// should use ClaimJobForWorker so resource/capability routing is enforced.
func (s *Store) ClaimJob(worker string, lease time.Duration) (*core.Job, error) {
	return s.ClaimJobForWorker(WorkerHeartbeat{ID: worker, ResourceClass: "cpu", Capabilities: []string{"cpu", "vector.relink"}, MaxConcurrency: 1}, lease)
}

func (s *Store) ClaimJobForWorker(h WorkerHeartbeat, lease time.Duration) (*core.Job, error) {
	h.ID = strings.TrimSpace(h.ID)
	if h.ID == "" {
		return nil, errors.New("worker id is required")
	}
	if lease <= 0 {
		lease = 120 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	state := s.registerWorkerLocked(h, now)
	if state.Inflight >= state.MaxConcurrency {
		return nil, nil
	}
	var chosen *core.Job
	for _, j := range s.state.Jobs {
		if err := s.normalizeJobForClaimLocked(j, now); err != nil {
			return nil, err
		}
		if j == nil || j.Status != "queued" || !workerMatchesJob(h, j) {
			continue
		}
		if chosen == nil || j.Priority > chosen.Priority || (j.Priority == chosen.Priority && j.CreatedAt.Before(chosen.CreatedAt)) {
			chosen = j
		}
	}
	if chosen == nil {
		return nil, nil
	}
	chosen.Status = "claimed"
	chosen.ClaimedBy = h.ID
	chosen.LeaseToken = NewID("lease")
	chosen.LeaseUntil = now.Add(lease)
	chosen.Attempts++
	chosen.UpdatedAt = now
	if chosen.StartedAt.IsZero() {
		chosen.StartedAt = now
	}
	cp := *chosen
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) CompleteJob(id, worker string, result json.RawMessage, jobErr string) (*core.Job, error) {
	return s.CompleteJobLease(id, worker, "", result, jobErr)
}

func (s *Store) CompleteJobLease(id, worker, leaseToken string, result json.RawMessage, jobErr string) (*core.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.state.Jobs[id]
	if !ok || j == nil {
		return nil, errors.New("job not found")
	}
	if j.Status != "claimed" {
		return nil, fmt.Errorf("job is not claimed: %s", j.Status)
	}
	if j.ClaimedBy != worker {
		return nil, errors.New("job claimed by another worker")
	}
	if j.LeaseToken != "" && leaseToken != j.LeaseToken {
		return nil, errors.New("stale or invalid job lease token")
	}
	now := time.Now().UTC()
	if !j.LeaseUntil.IsZero() && !now.Before(j.LeaseUntil) {
		// A lease token fences ownership, but its validity also has a deadline.
		// Reject late results even if no other worker has claimed the job yet;
		// otherwise a paused partitioned worker could mutate state after expiry.
		return nil, errors.New("stale or expired job lease")
	}
	j.Result = result
	j.Error = strings.TrimSpace(jobErr)
	j.UpdatedAt = now
	j.ClaimedBy = ""
	j.LeaseToken = ""
	j.LeaseUntil = time.Time{}
	if j.Error == "" {
		j.NextAttemptAt = time.Time{}
		if j.RequiresMasterApply {
			j.Status = "apply_wait"
			j.ApplyError = ""
			j.ApplyNextAttemptAt = now
		} else {
			j.Status = "done"
			j.FinishedAt = now
		}
	} else if j.Attempts < maxInt(1, j.MaxAttempts) {
		j.Status = "retry_wait"
		backoff := maxInt(1, j.BackoffSeconds)
		shift := j.Attempts - 1
		if shift > 6 {
			shift = 6
		}
		delay := time.Duration(backoff*(1<<shift)) * time.Second
		if delay > time.Hour {
			delay = time.Hour
		}
		j.NextAttemptAt = now.Add(delay)
	} else {
		j.Status = "failed"
		j.FinishedAt = now
	}
	cp := *j
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) Job(id string) (*core.Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j := s.state.Jobs[id]
	if j == nil {
		return nil, false
	}
	cp := *j
	cp.Payload = append(json.RawMessage(nil), j.Payload...)
	cp.Result = append(json.RawMessage(nil), j.Result...)
	return &cp, true
}

func (s *Store) JobsSnapshot(limit int, status, kind string) []core.Job {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	status = strings.TrimSpace(status)
	kind = strings.TrimSpace(kind)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Job, 0)
	for _, j := range s.state.Jobs {
		if j == nil || (status != "" && j.Status != status) || (kind != "" && j.Type != kind) {
			continue
		}
		cp := *j
		// Admin status does not need potentially large/sensitive payloads.
		cp.Payload = nil
		cp.Result = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) RetryJob(id string) (*core.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.state.Jobs[id]
	if j == nil {
		return nil, errors.New("job not found")
	}
	if j.Status != "failed" && j.Status != "canceled" {
		return nil, fmt.Errorf("job %s cannot be retried from status %s", id, j.Status)
	}
	j.Status = "queued"
	j.Error = ""
	j.Result = nil
	j.Attempts = 0
	j.NextAttemptAt = time.Time{}
	j.FinishedAt = time.Time{}
	j.UpdatedAt = time.Now().UTC()
	cp := *j
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) WorkersSnapshot() []core.WorkerState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	staleAfter := time.Duration(s.state.Config.Worker.StaleAfterSeconds) * time.Second
	if staleAfter <= 0 {
		staleAfter = 60 * time.Second
	}
	out := make([]core.WorkerState, 0, len(s.workers))
	for _, w := range s.workers {
		cp := w
		cp.Inflight = 0
		for _, j := range s.state.Jobs {
			if j != nil && j.Status == "claimed" && j.ClaimedBy == w.ID && now.Before(j.LeaseUntil) {
				cp.Inflight++
			}
		}
		if now.Sub(cp.LastHeartbeat) > staleAfter {
			cp.Status = "stale"
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) HasLiveWorker(resource string, capabilities ...string) bool {
	resource = strings.ToLower(strings.TrimSpace(resource))
	for _, w := range s.WorkersSnapshot() {
		if w.Status != "online" || (resource != "" && resource != "any" && w.ResourceClass != resource) {
			continue
		}
		if capabilitiesContain(w.Capabilities, capabilities) && w.Inflight < w.MaxConcurrency {
			return true
		}
	}
	return false
}

func (s *Store) OrchestratorStatus() OrchestratorStatus {
	out := OrchestratorStatus{Workers: s.WorkersSnapshot(), JobsByStatus: map[string]int{}, JobsByType: map[string]int{}, JobsByResource: map[string]int{}}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.state.Jobs {
		if j == nil {
			continue
		}
		out.JobsByStatus[j.Status]++
		out.JobsByType[j.Type]++
		resource := j.ResourceClass
		if resource == "" {
			resource = "any"
		}
		out.JobsByResource[resource]++
		switch j.Status {
		case "queued":
			out.Queued++
			if out.OldestQueued.IsZero() || j.CreatedAt.Before(out.OldestQueued) {
				out.OldestQueued = j.CreatedAt
			}
		case "claimed":
			out.Claimed++
		case "retry_wait":
			out.Retrying++
		case "apply_wait":
			out.Applying++
		case "blocked":
			out.Blocked++
		case "failed":
			out.Failed++
		case "done":
			out.Done++
		}
	}
	return out
}

func (s *Store) PendingJobCount(kind string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, j := range s.state.Jobs {
		if j == nil || (kind != "" && j.Type != kind) {
			continue
		}
		if j.Status == "queued" || j.Status == "claimed" || j.Status == "retry_wait" || j.Status == "blocked" || j.Status == "apply_wait" {
			n++
		}
	}
	return n
}

// PendingMasterApplyJobs returns durable worker results whose mutation still
// needs to be committed by the master. The result payload remains persisted,
// so a master restart can resume without recomputing the worker job.
func (s *Store) PendingMasterApplyJobs(limit int) []core.Job {
	if limit <= 0 || limit > 1024 {
		limit = 64
	}
	now := time.Now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Job, 0, limit)
	for _, j := range s.state.Jobs {
		if j == nil || j.Status != "apply_wait" || (!j.ApplyNextAttemptAt.IsZero() && now.Before(j.ApplyNextAttemptAt)) {
			continue
		}
		cp := *j
		cp.Payload = append(json.RawMessage(nil), j.Payload...)
		cp.Result = append(json.RawMessage(nil), j.Result...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// FinishMasterApply closes the second phase of a worker job. Failed apply
// attempts are retried from the already persisted Result; successful worker
// computation is never lost merely because the master restarted mid-commit.
func (s *Store) FinishMasterApply(id string, applyErr error) (*core.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.state.Jobs[id]
	if j == nil {
		return nil, errors.New("job not found")
	}
	if j.Status != "apply_wait" {
		return nil, fmt.Errorf("job is not awaiting master apply: %s", j.Status)
	}
	now := time.Now().UTC()
	j.ApplyAttempts++
	j.UpdatedAt = now
	if applyErr == nil {
		j.Status = "done"
		j.ApplyError = ""
		j.ApplyNextAttemptAt = time.Time{}
		j.FinishedAt = now
	} else {
		j.ApplyError = strings.TrimSpace(applyErr.Error())
		maxAttempts := j.MaxApplyAttempts
		if maxAttempts <= 0 {
			maxAttempts = maxInt(1, s.state.Config.Worker.MasterApplyMaxAttempts)
		}
		if j.ApplyAttempts >= maxAttempts {
			j.Status = "failed"
			j.Error = "master apply failed: " + j.ApplyError
			j.FinishedAt = now
			j.ApplyNextAttemptAt = time.Time{}
		} else {
			backoff := j.ApplyBackoffSeconds
			if backoff <= 0 {
				backoff = maxInt(1, s.state.Config.Worker.MasterApplyBackoffSeconds)
			}
			shift := j.ApplyAttempts - 1
			if shift > 6 {
				shift = 6
			}
			delay := time.Duration(backoff*(1<<shift)) * time.Second
			if delay > time.Hour {
				delay = time.Hour
			}
			j.ApplyNextAttemptAt = now.Add(delay)
		}
	}
	cp := *j
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) CancelJob(id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.state.Jobs[id]
	if j == nil {
		return errors.New("job not found")
	}
	if j.Status == "done" || j.Status == "failed" || j.Status == "canceled" {
		return nil
	}
	now := time.Now().UTC()
	j.Status = "canceled"
	j.Error = strings.TrimSpace(reason)
	j.ClaimedBy = ""
	j.LeaseToken = ""
	j.LeaseUntil = time.Time{}
	j.FinishedAt = now
	j.UpdatedAt = now
	cp := *j
	return s.commitLocked("job.upsert", cp)
}

// PruneTerminalJobs bounds durable scheduler history while preserving jobs that
// are still referenced as dependencies by non-terminal work.
func (s *Store) PruneTerminalJobs(retention time.Duration, maxTerminal int) (int, error) {
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	if maxTerminal < 100 {
		maxTerminal = 20000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	cutoff := now.Add(-retention)
	referenced := map[string]bool{}
	for _, j := range s.state.Jobs {
		if j == nil || j.Status == "done" || j.Status == "failed" || j.Status == "canceled" {
			continue
		}
		for _, dep := range j.DependsOn {
			referenced[dep] = true
		}
	}
	type terminal struct {
		id string
		t  time.Time
	}
	items := make([]terminal, 0)
	for id, j := range s.state.Jobs {
		if j == nil || referenced[id] || (j.Status != "done" && j.Status != "failed" && j.Status != "canceled") {
			continue
		}
		t := j.FinishedAt
		if t.IsZero() {
			t = j.UpdatedAt
		}
		items = append(items, terminal{id: id, t: t})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].t.Before(items[j].t) })
	remove := map[string]bool{}
	for _, x := range items {
		if !x.t.IsZero() && x.t.Before(cutoff) {
			remove[x.id] = true
		}
	}
	remaining := len(items) - len(remove)
	if remaining > maxTerminal {
		need := remaining - maxTerminal
		for _, x := range items {
			if need == 0 {
				break
			}
			if remove[x.id] {
				continue
			}
			remove[x.id] = true
			need--
		}
	}
	if len(remove) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(remove))
	for id := range remove {
		delete(s.state.Jobs, id)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return len(ids), s.commitLocked("job.delete", ids)
}

// IsOrchestratorLeader prevents two clustered masters from planning/applying
// the same durable work. Standalone installations are always authoritative.
func (s *Store) IsOrchestratorLeader() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.state.Config.Cluster
	if !cfg.Enabled {
		return true
	}
	leader := s.state.Cluster.LeaderID
	if !cfg.AutoElection && leader == "" {
		leader = cfg.LeaderID
	}
	return leader != "" && leader == cfg.NodeID && s.state.Cluster.Role == ClusterLeader
}

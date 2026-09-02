package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOrchestratorCapabilityRoutingAndLeaseFencing(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	j, err := s.EnqueueJobSpec(JobSpec{
		Type: "model.chat", Payload: map[string]any{"input": "x"}, Priority: 100,
		ResourceClass: "gpu", RequiredCapabilities: []string{"gpu", "model.chat"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cpu := WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu", "vector.relink"}, MaxConcurrency: 1}
	got, err := s.ClaimJobForWorker(cpu, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("CPU worker must not claim GPU job: %+v", got)
	}

	gpu := WorkerHeartbeat{ID: "gpu-1", ResourceClass: "gpu", Capabilities: []string{"gpu", "model.chat"}, MaxConcurrency: 1}
	got, err = s.ClaimJobForWorker(gpu, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != j.ID || got.LeaseToken == "" {
		t.Fatalf("GPU claim=%+v", got)
	}

	if _, err := s.CompleteJobLease(got.ID, gpu.ID, "stale-token", json.RawMessage(`{"ok":true}`), ""); err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("stale lease must be fenced, err=%v", err)
	}
	done, err := s.CompleteJobLease(got.ID, gpu.ID, got.LeaseToken, json.RawMessage(`{"ok":true}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" {
		t.Fatalf("status=%s", done.Status)
	}
}

func TestOrchestratorRetryDependencyAndWorkerStaleness(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Worker.RetryBackoffSeconds = 1
	cfg.Worker.HeartbeatSeconds = 2
	cfg.Worker.StaleAfterSeconds = 2
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	parent, err := s.EnqueueJobSpec(JobSpec{Type: "parent", Payload: map[string]any{}, ResourceClass: "cpu", MaxAttempts: 2, BackoffSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.EnqueueJobSpec(JobSpec{Type: "child", Payload: map[string]any{}, ResourceClass: "cpu", DependsOn: []string{parent.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != "blocked" {
		t.Fatalf("child status=%s", child.Status)
	}

	w := WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	p1, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == nil || p1.ID != parent.ID {
		t.Fatalf("claim=%+v", p1)
	}
	retry, err := s.CompleteJobLease(p1.ID, w.ID, p1.LeaseToken, nil, "temporary")
	if err != nil {
		t.Fatal(err)
	}
	if retry.Status != "retry_wait" || retry.NextAttemptAt.IsZero() {
		t.Fatalf("retry=%+v", retry)
	}

	// Avoid sleeping: advance this job's retry clock under the package lock.
	s.mu.Lock()
	s.state.Jobs[parent.ID].NextAttemptAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	p2, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if p2 == nil || p2.ID != parent.ID || p2.Attempts != 2 {
		t.Fatalf("second claim=%+v", p2)
	}
	if _, err := s.CompleteJobLease(p2.ID, w.ID, p2.LeaseToken, json.RawMessage(`{"ok":true}`), ""); err != nil {
		t.Fatal(err)
	}

	c, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || c.ID != child.ID {
		t.Fatalf("child claim after dependency=%+v", c)
	}

	s.mu.Lock()
	ws := s.workers[w.ID]
	ws.LastHeartbeat = time.Now().Add(-2 * time.Second)
	s.workers[w.ID] = ws
	s.mu.Unlock()
	workers := s.WorkersSnapshot()
	if len(workers) != 1 || workers[0].Status != "stale" {
		t.Fatalf("workers=%+v", workers)
	}
}

func TestDependencyFailureFailsBlockedChild(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnqueueJobSpec(JobSpec{Type: "parent", Payload: map[string]any{}, ResourceClass: "cpu", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.EnqueueJobSpec(JobSpec{Type: "child", Payload: map[string]any{}, ResourceClass: "cpu", DependsOn: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	w := WorkerHeartbeat{ID: "cpu", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	pj, _ := s.ClaimJobForWorker(w, time.Minute)
	if _, err := s.CompleteJobLease(pj.ID, w.ID, pj.LeaseToken, nil, "permanent"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("failed dependency child must not be claimable: %+v", got)
	}
	cj, ok := s.Job(c.ID)
	if !ok || cj.Status != "failed" || !strings.Contains(cj.Error, "dependency") {
		t.Fatalf("child=%+v", cj)
	}
}

func TestPruneTerminalJobsPreservesReferencedDependencies(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnqueueJobSpec(JobSpec{Type: "parent", Payload: map[string]any{}, ResourceClass: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	w := WorkerHeartbeat{ID: "cpu", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	pj, _ := s.ClaimJobForWorker(w, time.Minute)
	if _, err := s.CompleteJobLease(pj.ID, w.ID, pj.LeaseToken, nil, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.EnqueueJobSpec(JobSpec{Type: "child", Payload: map[string]any{}, ResourceClass: "gpu", RequiredCapabilities: []string{"gpu"}, DependsOn: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.state.Jobs[p.ID].FinishedAt = time.Now().Add(-48 * time.Hour)
	s.state.Jobs[p.ID].UpdatedAt = s.state.Jobs[p.ID].FinishedAt
	s.mu.Unlock()
	n, err := s.PruneTerminalJobs(time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("pruned referenced dependency: %d", n)
	}
	if _, ok := s.Job(p.ID); !ok {
		t.Fatal("referenced parent was removed")
	}
}

func TestExpiredLeaseCannotCompleteBeforeReclaim(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.EnqueueJobSpec(JobSpec{Type: "cpu.task", Payload: map[string]any{"x": 1}, ResourceClass: "cpu", RequiredCapabilities: []string{"cpu"}})
	if err != nil {
		t.Fatal(err)
	}
	w := WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	j, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil || j == nil {
		t.Fatalf("claim=%+v err=%v", j, err)
	}
	s.mu.Lock()
	s.state.Jobs[j.ID].LeaseUntil = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if _, err := s.CompleteJobLease(j.ID, w.ID, j.LeaseToken, json.RawMessage(`{"ok":true}`), ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired lease completion must be fenced, err=%v", err)
	}
	cur, ok := s.Job(j.ID)
	if !ok || cur.Status != "claimed" {
		t.Fatalf("late completion mutated job before scheduler recovery: %+v", cur)
	}
	// Any subsequent scheduler/claim pass normalizes the expired lease and may
	// make the job retryable according to its durable attempt policy.
	other := WorkerHeartbeat{ID: "cpu-2", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	next, err := s.ClaimJobForWorker(other, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("retry backoff should prevent immediate reclaim: %+v", next)
	}
	cur, _ = s.Job(j.ID)
	if cur.Status != "retry_wait" || cur.Error != "worker lease expired" {
		t.Fatalf("expired lease not normalized to retry_wait: %+v", cur)
	}
}

func TestOrchestratorRejectsDanglingDependencies(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EnqueueJobSpec(JobSpec{Type: "child", Payload: map[string]any{}, DependsOn: []string{"job_missing"}}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("dangling dependency must fail closed, err=%v", err)
	}
	if s.PendingJobCount("") != 0 {
		t.Fatalf("failed planning left durable work behind: %+v", s.OrchestratorStatus())
	}
}

func TestApplyWaitJobSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.EnqueueJobSpec(JobSpec{Type: "master.apply", Payload: map[string]any{"x": 1}, ResourceClass: "cpu", RequiredCapabilities: []string{"cpu"}, RequiresMasterApply: true})
	if err != nil {
		t.Fatal(err)
	}
	w := WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu"}, MaxConcurrency: 1}
	claimed, err := s.ClaimJobForWorker(w, time.Minute)
	if err != nil || claimed == nil || claimed.ID != j.ID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := json.RawMessage(`{"ok":true}`)
	completed, err := s.CompleteJobLease(claimed.ID, w.ID, claimed.LeaseToken, result, "")
	if err != nil || completed.Status != "apply_wait" {
		t.Fatalf("complete=%+v err=%v", completed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	recovered, ok := s2.Job(j.ID)
	if !ok || recovered.Status != "apply_wait" {
		t.Fatalf("durable apply_wait job not recovered: %+v", recovered)
	}
	if len(recovered.Payload) == 0 || string(recovered.Result) != string(result) {
		t.Fatalf("recovered payload/result incomplete: %+v", recovered)
	}
	pending := s2.PendingMasterApplyJobs(10)
	if len(pending) != 1 || pending[0].ID != j.ID {
		t.Fatalf("recovered master apply queue=%+v", pending)
	}
}

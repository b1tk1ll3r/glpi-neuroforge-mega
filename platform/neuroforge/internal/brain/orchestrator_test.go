package brain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

func TestGraphBackfillPlansBoundedJobsAndDurablyAppliesResult(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Worker.GraphBackfillEnabled = true
	cfg.Worker.RequireWorkerForGraph = true
	cfg.Worker.GraphBackfillBatchSize = 4
	cfg.Worker.GraphBackfillMaxQueued = 8
	cfg.Worker.GraphBackfillMinDegree = 2
	cfg.Worker.GraphCandidateMultiplier = 4
	cfg.Brain.RecallK = 3
	cfg.Brain.MinSimilarity = .1
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		m := core.Memory{ID: store.NewID("kb"), Kind: "knowledge.chunk", MemoryType: core.MemorySemantic, Text: "knowledge", Vector: []float32{1, float32(i+1) / 100}}
		if err := s.AddMemory(&m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RegisterWorker(store.WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu", "vector.relink"}, MaxConcurrency: 2}); err != nil {
		t.Fatal(err)
	}
	e := New(s, provider.NewRouter(s), cost.New(s))
	planned, err := e.PlanGraphBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if planned != 4 || s.PendingJobCount("vector.relink") != 4 {
		t.Fatalf("planned=%d pending=%d", planned, s.PendingJobCount("vector.relink"))
	}
	jobs := s.JobsSnapshot(10, "", "vector.relink")
	if len(jobs) != 4 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	full, ok := s.Job(jobs[0].ID)
	if !ok || !full.RequiresMasterApply {
		t.Fatalf("job=%+v", full)
	}
	var payload relinkPayload
	if err := json.Unmarshal(full.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Candidates) == 0 || len(payload.Candidates) > 256 {
		t.Fatalf("candidate payload is not bounded: %d", len(payload.Candidates))
	}

	worker := store.WorkerHeartbeat{ID: "cpu-1", ResourceClass: "cpu", Capabilities: []string{"cpu", "vector.relink"}, MaxConcurrency: 2}
	claimed, err := s.ClaimJobForWorker(worker, time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	var p relinkPayload
	if err := json.Unmarshal(claimed.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Candidates) < 2 {
		t.Fatalf("need at least two candidates: %+v", p)
	}
	result, _ := json.Marshal(map[string]any{"target_id": p.TargetID, "neighbors": []map[string]any{{"id": p.Candidates[0].ID, "similarity": .95}, {"id": p.Candidates[1].ID, "similarity": .90}}})
	completed, err := s.CompleteJobLease(claimed.ID, worker.ID, claimed.LeaseToken, result, "")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "apply_wait" {
		t.Fatalf("worker result must be durable before master apply: %+v", completed)
	}
	if err := e.ApplyAndFinalizeJob(completed); err != nil {
		t.Fatal(err)
	}
	final, _ := s.Job(completed.ID)
	if final.Status != "done" || final.ApplyAttempts != 1 {
		t.Fatalf("final=%+v", final)
	}
	if degree := s.GraphDegree(p.TargetID); degree < 2 {
		t.Fatalf("target degree=%d; expected real multi-neighbor graph", degree)
	}
	m, ok := s.GetMemory(p.TargetID)
	if !ok || m.GraphLinkedAt.IsZero() || m.GraphVersion != m.Version {
		t.Fatalf("graph linkage checkpoint missing: %+v", m)
	}
}

func TestMasterApplyReplayDoesNotInflateSemanticEdge(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"A", "B"} {
		m := core.Memory{ID: id, Kind: "knowledge.chunk", MemoryType: core.MemorySemantic, Text: id, Vector: []float32{1, .1}}
		if err := s.AddMemory(&m); err != nil {
			t.Fatal(err)
		}
	}
	e := New(s, provider.NewRouter(s), cost.New(s))
	a, _ := s.GetMemory("A")
	b, _ := s.GetMemory("B")
	payload, _ := json.Marshal(relinkPayload{
		TargetID: "A", TargetVersion: a.Version, TargetFingerprint: vectorFingerprint(a.Vector), Target: append([]float32(nil), a.Vector...),
		Candidates: []relinkCandidate{{ID: "B", Version: b.Version, Fingerprint: vectorFingerprint(b.Vector), Vector: append([]float32(nil), b.Vector...)}},
		K:          1, MinSimilarity: .1,
	})
	result, _ := json.Marshal(map[string]any{"target_id": "A", "neighbors": []map[string]any{{"id": "B", "similarity": .9}}})
	j := &core.Job{Type: "vector.relink", Status: "apply_wait", Payload: payload, Result: result}
	if err := e.ApplyJobResult(j); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyJobResult(j); err != nil {
		t.Fatal(err)
	}
	g := s.KnowledgeGraph("A", 1, 10)
	if len(g.Edges) != 1 {
		t.Fatalf("edges=%+v", g.Edges)
	}
	if g.Edges[0].Activations != 1 {
		t.Fatalf("retry replay inflated activations: %+v", g.Edges[0])
	}
}

func TestRelinkResultFromDeletedRecreatedMemoryIsObsolete(t *testing.T) {
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, m := range []core.Memory{
		{ID: "A", Kind: "knowledge.chunk", MemoryType: core.MemorySemantic, Text: "old A", Vector: []float32{1, .1}},
		{ID: "B", Kind: "knowledge.chunk", MemoryType: core.MemorySemantic, Text: "B", Vector: []float32{1, .2}},
	} {
		mm := m
		if err := s.AddMemory(&mm); err != nil {
			t.Fatal(err)
		}
	}
	e := New(s, provider.NewRouter(s), cost.New(s))
	job, err := e.enqueueRelink(mustMemory(t, s, "A"))
	if err != nil {
		t.Fatal(err)
	}
	var p relinkPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Candidates) == 0 {
		t.Fatal("expected relink candidate")
	}

	// Simulate a knowledge sync replacing the stable ID while an old worker is
	// still computing. Delete removes old edges; the re-created memory may reuse
	// version 1, so the vector fingerprint is the required fencing signal.
	if err := s.DeleteMemory("A"); err != nil {
		t.Fatal(err)
	}
	recreated := core.Memory{ID: "A", Kind: "knowledge.chunk", MemoryType: core.MemorySemantic, Text: "new A", Vector: []float32{.1, 1}}
	if err := s.AddMemory(&recreated); err != nil {
		t.Fatal(err)
	}

	result, _ := json.Marshal(map[string]any{"target_id": "A", "neighbors": []map[string]any{{"id": p.Candidates[0].ID, "similarity": .99}}})
	job.Status = "apply_wait"
	job.Result = result
	if err := e.ApplyJobResult(job); !errors.Is(err, errObsoleteRelink) {
		t.Fatalf("expected obsolete relink result, got %v", err)
	}
	if got := s.GraphDegree("A"); got != 0 {
		t.Fatalf("obsolete worker result mutated recreated memory graph: degree=%d", got)
	}
	m := mustMemory(t, s, "A")
	if !m.GraphLinkedAt.IsZero() || m.GraphVersion != 0 {
		t.Fatalf("recreated memory was incorrectly marked linked: %+v", m)
	}
}

func mustMemory(t *testing.T, s *store.Store, id string) *core.Memory {
	t.Helper()
	m, ok := s.GetMemory(id)
	if !ok {
		t.Fatalf("memory %s missing", id)
	}
	return m
}

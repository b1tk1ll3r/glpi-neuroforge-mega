package brain

import (
	"context"
	"errors"
	"time"

	"neuroforge/internal/core"
)

// PlanGraphBackfill keeps the associative graph converging toward a durable,
// multi-neighbor state. It deliberately uses a bounded queue so a large KB
// import cannot create an unbounded O(N^2) burst of relink payloads.
func (e *Engine) PlanGraphBackfill() (int, error) {
	cfg := e.store.Config()
	if !e.store.IsOrchestratorLeader() {
		return 0, nil
	}
	wc := cfg.Worker
	if !wc.GraphBackfillEnabled {
		return 0, nil
	}
	if wc.RequireWorkerForGraph && !e.store.HasLiveWorker("cpu", "cpu", "vector.relink") {
		return 0, nil
	}
	maxQueued := wc.GraphBackfillMaxQueued
	if maxQueued <= 0 {
		maxQueued = 512
	}
	pending := e.store.PendingJobCount("vector.relink")
	if pending >= maxQueued {
		return 0, nil
	}
	batch := wc.GraphBackfillBatchSize
	if batch <= 0 {
		batch = 64
	}
	if room := maxQueued - pending; batch > room {
		batch = room
	}
	retryAfter := time.Duration(wc.GraphRetryAfterMinutes) * time.Minute
	if retryAfter <= 0 {
		retryAfter = 6 * time.Hour
	}
	items := e.store.GraphBackfillCandidates(batch, wc.GraphBackfillMinDegree, retryAfter)
	planned := 0
	for i := range items {
		if _, err := e.enqueueRelink(&items[i]); err != nil {
			return planned, err
		}
		planned++
	}
	return planned, nil
}

// ApplyAndFinalizeJob is the durable second phase for jobs whose worker result
// mutates authoritative master state. The worker result is already in the WAL;
// apply can therefore be retried after a master crash without recomputing it.
func (e *Engine) ApplyAndFinalizeJob(j *core.Job) error {
	if j == nil || j.Status != "apply_wait" {
		return nil
	}
	err := e.ApplyJobResult(j)
	if errors.Is(err, errObsoleteRelink) {
		// Obsolete computation is not a failed mutation. Close the old job and
		// leave the current memory unlinked so normal backfill schedules it again.
		err = nil
	}
	_, finishErr := e.store.FinishMasterApply(j.ID, err)
	if finishErr != nil {
		return finishErr
	}
	return err
}

func (e *Engine) ApplyPendingJobResults(limit int) (applied, failed int) {
	jobs := e.store.PendingMasterApplyJobs(limit)
	for i := range jobs {
		if err := e.ApplyAndFinalizeJob(&jobs[i]); err != nil {
			failed++
		} else {
			applied++
		}
	}
	return applied, failed
}

func (e *Engine) RunOrchestrator(ctx context.Context) {
	var lastPrune time.Time
	// Run quickly on boot so a previously imported knowledge corpus starts
	// linking as soon as a capable worker has registered.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cfg := e.store.Config()
			if !e.store.IsOrchestratorLeader() {
				timer.Reset(5 * time.Second)
				continue
			}
			_, _ = e.ApplyPendingJobResults(64)
			if lastPrune.IsZero() || time.Since(lastPrune) >= 10*time.Minute {
				_, _ = e.store.PruneTerminalJobs(time.Duration(cfg.Worker.JobRetentionHours)*time.Hour, cfg.Worker.MaxTerminalJobs)
				lastPrune = time.Now().UTC()
			}
			interval := time.Duration(cfg.Worker.GraphBackfillIntervalS) * time.Second
			if interval < 2*time.Second {
				interval = 10 * time.Second
			}
			_, _ = e.PlanGraphBackfill()
			timer.Reset(interval)
		}
	}
}

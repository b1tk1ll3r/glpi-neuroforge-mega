package brain

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func electionTimeout(cfg core.Config, term uint64) time.Duration {
	minMS, maxMS := cfg.Cluster.ElectionMinMS, cfg.Cluster.ElectionMaxMS
	if minMS <= 0 {
		minMS = 1200
	}
	if maxMS <= minMS {
		maxMS = minMS * 2
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%s:%d:%d", cfg.Cluster.NodeID, term, time.Now().UnixNano()/int64(time.Millisecond))))
	span := uint64(maxMS - minMS)
	jitter := 0
	if span > 0 {
		jitter = int(h.Sum64() % span)
	}
	return time.Duration(minMS+jitter) * time.Millisecond
}

func (e *Engine) resetElectionDeadline(cfg core.Config, state core.ClusterState, now time.Time) {
	e.electionMu.Lock()
	defer e.electionMu.Unlock()
	if e.electionDeadline.IsZero() || state.LastHeartbeat.After(e.observedHeartbeat) {
		e.observedHeartbeat = state.LastHeartbeat
		e.electionDeadline = now.Add(electionTimeout(cfg, state.Term))
	}
}

func (e *Engine) electionDue(now time.Time) bool {
	e.electionMu.Lock()
	defer e.electionMu.Unlock()
	if e.electionRunning || e.electionDeadline.IsZero() || now.Before(e.electionDeadline) {
		return false
	}
	e.electionRunning = true
	return true
}
func (e *Engine) electionFinished(cfg core.Config, term uint64) {
	e.electionMu.Lock()
	defer e.electionMu.Unlock()
	e.electionRunning = false
	e.electionDeadline = time.Now().Add(electionTimeout(cfg, term))
}

func (e *Engine) attemptElection(ctx context.Context) {
	cfg := e.store.Config()
	req, err := e.store.StartElection()
	if err != nil {
		e.electionFinished(cfg, e.store.ClusterState().Term)
		return
	}
	defer e.electionFinished(cfg, req.Term)
	voters, quorum := clusterVoters(cfg)
	votes := 1
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, peer := range cfg.Cluster.Peers {
		if !peer.Enabled || !peer.Voting {
			continue
		}
		wg.Add(1)
		go func(p core.ClusterPeer) {
			defer wg.Done()
			var resp core.ClusterVoteResponse
			if err := e.clusterPost(ctx, p.BaseURL, "/internal/v1/cluster/request-vote", req, &resp); err != nil {
				return
			}
			if resp.Term > req.Term {
				_ = e.store.StepDown(resp.Term, "")
				return
			}
			if resp.Term == req.Term && resp.VoteGranted {
				mu.Lock()
				votes++
				mu.Unlock()
			}
		}(peer)
	}
	wg.Wait()
	if e.store.ClusterState().Term != req.Term {
		return
	}
	if votes >= quorum {
		if err := e.store.BecomeLeader(req.Term); err == nil {
			e.sendHeartbeats(ctx)
		}
		return
	}
	_ = voters // retained in status/debugging; quorum already derived from same set.
}

func (e *Engine) sendHeartbeats(ctx context.Context) {
	cfg := e.store.Config()
	state := e.store.ClusterState()
	if !cfg.Cluster.Enabled || !cfg.Cluster.AutoElection || state.Role != store.ClusterLeader || state.LeaderID != cfg.Cluster.NodeID {
		return
	}
	h := core.ClusterHeartbeat{Term: state.Term, LeaderID: cfg.Cluster.NodeID, CommitIndex: state.CommitIndex, LastIndex: state.LastIndex}
	var wg sync.WaitGroup
	for _, peer := range cfg.Cluster.Peers {
		if !peer.Enabled {
			continue
		}
		wg.Add(1)
		go func(p core.ClusterPeer) {
			defer wg.Done()
			var resp core.ClusterHeartbeatResponse
			if err := e.clusterPost(ctx, p.BaseURL, "/internal/v1/cluster/heartbeat", h, &resp); err != nil {
				return
			}
			if resp.Term > h.Term {
				_ = e.store.StepDown(resp.Term, "")
			}
		}(peer)
	}
	wg.Wait()
	_ = e.store.TouchLeaderHeartbeat()
}

func (e *Engine) ClusterVote(req core.ClusterVoteRequest) (core.ClusterVoteResponse, error) {
	return e.store.GrantVote(req)
}
func (e *Engine) ClusterHeartbeat(h core.ClusterHeartbeat) (core.ClusterHeartbeatResponse, error) {
	return e.store.AcceptHeartbeat(h)
}

func (e *Engine) RunV5Maintenance(ctx context.Context) {
	go e.RunV4Maintenance(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastHeartbeatSent, lastTier, lastIndexMerge time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cfg := e.store.Config()
			if cfg.Cluster.Enabled && cfg.Cluster.AutoElection {
				state := e.store.ClusterState()
				if state.Role == store.ClusterLeader && state.LeaderID == cfg.Cluster.NodeID {
					hb := time.Duration(cfg.Cluster.HeartbeatMS) * time.Millisecond
					if hb <= 0 {
						hb = 350 * time.Millisecond
					}
					if now.Sub(lastHeartbeatSent) >= hb {
						e.sendHeartbeats(ctx)
						lastHeartbeatSent = now
					}
				} else {
					e.resetElectionDeadline(cfg, state, now)
					if e.electionDue(now) {
						go e.attemptElection(ctx)
					}
				}
			}
			if cfg.Storage.Tiering.Enabled {
				iv := time.Duration(cfg.Storage.Tiering.IntervalMinutes) * time.Minute
				if iv <= 0 {
					iv = 5 * time.Minute
				}
				if lastTier.IsZero() || now.Sub(lastTier) >= iv {
					e.store.TierMemoryBodies(now)
					lastTier = now
				}
			}
			if cfg.Storage.IndexSegments.Enabled && cfg.Storage.IndexSegments.BackgroundMergeMinutes > 0 {
				iv := time.Duration(cfg.Storage.IndexSegments.BackgroundMergeMinutes) * time.Minute
				if lastIndexMerge.IsZero() || now.Sub(lastIndexMerge) >= iv {
					st := e.store.IndexSnapshotStatus()
					deltas, _ := st["deltas"].(int)
					threshold := cfg.Storage.IndexSegments.MergeAtDeltas
					if threshold <= 0 {
						threshold = 8
					}
					if deltas >= threshold {
						_, _ = e.store.CompactIndexSegments()
					}
					lastIndexMerge = now
				}
			}
		}
	}
}

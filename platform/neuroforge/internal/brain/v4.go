package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

type ClusterRepairResult struct {
	Pending   int      `json:"pending"`
	Committed int      `json:"committed"`
	Aborted   int      `json:"aborted"`
	Deferred  int      `json:"deferred"`
	Errors    []string `json:"errors,omitempty"`
}

type clusterPrepareResult struct {
	peer   core.ClusterPeer
	ok     bool
	voting bool
	err    error
}

func (e *Engine) addMemory(ctx context.Context, m *core.Memory) error {
	cfg := e.store.Config()
	if !cfg.Cluster.Enabled {
		return e.store.AddMemory(m)
	}
	if m == nil {
		return errors.New("memory required")
	}
	if m.ID == "" {
		m.ID = store.NewID("mem")
	}
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.AccessedAt.IsZero() {
		m.AccessedAt = now
	}
	if m.Salience == 0 {
		m.Salience = 1
	}
	if m.Confidence == 0 {
		m.Confidence = 1
	}
	if m.MemoryType == "" {
		m.MemoryType = memoryTypeForKind(m.Kind)
	}
	if m.Status == "" {
		m.Status = core.MemoryActive
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.ShardID == "" {
		m.ShardID = cfg.Sharding.LocalShardID
	}
	if m.OriginShardID == "" {
		m.OriginShardID = m.ShardID
	}
	leaderID := e.store.EffectiveLeaderID()
	if cfg.Cluster.AutoElection && leaderID == "" {
		return errors.New("cluster has no elected leader")
	}
	if m.HomeShardID == "" {
		m.HomeShardID = leaderID
	}
	if cfg.Cluster.NodeID != leaderID {
		return e.forwardMemoryToClusterLeader(ctx, m)
	}
	return e.quorumCommitMemoryLeader(ctx, m)
}

func clusterVoters(cfg core.Config) (voters int, quorum int) {
	voters = 1 // local node is always a voter when cluster mode is enabled.
	for _, p := range cfg.Cluster.Peers {
		if p.Enabled && p.Voting {
			voters++
		}
	}
	quorum = cfg.Cluster.Quorum
	if quorum <= 0 {
		quorum = voters/2 + 1
	}
	return voters, quorum
}

func (e *Engine) quorumCommitMemoryLeader(ctx context.Context, m *core.Memory) error {
	e.clusterMu.Lock()
	defer e.clusterMu.Unlock()
	cfg := e.store.Config()
	if !cfg.Cluster.Enabled {
		return e.store.AddMemory(m)
	}
	leaderID := e.store.EffectiveLeaderID()
	if cfg.Cluster.NodeID != leaderID {
		return errors.New("local node is not cluster leader")
	}
	voters, quorum := clusterVoters(cfg)
	if quorum < 1 || quorum > voters {
		return fmt.Errorf("cluster quorum %d is invalid for %d voters", quorum, voters)
	}
	clusterState := e.store.ClusterState()
	term := cfg.Cluster.Term
	if cfg.Cluster.AutoElection {
		term = clusterState.Term
	}
	idx, err := e.store.NextClusterIndex(term)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	entry := core.ClusterEntry{
		ID: store.NewID("entry"), Term: term, Index: idx, LeaderID: cfg.Cluster.NodeID,
		Type: "memory.upsert", Payload: payload, CreatedAt: time.Now().UTC(),
	}
	if err := e.store.PrepareClusterEntry(entry); err != nil {
		return fmt.Errorf("prepare local cluster entry: %w", err)
	}

	prepared := []core.ClusterPeer{}
	votingAcks := 1
	ch := make(chan clusterPrepareResult, len(cfg.Cluster.Peers))
	var wg sync.WaitGroup
	for _, peer := range cfg.Cluster.Peers {
		if !peer.Enabled {
			continue
		}
		wg.Add(1)
		go func(p core.ClusterPeer) {
			defer wg.Done()
			err := e.clusterPost(ctx, p.BaseURL, "/internal/v1/cluster/prepare", entry, nil)
			ch <- clusterPrepareResult{peer: p, ok: err == nil, voting: p.Voting, err: err}
		}(peer)
	}
	wg.Wait()
	close(ch)
	var prepareErrors []string
	for r := range ch {
		if r.ok {
			prepared = append(prepared, r.peer)
			if r.voting {
				votingAcks++
			}
		} else if r.err != nil {
			prepareErrors = append(prepareErrors, r.peer.ID+": "+r.err.Error())
		}
	}
	if votingAcks < quorum {
		_ = e.store.RecordClusterDecision(entry, "abort")
		_ = e.store.AbortPreparedClusterEntry(entry.ID)
		for _, p := range prepared {
			_ = e.clusterPost(context.Background(), p.BaseURL, "/internal/v1/cluster/abort", map[string]string{"id": entry.ID}, nil)
		}
		return fmt.Errorf("cluster quorum not reached: %d/%d voting prepares (quorum %d); %s", votingAcks, voters, quorum, strings.Join(prepareErrors, "; "))
	}

	if cfg.Cluster.AutoElection {
		cur := e.store.ClusterState()
		if cur.Term != term || cur.Role != store.ClusterLeader || cur.LeaderID != cfg.Cluster.NodeID {
			_ = e.store.RecordClusterDecision(entry, "abort")
			_ = e.store.AbortPreparedClusterEntry(entry.ID)
			for _, p := range prepared {
				_ = e.clusterPost(context.Background(), p.BaseURL, "/internal/v1/cluster/abort", map[string]string{"id": entry.ID}, nil)
			}
			return errors.New("leadership changed before commit decision")
		}
	}

	// The decision log is fsync'd before the local mutation is made visible.
	// Prepared followers can recover the decision from this leader after a
	// transient disconnect or process restart.
	if err := e.store.RecordClusterDecision(entry, "commit"); err != nil {
		return fmt.Errorf("persist cluster commit decision: %w", err)
	}
	if err := e.store.CommitPreparedClusterEntry(entry); err != nil {
		return fmt.Errorf("commit local cluster entry: %w", err)
	}

	// Best effort delivery after a durable leader decision. Failures remain as
	// prepared entries on followers and are repaired by RepairCluster.
	for _, p := range prepared {
		_ = e.clusterPost(ctx, p.BaseURL, "/internal/v1/cluster/commit", entry, nil)
	}
	if got, ok := e.store.GetMemory(m.ID); ok {
		*m = *got
	}
	return nil
}

func (e *Engine) clusterPost(ctx context.Context, baseURL, path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	cfg := e.store.Config()
	timeout := time.Duration(cfg.Cluster.RequestTimeoutS) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Token", e.store.Secrets().ClusterToken)
	resp, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) clusterLeaderURL(cfg core.Config) (string, bool) {
	leaderID := e.store.EffectiveLeaderID()
	for _, p := range cfg.Cluster.Peers {
		if p.Enabled && p.ID == leaderID {
			return p.BaseURL, true
		}
	}
	return "", false
}

func (e *Engine) forwardMemoryToClusterLeader(ctx context.Context, m *core.Memory) error {
	cfg := e.store.Config()
	url, ok := e.clusterLeaderURL(cfg)
	if !ok {
		return fmt.Errorf("cluster leader %q is not configured as a peer", e.store.EffectiveLeaderID())
	}
	var out core.Memory
	if err := e.clusterPost(ctx, url, "/internal/v1/cluster/propose/memory", m, &out); err != nil {
		return err
	}
	*m = out
	return nil
}

func (e *Engine) ClusterPrepare(entry core.ClusterEntry) error {
	return e.store.PrepareClusterEntry(entry)
}

func (e *Engine) ClusterCommit(entry core.ClusterEntry) error {
	if err := e.store.RecordClusterDecision(entry, "commit"); err != nil {
		return err
	}
	return e.store.CommitPreparedClusterEntry(entry)
}

func (e *Engine) ClusterAbort(id string) error {
	for _, entry := range e.store.PendingClusterEntries() {
		if entry.ID == id {
			_ = e.store.RecordClusterDecision(entry, "abort")
			break
		}
	}
	return e.store.AbortPreparedClusterEntry(id)
}

func (e *Engine) ClusterProposeMemory(ctx context.Context, m *core.Memory) error {
	cfg := e.store.Config()
	if !cfg.Cluster.Enabled || cfg.Cluster.NodeID != e.store.EffectiveLeaderID() {
		return errors.New("cluster proposal endpoint is only available on the current leader")
	}
	if m.ID == "" {
		m.ID = store.NewID("mem")
	}
	return e.addMemory(ctx, m)
}

func (e *Engine) RepairCluster(ctx context.Context) ClusterRepairResult {
	cfg := e.store.Config()
	pending := e.store.PendingClusterEntries()
	out := ClusterRepairResult{Pending: len(pending)}
	if !cfg.Cluster.Enabled {
		out.Errors = append(out.Errors, "cluster is disabled")
		return out
	}
	for _, entry := range pending {
		var decision store.ClusterDecision
		var found bool
		if cfg.Cluster.NodeID == e.store.EffectiveLeaderID() {
			decision, found = e.store.ClusterDecision(entry.ID)
		} else {
			leaderURL, ok := e.clusterLeaderURL(cfg)
			if !ok {
				out.Errors = append(out.Errors, entry.ID+": leader URL not configured")
				out.Deferred++
				continue
			}
			timeout := time.Duration(cfg.Cluster.RequestTimeoutS) * time.Second
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			req, err := http.NewRequestWithContext(callCtx, http.MethodGet, strings.TrimRight(leaderURL, "/")+"/internal/v1/cluster/decision/"+entry.ID, nil)
			if err == nil {
				req.Header.Set("X-Cluster-Token", e.store.Secrets().ClusterToken)
				resp, reqErr := e.http.Do(req)
				if reqErr == nil {
					raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &decision) == nil {
						found = true
					}
				} else {
					err = reqErr
				}
			}
			cancel()
			if err != nil {
				out.Errors = append(out.Errors, entry.ID+": "+err.Error())
			}
		}
		if !found {
			out.Deferred++
			continue
		}
		switch decision.Decision {
		case "commit":
			if err := e.store.CommitPreparedClusterEntry(entry); err != nil {
				out.Errors = append(out.Errors, entry.ID+": "+err.Error())
				out.Deferred++
			} else {
				out.Committed++
			}
		case "abort":
			if err := e.store.AbortPreparedClusterEntry(entry.ID); err != nil {
				out.Errors = append(out.Errors, entry.ID+": "+err.Error())
				out.Deferred++
			} else {
				out.Aborted++
			}
		default:
			out.Deferred++
		}
	}
	return out
}

func (e *Engine) RunV4Maintenance(ctx context.Context) {
	go e.RunV3Maintenance(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var lastCompaction time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := e.store.Config()
			if cfg.Cluster.Enabled {
				_ = e.RepairCluster(ctx)
			}
			if cfg.Storage.Segments.Enabled && cfg.Storage.Segments.CompactTombstonePct > 0 && time.Since(lastCompaction) >= time.Hour {
				stats := e.store.SegmentStats()
				ratio := 0.0
				if stats.Records > 0 {
					ratio = float64(stats.Tombstones) / float64(stats.Records)
				}
				if ratio >= cfg.Storage.Segments.CompactTombstonePct {
					if _, err := e.store.CompactMemorySegments(); err == nil {
						_ = e.store.ForceCheckpoint()
						lastCompaction = time.Now()
					}
				}
			}
		}
	}
}

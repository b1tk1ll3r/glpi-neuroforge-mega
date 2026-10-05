package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"neuroforge/internal/core"
)

type ClusterDecision struct {
	EntryID   string    `json:"entry_id"`
	Term      uint64    `json:"term"`
	Index     uint64    `json:"index"`
	Decision  string    `json:"decision"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) clusterDir() string         { return filepath.Join(s.dir, "cluster") }
func (s *Store) pendingClusterDir() string  { return filepath.Join(s.clusterDir(), "pending") }
func (s *Store) decisionClusterDir() string { return filepath.Join(s.clusterDir(), "decisions") }

func writeJSONSync(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (s *Store) ClusterState() core.ClusterState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Cluster
}

func (s *Store) NextClusterIndex(term uint64) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if term < s.state.Cluster.Term {
		return 0, fmt.Errorf("stale cluster term %d < %d", term, s.state.Cluster.Term)
	}
	last := s.state.Cluster.LastIndex
	if s.state.Cluster.CommitIndex > last {
		last = s.state.Cluster.CommitIndex
	}
	return last + 1, nil
}

// clusterEntryIDPattern matches NewID output. Entry IDs become file names in
// the pending/decision directories, so separators and dots must be rejected.
var clusterEntryIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validClusterEntryID(id string) bool { return clusterEntryIDPattern.MatchString(id) }

func (s *Store) PrepareClusterEntry(entry core.ClusterEntry) error {
	if entry.ID == "" || entry.Type == "" || entry.Term == 0 || entry.Index == 0 || entry.LeaderID == "" {
		return errors.New("invalid cluster entry")
	}
	if !validClusterEntryID(entry.ID) {
		return errors.New("invalid cluster entry id")
	}
	s.mu.RLock()
	cfg := s.state.Config.Cluster
	state := s.state.Cluster
	s.mu.RUnlock()
	if !cfg.Enabled {
		return errors.New("cluster is disabled")
	}
	if entry.Term < state.Term {
		return fmt.Errorf("stale cluster term %d < %d", entry.Term, state.Term)
	}
	leaderID := cfg.LeaderID
	if cfg.AutoElection && state.LeaderID != "" {
		leaderID = state.LeaderID
	}
	if entry.LeaderID != leaderID {
		return fmt.Errorf("entry leader %q does not match current leader %q", entry.LeaderID, leaderID)
	}
	if entry.Index <= state.CommitIndex {
		// Idempotent retry of an already committed entry is acceptable only if
		// the decision exists locally.
		if d, ok := s.ClusterDecision(entry.ID); ok && d.Decision == "commit" {
			return nil
		}
		return fmt.Errorf("cluster index %d is already committed through %d", entry.Index, state.CommitIndex)
	}
	if err := s.appendClusterLogEntry(entry); err != nil {
		return err
	}
	return writeJSONSync(filepath.Join(s.pendingClusterDir(), entry.ID+".json"), &entry)
}

func (s *Store) AbortPreparedClusterEntry(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("entry id required")
	}
	if !validClusterEntryID(id) {
		return errors.New("invalid cluster entry id")
	}
	err := os.Remove(filepath.Join(s.pendingClusterDir(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) PendingClusterEntries() []core.ClusterEntry {
	ents, err := os.ReadDir(s.pendingClusterDir())
	if err != nil {
		return nil
	}
	out := make([]core.ClusterEntry, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		var e core.ClusterEntry
		if s.loadJSON(filepath.Join(s.pendingClusterDir(), ent.Name()), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Term == out[j].Term {
			return out[i].Index < out[j].Index
		}
		return out[i].Term < out[j].Term
	})
	return out
}

func (s *Store) RecordClusterDecision(entry core.ClusterEntry, decision string) error {
	if decision != "commit" && decision != "abort" {
		return errors.New("cluster decision must be commit or abort")
	}
	if !validClusterEntryID(entry.ID) {
		return errors.New("invalid cluster entry id")
	}
	d := ClusterDecision{EntryID: entry.ID, Term: entry.Term, Index: entry.Index, Decision: decision, CreatedAt: time.Now().UTC()}
	if err := s.appendClusterLogDecision(d); err != nil {
		return err
	}
	return writeJSONSync(filepath.Join(s.decisionClusterDir(), entry.ID+".json"), &d)
}

func (s *Store) ClusterDecision(id string) (ClusterDecision, bool) {
	var d ClusterDecision
	if !validClusterEntryID(id) {
		return ClusterDecision{}, false
	}
	if err := s.loadJSON(filepath.Join(s.decisionClusterDir(), id+".json"), &d); err != nil {
		return ClusterDecision{}, false
	}
	return d, d.EntryID != ""
}

func (s *Store) CommitPreparedClusterEntry(entry core.ClusterEntry) error {
	if !validClusterEntryID(entry.ID) {
		return errors.New("invalid cluster entry id")
	}
	if d, ok := s.ClusterDecision(entry.ID); ok && d.Decision == "abort" {
		return errors.New("cluster entry was aborted")
	}
	var pending core.ClusterEntry
	pendingPath := filepath.Join(s.pendingClusterDir(), entry.ID+".json")
	if err := s.loadJSON(pendingPath, &pending); err != nil {
		// Leader may recover after applying the memory but before deleting pending.
		state := s.ClusterState()
		if state.CommitIndex >= entry.Index {
			return nil
		}
		return fmt.Errorf("prepared cluster entry missing: %w", err)
	}
	if pending.Term != entry.Term || pending.Index != entry.Index || pending.Type != entry.Type || pending.LeaderID != entry.LeaderID {
		return errors.New("prepared cluster entry does not match commit")
	}

	switch entry.Type {
	case "memory.upsert":
		var m core.Memory
		if err := json.Unmarshal(entry.Payload, &m); err != nil {
			return err
		}
		if err := s.UpsertClusterMemory(&m); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported cluster entry type %q", entry.Type)
	}

	s.mu.Lock()
	if entry.Term > s.state.Cluster.Term {
		s.state.Cluster.Term = entry.Term
	}
	if entry.Index > s.state.Cluster.LastIndex {
		s.state.Cluster.LastIndex = entry.Index
	}
	if entry.Index > s.state.Cluster.CommitIndex {
		s.state.Cluster.CommitIndex = entry.Index
	}
	s.state.Cluster.LastCommit = time.Now().UTC()
	state := s.state.Cluster
	err := s.commitLocked("cluster.state", state)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_ = os.Remove(pendingPath)
	return nil
}

func (s *Store) UpsertClusterMemory(m *core.Memory) error {
	if m == nil || strings.TrimSpace(m.ID) == "" {
		return errors.New("cluster memory id required")
	}
	if existing, ok := s.GetMemory(m.ID); ok {
		// A commit retry is idempotent only when the immutable learning payload
		// matches. Location/timestamps may legitimately differ between replicas.
		if sameClusterMemory(existing, m) {
			return nil
		}
		return fmt.Errorf("cluster memory %s already exists with different content", m.ID)
	}
	cp := cloneMemory(*m)
	cfg := s.Config()
	if cp.OriginShardID == "" {
		cp.OriginShardID = cp.ShardID
	}
	if cp.OriginShardID == "" {
		cp.OriginShardID = s.EffectiveLeaderID()
	}
	cp.ShardID = cfg.Sharding.LocalShardID
	if cp.HomeShardID == "" {
		cp.HomeShardID = s.EffectiveLeaderID()
	}
	return s.AddMemory(&cp)
}

func sameClusterMemory(a, b *core.Memory) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Text != b.Text || a.Kind != b.Kind || a.MemoryType != b.MemoryType ||
		a.TruthKey != b.TruthKey || a.Version != b.Version || len(a.Vector) != len(b.Vector) {
		return false
	}
	for i := range a.Vector {
		if a.Vector[i] != b.Vector[i] {
			return false
		}
	}
	return true
}

func (s *Store) ClusterStatus() map[string]any {
	s.mu.RLock()
	cfg := s.state.Config.Cluster
	state := s.state.Cluster
	s.mu.RUnlock()
	voters := 1
	peers := 0
	for _, p := range cfg.Peers {
		if !p.Enabled {
			continue
		}
		peers++
		if p.Voting {
			voters++
		}
	}
	quorum := cfg.Quorum
	if quorum <= 0 {
		quorum = voters/2 + 1
	}
	leaderID := cfg.LeaderID
	if cfg.AutoElection {
		leaderID = state.LeaderID
	}
	return map[string]any{
		"enabled": cfg.Enabled, "node_id": cfg.NodeID, "leader_id": leaderID, "configured_leader_id": cfg.LeaderID, "auto_election": cfg.AutoElection, "role": state.Role, "configured_term": cfg.Term,
		"term": state.Term, "voted_for": state.VotedFor, "last_heartbeat": state.LastHeartbeat, "last_index": state.LastIndex, "commit_index": state.CommitIndex, "last_commit": state.LastCommit,
		"peers": peers, "voters": voters, "quorum": quorum, "pending": len(s.PendingClusterEntries()), "replicated_log": s.ClusterLogStats(),
	}
}

package store

import (
	"time"
)

// ObservabilitySnapshot is intentionally cheap to collect. In particular it
// does not iterate over every memory, which keeps Prometheus scrapes O(1) with
// respect to the number of stored memories.
type ObservabilitySnapshot struct {
	Revision        uint64 `json:"revision"`
	Memories        int    `json:"memories"`
	Synapses        int    `json:"synapses"`
	Goals           int    `json:"goals"`
	Sources         int    `json:"sources"`
	LearningCycles  int    `json:"learning_cycles"`
	KnowledgeEvents int    `json:"knowledge_events"`
	UsageEvents     int    `json:"usage_events"`

	JobsQueued  int `json:"jobs_queued"`
	JobsClaimed int `json:"jobs_claimed"`
	JobsDone    int `json:"jobs_done"`
	JobsFailed  int `json:"jobs_failed"`

	HNSWNodes      int    `json:"hnsw_nodes"`
	HNSWDimensions int    `json:"hnsw_dimensions"`
	DiskPQItems    int    `json:"disk_pq_items"`
	DiskPQBytes    int64  `json:"disk_pq_bytes"`
	IndexMode      string `json:"index_mode"`
	RemoteShards   int    `json:"remote_shards"`

	Segments SegmentStats `json:"segments"`

	HotMemories       int    `json:"hot_memories"`
	ColdMemories      int    `json:"cold_memories"`
	HotBytes          int64  `json:"hot_bytes"`
	TierEvictions     uint64 `json:"tier_evictions_total"`
	PageCacheEnabled  bool   `json:"page_cache_enabled"`
	PageCacheMaxBytes int64  `json:"page_cache_max_bytes"`
	PageCacheBytes    int64  `json:"page_cache_bytes"`
	PageCacheEntries  int    `json:"page_cache_entries"`
	PageCacheHits     uint64 `json:"page_cache_hits_total"`
	PageCacheMisses   uint64 `json:"page_cache_misses_total"`
	PageCacheEvicts   uint64 `json:"page_cache_evictions_total"`

	IndexSnapshotRevision uint64    `json:"index_snapshot_revision"`
	IndexDeltaCount       int       `json:"index_delta_count"`
	DiskANNRevision       uint64    `json:"disk_ann_revision"`
	DiskANNBuilding       bool      `json:"disk_ann_building"`
	DiskANNBuiltAt        time.Time `json:"disk_ann_built_at,omitempty"`

	WALEventsSinceCheckpoint int `json:"wal_events_since_checkpoint"`

	ClusterEnabled     bool            `json:"cluster_enabled"`
	ClusterNodeID      string          `json:"cluster_node_id"`
	ClusterLeaderID    string          `json:"cluster_leader_id"`
	ClusterRole        string          `json:"cluster_role"`
	ClusterTerm        uint64          `json:"cluster_term"`
	ClusterLastIndex   uint64          `json:"cluster_last_index"`
	ClusterCommitIndex uint64          `json:"cluster_commit_index"`
	ClusterPeers       int             `json:"cluster_peers"`
	ClusterVoters      int             `json:"cluster_voters"`
	ClusterQuorum      int             `json:"cluster_quorum"`
	ClusterLog         ClusterLogStats `json:"cluster_log"`
}

func (s *Store) ObservabilitySnapshot() ObservabilitySnapshot {
	s.mu.RLock()
	out := ObservabilitySnapshot{
		Revision:                 s.state.Revision,
		Memories:                 len(s.state.Memories),
		Synapses:                 len(s.state.Synapses),
		Goals:                    len(s.state.Goals),
		Sources:                  len(s.state.Sources),
		LearningCycles:           len(s.state.Cycles),
		KnowledgeEvents:          len(s.state.KnowledgeEvents),
		UsageEvents:              len(s.state.Usage),
		HNSWDimensions:           len(s.indexes),
		IndexMode:                indexMode(s.state.Config),
		RemoteShards:             0,
		HotMemories:              len(s.hotBodies),
		HotBytes:                 s.hotBodyBytes,
		TierEvictions:            s.tierEvictions,
		IndexSnapshotRevision:    s.indexSnapshotRevision,
		IndexDeltaCount:          s.indexDeltaCount,
		DiskANNRevision:          s.diskANNRevision,
		DiskANNBuilding:          s.diskANNBuilding,
		DiskANNBuiltAt:           s.diskANNBuiltAt,
		WALEventsSinceCheckpoint: s.walEventsSinceCheckpoint,
		ClusterEnabled:           s.state.Config.Cluster.Enabled,
		ClusterNodeID:            s.state.Config.Cluster.NodeID,
		ClusterLeaderID:          s.state.Cluster.LeaderID,
		ClusterRole:              s.state.Cluster.Role,
		ClusterTerm:              s.state.Cluster.Term,
		ClusterLastIndex:         s.state.Cluster.LastIndex,
		ClusterCommitIndex:       s.state.Cluster.CommitIndex,
	}
	if !s.state.Config.Cluster.AutoElection && out.ClusterLeaderID == "" {
		out.ClusterLeaderID = s.state.Config.Cluster.LeaderID
	}
	for _, j := range s.state.Jobs {
		switch j.Status {
		case "queued":
			out.JobsQueued++
		case "claimed":
			out.JobsClaimed++
		case "done", "completed":
			out.JobsDone++
		case "failed", "error":
			out.JobsFailed++
		}
	}
	for _, idx := range s.indexes {
		out.HNSWNodes += idx.Len()
	}
	for _, idx := range s.diskIndexes {
		out.DiskPQItems += idx.Len()
		out.DiskPQBytes += idx.DiskBytes()
	}
	for _, sh := range s.state.Config.Sharding.Remote {
		if sh.Enabled {
			out.RemoteShards++
		}
	}
	for _, p := range s.state.Config.Cluster.Peers {
		if !p.Enabled {
			continue
		}
		out.ClusterPeers++
		if p.Voting {
			out.ClusterVoters++
		}
	}
	// The local node is always a voter in the current cluster model.
	out.ClusterVoters++
	out.ClusterQuorum = s.state.Config.Cluster.Quorum
	if out.ClusterQuorum <= 0 {
		out.ClusterQuorum = out.ClusterVoters/2 + 1
	}
	if s.segments != nil {
		out.Segments = s.segments.Stats()
	}
	out.ColdMemories = out.Memories - out.HotMemories
	if out.ColdMemories < 0 {
		out.ColdMemories = 0
	}
	pc := s.pageCache
	s.mu.RUnlock()

	if pc != nil {
		pc.mu.Lock()
		out.PageCacheEnabled = pc.enabled
		out.PageCacheMaxBytes = pc.maxBytes
		out.PageCacheBytes = pc.bytes
		out.PageCacheEntries = len(pc.items)
		out.PageCacheHits = pc.hits
		out.PageCacheMisses = pc.misses
		out.PageCacheEvicts = pc.evictions
		pc.mu.Unlock()
	}
	if out.ClusterEnabled {
		out.ClusterLog = s.ClusterLogStats()
	}
	return out
}

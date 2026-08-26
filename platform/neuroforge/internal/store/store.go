package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

type Store struct {
	mu                       sync.RWMutex
	dir                      string
	state                    core.PersistedState
	secrets                  core.Secrets
	indexes                  map[int]*vector.HNSW
	diskIndexes              map[int]*vector.PQIndex
	diskANNRevision          uint64
	diskANNBuiltAt           time.Time
	diskANNSegmentRecords    int
	diskANNBuilding          bool
	segments                 *SegmentStore
	vectorJournal            *VectorJournal
	indexShadow              map[int]indexSnapshotShadow
	indexSnapshotRevision    uint64
	indexDeltaCount          int
	walEventsSinceCheckpoint int
	pageCache                *MemoryPageCache
	hotBodies                map[string]hotBodyState
	hotHeap                  hotBodyHeap
	hotBodyBytes             int64
	hotGeneration            uint64
	tierEvictions            uint64
	clusterLogMu             sync.Mutex
	clusterLog               *ClusterLog
	provenanceSourceIDs      map[string]map[string]struct{}
}

func New(dir string) (*Store, error) {
	if dir == "" {
		dir = "./data"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, indexes: map[int]*vector.HNSW{}, diskIndexes: map[int]*vector.PQIndex{}, provenanceSourceIDs: map[string]map[string]struct{}{}}
	s.state = core.PersistedState{Config: core.DefaultConfig(), Memories: map[string]*core.Memory{}, Synapses: map[string]*core.Synapse{}, Jobs: map[string]*core.Job{}, Goals: map[string]*core.Goal{}, Sources: map[string]*core.KnowledgeSource{}, ResearchRuns: map[string]*core.ResearchRun{}}
	_ = s.loadJSON(filepath.Join(dir, "state.json"), &s.state)
	_ = s.loadJSON(filepath.Join(dir, "secrets.json"), &s.secrets)
	if s.state.Memories == nil {
		s.state.Memories = map[string]*core.Memory{}
	}
	if s.state.Synapses == nil {
		s.state.Synapses = map[string]*core.Synapse{}
	}
	if s.state.Jobs == nil {
		s.state.Jobs = map[string]*core.Job{}
	}
	if s.state.Goals == nil {
		s.state.Goals = map[string]*core.Goal{}
	}
	if s.state.ResearchRuns == nil {
		s.state.ResearchRuns = map[string]*core.ResearchRun{}
	}
	if s.state.Config.Listen == "" {
		s.state.Config = core.DefaultConfig()
	} else {
		applyNewDefaults(&s.state.Config)
	}
	s.pageCache = newMemoryPageCache(s.state.Config.Storage.PageCache.Enabled, s.state.Config.Storage.PageCache.MaxBytes)
	// v0.6 binary vector sidecar: rebuildable acceleration data used by the
	// disk ANN builder. Corruption must never prevent the authoritative memory
	// store from opening; move a bad cache aside and recreate it empty.
	vjPath := filepath.Join(dir, "vector-journal.nfv")
	vj, vjErr := openVectorJournal(vjPath, vectorJournalOptionsFromConfig(s.state.Config))
	if vjErr != nil {
		_ = os.Rename(vjPath, vjPath+".corrupt-"+fmt.Sprint(time.Now().UnixNano()))
		vj, vjErr = openVectorJournal(vjPath, vectorJournalOptionsFromConfig(s.state.Config))
	}
	if vjErr == nil {
		s.vectorJournal = vj
	}

	if s.state.Config.Storage.Segments.Enabled {
		seg, err := openSegmentStore(filepath.Join(dir, "memory-segments"), s.state.Config.Storage.Segments.MaxSegmentBytes, s.state.Config.Storage.Segments.MmapSealed)
		if err != nil {
			return nil, fmt.Errorf("open memory segments: %w", err)
		}
		s.segments = seg
		if seg.HasRecords() {
			// v0.5: memory metadata is reconstructed while segment files are scanned;
			// state.json no longer needs one metadata object per memory.
			meta := seg.ConsumeMetadata()
			if s.state.MemoryCatalog.SegmentBacked && s.state.MemoryCatalog.Count > 0 && len(meta) == 0 {
				return nil, errors.New("memory catalog expects segment-backed memories but no live segment metadata could be reconstructed")
			}
			s.state.Memories = meta
		}
	}
	if err := s.replayWAL(); err != nil {
		return nil, err
	}
	applyNewDefaults(&s.state.Config)
	if s.state.Cluster.Term < s.state.Config.Cluster.Term {
		s.state.Cluster.Term = s.state.Config.Cluster.Term
	}
	s.initializeClusterRoleLocked()
	if s.state.Goals == nil {
		s.state.Goals = map[string]*core.Goal{}
	}
	if s.state.ResearchRuns == nil {
		s.state.ResearchRuns = map[string]*core.ResearchRun{}
	}
	if s.state.Sources == nil {
		s.state.Sources = map[string]*core.KnowledgeSource{}
	}
	// A process crash can leave the last persisted research run marked running.
	// Research telemetry is observational, so mark such runs interrupted on boot;
	// the authoritative memories/sources themselves are recovered independently.
	nowResearchRecovery := time.Now().UTC()
	for _, run := range s.state.ResearchRuns {
		if run != nil && run.Status == "running" {
			run.Status = "interrupted"
			run.UpdatedAt = nowResearchRecovery
			run.CompletedAt = nowResearchRecovery
			if run.LastError == "" {
				run.LastError = "server restarted before research trace completed"
			}
		}
	}
	// v0.8 goal migration: legacy goals had no per-goal schedule. Preserve active
	// autonomy by making them due immediately when global autonomy is enabled.
	for _, g := range s.state.Goals {
		if g.IntervalMinutes <= 0 {
			g.IntervalMinutes = s.state.Config.Autonomy.DefaultGoalIntervalMinutes
		}
		if g.Status == core.GoalActive && s.state.Config.Autonomy.Enabled && g.NextCycleAt.IsZero() {
			g.AutoRun = true
			g.NextCycleAt = time.Now().UTC()
		}
		if s.state.Config.Research.Goal.Enabled && !g.ResearchEnabled {
			g.ResearchEnabled = true
		}
	}
	migrateMemories(s.state.Memories, s.state.Config.Sharding.LocalShardID)
	for _, m := range s.state.Memories {
		if m.VectorDim == 0 && len(m.Vector) > 0 {
			m.VectorDim = len(m.Vector)
		}
	}
	s.rebuildProvenanceSourceIndexLocked()
	if s.segments != nil && !s.segments.HasRecords() && len(s.state.Memories) > 0 {
		for _, m := range s.state.Memories {
			if strings.TrimSpace(m.Text) == "" && len(m.Vector) == 0 {
				return nil, errors.New("memory segment store is empty but checkpoint contains compact memory metadata; restore memory-segments from backup")
			}
		}
		if err := s.segments.Rebuild(s.state.Memories, s.state.Revision); err != nil {
			return nil, fmt.Errorf("migrate memories to segment store: %w", err)
		}
	}
	s.initHotTrackerLocked()
	if s.secrets.ShardAPIToken == nil {
		s.secrets.ShardAPIToken = map[string]string{}
	}
	if s.secrets.AppAPIKey == "" {
		s.secrets.AppAPIKey = randomID(24)
	}
	if s.secrets.WorkerToken == "" {
		s.secrets.WorkerToken = randomID(24)
	}
	if s.secrets.MetricsToken == "" {
		s.secrets.MetricsToken = randomID(24)
	}
	if s.secrets.AdminToken == "" {
		s.secrets.AdminToken = randomID(24)
	}
	if s.secrets.ClusterToken == "" {
		s.secrets.ClusterToken = randomID(24)
	}
	_ = s.loadDiskANNLocked()
	if !s.loadIndexSnapshotLocked() {
		s.rebuildIndexesLocked()
	}
	if err := s.persistSecretsLocked(); err != nil {
		return nil, err
	}
	if err := s.checkpointLocked(); err != nil {
		return nil, err
	}
	if s.state.Config.Storage.Tiering.Enabled && s.segments != nil {
		s.tierMemoryBodiesLocked(time.Now().UTC())
	}
	return s, nil
}

func applyNewDefaults(c *core.Config) {
	d := core.DefaultConfig()
	if c.Routing.ChatProvider == "" {
		c.Routing.ChatProvider = d.Routing.ChatProvider
	}
	if c.Routing.EmbeddingProvider == "" {
		c.Routing.EmbeddingProvider = d.Routing.EmbeddingProvider
	}
	for i := range c.Ollama {
		// v0.7.3: request_timeout_seconds=0 intentionally means no model-inference
		// deadline. Keep network dial protection in the transport instead.
		if strings.TrimSpace(c.Ollama[i].Think) == "" {
			c.Ollama[i].Think = "off"
		}
		if strings.TrimSpace(c.Ollama[i].ChatKeepAlive) == "" {
			c.Ollama[i].ChatKeepAlive = "30m"
		}
		if strings.TrimSpace(c.Ollama[i].EmbeddingKeepAlive) == "" {
			c.Ollama[i].EmbeddingKeepAlive = "5m"
		}
	}
	if c.Brain.TypeWeights == nil {
		c.Brain.TypeWeights = d.Brain.TypeWeights
	}
	if c.Brain.LearningPolicy.MaxMemoryTextChars == 0 && c.Brain.LearningPolicy.DuplicateSimilarity == 0 && c.Brain.LearningPolicy.SemanticMinConfirmations == 0 {
		c.Brain.LearningPolicy = d.Brain.LearningPolicy
	} else {
		if c.Brain.LearningPolicy.MaxMemoryTextChars == 0 {
			c.Brain.LearningPolicy.MaxMemoryTextChars = d.Brain.LearningPolicy.MaxMemoryTextChars
		}
		if c.Brain.LearningPolicy.DuplicateSimilarity == 0 {
			c.Brain.LearningPolicy.DuplicateSimilarity = d.Brain.LearningPolicy.DuplicateSimilarity
		}
		if c.Brain.LearningPolicy.SemanticMinConfirmations == 0 {
			c.Brain.LearningPolicy.SemanticMinConfirmations = d.Brain.LearningPolicy.SemanticMinConfirmations
		}
		if c.Brain.LearningPolicy.SemanticMinConfidence == 0 {
			c.Brain.LearningPolicy.SemanticMinConfidence = d.Brain.LearningPolicy.SemanticMinConfidence
		}
		if c.Brain.LearningPolicy.SourceTrust == nil {
			c.Brain.LearningPolicy.SourceTrust = d.Brain.LearningPolicy.SourceTrust
		}
	}
	if c.Brain.Index.M == 0 && c.Brain.Index.EfConstruction == 0 && c.Brain.Index.EfSearch == 0 {
		c.Brain.Index = d.Brain.Index
	} else {
		if c.Brain.Index.Mode == "" {
			c.Brain.Index.Mode = d.Brain.Index.Mode
		}
		if c.Brain.Index.CandidateScale == 0 {
			c.Brain.Index.CandidateScale = d.Brain.Index.CandidateScale
		}
		if c.Brain.Index.HotMaxItems == 0 {
			c.Brain.Index.HotMaxItems = d.Brain.Index.HotMaxItems
		}
		if c.Brain.Index.DiskPQ.Partitions == 0 {
			c.Brain.Index.DiskPQ = d.Brain.Index.DiskPQ
		} else {
			if c.Brain.Index.DiskPQ.ProbePartitions == 0 {
				c.Brain.Index.DiskPQ.ProbePartitions = d.Brain.Index.DiskPQ.ProbePartitions
			}
			if c.Brain.Index.DiskPQ.Subquantizers == 0 {
				c.Brain.Index.DiskPQ.Subquantizers = d.Brain.Index.DiskPQ.Subquantizers
			}
			if c.Brain.Index.DiskPQ.Centroids == 0 {
				c.Brain.Index.DiskPQ.Centroids = d.Brain.Index.DiskPQ.Centroids
			}
			if c.Brain.Index.DiskPQ.TrainingSamples == 0 {
				c.Brain.Index.DiskPQ.TrainingSamples = d.Brain.Index.DiskPQ.TrainingSamples
			}
			if c.Brain.Index.DiskPQ.KMeansIters == 0 {
				c.Brain.Index.DiskPQ.KMeansIters = d.Brain.Index.DiskPQ.KMeansIters
			}
			if c.Brain.Index.DiskPQ.CandidateScale == 0 {
				c.Brain.Index.DiskPQ.CandidateScale = d.Brain.Index.DiskPQ.CandidateScale
			}
			if c.Brain.Index.DiskPQ.MinMemories == 0 {
				c.Brain.Index.DiskPQ.MinMemories = d.Brain.Index.DiskPQ.MinMemories
			}
			if c.Brain.Index.DiskPQ.RebuildIntervalMinutes == 0 {
				c.Brain.Index.DiskPQ.RebuildIntervalMinutes = d.Brain.Index.DiskPQ.RebuildIntervalMinutes
			}
		}
	}
	if c.Brain.AutoReward.Mode == "" {
		c.Brain.AutoReward = d.Brain.AutoReward
	}
	if c.Brain.Consolidation.IntervalMinutes == 0 && c.Brain.Consolidation.MinEpisodes == 0 {
		c.Brain.Consolidation = d.Brain.Consolidation
	}
	if c.OpenAI.Prices == nil {
		c.OpenAI.Prices = d.OpenAI.Prices
	} else {
		for model, def := range d.OpenAI.Prices {
			p, ok := c.OpenAI.Prices[model]
			if !ok {
				c.OpenAI.Prices[model] = def
				continue
			}
			// v0.3 price records predate long-context tiers. Preserve all
			// configured short-context rates while adding the known tier fields.
			if p.LongContextThresholdTokens == 0 && def.LongContextThresholdTokens > 0 {
				p.LongContextThresholdTokens = def.LongContextThresholdTokens
				p.LongInputPerM = def.LongInputPerM
				p.LongCachedInputPerM = def.LongCachedInputPerM
				p.LongOutputPerM = def.LongOutputPerM
				c.OpenAI.Prices[model] = p
			}
		}
	}
	if c.Sharding.LocalShardID == "" {
		c.Sharding.LocalShardID = d.Sharding.LocalShardID
	}
	if c.Sharding.RequestTimeoutS == 0 {
		c.Sharding.RequestTimeoutS = d.Sharding.RequestTimeoutS
	}
	if c.Storage.CheckpointEvery == 0 && c.Storage.MaxWALSegmentBytes == 0 {
		c.Storage = d.Storage
	} else {
		if c.Storage.CheckpointEvery == 0 {
			c.Storage.CheckpointEvery = d.Storage.CheckpointEvery
		}
		if c.Storage.MaxWALSegmentBytes == 0 {
			c.Storage.MaxWALSegmentBytes = d.Storage.MaxWALSegmentBytes
		}
		// v0.3 had no segment settings. Treat an all-zero nested block as migration.
		if c.Storage.Segments.MaxSegmentBytes == 0 {
			c.Storage.Segments = d.Storage.Segments
		}
		if c.Storage.IndexSegments.BaseEvery == 0 && c.Storage.IndexSegments.MaxDeltas == 0 {
			c.Storage.IndexSegments = d.Storage.IndexSegments
		} else {
			if c.Storage.IndexSegments.BackgroundMergeMinutes == 0 {
				c.Storage.IndexSegments.BackgroundMergeMinutes = d.Storage.IndexSegments.BackgroundMergeMinutes
			}
			if c.Storage.IndexSegments.MergeAtDeltas == 0 {
				c.Storage.IndexSegments.MergeAtDeltas = d.Storage.IndexSegments.MergeAtDeltas
			}
		}
		if c.Storage.PageCache.MaxBytes == 0 {
			c.Storage.PageCache = d.Storage.PageCache
		}
		if strings.TrimSpace(c.Storage.VectorJournal.Compression) == "" {
			c.Storage.VectorJournal = d.Storage.VectorJournal
		} else {
			if c.Storage.VectorJournal.BlockVectors == 0 {
				c.Storage.VectorJournal.BlockVectors = d.Storage.VectorJournal.BlockVectors
			}
			if c.Storage.VectorJournal.MinBlockBytes == 0 {
				c.Storage.VectorJournal.MinBlockBytes = d.Storage.VectorJournal.MinBlockBytes
			}
			if c.Storage.VectorJournal.MinSavingsPct == 0 {
				c.Storage.VectorJournal.MinSavingsPct = d.Storage.VectorJournal.MinSavingsPct
			}
		}
		if c.Storage.Tiering.HotMaxBytes == 0 {
			c.Storage.Tiering = d.Storage.Tiering
		}
	}
	if c.Retention.IntervalMinutes == 0 {
		c.Retention = d.Retention
	}
	if c.Autonomy.IntervalMinutes == 0 {
		c.Autonomy.IntervalMinutes = d.Autonomy.IntervalMinutes
	}
	if c.Autonomy.MaxGoalsPerCycle == 0 {
		c.Autonomy.MaxGoalsPerCycle = d.Autonomy.MaxGoalsPerCycle
	}
	if c.Autonomy.DefaultGoalIntervalMinutes == 0 {
		c.Autonomy.DefaultGoalIntervalMinutes = d.Autonomy.DefaultGoalIntervalMinutes
	}
	if c.Ingestion.ChunkChars == 0 {
		c.Ingestion.ChunkChars = d.Ingestion.ChunkChars
	}
	if c.Ingestion.ChunkOverlap == 0 {
		c.Ingestion.ChunkOverlap = d.Ingestion.ChunkOverlap
	}
	if c.Ingestion.MaxDocumentBytes == 0 {
		c.Ingestion.MaxDocumentBytes = d.Ingestion.MaxDocumentBytes
	}
	if c.Ingestion.MaxChunks == 0 {
		c.Ingestion.MaxChunks = d.Ingestion.MaxChunks
	}
	if c.Research.SearXNG.BaseURL == "" {
		c.Research.SearXNG.BaseURL = d.Research.SearXNG.BaseURL
	}
	if c.Research.SearXNG.Language == "" {
		c.Research.SearXNG.Language = d.Research.SearXNG.Language
	}
	if c.Research.SearXNG.Categories == "" {
		c.Research.SearXNG.Categories = d.Research.SearXNG.Categories
	}
	if c.Research.SearXNG.TimeoutSeconds == 0 {
		c.Research.SearXNG.TimeoutSeconds = d.Research.SearXNG.TimeoutSeconds
	}
	if c.Research.SearXNG.MaxResults == 0 {
		c.Research.SearXNG.MaxResults = d.Research.SearXNG.MaxResults
	}
	if c.Research.WebFetch.TimeoutSeconds == 0 {
		c.Research.WebFetch.TimeoutSeconds = d.Research.WebFetch.TimeoutSeconds
	}
	if c.Research.WebFetch.MaxBytes == 0 {
		c.Research.WebFetch.MaxBytes = d.Research.WebFetch.MaxBytes
	}
	if c.Research.WebFetch.MaxChars == 0 {
		c.Research.WebFetch.MaxChars = d.Research.WebFetch.MaxChars
	}
	if c.Research.WebFetch.UserAgent == "" {
		c.Research.WebFetch.UserAgent = d.Research.WebFetch.UserAgent
	}
	if c.Research.Goal.MaxQueriesPerCycle == 0 {
		c.Research.Goal.MaxQueriesPerCycle = d.Research.Goal.MaxQueriesPerCycle
	}
	if c.Research.Goal.MaxResultsPerQuery == 0 {
		c.Research.Goal.MaxResultsPerQuery = d.Research.Goal.MaxResultsPerQuery
	}
	if c.Research.Goal.MaxPagesPerCycle == 0 {
		c.Research.Goal.MaxPagesPerCycle = d.Research.Goal.MaxPagesPerCycle
	}
	if c.Rebalancing.IntervalMinutes == 0 {
		c.Rebalancing = d.Rebalancing
	}
	if c.HTTP.ReadHeaderTimeoutSeconds == 0 {
		c.HTTP.ReadHeaderTimeoutSeconds = d.HTTP.ReadHeaderTimeoutSeconds
	}
	if c.HTTP.ReadTimeoutSeconds == 0 {
		c.HTTP.ReadTimeoutSeconds = d.HTTP.ReadTimeoutSeconds
	}
	if c.HTTP.WriteTimeoutSeconds == 0 {
		c.HTTP.WriteTimeoutSeconds = d.HTTP.WriteTimeoutSeconds
	}
	if c.HTTP.IdleTimeoutSeconds == 0 {
		c.HTTP.IdleTimeoutSeconds = d.HTTP.IdleTimeoutSeconds
	}
	if c.HTTP.ShutdownTimeoutSeconds == 0 {
		c.HTTP.ShutdownTimeoutSeconds = d.HTTP.ShutdownTimeoutSeconds
	}
	if c.HTTP.MaxHeaderBytes == 0 {
		c.HTTP.MaxHeaderBytes = d.HTTP.MaxHeaderBytes
	}
	if c.HTTP.MaxBodyBytes == 0 || c.HTTP.MaxBodyBytes == 4<<20 {
		c.HTTP.MaxBodyBytes = d.HTTP.MaxBodyBytes
	}
	if c.HTTP.MaxConcurrentRequests == 0 {
		c.HTTP.MaxConcurrentRequests = d.HTTP.MaxConcurrentRequests
	}
	if c.Cluster.NodeID == "" {
		c.Cluster.NodeID = c.Sharding.LocalShardID
		if c.Cluster.NodeID == "" {
			c.Cluster.NodeID = d.Cluster.NodeID
		}
	}
	if c.Cluster.LeaderID == "" {
		c.Cluster.LeaderID = c.Cluster.NodeID
	}
	if c.Cluster.Term == 0 {
		c.Cluster.Term = d.Cluster.Term
	}
	if c.Cluster.RequestTimeoutS == 0 {
		c.Cluster.RequestTimeoutS = d.Cluster.RequestTimeoutS
	}
	if c.Cluster.ElectionMinMS == 0 {
		c.Cluster.ElectionMinMS = d.Cluster.ElectionMinMS
	}
	if c.Cluster.ElectionMaxMS == 0 {
		c.Cluster.ElectionMaxMS = d.Cluster.ElectionMaxMS
	}
	if c.Cluster.HeartbeatMS == 0 {
		c.Cluster.HeartbeatMS = d.Cluster.HeartbeatMS
	}
	if c.Cluster.LogSegmentBytes == 0 {
		c.Cluster.LogSegmentBytes = d.Cluster.LogSegmentBytes
	}
}

func migrateMemories(memories map[string]*core.Memory, localShard string) {
	for _, m := range memories {
		if m.MemoryType == "" {
			m.MemoryType = inferMemoryType(m.Kind)
		}
		if m.ShardID == "" {
			m.ShardID = localShard
		}
		if m.OriginShardID == "" {
			m.OriginShardID = m.ShardID
		}
		if m.Confidence == 0 {
			m.Confidence = 1
		}
		if m.HomeShardID == "" {
			m.HomeShardID = m.ShardID
		}
		if m.Status == "" {
			m.Status = core.MemoryActive
		}
		if m.Version == 0 {
			m.Version = 1
		}
		if m.VectorDim == 0 && len(m.Vector) > 0 {
			m.VectorDim = len(m.Vector)
		}
	}
}

func inferMemoryType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "user", "assistant", "event", "experience", "episode":
		return core.MemoryEpisodic
	case "procedure", "procedural", "rule", "instruction":
		return core.MemoryProcedural
	case "working", "scratch":
		return core.MemoryWorking
	default:
		return core.MemorySemantic
	}
}

func (s *Store) loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeAtomic(path string, perm os.FileMode, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) persistLocked() error {
	return s.checkpointLocked()
}
func (s *Store) persistSecretsLocked() error {
	return writeAtomic(filepath.Join(s.dir, "secrets.json"), 0600, &s.secrets)
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func NewID(prefix string) string { return prefix + "_" + randomID(12) }

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeDiskANNLocked()
	if s.segments != nil {
		s.segments.Close()
	}
	s.clusterLogMu.Lock()
	if s.clusterLog != nil {
		_ = s.clusterLog.Close()
		s.clusterLog = nil
	}
	s.clusterLogMu.Unlock()
	return nil
}

func (s *Store) CompactMemorySegments() (SegmentStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.segments == nil {
		return SegmentStats{}, errors.New("memory segment store is disabled")
	}
	if err := s.segments.Rebuild(s.state.Memories, s.state.Revision); err != nil {
		return SegmentStats{}, err
	}
	return s.segments.Stats(), nil
}

func (s *Store) SegmentStats() SegmentStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.segments == nil {
		return SegmentStats{}
	}
	return s.segments.Stats()
}

func (s *Store) Config() core.Config { s.mu.RLock(); defer s.mu.RUnlock(); return s.state.Config }
func (s *Store) UpdateConfig(c core.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	applyNewDefaults(&c)
	if err := s.validateConfigLocked(c); err != nil {
		return err
	}
	old := s.state.Config
	segmentChanged := old.Storage.Segments.Enabled != c.Storage.Segments.Enabled ||
		old.Storage.Segments.MaxSegmentBytes != c.Storage.Segments.MaxSegmentBytes ||
		old.Storage.Segments.MmapSealed != c.Storage.Segments.MmapSealed
	if segmentChanged {
		if s.segments != nil {
			s.segments.Close()
			s.segments = nil
		}
		if c.Storage.Segments.Enabled {
			seg, err := openSegmentStore(filepath.Join(s.dir, "memory-segments"), c.Storage.Segments.MaxSegmentBytes, c.Storage.Segments.MmapSealed)
			if err != nil {
				return err
			}
			s.segments = seg
			if (!old.Storage.Segments.Enabled || !seg.HasRecords()) && len(s.state.Memories) > 0 {
				if err := seg.Rebuild(s.state.Memories, s.state.Revision); err != nil {
					return err
				}
			}
		}
	}
	if old.Cluster.Enabled != c.Cluster.Enabled || old.Cluster.LogSegmentBytes != c.Cluster.LogSegmentBytes {
		s.clusterLogMu.Lock()
		if s.clusterLog != nil {
			_ = s.clusterLog.Close()
			s.clusterLog = nil
		}
		s.clusterLogMu.Unlock()
	}
	s.state.Config = c
	if s.vectorJournal != nil {
		s.vectorJournal.Configure(vectorJournalOptionsFromConfig(c))
	}
	if indexMode(c) == "hnsw" {
		s.closeDiskANNLocked()
		s.diskANNRevision = 0
		s.diskANNBuiltAt = time.Time{}
	} else if len(s.diskIndexes) == 0 {
		_ = s.loadDiskANNLocked()
	}
	if s.pageCache == nil {
		s.pageCache = newMemoryPageCache(c.Storage.PageCache.Enabled, c.Storage.PageCache.MaxBytes)
	} else {
		s.pageCache.Reconfigure(c.Storage.PageCache.Enabled, c.Storage.PageCache.MaxBytes)
	}
	s.rebuildIndexesLocked()
	return s.commitLocked("config.set", c)
}
func (s *Store) Secrets() core.Secrets {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.secrets
	cp.ShardAPIToken = cloneStringMap(s.secrets.ShardAPIToken)
	return cp
}
func (s *Store) UpdateSecrets(sec core.Secrets) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sec.ShardAPIToken == nil {
		sec.ShardAPIToken = map[string]string{}
	}
	s.secrets = sec
	return s.persistSecretsLocked()
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Store) newIndexLocked() *vector.HNSW {
	c := s.state.Config.Brain.Index
	return vector.NewHNSW(vector.HNSWConfig{M: c.M, EfConstruction: c.EfConstruction, EfSearch: c.EfSearch})
}

func (s *Store) rebuildHotIndexesLocked() {
	s.indexes = map[int]*vector.HNSW{}
	if !s.state.Config.Brain.Index.Enabled || indexMode(s.state.Config) == "disk-pq" {
		return
	}
	type hotCandidate struct {
		id       string
		accessed time.Time
	}
	candidates := make([]hotCandidate, 0)
	for id, m := range s.state.Memories {
		if m == nil || !memorySearchable(m) || len(m.Vector) == 0 {
			continue
		}
		candidates = append(candidates, hotCandidate{id: id, accessed: m.AccessedAt})
	}
	maxHot := s.state.Config.Brain.Index.HotMaxItems
	if maxHot > 0 && len(candidates) > maxHot {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].accessed.After(candidates[j].accessed) })
		candidates = candidates[:maxHot]
	}
	batches := map[int][]vector.HNSWItem{}
	for _, c := range candidates {
		m := s.state.Memories[c.id]
		if m == nil || len(m.Vector) == 0 {
			continue
		}
		dim := len(m.Vector)
		batches[dim] = append(batches[dim], vector.HNSWItem{ID: m.ID, Vector: m.Vector})
	}
	for dim, items := range batches {
		idx := s.newIndexLocked()
		idx.AddBatch(items)
		s.indexes[dim] = idx
	}
}

func (s *Store) rebuildIndexesLocked() {
	s.indexes = map[int]*vector.HNSW{}
	if !s.state.Config.Brain.Index.Enabled {
		return
	}
	mode := indexMode(s.state.Config)
	if mode == "disk-pq" {
		return
	}
	// Once a disk PQ baseline exists, HNSW is deliberately only the hot/delta
	// tier. Until then hybrid mode retains the v0.5 full-HNSW behavior so a new
	// installation never becomes unsearchable while the first PQ build runs.
	if mode == "hybrid" && len(s.diskIndexes) > 0 {
		s.rebuildHotIndexesLocked()
		return
	}
	const buildBatch = 4096
	pending := map[int][]vector.HNSWItem{}
	flush := func(dim int) {
		items := pending[dim]
		if len(items) == 0 {
			return
		}
		idx := s.indexes[dim]
		if idx == nil {
			idx = s.newIndexLocked()
			s.indexes[dim] = idx
		}
		idx.AddBatch(items)
		pending[dim] = pending[dim][:0]
	}
	for id, meta := range s.state.Memories {
		if meta == nil || !memorySearchable(meta) || (meta.VectorDim == 0 && len(meta.Vector) == 0) {
			continue
		}
		m, ok := s.fullMemoryForReadLocked(id)
		if !ok || len(m.Vector) == 0 {
			continue
		}
		dim := len(m.Vector)
		pending[dim] = append(pending[dim], vector.HNSWItem{ID: m.ID, Vector: m.Vector})
		if len(pending[dim]) >= buildBatch {
			flush(dim)
		}
	}
	for dim := range pending {
		flush(dim)
	}
}

func (s *Store) AddMemory(m *core.Memory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ID == "" {
		m.ID = NewID("mem")
	}
	if _, exists := s.state.Memories[m.ID]; exists {
		return fmt.Errorf("memory %s already exists", m.ID)
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
		m.MemoryType = inferMemoryType(m.Kind)
	}
	if m.ShardID == "" {
		m.ShardID = s.state.Config.Sharding.LocalShardID
	}
	if m.OriginShardID == "" {
		m.OriginShardID = m.ShardID
	}
	if m.HomeShardID == "" {
		m.HomeShardID = m.ShardID
	}
	if m.Status == "" {
		m.Status = core.MemoryActive
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.VectorDim == 0 && len(m.Vector) > 0 {
		m.VectorDim = len(m.Vector)
	}
	affected := s.resolveConflictLocked(m)
	stored := cloneMemory(*m)
	s.state.Memories[m.ID] = &stored
	s.indexProvenanceSourceLocked(m.ID, stored.Provenance.Source)
	s.trackHotMemoryLocked(m.ID, &stored)
	if s.state.Config.Brain.Index.Enabled && indexMode(s.state.Config) != "disk-pq" && len(m.Vector) > 0 {
		dim := len(m.Vector)
		idx := s.indexes[dim]
		if idx == nil {
			idx = s.newIndexLocked()
			s.indexes[dim] = idx
		}
		idx.Add(m.ID, m.Vector)
	}
	if s.vectorJournal != nil && len(m.Vector) > 0 {
		if err := s.vectorJournal.AppendNew(s.state.Revision+1, []core.Memory{cloneMemory(*m)}); err != nil {
			return err
		}
	}
	affected = append(affected, cloneMemory(*m))
	return s.commitLocked("memory.upsert", affected)
}

func (s *Store) GetMemory(id string) (*core.Memory, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.fullMemoryForReadLocked(id)
	if !ok {
		return nil, false
	}
	cp := cloneMemory(m)
	return &cp, true
}

func (s *Store) fullMemoryForReadLocked(id string) (core.Memory, bool) {
	meta := s.state.Memories[id]
	if meta == nil {
		return core.Memory{}, false
	}
	if meta.Text != "" || len(meta.Vector) > 0 || s.segments == nil {
		return cloneMemory(*meta), true
	}
	if s.pageCache != nil {
		if m, ok := s.pageCache.Get(id); ok {
			return m, true
		}
	}
	m, found, deleted, err := s.segments.Get(id)
	if err != nil || !found || deleted {
		return cloneMemory(*meta), true
	}
	if s.pageCache != nil {
		s.pageCache.Put(m)
	}
	return m, true
}

func cloneMemory(m core.Memory) core.Memory {
	m.Vector = append([]float32(nil), m.Vector...)
	m.Tags = append([]string(nil), m.Tags...)
	m.ConsolidatedFrom = append([]string(nil), m.ConsolidatedFrom...)
	m.Supersedes = append([]string(nil), m.Supersedes...)
	m.EvidenceSourceIDs = append([]string(nil), m.EvidenceSourceIDs...)
	return m
}

type SearchHit struct {
	Memory           core.Memory `json:"memory"`
	Similarity       float64     `json:"similarity"`
	BaseScore        float64     `json:"base_score"`
	GraphBoost       float64     `json:"graph_boost"`
	Score            float64     `json:"score"`
	TypeWeight       float64     `json:"type_weight"`
	SalienceFactor   float64     `json:"salience_factor"`
	ConfidenceFactor float64     `json:"confidence_factor"`
	CandidateSource  string      `json:"candidate_source"`
}

func (s *Store) SearchVector(q []float32, k int, min float64, graphBonus float64) []SearchHit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.searchVectorLocked(q, k, min, graphBonus)
}

func (s *Store) searchVectorLocked(q []float32, k int, min float64, graphBonus float64) []SearchHit {
	if k <= 0 || len(q) == 0 {
		return nil
	}
	candidateIDs := make([]string, 0)
	candidateSource := map[string]string{}
	addCandidate := func(id, source string) {
		if id == "" {
			return
		}
		candidateIDs = append(candidateIDs, id)
		if old := candidateSource[id]; old == "" {
			candidateSource[id] = source
		} else if old != source && !strings.Contains(old, source) {
			candidateSource[id] = old + "+" + source
		}
	}
	cfg := s.state.Config
	if cfg.Brain.Index.Enabled {
		if idx := s.indexes[len(q)]; idx != nil {
			scale := cfg.Brain.Index.CandidateScale
			if scale < 1 {
				scale = 4
			}
			want := k * scale
			if want < cfg.Brain.Index.EfSearch {
				want = cfg.Brain.Index.EfSearch
			}
			for _, h := range idx.Search(q, want) {
				addCandidate(h.ID, "hnsw")
			}
		}
		if pq := s.diskIndexes[len(q)]; pq != nil {
			scale := cfg.Brain.Index.DiskPQ.CandidateScale
			if scale < 1 {
				scale = 12
			}
			want := k * scale
			if want < 32 {
				want = 32
			}
			for _, h := range pq.Search(q, want) {
				addCandidate(h.ID, "disk-pq")
			}
		}
	}
	if len(candidateIDs) == 0 {
		candidateIDs = make([]string, 0, len(s.state.Memories))
		for id, m := range s.state.Memories {
			dim := m.VectorDim
			if dim == 0 {
				dim = len(m.Vector)
			}
			if dim == len(q) {
				addCandidate(id, "scan")
			}
		}
	}

	hits := make([]SearchHit, 0, len(candidateIDs))
	seen := map[string]bool{}
	for _, id := range candidateIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		meta := s.state.Memories[id]
		if meta == nil || !memorySearchable(meta) {
			continue
		}
		m, ok := s.fullMemoryForReadLocked(id)
		if !ok || len(m.Vector) != len(q) {
			continue
		}
		sim := vector.Cosine(q, m.Vector)
		if sim < min {
			continue
		}
		typeWeight := 1.0
		if w, ok := cfg.Brain.TypeWeights[m.MemoryType]; ok && w > 0 {
			typeWeight = w
		}
		confidence := m.Confidence
		if confidence <= 0 {
			confidence = 1
		}
		salienceFactor := 0.75 + 0.25*m.Salience
		confidenceFactor := 0.85 + 0.15*confidence
		baseScore := sim * salienceFactor * typeWeight * confidenceFactor
		hits = append(hits, SearchHit{Memory: cloneMemory(m), Similarity: sim, BaseScore: baseScore, Score: baseScore, TypeWeight: typeWeight, SalienceFactor: salienceFactor, ConfidenceFactor: confidenceFactor, CandidateSource: candidateSource[id]})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	if graphBonus > 0 && len(hits) > 0 {
		base := map[string]float64{}
		for _, h := range hits {
			base[h.Memory.ID] = h.Score
		}
		for _, syn := range s.state.Synapses {
			var to string
			if _, ok := base[syn.A]; ok {
				to = syn.B
			} else if _, ok := base[syn.B]; ok {
				to = syn.A
			} else {
				continue
			}
			meta, ok := s.state.Memories[to]
			if !ok || !memorySearchable(meta) {
				continue
			}
			m, fullOK := s.fullMemoryForReadLocked(to)
			if !fullOK || len(m.Vector) != len(q) {
				continue
			}
			bonus := graphBonus * syn.Weight
			found := false
			for i := range hits {
				if hits[i].Memory.ID == to {
					hits[i].GraphBoost += bonus
					hits[i].Score += bonus
					found = true
					break
				}
			}
			if !found && len(hits) < k {
				sim := vector.Cosine(q, m.Vector)
				if sim >= min {
					baseScore := sim * 0.5
					hits = append(hits, SearchHit{Memory: cloneMemory(m), Similarity: sim, BaseScore: baseScore, GraphBoost: bonus, Score: bonus + baseScore, TypeWeight: 1, SalienceFactor: 1, ConfidenceFactor: 1, CandidateSource: "synapse"})
				}
			}
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
		if len(hits) > k {
			hits = hits[:k]
		}
	}
	return hits
}

func edgeKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (s *Store) Reinforce(a, b string, similarity, delta, decayPerDay, maxWeight float64) error {
	if a == "" || b == "" || a == b {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Memories[a] == nil || s.state.Memories[b] == nil {
		return nil
	}
	key := edgeKey(a, b)
	now := time.Now().UTC()
	syn, ok := s.state.Synapses[key]
	if !ok {
		syn = &core.Synapse{A: a, B: b, Similarity: similarity, LastUpdated: now}
		s.state.Synapses[key] = syn
	}
	days := now.Sub(syn.LastUpdated).Hours() / 24
	if days > 0 && decayPerDay > 0 {
		syn.Weight *= pow(1-decayPerDay, days)
	}
	syn.Weight = vector.Clamp(syn.Weight+delta, -maxWeight, maxWeight)
	if similarity > syn.Similarity {
		syn.Similarity = similarity
	}
	syn.Activations++
	syn.LastUpdated = now
	return s.commitLocked("synapse.upsert", *syn)
}

func pow(base, exp float64) float64 {
	if base <= 0 {
		return 0
	}
	return math.Pow(base, exp)
}

func (s *Store) DecayAndPruneSynapses(decayPerDay, pruneBelow float64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	pruned := 0
	for key, syn := range s.state.Synapses {
		days := now.Sub(syn.LastUpdated).Hours() / 24
		if days > 0 && decayPerDay > 0 {
			syn.Weight *= pow(1-decayPerDay, days)
			syn.LastUpdated = now
		}
		if pruneBelow > 0 && math.Abs(syn.Weight) < pruneBelow {
			delete(s.state.Synapses, key)
			pruned++
		}
	}
	items := make([]core.Synapse, 0, len(s.state.Synapses))
	for _, syn := range s.state.Synapses {
		items = append(items, *syn)
	}
	return pruned, s.commitLocked("synapse.replace", items)
}

func (s *Store) Touch(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	changed := make([]core.Memory, 0, len(ids))
	for _, id := range ids {
		if _, exists := s.state.Memories[id]; !exists {
			continue
		}
		m, ok := s.materializeMemoryLocked(id)
		if !ok {
			continue
		}
		m.AccessedAt = now
		m.AccessCount++
		s.trackHotMemoryLocked(id, m)
		changed = append(changed, cloneMemory(*m))
	}
	if len(changed) == 0 {
		return nil
	}
	return s.commitLocked("memory.upsert", changed)
}

func (s *Store) CorroborateMemory(id, sourceID string, evidenceConfidence float64) (bool, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(sourceID) == "" {
		return false, errors.New("memory id and source id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.materializeMemoryLocked(id)
	if !ok {
		return false, errors.New("memory not found")
	}
	for _, sid := range m.EvidenceSourceIDs {
		if sid == sourceID {
			return false, nil
		}
	}
	m.EvidenceSourceIDs = append(m.EvidenceSourceIDs, sourceID)
	m.EvidenceCount = len(m.EvidenceSourceIDs)
	if m.EvidenceCount < 1 {
		m.EvidenceCount = 1
	}
	// Independent corroboration closes part of the remaining confidence gap
	// without allowing one extra source to jump straight to 1.0.
	ev := vector.Clamp(evidenceConfidence, 0, 1)
	m.Confidence = vector.Clamp(1-(1-vector.Clamp(m.Confidence, 0, 1))*(1-0.35*ev), 0, 1)
	m.Salience = math.Min(2.5, m.Salience+0.04*ev)
	return true, s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*m)})
}

func (s *Store) SetMemoryReward(id string, reward float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.materializeMemoryLocked(id)
	if !ok {
		return errors.New("memory not found")
	}
	m.Reward = vector.Clamp(reward, -1, 1)
	return s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*m)})
}

func (s *Store) SetMemoryStatus(id, status string) error {
	if status != core.MemoryActive && status != core.MemorySuperseded && status != core.MemoryConflicted && status != core.MemoryArchived {
		return errors.New("invalid memory status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.materializeMemoryLocked(id)
	if !ok {
		return errors.New("memory not found")
	}
	m.Status = status
	return s.commitLocked("memory.upsert", []core.Memory{cloneMemory(*m)})
}

func (s *Store) MarkConsolidated(sourceIDs []string, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := make([]core.Memory, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		m, ok := s.materializeMemoryLocked(id)
		if !ok {
			continue
		}
		m.ConsolidatedInto = targetID
		m.ConsolidationCount++
		changed = append(changed, cloneMemory(*m))
	}
	if len(changed) == 0 {
		return nil
	}
	return s.commitLocked("memory.upsert", changed)
}

func (s *Store) MemoriesSnapshot() []core.Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Memory, 0, len(s.state.Memories))
	for id := range s.state.Memories {
		if m, ok := s.fullMemoryForReadLocked(id); ok {
			out = append(out, cloneMemory(m))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) SynapsesSnapshot() []core.Synapse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Synapse, 0, len(s.state.Synapses))
	for _, x := range s.state.Synapses {
		out = append(out, *x)
	}
	return out
}

func (s *Store) MaintenanceStatus() core.MaintenanceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Maintenance
}

func (s *Store) UpdateMaintenance(status core.MaintenanceStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Maintenance = status
	return s.commitLocked("maintenance.set", status)
}

func (s *Store) Stats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pending := 0
	for _, j := range s.state.Jobs {
		if j.Status == "queued" || j.Status == "claimed" {
			pending++
		}
	}
	types := map[string]int{}
	indexNodes := 0
	for _, m := range s.state.Memories {
		types[m.MemoryType]++
	}
	for _, idx := range s.indexes {
		indexNodes += idx.Len()
	}
	remoteEnabled := 0
	for _, sh := range s.state.Config.Sharding.Remote {
		if sh.Enabled {
			remoteEnabled++
		}
	}
	statuses := map[string]int{}
	for _, m := range s.state.Memories {
		statuses[m.Status]++
	}
	var segmentStats SegmentStats
	if s.segments != nil {
		segmentStats = s.segments.Stats()
	}
	pqItems := 0
	var pqBytes int64
	for _, idx := range s.diskIndexes {
		pqItems += idx.Len()
		pqBytes += idx.DiskBytes()
	}
	return map[string]any{
		"revision": s.state.Revision, "memories": len(s.state.Memories), "memory_types": types, "memory_statuses": statuses, "synapses": len(s.state.Synapses),
		"goals": len(s.state.Goals), "learning_cycles": len(s.state.Cycles),
		"usage_events": len(s.state.Usage), "pending_jobs": pending, "hnsw_nodes": indexNodes,
		"hnsw_dimensions": len(s.indexes), "disk_pq_items": pqItems, "disk_pq_bytes": pqBytes, "index_mode": indexMode(s.state.Config),
		"remote_shards": remoteEnabled, "maintenance": s.state.Maintenance,
		"memory_segments": segmentStats, "cluster": s.state.Cluster,
	}
}

func (s *Store) AddUsage(e core.UsageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = NewID("use")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	s.state.Usage = append(s.state.Usage, e)
	if len(s.state.Usage) > 100000 {
		s.state.Usage = s.state.Usage[len(s.state.Usage)-100000:]
	}
	return s.commitLocked("usage.add", e)
}

func (s *Store) UsageTotals(now time.Time) (daily, monthly float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	y, m, d := now.Date()
	for _, e := range s.state.Usage {
		ey, em, ed := e.CreatedAt.In(now.Location()).Date()
		if ey == y && em == m {
			monthly += e.CostUSD
			if ed == d {
				daily += e.CostUSD
			}
		}
	}
	return
}

func (s *Store) RecentUsage(limit int) []core.UsageEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.state.Usage) {
		limit = len(s.state.Usage)
	}
	out := append([]core.UsageEvent(nil), s.state.Usage[len(s.state.Usage)-limit:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (s *Store) EnqueueJob(kind string, payload any) (*core.Job, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	j := &core.Job{ID: NewID("job"), Type: kind, Payload: b, Status: "queued", CreatedAt: now, UpdatedAt: now}
	s.state.Jobs[j.ID] = j
	return j, s.commitLocked("job.upsert", *j)
}

func (s *Store) ClaimJob(worker string, lease time.Duration) (*core.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var chosen *core.Job
	for _, j := range s.state.Jobs {
		if j.Status == "claimed" && !j.LeaseUntil.IsZero() && now.After(j.LeaseUntil) {
			j.Status = "queued"
			j.ClaimedBy = ""
		}
		if j.Status == "queued" && (chosen == nil || j.CreatedAt.Before(chosen.CreatedAt)) {
			chosen = j
		}
	}
	if chosen == nil {
		return nil, nil
	}
	chosen.Status = "claimed"
	chosen.ClaimedBy = worker
	chosen.LeaseUntil = now.Add(lease)
	chosen.UpdatedAt = now
	cp := *chosen
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) CompleteJob(id, worker string, result json.RawMessage, jobErr string) (*core.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.state.Jobs[id]
	if !ok {
		return nil, errors.New("job not found")
	}
	if j.ClaimedBy != worker {
		return nil, errors.New("job claimed by another worker")
	}
	j.Result = result
	j.Error = jobErr
	j.UpdatedAt = time.Now().UTC()
	if jobErr != "" {
		j.Status = "failed"
	} else {
		j.Status = "done"
	}
	cp := *j
	return &cp, s.commitLocked("job.upsert", cp)
}

func (s *Store) DeleteMemory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.state.Memories[id]; old != nil {
		s.unindexProvenanceSourceLocked(id, old.Provenance.Source)
	}
	delete(s.state.Memories, id)
	s.untrackHotMemoryLocked(id)
	if s.pageCache != nil {
		s.pageCache.Delete(id)
	}
	for k, x := range s.state.Synapses {
		if x.A == id || x.B == id {
			delete(s.state.Synapses, k)
		}
	}
	s.rebuildIndexesLocked()
	return s.commitLocked("memory.delete", []string{id})
}

func (s *Store) ExportSafe() core.PersistedState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.state)
	var cp core.PersistedState
	_ = json.Unmarshal(b, &cp)
	if cp.Memories == nil {
		cp.Memories = map[string]*core.Memory{}
	}
	for id := range s.state.Memories {
		if m, ok := s.fullMemoryForReadLocked(id); ok {
			mm := cloneMemory(m)
			cp.Memories[id] = &mm
		}
	}
	return cp
}

func (s *Store) ValidateConfig(c core.Config) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validateConfigLocked(c)
}

func (s *Store) validateConfigLocked(c core.Config) error {
	if c.Brain.RecallK < 1 || c.Brain.RecallK > 100 {
		return errors.New("brain.recall_k must be 1..100")
	}
	if c.Brain.MinSimilarity < -1 || c.Brain.MinSimilarity > 1 {
		return errors.New("brain.min_similarity must be -1..1")
	}
	if c.Brain.Index.Enabled {
		mode := indexMode(c)
		if c.Brain.Index.Mode != "" && c.Brain.Index.Mode != "hnsw" && c.Brain.Index.Mode != "hybrid" && c.Brain.Index.Mode != "disk-pq" {
			return errors.New("brain.index.mode must be hnsw, hybrid, or disk-pq")
		}
		if mode != "disk-pq" {
			if c.Brain.Index.M < 2 || c.Brain.Index.M > 128 {
				return errors.New("brain.index.m must be 2..128")
			}
			if c.Brain.Index.EfConstruction < c.Brain.Index.M || c.Brain.Index.EfConstruction > 5000 {
				return errors.New("brain.index.ef_construction must be >= m and <= 5000")
			}
			if c.Brain.Index.EfSearch < 1 || c.Brain.Index.EfSearch > 5000 {
				return errors.New("brain.index.ef_search must be 1..5000")
			}
		}
		if c.Brain.Index.HotMaxItems < 0 {
			return errors.New("brain.index.hot_max_items must be >= 0")
		}
		p := c.Brain.Index.DiskPQ
		if p.Partitions < 2 || p.Partitions > 4096 {
			return errors.New("brain.index.disk_pq.partitions must be 2..4096")
		}
		if p.ProbePartitions < 1 || p.ProbePartitions > p.Partitions {
			return errors.New("brain.index.disk_pq.probe_partitions must be 1..partitions")
		}
		if p.Subquantizers < 1 || p.Subquantizers > 256 {
			return errors.New("brain.index.disk_pq.subquantizers must be 1..256")
		}
		if p.Centroids < 2 || p.Centroids > 256 {
			return errors.New("brain.index.disk_pq.centroids must be 2..256")
		}
		if p.TrainingSamples < p.Centroids*2 || p.TrainingSamples > 1000000 {
			return errors.New("brain.index.disk_pq.training_samples must be >= 2*centroids and <= 1000000")
		}
		if p.KMeansIters < 1 || p.KMeansIters > 50 {
			return errors.New("brain.index.disk_pq.kmeans_iters must be 1..50")
		}
		if p.BuildWorkers < 0 || p.BuildWorkers > 64 {
			return errors.New("brain.index.disk_pq.build_workers must be 0..64")
		}
		if p.CandidateScale < 1 || p.CandidateScale > 100 {
			return errors.New("brain.index.disk_pq.candidate_scale must be 1..100")
		}
		if p.MinMemories < 0 || p.RebuildIntervalMinutes < 1 {
			return errors.New("invalid brain.index.disk_pq maintenance configuration")
		}
	}
	if c.Brain.AutoReward.Mode != "" && c.Brain.AutoReward.Mode != "vector" && c.Brain.AutoReward.Mode != "llm" {
		return errors.New("brain.auto_reward.mode must be vector or llm")
	}
	lp := c.Brain.LearningPolicy
	if lp.MinConfidence < 0 || lp.MinConfidence > 1 || lp.DuplicateSimilarity < -1 || lp.DuplicateSimilarity > 1 || lp.SemanticMinConfidence < 0 || lp.SemanticMinConfidence > 1 {
		return errors.New("invalid brain.learning_policy confidence/similarity values")
	}
	if lp.SemanticMinConfirmations < 2 || lp.SemanticMinConfirmations > 100 || lp.MaxMemoryTextChars < 256 || lp.MaxMemoryTextChars > 10_000_000 {
		return errors.New("invalid brain.learning_policy confirmations or max_memory_text_chars")
	}
	if lp.NegativeArchiveThreshold < -1 || lp.NegativeArchiveThreshold > 0 {
		return errors.New("brain.learning_policy.negative_archive_threshold must be -1..0")
	}
	for source, trust := range lp.SourceTrust {
		if strings.TrimSpace(source) == "" || trust < 0 || trust > 1 {
			return fmt.Errorf("brain.learning_policy.source_trust[%q] must be 0..1", source)
		}
	}
	if c.Brain.Consolidation.MinEpisodes < 2 || c.Brain.Consolidation.MaxClusterSize < c.Brain.Consolidation.MinEpisodes {
		return errors.New("invalid consolidation cluster sizes")
	}
	if c.Brain.Consolidation.SimilarityThreshold < -1 || c.Brain.Consolidation.SimilarityThreshold > 1 {
		return errors.New("consolidation similarity_threshold must be -1..1")
	}
	if c.Storage.CheckpointEvery < 1 || c.Storage.CheckpointEvery > 1000000 {
		return errors.New("storage.checkpoint_every must be 1..1000000")
	}
	if c.Storage.MaxWALSegmentBytes < 1<<20 {
		return errors.New("storage.max_wal_segment_bytes must be at least 1 MiB")
	}
	if c.Storage.Segments.Enabled {
		if c.Storage.Segments.MaxSegmentBytes < 1<<20 {
			return errors.New("storage.segments.max_segment_bytes must be at least 1 MiB")
		}
		if c.Storage.Segments.CompactTombstonePct < 0 || c.Storage.Segments.CompactTombstonePct > 1 {
			return errors.New("storage.segments.compact_tombstone_pct must be 0..1")
		}
	}
	if c.Storage.IndexSegments.Enabled && (c.Storage.IndexSegments.BaseEvery < 1 || c.Storage.IndexSegments.MaxDeltas < 1 || c.Storage.IndexSegments.BackgroundMergeMinutes < 1 || c.Storage.IndexSegments.MergeAtDeltas < 1) {
		return errors.New("storage.index_segments values must be positive")
	}
	if c.Storage.PageCache.Enabled && c.Storage.PageCache.MaxBytes < 1<<20 {
		return errors.New("storage.page_cache.max_bytes must be at least 1 MiB")
	}
	if c.Storage.VectorJournal.Compression != "off" && c.Storage.VectorJournal.Compression != "sqar-auto" {
		return errors.New("storage.vector_journal.compression must be off or sqar-auto")
	}
	if c.Storage.VectorJournal.BlockVectors < 1 || c.Storage.VectorJournal.BlockVectors > 4096 {
		return errors.New("storage.vector_journal.block_vectors must be 1..4096")
	}
	if c.Storage.VectorJournal.MinBlockBytes < 0 || c.Storage.VectorJournal.MinBlockBytes > 128<<20 {
		return errors.New("storage.vector_journal.min_block_bytes must be 0..128 MiB")
	}
	if c.Storage.VectorJournal.MinSavingsPct < 0 || c.Storage.VectorJournal.MinSavingsPct > 0.5 {
		return errors.New("storage.vector_journal.min_savings_pct must be 0..0.5")
	}
	if c.Storage.Tiering.Enabled && (c.Storage.Tiering.HotMaxBytes < 1<<20 || c.Storage.Tiering.HotAgeMinutes < 1 || c.Storage.Tiering.IntervalMinutes < 1) {
		return errors.New("invalid storage.tiering configuration")
	}
	if c.Retention.IntervalMinutes < 1 || c.Retention.MinAgeDays < 0 || c.Retention.WorkingTTLHours < 0 || c.Retention.MinUtility < 0 || c.Retention.MinUtility > 1 {
		return errors.New("invalid retention configuration")
	}
	if c.Autonomy.IntervalMinutes < 1 || c.Autonomy.MaxGoalsPerCycle < 1 || c.Autonomy.MaxGoalsPerCycle > 100 || c.Autonomy.DefaultGoalIntervalMinutes < 1 || c.Autonomy.DefaultGoalIntervalMinutes > 10080 {
		return errors.New("invalid autonomy configuration")
	}
	if c.Ingestion.ChunkChars < 256 || c.Ingestion.ChunkChars > 100000 || c.Ingestion.ChunkOverlap < 0 || c.Ingestion.ChunkOverlap >= c.Ingestion.ChunkChars || c.Ingestion.MaxDocumentBytes < 64<<10 || c.Ingestion.MaxDocumentBytes > 256<<20 || c.Ingestion.MaxChunks < 1 || c.Ingestion.MaxChunks > 100000 {
		return errors.New("invalid ingestion configuration")
	}
	if c.Research.SearXNG.SafeSearch < 0 || c.Research.SearXNG.SafeSearch > 2 || c.Research.SearXNG.TimeoutSeconds < 1 || c.Research.SearXNG.TimeoutSeconds > 300 || c.Research.SearXNG.MaxResults < 1 || c.Research.SearXNG.MaxResults > 100 {
		return errors.New("invalid research.searxng configuration")
	}
	if c.Research.Enabled && c.Research.SearXNG.Enabled {
		raw := strings.TrimSpace(c.Research.SearXNG.BaseURL)
		if raw == "" {
			return errors.New("research.searxng.base_url is required when SearXNG research is enabled")
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return errors.New("research.searxng.base_url must be an absolute http(s) URL without embedded credentials")
		}
	}
	if c.Research.WebFetch.TimeoutSeconds < 1 || c.Research.WebFetch.TimeoutSeconds > 300 || c.Research.WebFetch.MaxBytes < 64<<10 || c.Research.WebFetch.MaxBytes > 64<<20 || c.Research.WebFetch.MaxChars < 1000 || c.Research.WebFetch.MaxChars > 5_000_000 {
		return errors.New("invalid research.web_fetch configuration")
	}
	if c.Research.Goal.MaxQueriesPerCycle < 1 || c.Research.Goal.MaxQueriesPerCycle > 20 || c.Research.Goal.MaxResultsPerQuery < 1 || c.Research.Goal.MaxResultsPerQuery > 50 || c.Research.Goal.MaxPagesPerCycle < 0 || c.Research.Goal.MaxPagesPerCycle > 50 {
		return errors.New("invalid research.goal configuration")
	}
	if c.Rebalancing.IntervalMinutes < 1 || c.Rebalancing.MaxPerCycle < 1 || (c.Rebalancing.Mode != "replicate" && c.Rebalancing.Mode != "move") {
		return errors.New("rebalancing.mode must be replicate or move and intervals/limits must be positive")
	}
	if c.HTTP.ReadHeaderTimeoutSeconds < 1 || c.HTTP.ReadHeaderTimeoutSeconds > 120 || c.HTTP.ReadTimeoutSeconds < 1 || c.HTTP.ReadTimeoutSeconds > 600 || c.HTTP.WriteTimeoutSeconds < 0 || c.HTTP.WriteTimeoutSeconds > 86400 || c.HTTP.IdleTimeoutSeconds < 1 || c.HTTP.IdleTimeoutSeconds > 1800 || c.HTTP.ShutdownTimeoutSeconds < 1 || c.HTTP.ShutdownTimeoutSeconds > 300 {
		return errors.New("http timeout values are outside safe bounds (write_timeout_seconds may be 0 for unlimited)")
	}
	if c.HTTP.MaxHeaderBytes < 16<<10 || c.HTTP.MaxHeaderBytes > 16<<20 {
		return errors.New("http.max_header_bytes must be 16 KiB..16 MiB")
	}
	if c.HTTP.MaxBodyBytes < 64<<10 || c.HTTP.MaxBodyBytes > 128<<20 {
		return errors.New("http.max_body_bytes must be 64 KiB..128 MiB")
	}
	if c.HTTP.MaxConcurrentRequests < 1 || c.HTTP.MaxConcurrentRequests > 10000 {
		return errors.New("http.max_concurrent_requests must be 1..10000")
	}
	if c.Cluster.Enabled {
		if strings.TrimSpace(c.Cluster.NodeID) == "" {
			return errors.New("cluster.node_id is required")
		}
		if !c.Cluster.AutoElection && strings.TrimSpace(c.Cluster.LeaderID) == "" {
			return errors.New("cluster.leader_id is required when auto_election is disabled")
		}
		if c.Cluster.Term == 0 || c.Cluster.RequestTimeoutS < 1 {
			return errors.New("cluster.term and request_timeout_seconds must be positive")
		}
		if c.Cluster.LogSegmentBytes < 1<<20 {
			return errors.New("cluster.log_segment_bytes must be at least 1 MiB")
		}
		if c.Cluster.AutoElection && (c.Cluster.ElectionMinMS < 200 || c.Cluster.ElectionMaxMS <= c.Cluster.ElectionMinMS || c.Cluster.HeartbeatMS < 50 || c.Cluster.HeartbeatMS >= c.Cluster.ElectionMinMS) {
			return errors.New("invalid cluster election/heartbeat timings")
		}
		peerIDs := map[string]bool{}
		voters := 1
		leaderConfigured := c.Cluster.AutoElection || c.Cluster.LeaderID == c.Cluster.NodeID
		for i, p := range c.Cluster.Peers {
			if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.BaseURL) == "" {
				return fmt.Errorf("cluster.peers[%d] requires id and base_url", i)
			}
			if p.ID == c.Cluster.NodeID {
				return fmt.Errorf("cluster peer %q conflicts with local node id", p.ID)
			}
			if peerIDs[p.ID] {
				return fmt.Errorf("duplicate cluster peer id %q", p.ID)
			}
			peerIDs[p.ID] = true
			if p.Enabled && p.Voting {
				voters++
			}
			if p.Enabled && p.ID == c.Cluster.LeaderID {
				leaderConfigured = true
			}
		}
		if !leaderConfigured {
			return fmt.Errorf("cluster leader %q must be the local node or an enabled peer", c.Cluster.LeaderID)
		}
		if c.Cluster.Quorum < 0 || c.Cluster.Quorum > voters {
			return fmt.Errorf("cluster.quorum must be 0 (automatic) or <= %d voters", voters)
		}
		if !c.Cluster.AutoElection && c.Cluster.Term < s.state.Cluster.Term {
			return fmt.Errorf("cluster.term %d cannot be lower than persisted term %d", c.Cluster.Term, s.state.Cluster.Term)
		}
	}
	if c.OpenAI.DailyBudgetUSD < 0 || c.OpenAI.MonthlyBudgetUSD < 0 {
		return errors.New("budgets must be >= 0")
	}
	validProvider := func(v string) bool { return v == "" || v == "auto" || v == "ollama" || v == "openai" }
	if !validProvider(c.Routing.ChatProvider) {
		return errors.New("routing.chat_provider must be auto, ollama or openai")
	}
	if !validProvider(c.Routing.EmbeddingProvider) {
		return errors.New("routing.embedding_provider must be auto, ollama or openai")
	}
	for name, route := range map[string]core.ModelRoute{
		"critic": c.Routing.Critic, "consolidator": c.Routing.Consolidator, "goal": c.Routing.Goal,
	} {
		if !validProvider(route.Provider) {
			return fmt.Errorf("routing.%s.provider must be auto, ollama or openai", name)
		}
	}
	ollamaIDs := map[string]bool{}
	for i, o := range c.Ollama {
		if strings.TrimSpace(o.ID) == "" {
			return fmt.Errorf("ollama[%d].id is required", i)
		}
		if strings.TrimSpace(o.BaseURL) == "" {
			return fmt.Errorf("ollama[%d].base_url is required", i)
		}
		if ollamaIDs[o.ID] {
			return fmt.Errorf("duplicate ollama id %q", o.ID)
		}
		ollamaIDs[o.ID] = true
		if o.Weight < 0 {
			return fmt.Errorf("ollama[%d].weight must be >= 0", i)
		}
		if o.RequestTimeoutSeconds < 0 || o.RequestTimeoutSeconds > 86400 {
			return fmt.Errorf("ollama[%d].request_timeout_seconds must be 0 (unlimited) or 1..86400", i)
		}
		if o.NumCtx < 0 || o.NumCtx > 2_000_000 {
			return fmt.Errorf("ollama[%d].num_ctx must be 0..2000000", i)
		}
		if o.NumPredict < 0 || o.NumPredict > 1_000_000 {
			return fmt.Errorf("ollama[%d].num_predict must be 0..1000000", i)
		}
		think := strings.ToLower(strings.TrimSpace(o.Think))
		if think != "" && think != "off" && think != "on" && think != "low" && think != "medium" && think != "high" && think != "max" && think != "false" && think != "true" {
			return fmt.Errorf("ollama[%d].think must be off, on, low, medium, high or max", i)
		}
		if len(o.ChatKeepAlive) > 64 || len(o.EmbeddingKeepAlive) > 64 {
			return fmt.Errorf("ollama[%d] keep_alive values are too long", i)
		}
	}
	checkNode := func(field, id string) error {
		if id != "" && !ollamaIDs[id] {
			return fmt.Errorf("%s references unknown Ollama node %q", field, id)
		}
		return nil
	}
	if err := checkNode("routing.chat_node_id", c.Routing.ChatNodeID); err != nil {
		return err
	}
	if err := checkNode("routing.embedding_node_id", c.Routing.EmbeddingNodeID); err != nil {
		return err
	}
	if err := checkNode("routing.critic.node_id", c.Routing.Critic.NodeID); err != nil {
		return err
	}
	if err := checkNode("routing.consolidator.node_id", c.Routing.Consolidator.NodeID); err != nil {
		return err
	}
	if err := checkNode("routing.goal.node_id", c.Routing.Goal.NodeID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, sh := range c.Sharding.Remote {
		if strings.TrimSpace(sh.ID) == "" || strings.TrimSpace(sh.BaseURL) == "" {
			return fmt.Errorf("sharding.remote[%d] requires id and base_url", i)
		}
		if sh.ID == c.Sharding.LocalShardID {
			return fmt.Errorf("remote shard %q conflicts with local shard id", sh.ID)
		}
		if seen[sh.ID] {
			return fmt.Errorf("duplicate remote shard id %q", sh.ID)
		}
		seen[sh.ID] = true
	}
	return nil
}

// SearchVectorByProvenanceSource performs an ANN-first lookup constrained to one
// provenance source. It is a compatibility wrapper around the multi-source path.
func (s *Store) SearchVectorByProvenanceSource(q []float32, k int, min float64, graphBonus float64, source string) []SearchHit {
	return s.SearchVectorByProvenanceSources(q, k, min, graphBonus, source)
}

// SearchVectorByProvenanceSources performs one ANN pass for a set of allowed
// provenance sources and only falls back to the rebuildable per-source ID index
// when ANN did not produce enough matching items. This avoids repeating the
// global ANN search for small trusted source sets such as accepted+corrected
// helpdesk outcomes.
func (s *Store) SearchVectorByProvenanceSources(q []float32, k int, min float64, graphBonus float64, sources ...string) []SearchHit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if k <= 0 || len(q) == 0 || len(sources) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source != "" {
			allowed[source] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	want := k * 32
	if want < 256 {
		want = 256
	}
	if want > 5000 {
		want = 5000
	}
	candidates := s.searchVectorLocked(q, want, min, graphBonus)
	out := make([]SearchHit, 0, k)
	seen := map[string]bool{}
	for _, h := range candidates {
		if _, ok := allowed[h.Memory.Provenance.Source]; !ok {
			continue
		}
		out = append(out, h)
		seen[h.Memory.ID] = true
		if len(out) >= k {
			return out
		}
	}

	cfg := s.state.Config
	for source := range allowed {
		ids := s.provenanceSourceIDs[source]
		for id := range ids {
			meta := s.state.Memories[id]
			if seen[id] || meta == nil || !memorySearchable(meta) {
				continue
			}
			m, ok := s.fullMemoryForReadLocked(id)
			if !ok || len(m.Vector) != len(q) {
				continue
			}
			sim := vector.Cosine(q, m.Vector)
			if sim < min {
				continue
			}
			typeWeight := 1.0
			if w, ok := cfg.Brain.TypeWeights[m.MemoryType]; ok && w > 0 {
				typeWeight = w
			}
			confidence := m.Confidence
			if confidence <= 0 {
				confidence = 1
			}
			salienceFactor := 0.75 + 0.25*m.Salience
			confidenceFactor := 0.85 + 0.15*confidence
			baseScore := sim * salienceFactor * typeWeight * confidenceFactor
			out = append(out, SearchHit{Memory: cloneMemory(m), Similarity: sim, BaseScore: baseScore, Score: baseScore, TypeWeight: typeWeight, SalienceFactor: salienceFactor, ConfidenceFactor: confidenceFactor, CandidateSource: "namespace-scan"})
			seen[id] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

package core

import (
	"encoding/json"
	"time"
)

const (
	MemoryEpisodic   = "episodic"
	MemorySemantic   = "semantic"
	MemoryProcedural = "procedural"
	MemoryWorking    = "working"
)

const (
	MemoryActive     = "active"
	MemorySuperseded = "superseded"
	MemoryConflicted = "conflicted"
	MemoryArchived   = "archived"
)

const (
	GoalActive    = "active"
	GoalPaused    = "paused"
	GoalCompleted = "completed"
	GoalFailed    = "failed"
)

type ModelPrice struct {
	InputPerM                  float64 `json:"input_per_m"`
	CachedInputPerM            float64 `json:"cached_input_per_m"`
	OutputPerM                 float64 `json:"output_per_m"`
	EmbeddingInputPerM         float64 `json:"embedding_input_per_m"`
	LongContextThresholdTokens int64   `json:"long_context_threshold_tokens,omitempty"`
	LongInputPerM              float64 `json:"long_input_per_m,omitempty"`
	LongCachedInputPerM        float64 `json:"long_cached_input_per_m,omitempty"`
	LongOutputPerM             float64 `json:"long_output_per_m,omitempty"`
}

type OllamaServer struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	BaseURL               string `json:"base_url"`
	ChatModel             string `json:"chat_model"`
	EmbeddingModel        string `json:"embedding_model"`
	Weight                int    `json:"weight"`
	Enabled               bool   `json:"enabled"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"` // 0 = no inference deadline
	NumCtx                int    `json:"num_ctx"`                 // 0 = Ollama/model default
	NumPredict            int    `json:"num_predict"`             // 0 = inherit caller/global max output
	Think                 string `json:"think"`                   // off/on/low/medium/high/max
	ChatKeepAlive         string `json:"chat_keep_alive"`         // e.g. 30m; empty = Ollama default
	EmbeddingKeepAlive    string `json:"embedding_keep_alive"`    // e.g. 5m or 0 to unload immediately
}

// ModelRoute pins a logical model role to a provider and, optionally, to one
// concrete Ollama node. Empty fields inherit the surrounding/default route.
type ModelRoute struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	NodeID   string `json:"node_id,omitempty"`
}

// RoutingConfig keeps the simple v0.1-style provider switches compatible while
// allowing operators to bind expensive model roles to dedicated Ollama nodes.
type RoutingConfig struct {
	ChatProvider      string `json:"chat_provider"`
	EmbeddingProvider string `json:"embedding_provider"`
	ChatModel         string `json:"chat_model,omitempty"`
	EmbeddingModel    string `json:"embedding_model,omitempty"`
	ChatNodeID        string `json:"chat_node_id,omitempty"`
	EmbeddingNodeID   string `json:"embedding_node_id,omitempty"`

	Critic       ModelRoute `json:"critic,omitempty"`
	Consolidator ModelRoute `json:"consolidator,omitempty"`
	Goal         ModelRoute `json:"goal,omitempty"`
}

type LearningPolicyConfig struct {
	Enabled                  bool               `json:"enabled"`
	LearnChatInputs          bool               `json:"learn_chat_inputs"`
	LearnChatResponses       bool               `json:"learn_chat_responses"`
	AllowExplicitLearn       bool               `json:"allow_explicit_learn"`
	AllowImports             bool               `json:"allow_imports"`
	LearnGoalCycles          bool               `json:"learn_goal_cycles"`
	MinConfidence            float64            `json:"min_confidence"`
	DuplicateSimilarity      float64            `json:"duplicate_similarity"`
	SemanticMinConfirmations int                `json:"semantic_min_confirmations"`
	SemanticMinConfidence    float64            `json:"semantic_min_confidence"`
	ArchiveNegativeResponses bool               `json:"archive_negative_responses"`
	NegativeArchiveThreshold float64            `json:"negative_archive_threshold"`
	MaxMemoryTextChars       int                `json:"max_memory_text_chars"`
	SourceTrust              map[string]float64 `json:"source_trust"`
}

type MemoryShard struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Enabled   bool   `json:"enabled"`
	Search    bool   `json:"search"`
	Replicate bool   `json:"replicate"`
	Weight    int    `json:"weight"`
}

type ClusterPeer struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
	Voting  bool   `json:"voting"`
}

type ClusterState struct {
	Term          uint64    `json:"term"`
	LastIndex     uint64    `json:"last_index"`
	CommitIndex   uint64    `json:"commit_index"`
	LastCommit    time.Time `json:"last_commit,omitempty"`
	Role          string    `json:"role,omitempty"`
	LeaderID      string    `json:"leader_id,omitempty"`
	VotedFor      string    `json:"voted_for,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
}

type ClusterEntry struct {
	ID        string          `json:"id"`
	Term      uint64          `json:"term"`
	Index     uint64          `json:"index"`
	LeaderID  string          `json:"leader_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type ClusterVoteRequest struct {
	Term         uint64 `json:"term"`
	CandidateID  string `json:"candidate_id"`
	LastLogIndex uint64 `json:"last_log_index"`
}

type ClusterVoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"vote_granted"`
	VoterID     string `json:"voter_id"`
}

type ClusterHeartbeat struct {
	Term        uint64 `json:"term"`
	LeaderID    string `json:"leader_id"`
	CommitIndex uint64 `json:"commit_index"`
	LastIndex   uint64 `json:"last_index"`
}

type ClusterHeartbeatResponse struct {
	Term        uint64 `json:"term"`
	Accepted    bool   `json:"accepted"`
	NodeID      string `json:"node_id"`
	LastIndex   uint64 `json:"last_index"`
	CommitIndex uint64 `json:"commit_index"`
}

type Config struct {
	Listen string `json:"listen"`

	Routing RoutingConfig `json:"routing"`

	Brain struct {
		RecallK              int                `json:"recall_k"`
		MinSimilarity        float64            `json:"min_similarity"`
		LearningRate         float64            `json:"learning_rate"`
		CoactivationReward   float64            `json:"coactivation_reward"`
		FeedbackRewardScale  float64            `json:"feedback_reward_scale"`
		DecayPerDay          float64            `json:"decay_per_day"`
		MaxSynapseWeight     float64            `json:"max_synapse_weight"`
		GraphBonus           float64            `json:"graph_bonus"`
		GraphMaxHops         int                `json:"graph_max_hops"`
		GraphHopDecay        float64            `json:"graph_hop_decay"`
		GraphMaxExpansion    int                `json:"graph_max_expansion"`
		GraphMinEdgeWeight   float64            `json:"graph_min_edge_weight"`
		MaxContextMemories   int                `json:"max_context_memories"`
		AutoLearn            bool               `json:"auto_learn"`
		ExternalRelinkWorker bool               `json:"external_relink_worker"`
		TypeWeights          map[string]float64 `json:"type_weights"`

		LearningPolicy LearningPolicyConfig `json:"learning_policy"`

		Index struct {
			Enabled        bool   `json:"enabled"`
			Mode           string `json:"mode"`
			M              int    `json:"m"`
			EfConstruction int    `json:"ef_construction"`
			EfSearch       int    `json:"ef_search"`
			CandidateScale int    `json:"candidate_scale"`
			HotMaxItems    int    `json:"hot_max_items"`

			DiskPQ struct {
				Partitions             int `json:"partitions"`
				ProbePartitions        int `json:"probe_partitions"`
				Subquantizers          int `json:"subquantizers"`
				Centroids              int `json:"centroids"`
				TrainingSamples        int `json:"training_samples"`
				KMeansIters            int `json:"kmeans_iters"`
				BuildWorkers           int `json:"build_workers"`
				CandidateScale         int `json:"candidate_scale"`
				MinMemories            int `json:"min_memories"`
				RebuildIntervalMinutes int `json:"rebuild_interval_minutes"`
			} `json:"disk_pq"`
		} `json:"index"`

		AutoReward struct {
			Enabled  bool    `json:"enabled"`
			Mode     string  `json:"mode"`
			Provider string  `json:"provider"`
			Model    string  `json:"model"`
			Scale    float64 `json:"scale"`
		} `json:"auto_reward"`

		Consolidation struct {
			Enabled             bool    `json:"enabled"`
			IntervalMinutes     int     `json:"interval_minutes"`
			MinEpisodes         int     `json:"min_episodes"`
			MaxClusterSize      int     `json:"max_cluster_size"`
			MinAccessCount      int64   `json:"min_access_count"`
			SimilarityThreshold float64 `json:"similarity_threshold"`
			MaxPerCycle         int     `json:"max_per_cycle"`
			UseLLM              bool    `json:"use_llm"`
			Provider            string  `json:"provider"`
			Model               string  `json:"model"`
			SynapsePruneBelow   float64 `json:"synapse_prune_below"`
		} `json:"consolidation"`
	} `json:"brain"`

	OpenAI struct {
		Enabled          bool                  `json:"enabled"`
		BaseURL          string                `json:"base_url"`
		ChatModel        string                `json:"chat_model"`
		EmbeddingModel   string                `json:"embedding_model"`
		MaxOutputTokens  int                   `json:"max_output_tokens"`
		DailyBudgetUSD   float64               `json:"daily_budget_usd"`
		MonthlyBudgetUSD float64               `json:"monthly_budget_usd"`
		Prices           map[string]ModelPrice `json:"prices"`
	} `json:"openai"`

	Ollama []OllamaServer `json:"ollama"`

	Sharding struct {
		Enabled         bool          `json:"enabled"`
		LocalShardID    string        `json:"local_shard_id"`
		RequestTimeoutS int           `json:"request_timeout_seconds"`
		Remote          []MemoryShard `json:"remote"`
	} `json:"sharding"`

	Storage struct {
		CheckpointEvery    int   `json:"checkpoint_every"`
		WALSync            bool  `json:"wal_sync"`
		IndexSnapshot      bool  `json:"index_snapshot"`
		MaxWALSegmentBytes int64 `json:"max_wal_segment_bytes"`

		Segments struct {
			Enabled             bool    `json:"enabled"`
			MaxSegmentBytes     int64   `json:"max_segment_bytes"`
			MmapSealed          bool    `json:"mmap_sealed"`
			CompactTombstonePct float64 `json:"compact_tombstone_pct"`
		} `json:"segments"`

		IndexSegments struct {
			Enabled                bool `json:"enabled"`
			BaseEvery              int  `json:"base_every"`
			MaxDeltas              int  `json:"max_deltas"`
			BackgroundMergeMinutes int  `json:"background_merge_minutes"`
			MergeAtDeltas          int  `json:"merge_at_deltas"`
		} `json:"index_segments"`

		PageCache struct {
			Enabled  bool  `json:"enabled"`
			MaxBytes int64 `json:"max_bytes"`
		} `json:"page_cache"`

		VectorJournal struct {
			Compression   string  `json:"compression"`
			BlockVectors  int     `json:"block_vectors"`
			MinBlockBytes int     `json:"min_block_bytes"`
			MinSavingsPct float64 `json:"min_savings_pct"`
		} `json:"vector_journal"`

		Tiering struct {
			Enabled         bool  `json:"enabled"`
			HotMaxBytes     int64 `json:"hot_max_bytes"`
			HotAgeMinutes   int   `json:"hot_age_minutes"`
			IntervalMinutes int   `json:"interval_minutes"`
		} `json:"tiering"`
	} `json:"storage"`

	Retention struct {
		Enabled            bool    `json:"enabled"`
		IntervalMinutes    int     `json:"interval_minutes"`
		MinAgeDays         float64 `json:"min_age_days"`
		WorkingTTLHours    float64 `json:"working_ttl_hours"`
		MinUtility         float64 `json:"min_utility"`
		MaxMemories        int     `json:"max_memories"`
		CompressChars      int     `json:"compress_chars"`
		DeleteConsolidated bool    `json:"delete_consolidated"`
	} `json:"retention"`

	Autonomy struct {
		Enabled                    bool   `json:"enabled"`
		IntervalMinutes            int    `json:"interval_minutes"`
		MaxGoalsPerCycle           int    `json:"max_goals_per_cycle"`
		UseLLM                     bool   `json:"use_llm"`
		Provider                   string `json:"provider"`
		Model                      string `json:"model"`
		RunOnGoalCreate            bool   `json:"run_on_goal_create"`
		DefaultGoalIntervalMinutes int    `json:"default_goal_interval_minutes"`
	} `json:"autonomy"`

	Ingestion struct {
		ChunkChars       int   `json:"chunk_chars"`
		ChunkOverlap     int   `json:"chunk_overlap"`
		MaxDocumentBytes int64 `json:"max_document_bytes"`
		MaxChunks        int   `json:"max_chunks"`
		StoreOriginal    bool  `json:"store_original"`
	} `json:"ingestion"`

	Research struct {
		Enabled bool `json:"enabled"`
		SearXNG struct {
			Enabled        bool   `json:"enabled"`
			BaseURL        string `json:"base_url"`
			Language       string `json:"language"`
			Categories     string `json:"categories"`
			SafeSearch     int    `json:"safe_search"`
			TimeoutSeconds int    `json:"timeout_seconds"`
			MaxResults     int    `json:"max_results"`
		} `json:"searxng"`
		WebFetch struct {
			Enabled             bool   `json:"enabled"`
			TimeoutSeconds      int    `json:"timeout_seconds"`
			MaxBytes            int64  `json:"max_bytes"`
			MaxChars            int    `json:"max_chars"`
			UserAgent           string `json:"user_agent"`
			AllowPrivateTargets bool   `json:"allow_private_targets"`
		} `json:"web_fetch"`
		Goal struct {
			Enabled            bool `json:"enabled"`
			SearchEveryCycle   bool `json:"search_every_cycle"`
			MaxQueriesPerCycle int  `json:"max_queries_per_cycle"`
			MaxResultsPerQuery int  `json:"max_results_per_query"`
			MaxPagesPerCycle   int  `json:"max_pages_per_cycle"`
		} `json:"goal"`
	} `json:"research"`

	Rebalancing struct {
		Enabled         bool   `json:"enabled"`
		IntervalMinutes int    `json:"interval_minutes"`
		MaxPerCycle     int    `json:"max_per_cycle"`
		Mode            string `json:"mode"`
	} `json:"rebalancing"`

	HTTP struct {
		ReadHeaderTimeoutSeconds int   `json:"read_header_timeout_seconds"`
		ReadTimeoutSeconds       int   `json:"read_timeout_seconds"`
		WriteTimeoutSeconds      int   `json:"write_timeout_seconds"`
		IdleTimeoutSeconds       int   `json:"idle_timeout_seconds"`
		ShutdownTimeoutSeconds   int   `json:"shutdown_timeout_seconds"`
		MaxHeaderBytes           int   `json:"max_header_bytes"`
		MaxBodyBytes             int64 `json:"max_body_bytes"`
		MaxConcurrentRequests    int   `json:"max_concurrent_requests"`
	} `json:"http"`

	Security struct {
		SecureHeaders     bool `json:"secure_headers"`
		AllowSecretReveal bool `json:"allow_secret_reveal"`
	} `json:"security"`

	Cluster struct {
		Enabled         bool          `json:"enabled"`
		NodeID          string        `json:"node_id"`
		LeaderID        string        `json:"leader_id"`
		Term            uint64        `json:"term"`
		Quorum          int           `json:"quorum"`
		RequestTimeoutS int           `json:"request_timeout_seconds"`
		AutoElection    bool          `json:"auto_election"`
		ElectionMinMS   int           `json:"election_min_ms"`
		ElectionMaxMS   int           `json:"election_max_ms"`
		HeartbeatMS     int           `json:"heartbeat_ms"`
		LogSegmentBytes int64         `json:"log_segment_bytes"`
		Peers           []ClusterPeer `json:"peers"`
	} `json:"cluster"`

	API struct {
		RequireKey bool `json:"require_key"`
	} `json:"api"`

	Worker struct {
		LeaseSeconds              int  `json:"lease_seconds"`
		HeartbeatSeconds          int  `json:"heartbeat_seconds"`
		StaleAfterSeconds         int  `json:"stale_after_seconds"`
		DefaultMaxAttempts        int  `json:"default_max_attempts"`
		RetryBackoffSeconds       int  `json:"retry_backoff_seconds"`
		MaxQueuedJobs             int  `json:"max_queued_jobs"`
		MaxQueuedPayloadMB        int  `json:"max_queued_payload_mb"`
		MasterApplyMaxAttempts    int  `json:"master_apply_max_attempts"`
		MasterApplyBackoffSeconds int  `json:"master_apply_backoff_seconds"`
		JobRetentionHours         int  `json:"job_retention_hours"`
		MaxTerminalJobs           int  `json:"max_terminal_jobs"`
		GraphBackfillEnabled      bool `json:"graph_backfill_enabled"`
		GraphBackfillIntervalS    int  `json:"graph_backfill_interval_seconds"`
		GraphBackfillBatchSize    int  `json:"graph_backfill_batch_size"`
		GraphBackfillMaxQueued    int  `json:"graph_backfill_max_queued"`
		GraphBackfillMinDegree    int  `json:"graph_backfill_min_degree"`
		GraphCandidateMultiplier  int  `json:"graph_candidate_multiplier"`
		GraphRetryAfterMinutes    int  `json:"graph_retry_after_minutes"`
		RequireWorkerForGraph     bool `json:"require_worker_for_graph"`
		OffloadChat               bool `json:"offload_chat"`
		OffloadEmbeddings         bool `json:"offload_embeddings"`
		DistributedInferenceWaitS int  `json:"distributed_inference_wait_seconds"`
	} `json:"worker"`
}

type Secrets struct {
	OpenAIAPIKey      string            `json:"openai_api_key"`
	AppAPIKey         string            `json:"app_api_key"`
	IntegrationToken  string            `json:"integration_token,omitempty"`
	ControlReadToken  string            `json:"control_read_token,omitempty"`
	WorkerToken       string            `json:"worker_token"`
	AdminToken        string            `json:"admin_token"`
	MetricsToken      string            `json:"metrics_token,omitempty"`
	ShardAPIToken     map[string]string `json:"shard_api_tokens,omitempty"`
	ClusterToken      string            `json:"cluster_token,omitempty"`
	SearXNGAuthHeader string            `json:"searxng_auth_header,omitempty"`
}

type MemoryProvenance struct {
	Source             string    `json:"source,omitempty"`
	Actor              string    `json:"actor,omitempty"`
	EmbeddingProvider  string    `json:"embedding_provider,omitempty"`
	EmbeddingModel     string    `json:"embedding_model,omitempty"`
	EmbeddingNodeID    string    `json:"embedding_node_id,omitempty"`
	GenerationProvider string    `json:"generation_provider,omitempty"`
	GenerationModel    string    `json:"generation_model,omitempty"`
	GenerationNodeID   string    `json:"generation_node_id,omitempty"`
	GoalID             string    `json:"goal_id,omitempty"`
	SourceMemoryID     string    `json:"source_memory_id,omitempty"`
	SourceID           string    `json:"source_id,omitempty"`
	SourceURI          string    `json:"source_uri,omitempty"`
	SourceTitle        string    `json:"source_title,omitempty"`
	ChunkIndex         int       `json:"chunk_index,omitempty"`
	ChunkCount         int       `json:"chunk_count,omitempty"`
	ContentHash        string    `json:"content_hash,omitempty"`
	RetrievedAt        time.Time `json:"retrieved_at,omitempty"`
	Note               string    `json:"note,omitempty"`
}

type KnowledgeEvent struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	MemoryID   string            `json:"memory_id,omitempty"`
	RelatedIDs []string          `json:"related_ids,omitempty"`
	Summary    string            `json:"summary"`
	Reason     string            `json:"reason,omitempty"`
	Actor      string            `json:"actor,omitempty"`
	Model      string            `json:"model,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

type Memory struct {
	ID                 string           `json:"id"`
	Kind               string           `json:"kind"`
	MemoryType         string           `json:"memory_type"`
	Text               string           `json:"text"`
	Vector             []float32        `json:"vector,omitempty"`
	VectorDim          int              `json:"vector_dim,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	SessionID          string           `json:"session_id,omitempty"`
	ParentID           string           `json:"parent_id,omitempty"`
	ShardID            string           `json:"shard_id,omitempty"`
	OriginShardID      string           `json:"origin_shard_id,omitempty"`
	HomeShardID        string           `json:"home_shard_id,omitempty"`
	TruthKey           string           `json:"truth_key,omitempty"`
	Version            int64            `json:"version,omitempty"`
	Status             string           `json:"status,omitempty"`
	ConflictGroup      string           `json:"conflict_group,omitempty"`
	Supersedes         []string         `json:"supersedes,omitempty"`
	Compressed         bool             `json:"compressed,omitempty"`
	Salience           float64          `json:"salience"`
	Confidence         float64          `json:"confidence,omitempty"`
	Reward             float64          `json:"reward,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	AccessedAt         time.Time        `json:"accessed_at"`
	AccessCount        int64            `json:"access_count"`
	ConsolidatedFrom   []string         `json:"consolidated_from,omitempty"`
	ConsolidatedInto   string           `json:"consolidated_into,omitempty"`
	ConsolidationCount int              `json:"consolidation_count,omitempty"`
	EvidenceSourceIDs  []string         `json:"evidence_source_ids,omitempty"`
	EvidenceCount      int              `json:"evidence_count,omitempty"`
	GraphLinkedAt      time.Time        `json:"graph_linked_at,omitempty"`
	GraphDegree        int              `json:"graph_degree,omitempty"`
	GraphVersion       int64            `json:"graph_version,omitempty"`
	Provenance         MemoryProvenance `json:"provenance,omitempty"`
}

type Synapse struct {
	A           string    `json:"a"`
	B           string    `json:"b"`
	Weight      float64   `json:"weight"`
	Similarity  float64   `json:"similarity"`
	Relations   []string  `json:"relations,omitempty"`
	Activations int64     `json:"activations"`
	LastUpdated time.Time `json:"last_updated"`
}

type UsageEvent struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Category     string    `json:"category"`
	InputTokens  int64     `json:"input_tokens"`
	CachedTokens int64     `json:"cached_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	CreatedAt    time.Time `json:"created_at"`
}

type Job struct {
	ID                   string          `json:"id"`
	Type                 string          `json:"type"`
	Payload              json.RawMessage `json:"payload"`
	Result               json.RawMessage `json:"result,omitempty"`
	Status               string          `json:"status"`
	Priority             int             `json:"priority,omitempty"`
	ResourceClass        string          `json:"resource_class,omitempty"`
	RequiredCapabilities []string        `json:"required_capabilities,omitempty"`
	IdempotencyKey       string          `json:"idempotency_key,omitempty"`
	ParentJobID          string          `json:"parent_job_id,omitempty"`
	DependsOn            []string        `json:"depends_on,omitempty"`
	Attempts             int             `json:"attempts,omitempty"`
	MaxAttempts          int             `json:"max_attempts,omitempty"`
	BackoffSeconds       int             `json:"backoff_seconds,omitempty"`
	TimeoutSeconds       int             `json:"timeout_seconds,omitempty"`
	RequiresMasterApply  bool            `json:"requires_master_apply,omitempty"`
	ApplyAttempts        int             `json:"apply_attempts,omitempty"`
	MaxApplyAttempts     int             `json:"max_apply_attempts,omitempty"`
	ApplyBackoffSeconds  int             `json:"apply_backoff_seconds,omitempty"`
	ApplyNextAttemptAt   time.Time       `json:"apply_next_attempt_at,omitempty"`
	ApplyError           string          `json:"apply_error,omitempty"`
	ClaimedBy            string          `json:"claimed_by,omitempty"`
	LeaseToken           string          `json:"lease_token,omitempty"`
	LeaseUntil           time.Time       `json:"lease_until,omitempty"`
	NextAttemptAt        time.Time       `json:"next_attempt_at,omitempty"`
	StartedAt            time.Time       `json:"started_at,omitempty"`
	FinishedAt           time.Time       `json:"finished_at,omitempty"`
	Error                string          `json:"error,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type WorkerState struct {
	ID             string            `json:"id"`
	ResourceClass  string            `json:"resource_class"`
	Capabilities   []string          `json:"capabilities"`
	Labels         map[string]string `json:"labels,omitempty"`
	MaxConcurrency int               `json:"max_concurrency"`
	Inflight       int               `json:"inflight"`
	Version        string            `json:"version,omitempty"`
	Hostname       string            `json:"hostname,omitempty"`
	LastHeartbeat  time.Time         `json:"last_heartbeat"`
	RegisteredAt   time.Time         `json:"registered_at"`
	Status         string            `json:"status"`
}

type MaintenanceStatus struct {
	LastRun              time.Time `json:"last_run,omitempty"`
	LastConsolidationRun time.Time `json:"last_consolidation_run,omitempty"`
	LastRetentionRun     time.Time `json:"last_retention_run,omitempty"`
	LastAutonomyRun      time.Time `json:"last_autonomy_run,omitempty"`
	LastRebalanceRun     time.Time `json:"last_rebalance_run,omitempty"`
	LastConsolidated     int       `json:"last_consolidated"`
	TotalConsolidated    int64     `json:"total_consolidated"`
	LastPrunedSynapses   int       `json:"last_pruned_synapses"`
	LastForgotten        int       `json:"last_forgotten"`
	TotalForgotten       int64     `json:"total_forgotten"`
	LastAutonomyCycles   int       `json:"last_autonomy_cycles"`
	LastRebalanced       int       `json:"last_rebalanced"`
	LastError            string    `json:"last_error,omitempty"`
}

type Goal struct {
	ID                          string    `json:"id"`
	Title                       string    `json:"title"`
	Description                 string    `json:"description"`
	Status                      string    `json:"status"`
	Priority                    int       `json:"priority"`
	Progress                    float64   `json:"progress"`
	Target                      string    `json:"target,omitempty"`
	Prediction                  string    `json:"prediction,omitempty"`
	NextAction                  string    `json:"next_action,omitempty"`
	LastEvaluation              float64   `json:"last_evaluation,omitempty"`
	ProgressReason              string    `json:"progress_reason,omitempty"`
	ResearchEvidence            int       `json:"research_evidence,omitempty"`
	ResearchSources             int       `json:"research_sources,omitempty"`
	ResearchCorroborations      int       `json:"research_corroborations,omitempty"`
	ResearchSourceIDs           []string  `json:"research_source_ids,omitempty"`
	StagingDraftsCreated        int       `json:"staging_drafts_created,omitempty"`
	StagingDraftsUpdated        int       `json:"staging_drafts_updated,omitempty"`
	LastStagingDraftID          string    `json:"last_staging_draft_id,omitempty"`
	LastStagingError            string    `json:"last_staging_error,omitempty"`
	StagingDraftValidated       bool      `json:"staging_draft_validated,omitempty"`
	StagingQualityGateVersion   string    `json:"staging_quality_gate_version,omitempty"`
	LastStagingAttemptSignature string    `json:"last_staging_attempt_signature,omitempty"`
	MemoryIDs                   []string  `json:"memory_ids,omitempty"`
	Tags                        []string  `json:"tags,omitempty"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
	LastCycleAt                 time.Time `json:"last_cycle_at,omitempty"`
	AutoRun                     bool      `json:"auto_run"`
	IntervalMinutes             int       `json:"interval_minutes,omitempty"`
	NextCycleAt                 time.Time `json:"next_cycle_at,omitempty"`
	ResearchEnabled             bool      `json:"research_enabled"`
	ConsecutiveErrors           int       `json:"consecutive_errors,omitempty"`
	LastError                   string    `json:"last_error,omitempty"`
}

type LearningCycle struct {
	ID              string    `json:"id"`
	GoalID          string    `json:"goal_id"`
	Observation     string    `json:"observation"`
	Prediction      string    `json:"prediction"`
	Evaluation      float64   `json:"evaluation"`
	Learning        string    `json:"learning"`
	MemoryID        string    `json:"memory_id,omitempty"`
	CostUSD         float64   `json:"cost_usd"`
	CreatedAt       time.Time `json:"created_at"`
	ResearchRunID   string    `json:"research_run_id,omitempty"`
	ResearchQueries []string  `json:"research_queries,omitempty"`
	SourcesFound    int       `json:"sources_found,omitempty"`
	SourcesIngested int       `json:"sources_ingested,omitempty"`
	ResearchErrors  []string  `json:"research_errors,omitempty"`
}

type ResearchRunStats struct {
	Queries            int `json:"queries"`
	Results            int `json:"results"`
	DownloadsStarted   int `json:"downloads_started"`
	DownloadsCompleted int `json:"downloads_completed"`
	Pages              int `json:"pages"`
	Documents          int `json:"documents"`
	Claims             int `json:"claims"`
	NewEvidence        int `json:"new_evidence"`
	Duplicates         int `json:"duplicates"`
	Corroborations     int `json:"corroborations"`
	RejectedSources    int `json:"rejected_sources"`
	SkippedEvidence    int `json:"skipped_evidence"`
	Errors             int `json:"errors"`
}

// ResearchEvent is a bounded, source-safe trace event for one autonomous goal
// research run. Preview intentionally contains only a short excerpt; full source
// bodies remain in the source/memory stores and are loaded on demand.
type ResearchEvent struct {
	Seq        uint64            `json:"seq"`
	ID         string            `json:"id"`
	RunID      string            `json:"run_id"`
	GoalID     string            `json:"goal_id"`
	Type       string            `json:"type"`
	Phase      string            `json:"phase,omitempty"`
	Status     string            `json:"status,omitempty"`
	Query      string            `json:"query,omitempty"`
	URL        string            `json:"url,omitempty"`
	Title      string            `json:"title,omitempty"`
	SourceID   string            `json:"source_id,omitempty"`
	MemoryID   string            `json:"memory_id,omitempty"`
	Message    string            `json:"message,omitempty"`
	Preview    string            `json:"preview,omitempty"`
	Score      float64           `json:"score,omitempty"`
	Similarity float64           `json:"similarity,omitempty"`
	Confidence float64           `json:"confidence,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

// ResearchRun is the persisted live/audit view for a goal research cycle. Event
// history is deliberately bounded by the store so UI polling stays O(1) in the
// total knowledge size.
type ResearchRun struct {
	ID          string           `json:"id"`
	GoalID      string           `json:"goal_id"`
	GoalTitle   string           `json:"goal_title,omitempty"`
	Status      string           `json:"status"`
	StartedAt   time.Time        `json:"started_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
	Queries     []string         `json:"queries,omitempty"`
	Stats       ResearchRunStats `json:"stats"`
	LastSeq     uint64           `json:"last_seq"`
	LastError   string           `json:"last_error,omitempty"`
	Events      []ResearchEvent  `json:"events,omitempty"`
}

type KnowledgeSource struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	URI        string    `json:"uri,omitempty"`
	FileName   string    `json:"file_name,omitempty"`
	MIME       string    `json:"mime,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	Trust      float64   `json:"trust"`
	Status     string    `json:"status"`
	ChunkCount int       `json:"chunk_count"`
	MemoryIDs  []string  `json:"memory_ids,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type MemoryCatalogState struct {
	SegmentBacked bool   `json:"segment_backed"`
	Count         int    `json:"count"`
	Revision      uint64 `json:"revision"`
}

type PersistedState struct {
	Revision        uint64                      `json:"revision"`
	Config          Config                      `json:"config"`
	Memories        map[string]*Memory          `json:"memories,omitempty"`
	MemoryCatalog   MemoryCatalogState          `json:"memory_catalog,omitempty"`
	Synapses        map[string]*Synapse         `json:"synapses"`
	Usage           []UsageEvent                `json:"usage"`
	Jobs            map[string]*Job             `json:"jobs"`
	Goals           map[string]*Goal            `json:"goals"`
	Sources         map[string]*KnowledgeSource `json:"sources,omitempty"`
	ResearchRuns    map[string]*ResearchRun     `json:"research_runs,omitempty"`
	Cycles          []LearningCycle             `json:"cycles,omitempty"`
	KnowledgeEvents []KnowledgeEvent            `json:"knowledge_events,omitempty"`
	Maintenance     MaintenanceStatus           `json:"maintenance"`
	Cluster         ClusterState                `json:"cluster"`
}

func DefaultConfig() Config {
	var c Config
	c.Listen = ":8080"
	c.Routing.ChatProvider = "auto"
	c.Routing.EmbeddingProvider = "auto"
	c.Brain.RecallK = 8
	c.Brain.MinSimilarity = 0.35
	c.Brain.LearningRate = 0.18
	c.Brain.CoactivationReward = 0.10
	c.Brain.FeedbackRewardScale = 0.50
	c.Brain.DecayPerDay = 0.01
	c.Brain.MaxSynapseWeight = 4.0
	c.Brain.GraphBonus = 0.15
	c.Brain.GraphMaxHops = 3
	c.Brain.GraphHopDecay = 0.60
	c.Brain.GraphMaxExpansion = 64
	c.Brain.GraphMinEdgeWeight = 0.05
	c.Brain.MaxContextMemories = 8
	c.Brain.AutoLearn = true
	c.Brain.ExternalRelinkWorker = true
	c.Brain.TypeWeights = map[string]float64{
		MemoryEpisodic: 1.0, MemorySemantic: 1.15, MemoryProcedural: 1.20, MemoryWorking: 0.85,
	}
	c.Brain.LearningPolicy.Enabled = true
	c.Brain.LearningPolicy.LearnChatInputs = true
	c.Brain.LearningPolicy.LearnChatResponses = true
	c.Brain.LearningPolicy.AllowExplicitLearn = true
	c.Brain.LearningPolicy.AllowImports = true
	c.Brain.LearningPolicy.LearnGoalCycles = true
	c.Brain.LearningPolicy.MinConfidence = 0.20
	c.Brain.LearningPolicy.DuplicateSimilarity = 0.985
	c.Brain.LearningPolicy.SemanticMinConfirmations = 3
	c.Brain.LearningPolicy.SemanticMinConfidence = 0.55
	c.Brain.LearningPolicy.ArchiveNegativeResponses = true
	c.Brain.LearningPolicy.NegativeArchiveThreshold = -0.75
	c.Brain.LearningPolicy.MaxMemoryTextChars = 50000
	c.Brain.LearningPolicy.SourceTrust = map[string]float64{"chat.input": 1.0, "chat.response": 0.90, "api.learn": 1.0, "api.import": 0.70, "ingest.text": 0.90, "ingest.document": 0.85, "web.search": 0.55, "web.page": 0.70, "goal-cycle": 0.85, "consolidation": 1.0}
	c.Brain.Index.Enabled = true
	c.Brain.Index.Mode = "hybrid"
	c.Brain.Index.M = 16
	c.Brain.Index.EfConstruction = 120
	c.Brain.Index.EfSearch = 64
	c.Brain.Index.CandidateScale = 4
	c.Brain.Index.HotMaxItems = 50000
	c.Brain.Index.DiskPQ.Partitions = 128
	c.Brain.Index.DiskPQ.ProbePartitions = 48
	c.Brain.Index.DiskPQ.Subquantizers = 16
	c.Brain.Index.DiskPQ.Centroids = 128
	c.Brain.Index.DiskPQ.TrainingSamples = 8192
	c.Brain.Index.DiskPQ.KMeansIters = 6
	c.Brain.Index.DiskPQ.BuildWorkers = 0
	c.Brain.Index.DiskPQ.CandidateScale = 32
	c.Brain.Index.DiskPQ.MinMemories = 50000
	c.Brain.Index.DiskPQ.RebuildIntervalMinutes = 60
	c.Brain.AutoReward.Enabled = true
	c.Brain.AutoReward.Mode = "vector"
	c.Brain.AutoReward.Provider = "ollama"
	c.Brain.AutoReward.Scale = 0.35
	c.Brain.Consolidation.Enabled = true
	c.Brain.Consolidation.IntervalMinutes = 30
	c.Brain.Consolidation.MinEpisodes = 3
	c.Brain.Consolidation.MaxClusterSize = 8
	c.Brain.Consolidation.MinAccessCount = 1
	c.Brain.Consolidation.SimilarityThreshold = 0.72
	c.Brain.Consolidation.MaxPerCycle = 4
	c.Brain.Consolidation.UseLLM = false
	c.Brain.Consolidation.Provider = "ollama"
	c.Brain.Consolidation.SynapsePruneBelow = 0.01
	c.OpenAI.Enabled = false
	c.OpenAI.BaseURL = "https://api.openai.com"
	c.OpenAI.ChatModel = "gpt-5.6-luna"
	c.OpenAI.EmbeddingModel = "text-embedding-3-small"
	c.OpenAI.MaxOutputTokens = 1400
	c.OpenAI.DailyBudgetUSD = 2.00
	c.OpenAI.MonthlyBudgetUSD = 25.00
	c.OpenAI.Prices = map[string]ModelPrice{
		"gpt-5.6-luna": {
			InputPerM: 0.20, CachedInputPerM: 0.02, OutputPerM: 1.20,
			LongContextThresholdTokens: 272000, LongInputPerM: 0.40, LongCachedInputPerM: 0.04, LongOutputPerM: 1.80,
		},
		"text-embedding-3-small": {EmbeddingInputPerM: 0.02},
		"text-embedding-3-large": {EmbeddingInputPerM: 0.13},
	}
	c.Ollama = []OllamaServer{{
		ID: "local", Name: "Local Ollama", BaseURL: "http://localhost:11434",
		ChatModel: "gemma3", EmbeddingModel: "embeddinggemma", Weight: 1, Enabled: true,
		RequestTimeoutSeconds: 0, NumCtx: 8192, NumPredict: 0, Think: "off",
		ChatKeepAlive: "30m", EmbeddingKeepAlive: "5m",
	}}
	c.Sharding.LocalShardID = "local"
	c.Sharding.RequestTimeoutS = 8
	c.Storage.CheckpointEvery = 500
	c.Storage.WALSync = true
	c.Storage.IndexSnapshot = true
	c.Storage.MaxWALSegmentBytes = 64 << 20
	c.Storage.Segments.Enabled = true
	c.Storage.Segments.MaxSegmentBytes = 128 << 20
	c.Storage.Segments.MmapSealed = true
	c.Storage.Segments.CompactTombstonePct = 0.30
	c.Storage.IndexSegments.Enabled = true
	c.Storage.IndexSegments.BaseEvery = 20
	c.Storage.IndexSegments.MaxDeltas = 64
	c.Storage.IndexSegments.BackgroundMergeMinutes = 10
	c.Storage.IndexSegments.MergeAtDeltas = 8
	c.Storage.PageCache.Enabled = true
	c.Storage.PageCache.MaxBytes = 256 << 20
	c.Storage.VectorJournal.Compression = "sqar-auto"
	c.Storage.VectorJournal.BlockVectors = 128
	c.Storage.VectorJournal.MinBlockBytes = 64 << 10
	c.Storage.VectorJournal.MinSavingsPct = 0.01
	c.Storage.Tiering.Enabled = true
	c.Storage.Tiering.HotMaxBytes = 512 << 20
	c.Storage.Tiering.HotAgeMinutes = 60
	c.Storage.Tiering.IntervalMinutes = 5
	c.Retention.Enabled = true
	c.Retention.IntervalMinutes = 60
	c.Retention.MinAgeDays = 30
	c.Retention.WorkingTTLHours = 24
	c.Retention.MinUtility = 0.18
	c.Retention.MaxMemories = 0
	c.Retention.CompressChars = 480
	c.Retention.DeleteConsolidated = false
	c.Autonomy.Enabled = false
	c.Autonomy.IntervalMinutes = 30
	c.Autonomy.MaxGoalsPerCycle = 3
	c.Autonomy.UseLLM = false
	c.Autonomy.Provider = "ollama"
	c.Autonomy.RunOnGoalCreate = true
	c.Autonomy.DefaultGoalIntervalMinutes = 10
	c.Ingestion.ChunkChars = 2400
	c.Ingestion.ChunkOverlap = 280
	c.Ingestion.MaxDocumentBytes = 25 << 20
	c.Ingestion.MaxChunks = 2000
	c.Ingestion.StoreOriginal = true
	c.Research.Enabled = false
	c.Research.SearXNG.Enabled = false
	c.Research.SearXNG.BaseURL = "http://searxng:8080"
	c.Research.SearXNG.Language = "de-DE"
	c.Research.SearXNG.Categories = "general,science,it"
	c.Research.SearXNG.SafeSearch = 1
	c.Research.SearXNG.TimeoutSeconds = 20
	c.Research.SearXNG.MaxResults = 12
	c.Research.WebFetch.Enabled = true
	c.Research.WebFetch.TimeoutSeconds = 20
	c.Research.WebFetch.MaxBytes = 4 << 20
	c.Research.WebFetch.MaxChars = 120000
	c.Research.WebFetch.UserAgent = "NeuroForge/0.8 research bot"
	c.Research.WebFetch.AllowPrivateTargets = false
	c.Research.Goal.Enabled = true
	c.Research.Goal.SearchEveryCycle = true
	c.Research.Goal.MaxQueriesPerCycle = 2
	c.Research.Goal.MaxResultsPerQuery = 6
	c.Research.Goal.MaxPagesPerCycle = 4
	c.Rebalancing.Enabled = false
	c.Rebalancing.IntervalMinutes = 60
	c.Rebalancing.MaxPerCycle = 100
	c.Rebalancing.Mode = "replicate"
	c.HTTP.ReadHeaderTimeoutSeconds = 10
	c.HTTP.ReadTimeoutSeconds = 30
	c.HTTP.WriteTimeoutSeconds = 0
	c.HTTP.IdleTimeoutSeconds = 90
	c.HTTP.ShutdownTimeoutSeconds = 30
	c.HTTP.MaxHeaderBytes = 1 << 20
	c.HTTP.MaxBodyBytes = 32 << 20
	c.HTTP.MaxConcurrentRequests = 128
	c.Security.SecureHeaders = true
	c.Security.AllowSecretReveal = false
	c.Cluster.Enabled = false
	c.Cluster.NodeID = "local"
	c.Cluster.LeaderID = "local"
	c.Cluster.Term = 1
	c.Cluster.RequestTimeoutS = 5
	c.Cluster.AutoElection = false
	c.Cluster.ElectionMinMS = 1200
	c.Cluster.ElectionMaxMS = 2400
	c.Cluster.HeartbeatMS = 350
	c.Cluster.LogSegmentBytes = 64 << 20
	c.API.RequireKey = true
	c.Worker.LeaseSeconds = 120
	c.Worker.HeartbeatSeconds = 15
	c.Worker.StaleAfterSeconds = 60
	c.Worker.DefaultMaxAttempts = 3
	c.Worker.RetryBackoffSeconds = 15
	c.Worker.MaxQueuedJobs = 5000
	c.Worker.MaxQueuedPayloadMB = 128
	c.Worker.MasterApplyMaxAttempts = 5
	c.Worker.MasterApplyBackoffSeconds = 5
	c.Worker.JobRetentionHours = 24
	c.Worker.MaxTerminalJobs = 2000
	c.Worker.GraphBackfillEnabled = true
	c.Worker.GraphBackfillIntervalS = 10
	c.Worker.GraphBackfillBatchSize = 16
	c.Worker.GraphBackfillMaxQueued = 64
	c.Worker.GraphBackfillMinDegree = 3
	c.Worker.GraphCandidateMultiplier = 6
	c.Worker.GraphRetryAfterMinutes = 360
	c.Worker.RequireWorkerForGraph = true
	c.Worker.OffloadChat = false
	c.Worker.OffloadEmbeddings = false
	c.Worker.DistributedInferenceWaitS = 180
	return c
}

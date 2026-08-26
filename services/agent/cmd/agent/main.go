package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/example/glpi-ai-agent/internal/agent"
	"github.com/example/glpi-ai-agent/internal/config"
	"github.com/example/glpi-ai-agent/internal/contextdata"
	"github.com/example/glpi-ai-agent/internal/glpi"
	"github.com/example/glpi-ai-agent/internal/glpikb"
	"github.com/example/glpi-ai-agent/internal/knowledge"
	"github.com/example/glpi-ai-agent/internal/learning"
	"github.com/example/glpi-ai-agent/internal/metrics"
	"github.com/example/glpi-ai-agent/internal/ollama"
	"github.com/example/glpi-ai-agent/internal/queue"
	"github.com/example/glpi-ai-agent/internal/state"
	"github.com/example/glpi-ai-agent/internal/uptimekuma"
	webui "github.com/example/glpi-ai-agent/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration invalid", "error", err)
		os.Exit(1)
	}
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	g := glpi.New(cfg.GLPIURL, cfg.GLPIAPIVersion, cfg.GLPIClientID, cfg.GLPIClientSecret, cfg.GLPIUsername, cfg.GLPIPassword, cfg.GLPITimeout)
	nodes := make([]ollama.NodeConfig, 0, len(cfg.OllamaURLs))
	for i, nodeURL := range cfg.OllamaURLs {
		name := ""
		if i < len(cfg.OllamaNodeNames) {
			name = cfg.OllamaNodeNames[i]
		}
		weight := 1
		if i < len(cfg.OllamaNodeWeights) {
			weight = cfg.OllamaNodeWeights[i]
		}
		nodes = append(nodes, ollama.NodeConfig{Name: name, URL: nodeURL, Weight: weight})
	}
	o, err := ollama.NewPool(ollama.PoolConfig{
		Nodes: nodes, RoutingMode: cfg.OllamaRoutingMode, NodeMaxInflight: cfg.OllamaNodeMaxInflight,
		HealthInterval: cfg.OllamaNodeHealthInterval, FailureCooldown: cfg.OllamaNodeFailureCooldown,
		NodeRequestTimeout: cfg.OllamaNodeRequestTimeout, FailoverEnabled: cfg.OllamaFailoverEnabled,
		FailoverAttempts: cfg.OllamaFailoverAttempts, RequireSameModelDigest: cfg.OllamaRequireSameDigest,
		RequireEmbeddingModel: cfg.OllamaRequireEmbeddingModel, Model: cfg.OllamaModel, EmbeddingModel: cfg.OllamaEmbeddingModel,
	}, cfg.OllamaModel, cfg.OllamaEmbeddingModel, cfg.CommunicationLanguage, cfg.CommunicationStyle, cfg.OllamaNumPredict, cfg.OllamaKeepAlive, cfg.OllamaThink, cfg.OllamaJSONRetries)
	if err != nil {
		slog.Error("Ollama pool configuration failed", "error", err)
		os.Exit(1)
	}
	o.Start(ctx)
	slog.Info("Ollama pool configured", "nodes", len(nodes), "routing", cfg.OllamaRoutingMode, "max_inflight_per_node", cfg.OllamaNodeMaxInflight, "failover", cfg.OllamaFailoverEnabled, "failover_attempts", cfg.OllamaFailoverAttempts, "require_same_digest", cfg.OllamaRequireSameDigest)
	if err := g.ValidateContract(ctx); err != nil {
		slog.Error("GLPI API contract validation failed", "error", err)
		os.Exit(1)
	}
	if cfg.ContextEnabled {
		var optionalRoutes []string
		if cfg.ChangeCalendarEnabled {
			optionalRoutes = append(optionalRoutes, cfg.GLPIChangePath)
		}
		if cfg.UserDeviceContextEnabled {
			optionalRoutes = append(optionalRoutes, cfg.GLPIUserDevicePaths...)
		}
		if err := g.ValidateReadRoutes(ctx, optionalRoutes); err != nil {
			slog.Error("GLPI context API contract validation failed", "error", err)
			os.Exit(1)
		}
	}
	st, err := state.Open(cfg.DataDir, 2000)
	if err != nil {
		slog.Error("state store initialization failed", "error", err)
		os.Exit(1)
	}
	embeddingProfile := knowledge.ResolveEmbeddingProfile(cfg.KnowledgeEmbeddingProfile, cfg.OllamaEmbeddingModel)
	k, err := knowledge.NewStore(cfg.KnowledgeDir, cfg.DataDir, o, cfg.RAGEnabled, cfg.KnowledgeIndexSources(), knowledge.ScoringConfig{
		SemanticWeight: cfg.KnowledgeSemanticWeight, TitleWeight: cfg.KnowledgeTitleWeight, LexicalWeight: cfg.KnowledgeLexicalWeight, KeywordWeight: cfg.KnowledgeKeywordWeight, CategoryWeight: cfg.KnowledgeCategoryWeight,
		EmbeddingProfile: embeddingProfile, EmbeddingIdentity: cfg.OllamaEmbeddingModel, ChunkWords: cfg.KnowledgeChunkWords, ChunkOverlap: cfg.KnowledgeChunkOverlapWords, MaxChunksPerDoc: cfg.KnowledgeMaxChunksPerDoc, MaxQueryChunks: cfg.KnowledgeMaxQueryChunks,
		IndexMode: cfg.KnowledgeIndexMode, EmbedBatchSize: cfg.KnowledgeEmbedBatchSize, IndexScanInterval: cfg.KnowledgeIndexScanInterval,
		CategoryMode: cfg.KnowledgeCategoryMode, CategoryMapFile: cfg.KnowledgeCategoryMapFile, IgnoreGlobs: cfg.KnowledgeIgnoreGlobs,
	})
	if err != nil {
		slog.Error("knowledge store configuration failed", "error", err)
		os.Exit(1)
	}
	if cfg.KnowledgeVectorBackend != "" && cfg.KnowledgeVectorBackend != "local" {
		nf, nfErr := knowledge.NewNeuroForgeBackend(knowledge.NeuroForgeBackendConfig{
			BaseURL: cfg.NeuroForgeURL, APIKey: cfg.NeuroForgeAPIKey, Namespace: cfg.NeuroForgeNamespace, Timeout: cfg.NeuroForgeTimeout,
		})
		if nfErr != nil {
			slog.Error("NeuroForge semantic backend configuration failed", "error", nfErr)
			os.Exit(1)
		}
		if err := k.SetSemanticBackend(nf, cfg.KnowledgeVectorBackend, cfg.NeuroForgeSearchK, cfg.NeuroForgeFailOpen); err != nil {
			slog.Error("NeuroForge semantic backend activation failed", "error", err)
			os.Exit(1)
		}
		slog.Info("semantic vector backend configured", "backend", cfg.KnowledgeVectorBackend, "url", cfg.NeuroForgeURL, "namespace", cfg.NeuroForgeNamespace, "search_k", cfg.NeuroForgeSearchK, "fail_open", cfg.NeuroForgeFailOpen)
	}
	l, err := learning.Open(cfg.DataDir, cfg.LearningMaxExamples)
	if err != nil {
		slog.Error("learning store initialization failed", "error", err)
		os.Exit(1)
	}
	m := metrics.New()
	q := queue.New(cfg.QueueSize)
	var kuma *uptimekuma.Client
	if cfg.UptimeKumaEnabled {
		kuma = uptimekuma.New(cfg.UptimeKumaURL, cfg.UptimeKumaMode, cfg.UptimeKumaAPIKey, cfg.UptimeKumaTimeout)
	}
	contextCollector := contextdata.New(cfg, g, kuma)
	svc := agent.New(cfg, g, o, k, l, st, q, m, contextCollector)
	if cfg.OutcomeLearningEnabled || cfg.OutcomeRetrievalEnabled {
		outcomeClient, outcomeErr := learning.NewNeuroForgeOutcomeSink(cfg.NeuroForgeURL, cfg.NeuroForgeAPIKey, cfg.NeuroForgeTimeout)
		if outcomeErr != nil {
			slog.Error("NeuroForge outcome client configuration failed", "error", outcomeErr)
			os.Exit(1)
		}
		svc.SetOutcomeRetriever(outcomeClient)
		if cfg.OutcomeLearningEnabled {
			outcomeStore, storeErr := learning.OpenOutcomes(cfg.DataDir, cfg.OutcomeLearningMaxOutcomes)
			if storeErr != nil {
				slog.Error("ticket outcome store initialization failed", "error", storeErr)
				os.Exit(1)
			}
			svc.SetOutcomeLearning(outcomeStore, outcomeClient)
			slog.Info("outcome-gated learning enabled", "fail_open", cfg.OutcomeLearningFailOpen, "max_outcomes", cfg.OutcomeLearningMaxOutcomes)
		}
		if cfg.OutcomeRetrievalEnabled {
			slog.Info("validated outcome retrieval enabled", "search_k", cfg.OutcomeRetrievalSearchK, "min_similarity", cfg.OutcomeRetrievalMinSimilarity, "fail_open", cfg.OutcomeRetrievalFailOpen)
		}
	}
	web, err := webui.New(cfg, m, st, q, k, svc, o)
	if err != nil {
		slog.Error("web UI initialization failed", "error", err)
		os.Exit(1)
	}
	srv := webui.Listen(cfg.HTTPAddr, web.Handler())
	go func() {
		slog.Info("web server started", "addr", cfg.HTTPAddr, "dry_run", cfg.DryRun, "auto_reply", cfg.AutoReply)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("web server failed", "error", err)
			cancel()
		}
	}()

	// Large local knowledge bases are initialized after the HTTP server is up.
	// Ticket polling/workers remain paused until the local index is ready.
	go func() {
		slog.Info("knowledge initialization started in background", "knowledge_dir", cfg.KnowledgeDir, "rag_enabled", cfg.RAGEnabled)
		if err := waitForOllamaPool(ctx, o, cfg.OllamaNodeHealthInterval); err != nil {
			return
		}
		if err := k.Initialize(ctx); err != nil {
			slog.Error("knowledge store initialization failed; web UI remains available", "error", err, "knowledge_dir", cfg.KnowledgeDir, "data_dir", cfg.DataDir, "rag_enabled", cfg.RAGEnabled)
			return
		}
		m.SetKnowledgeDocs(k.Count())
		stats := k.LoadStats()
		if stats.IgnoredFiles > 0 || stats.UnmappedCategoryFiles > 0 {
			slog.Warn("knowledge loaded with compatibility rules", "ignored_files", stats.IgnoredFiles, "unmapped_category_files", stats.UnmappedCategoryFiles, "unmapped_categories", stats.UnmappedCategories, "category_mode", cfg.KnowledgeCategoryMode)
		}
		if cfg.GLPIKBEnabled {
			kbSync := glpikb.New(cfg, g, k, m)
			if err := kbSync.LoadCache(ctx); err != nil {
				slog.Warn("GLPI knowledge cache unavailable", "error", err)
			}
			syncCtx, syncCancel := context.WithTimeout(ctx, maxDuration(cfg.GLPITimeout*3, 30*time.Second))
			if err := kbSync.Sync(syncCtx); err != nil {
				slog.Error("initial GLPI knowledge base sync failed; continuing with local/cache knowledge", "error", err)
			}
			syncCancel()
			kbSync.Start(ctx)
			m.SetKnowledgeDocs(k.Count())
		}
		svc.Start(ctx)
		k.StartIncrementalSync(ctx, cfg.KnowledgeIndexScanInterval)
		slog.Info("ticket processing started", "knowledge_docs", k.Count(), "knowledge_index_mode", cfg.KnowledgeIndexMode)
	}()
	<-ctx.Done()
	shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	_ = srv.Shutdown(shutdownCtx)
	slog.Info("shutdown complete")
}

func waitForOllamaPool(ctx context.Context, client *ollama.Client, retryInterval time.Duration) error {
	if retryInterval < 2*time.Second {
		retryInterval = 5 * time.Second
	}
	for {
		err := client.Ping(ctx)
		if err == nil {
			statuses := client.NodeStatuses()
			healthy := 0
			for _, status := range statuses {
				if status.Healthy && status.Compatible {
					healthy++
				}
			}
			slog.Info("Ollama pool ready", "healthy_nodes", healthy, "nodes", len(statuses), "routing", client.RoutingMode())
			return nil
		}
		slog.Warn("waiting for compatible Ollama pool", "retry_in", retryInterval.String(), "error", err)
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

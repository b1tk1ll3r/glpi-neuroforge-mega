package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"neuroforge/internal/brain"
	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/httpapi"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

func envBool(name string) (bool, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return false, false
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, false
	}
	return v, true
}

func envInt(name string) (int, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return v, true
}

func validateManagedSecret(name, value string, minLen int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	upper := strings.ToUpper(value)
	if strings.Contains(upper, "CHANGE_ME") || strings.Contains(upper, "CHANGEME") || strings.Contains(upper, "PLACEHOLDER") {
		return fmt.Errorf("%s still contains a placeholder", name)
	}
	if len(value) < minLen {
		return fmt.Errorf("%s must be at least %d characters", name, minLen)
	}
	return nil
}

func validateManagedSecretsFromEnv() error {
	for _, item := range []struct {
		name string
		min  int
	}{
		{"NEUROFORGE_ADMIN_TOKEN", 24},
		{"NEUROFORGE_APP_API_KEY", 24},
		{"NEUROFORGE_INTEGRATION_TOKEN", 24},
		{"NEUROFORGE_CONTROL_READ_TOKEN", 24},
		{"NEUROFORGE_WORKER_TOKEN", 24},
		{"NEUROFORGE_METRICS_TOKEN", 24},
		{"NEUROFORGE_CLUSTER_TOKEN", 24},
	} {
		if err := validateManagedSecret(item.name, os.Getenv(item.name), item.min); err != nil {
			return err
		}
	}
	return nil
}

func maxIntMain(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() (retErr error) {
	data := flag.String("data", "./data", "data directory")
	listen := flag.String("listen", "", "listen address override")
	flag.Parse()

	if err := validateManagedSecretsFromEnv(); err != nil {
		return err
	}

	s, err := store.New(*data)
	if err != nil {
		return err
	}
	defer func() {
		if err := s.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("store close: %w", err)
		}
	}()

	sec := s.Secrets()
	changed := false
	for name, dst := range map[string]*string{
		"OPENAI_API_KEY":                &sec.OpenAIAPIKey,
		"NEUROFORGE_ADMIN_TOKEN":        &sec.AdminToken,
		"NEUROFORGE_APP_API_KEY":        &sec.AppAPIKey,
		"NEUROFORGE_INTEGRATION_TOKEN":  &sec.IntegrationToken,
		"NEUROFORGE_CONTROL_READ_TOKEN": &sec.ControlReadToken,
		"NEUROFORGE_WORKER_TOKEN":       &sec.WorkerToken,
		"NEUROFORGE_METRICS_TOKEN":      &sec.MetricsToken,
		"NEUROFORGE_CLUSTER_TOKEN":      &sec.ClusterToken,
	} {
		if v := os.Getenv(name); v != "" {
			*dst = v
			changed = true
		}
	}
	if changed {
		if err := s.UpdateSecrets(sec); err != nil {
			return err
		}
	}

	// Optional environment bootstrap for containerized mega-project deployments.
	// Values are applied only when explicitly set, so admin-managed persisted
	// routing remains authoritative otherwise.
	if base := os.Getenv("NEUROFORGE_OLLAMA_URL"); base != "" {
		cfg := s.Config()
		if len(cfg.Ollama) == 0 {
			cfg.Ollama = append(cfg.Ollama, core.OllamaServer{ID: "local", Name: "Shared Ollama", Enabled: true, Weight: 1})
		}
		cfg.Ollama[0].BaseURL = base
		if model := os.Getenv("NEUROFORGE_OLLAMA_CHAT_MODEL"); model != "" {
			cfg.Ollama[0].ChatModel = model
		}
		if model := os.Getenv("NEUROFORGE_OLLAMA_EMBEDDING_MODEL"); model != "" {
			cfg.Ollama[0].EmbeddingModel = model
		}
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("apply NeuroForge Ollama environment bootstrap: %w", err)
		}
	}

	// Controlled-learning and research bootstrap for the mega-project. These
	// values are only applied when the corresponding environment variable is
	// explicitly present, preserving persisted admin settings otherwise.
	if controlled, ok := envBool("NEUROFORGE_CONTROLLED_LEARNING"); ok && controlled {
		cfg := s.Config()
		cfg.Brain.AutoLearn = true
		cfg.Brain.LearningPolicy.Enabled = true
		cfg.Brain.LearningPolicy.LearnChatInputs = false
		cfg.Brain.LearningPolicy.LearnChatResponses = false
		cfg.Brain.LearningPolicy.AllowExplicitLearn = true
		cfg.Brain.LearningPolicy.AllowImports = false
		cfg.Brain.LearningPolicy.LearnGoalCycles = false
		if cfg.Brain.LearningPolicy.MinConfidence < 0.35 {
			cfg.Brain.LearningPolicy.MinConfidence = 0.35
		}
		if cfg.Brain.LearningPolicy.SemanticMinConfirmations < 3 {
			cfg.Brain.LearningPolicy.SemanticMinConfirmations = 3
		}
		if cfg.Brain.LearningPolicy.SemanticMinConfidence < 0.65 {
			cfg.Brain.LearningPolicy.SemanticMinConfidence = 0.65
		}
		if cfg.Brain.LearningPolicy.SourceTrust == nil {
			cfg.Brain.LearningPolicy.SourceTrust = map[string]float64{}
		}
		cfg.Brain.LearningPolicy.SourceTrust["chat.input"] = 0.25
		cfg.Brain.LearningPolicy.SourceTrust["chat.response"] = 0.20
		cfg.Brain.LearningPolicy.SourceTrust["web.search"] = 0.45
		cfg.Brain.LearningPolicy.SourceTrust["web.page"] = 0.60
		cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.accepted"] = 1.0
		cfg.Brain.LearningPolicy.SourceTrust["glpi.outcome.corrected"] = 1.0
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("apply controlled learning bootstrap: %w", err)
		}
	}
	// Goal-cycle learning is an independent trust boundary. Controlled learning
	// keeps it disabled by default, but operators may explicitly enable it
	// without re-enabling raw chat/input learning or memory imports. Apply this
	// override after the controlled-learning bootstrap so the explicit setting
	// wins on every restart.
	if v, ok := envBool("NEUROFORGE_GOAL_LEARNING_ENABLED"); ok {
		cfg := s.Config()
		cfg.Brain.LearningPolicy.LearnGoalCycles = v
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("apply goal learning environment bootstrap: %w", err)
		}
	}

	if _, hasResearch := os.LookupEnv("NEUROFORGE_RESEARCH_ENABLED"); hasResearch {
		cfg := s.Config()
		if v, ok := envBool("NEUROFORGE_RESEARCH_ENABLED"); ok {
			cfg.Research.Enabled = v
		}
		if v, ok := envBool("NEUROFORGE_SEARXNG_ENABLED"); ok {
			cfg.Research.SearXNG.Enabled = v
		}
		if v := strings.TrimSpace(os.Getenv("NEUROFORGE_SEARXNG_URL")); v != "" {
			cfg.Research.SearXNG.BaseURL = v
		}
		if v, ok := envBool("NEUROFORGE_AUTONOMY_ENABLED"); ok {
			cfg.Autonomy.Enabled = v
		}
		if v, ok := envBool("NEUROFORGE_RESEARCH_GOAL_ENABLED"); ok {
			cfg.Research.Goal.Enabled = v
		}
		if v, ok := envInt("NEUROFORGE_AUTONOMY_INTERVAL_MINUTES"); ok && v > 0 {
			cfg.Autonomy.IntervalMinutes = v
		}
		if v, ok := envInt("NEUROFORGE_RESEARCH_MAX_QUERIES"); ok && v > 0 {
			cfg.Research.Goal.MaxQueriesPerCycle = v
		}
		if v, ok := envInt("NEUROFORGE_RESEARCH_MAX_PAGES"); ok && v >= 0 {
			cfg.Research.Goal.MaxPagesPerCycle = v
		}
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("apply research environment bootstrap: %w", err)
		}
	}

	r := provider.NewRouter(s)
	c := cost.New(s)
	b := brain.New(s, r, c)
	stagingCfg := brain.StagingPublisherConfig{
		URL:   strings.TrimSpace(os.Getenv("NEUROFORGE_KB_STAGING_URL")),
		Token: strings.TrimSpace(os.Getenv("NEUROFORGE_KB_STAGING_TOKEN")),
	}
	if v, ok := envBool("NEUROFORGE_KB_STAGING_ENABLED"); ok {
		stagingCfg.Enabled = v
	}
	if v, ok := envInt("NEUROFORGE_KB_STAGING_MIN_EVIDENCE"); ok {
		stagingCfg.MinEvidence = v
	}
	if v, ok := envInt("NEUROFORGE_KB_STAGING_MIN_SOURCES"); ok {
		stagingCfg.MinSources = v
	}
	if v, ok := envInt("NEUROFORGE_KB_STAGING_MIN_CORROBORATIONS"); ok {
		stagingCfg.MinCorroborations = v
	}
	if v, ok := envInt("NEUROFORGE_KB_STAGING_MAX_EVIDENCE"); ok {
		stagingCfg.MaxEvidence = v
	}
	b.ConfigureStagingPublisher(stagingCfg)
	if stagingCfg.Enabled {
		log.Printf("KB human-review staging bridge enabled: %s (min evidence=%d, sources=%d, corroborations=%d)", stagingCfg.URL, maxIntMain(stagingCfg.MinEvidence, 4), maxIntMain(stagingCfg.MinSources, 2), maxIntMain(stagingCfg.MinCorroborations, 0))
	}
	if err := b.ReconcileGoalProgress(); err != nil {
		return fmt.Errorf("reconcile persisted goal research progress: %w", err)
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	maintenanceCtx, stopMaintenance := context.WithCancel(rootCtx)
	defer stopMaintenance()
	go b.RunV6Maintenance(maintenanceCtx)
	api := httpapi.New(s, b, r, c)
	if v, ok := envBool("NEUROFORGE_READINESS_OLLAMA_LIVE"); ok {
		api.SetReadinessOllamaLive(v)
	}
	cfg := s.Config()
	addr := cfg.Listen
	if *listen != "" {
		addr = *listen
	}
	if addr == "" {
		addr = ":8080"
	}

	h := cfg.HTTP
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: time.Duration(h.ReadHeaderTimeoutSeconds) * time.Second,
		ReadTimeout:       time.Duration(h.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(h.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(h.IdleTimeoutSeconds) * time.Second,
		MaxHeaderBytes:    h.MaxHeaderBytes,
	}
	log.Printf("NeuroForge v0.8.2 listening on %s", addr)
	log.Printf("Admin dashboard: /admin · readiness: /readyz · metrics: /metrics")
	if os.Getenv("NEUROFORGE_ADMIN_TOKEN") == "" {
		log.Printf("Admin token is intentionally not printed; read it locally from %s or set NEUROFORGE_ADMIN_TOKEN", filepath.Join(*data, "secrets.json"))
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	var serveErr error
	select {
	case serveErr = <-errCh:
		if serveErr != nil {
			log.Printf("http server stopped unexpectedly: %v", serveErr)
		}
	case <-rootCtx.Done():
		log.Printf("shutdown requested")
	}

	stopMaintenance()
	shutdownTimeout := h.ShutdownTimeoutSeconds
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(shutdownTimeout)*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown: %v", err)
		_ = srv.Close()
		if serveErr == nil {
			serveErr = err
		}
	}
	if err := s.ForceCheckpoint(); err != nil {
		log.Printf("final checkpoint: %v", err)
		if serveErr == nil {
			serveErr = err
		}
	}
	log.Printf("NeuroForge stopped")
	return serveErr
}

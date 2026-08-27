package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kb-editor/internal/aifallback"
	"kb-editor/internal/staging"
	"kb-editor/internal/store"
)

//go:embed web/* viewer/*
var webFS embed.FS

func main() {
	var dataDir string
	var listen string
	flag.StringVar(&dataDir, "data", envOr("DATA_DIR", "./data/knowledge"), "directory containing JSON knowledge files")
	flag.StringVar(&listen, "listen", envOr("LISTEN_ADDR", ":8080"), "HTTP listen address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := validateRuntimeSecrets(); err != nil {
		log.Fatal(err)
	}

	cfg, staticDir, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	s, err := store.New(dataDir)
	if err != nil {
		log.Fatalf("initialize store: %v", err)
	}

	stagingStore, err := stagingStoreFromEnv(s.DataDir())
	if err != nil {
		log.Fatal(err)
	}
	aiService, aiTimeout, err := aiServiceFromEnv(cfg.Mode, stagingStore)
	if err != nil {
		log.Fatal(err)
	}
	if aiService != nil {
		cfg.AIFallbackEnabled = true
		cfg.AIFallbackTimeoutSeconds = int(aiTimeout.Seconds())
		cfg.AIFallbackModel = aiService.Model()
	}

	reloadInterval, err := autoReloadInterval(cfg.Mode)
	if err != nil {
		log.Fatal(err)
	}
	if reloadInterval > 0 {
		go startAutoReload(ctx, s, reloadInterval)
	}

	sub, err := fs.Sub(webFS, staticDir)
	if err != nil {
		log.Fatal(err)
	}

	app := newApp(s, sub, cfg).withStaging(stagingStore).withAI(aiService)
	handler := requestLogger(optionalBasicAuth(app.routes()))

	writeTimeout := 60 * time.Second
	if aiService != nil && aiTimeout+30*time.Second > writeTimeout {
		writeTimeout = aiTimeout + 30*time.Second
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       90 * time.Second,
	}

	log.Printf("KB service listening on %s", listen)
	log.Printf("Mode: %s (writable=%t)", cfg.Mode, cfg.Writable)
	log.Printf("Data directory: %s (%d JSON files indexed)", s.DataDir(), s.Count())
	log.Printf("Staging directory: %s (%d JSON files)", stagingStore.Dir(), stagingStore.Count())
	if reloadInterval > 0 {
		log.Printf("Automatic index reload: %s", reloadInterval)
	}
	if aiService != nil {
		log.Printf("AI fallback enabled: model=%q timeout=%s staging=%s", aiService.Model(), aiTimeout, aiService.StagingDir())
	}
	if u := os.Getenv("BASIC_AUTH_USER"); u != "" {
		log.Printf("Basic authentication enabled for user %q", u)
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err != nil {
			log.Printf("KB HTTP server stopped unexpectedly: %v", err)
		}
	case <-ctx.Done():
		log.Printf("KB shutdown requested")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("KB graceful shutdown failed: %v", err)
		_ = srv.Close()
	}
}

func validateRuntimeSecrets() error {
	check := func(name string, min int) error {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			return nil
		}
		upper := strings.ToUpper(v)
		if strings.Contains(upper, "CHANGE_ME") || strings.Contains(upper, "CHANGEME") || strings.Contains(upper, "PLACEHOLDER") {
			return fmt.Errorf("%s still contains a placeholder", name)
		}
		if len(v) < min {
			return fmt.Errorf("%s must be at least %d characters", name, min)
		}
		return nil
	}
	for _, item := range []struct {
		name string
		min  int
	}{
		{"KB_INTEGRATION_TOKEN", 24},
		{"BASIC_AUTH_PASSWORD", 12},
		{"BRAIN_ACTIVITY_API_KEY", 24},
	} {
		if err := check(item.name, item.min); err != nil {
			return err
		}
	}
	return nil
}

func configFromEnv() (appConfig, string, error) {
	mode := strings.ToLower(strings.TrimSpace(envOr("APP_MODE", "editor")))
	switch mode {
	case "editor":
		return appConfig{
			Mode:     "editor",
			Title:    envOr("APP_TITLE", "Knowledge Base Editor"),
			Subtitle: envOr("APP_SUBTITLE", "JSON · Massenbearbeitung · Docker"),
			Writable: true,
		}, "web", nil
	case "google", "viewer", "search":
		return appConfig{
			Mode:     "google",
			Title:    envOr("APP_TITLE", "Helpdesk Search"),
			Subtitle: envOr("APP_SUBTITLE", "Interne Wissenssuche für den Helpdesk"),
			Writable: false,
		}, "viewer", nil
	default:
		return appConfig{}, "", fmt.Errorf("invalid APP_MODE %q: expected editor or google", mode)
	}
}

func autoReloadInterval(mode string) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("AUTO_RELOAD_INTERVAL"))
	if raw == "" {
		if mode == "google" {
			return 60 * time.Second, nil
		}
		return 0, nil
	}
	if raw == "0" || strings.EqualFold(raw, "off") || strings.EqualFold(raw, "disabled") {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid AUTO_RELOAD_INTERVAL %q: %w", raw, err)
	}
	if d < 5*time.Second {
		return 0, fmt.Errorf("AUTO_RELOAD_INTERVAL must be 0/off or at least 5s")
	}
	return d, nil
}

func startAutoReload(ctx context.Context, s *store.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Reload(); err != nil {
				log.Printf("automatic index reload failed: %v", err)
			}
		}
	}
}

func stagingStoreFromEnv(dataDir string) (*staging.Store, error) {
	stagingDir := strings.TrimSpace(os.Getenv("STAGING_DIR"))
	if stagingDir == "" {
		stagingDir = filepath.Join(filepath.Dir(dataDir), "staging")
	}
	stagingAbs, err := filepath.Abs(stagingDir)
	if err != nil {
		return nil, err
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if pathContains(dataAbs, stagingAbs) || pathContains(stagingAbs, dataAbs) {
		return nil, fmt.Errorf("STAGING_DIR (%s) must be separate from DATA_DIR (%s)", stagingAbs, dataAbs)
	}
	return staging.New(stagingAbs)
}

func aiServiceFromEnv(mode string, st *staging.Store) (*aifallback.Service, time.Duration, error) {
	enabled, err := envBool("AI_FALLBACK_ENABLED", false)
	if err != nil {
		return nil, 0, err
	}
	if !enabled {
		return nil, 0, nil
	}
	if mode != "google" {
		return nil, 0, fmt.Errorf("AI_FALLBACK_ENABLED is only supported with APP_MODE=google")
	}

	timeout, err := time.ParseDuration(envOr("OLLAMA_TIMEOUT", "10m"))
	if err != nil || timeout < time.Second {
		return nil, 0, fmt.Errorf("invalid OLLAMA_TIMEOUT: expected a duration such as 10m")
	}
	maxConcurrent, err := strconv.Atoi(envOr("OLLAMA_MAX_CONCURRENT", "1"))
	if err != nil || maxConcurrent < 1 || maxConcurrent > 16 {
		return nil, 0, fmt.Errorf("OLLAMA_MAX_CONCURRENT must be an integer between 1 and 16")
	}
	autoReply, err := envBool("OLLAMA_STAGING_AUTO_REPLY", false)
	if err != nil {
		return nil, 0, err
	}
	minScore, err := strconv.ParseFloat(envOr("OLLAMA_STAGING_MIN_SCORE", "0.78"), 64)
	if err != nil || minScore < 0 || minScore > 1 {
		return nil, 0, fmt.Errorf("OLLAMA_STAGING_MIN_SCORE must be between 0 and 1")
	}
	if st == nil {
		return nil, 0, fmt.Errorf("staging store is required for AI fallback")
	}
	svc, err := aifallback.New(aifallback.Config{
		BaseURL:       envOr("OLLAMA_BASE_URL", "http://ollama:11434"),
		Model:         strings.TrimSpace(os.Getenv("OLLAMA_MODEL")),
		Timeout:       timeout,
		MaxConcurrent: maxConcurrent,
		AutoReply:     autoReply,
		MinScore:      minScore,
	}, st)
	if err != nil {
		return nil, 0, err
	}
	return svc, timeout, nil
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q: expected true or false", key, raw)
	}
	return value, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func optionalBasicAuth(next http.Handler) http.Handler {
	user := os.Getenv("BASIC_AUTH_USER")
	pass := os.Getenv("BASIC_AUTH_PASSWORD")
	if user == "" && pass == "" {
		return next
	}
	if user == "" || pass == "" {
		log.Fatal("BASIC_AUTH_USER and BASIC_AUTH_PASSWORD must either both be set or both be empty")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodGet && (r.URL.Path == "/api/health" || r.URL.Path == "/api/integrations/staging/health")) || (r.Method == http.MethodPost && r.URL.Path == "/api/integrations/staging") {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="KB Helpdesk", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
	})
}

func mustJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		http.Error(w, fmt.Sprintf("Content-Type must be application/json, got %q", ct), http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

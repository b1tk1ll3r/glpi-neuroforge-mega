package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed index.html
var web embed.FS

type target struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	PublicURL string `json:"public_url"`
	Path      string `json:"-"`
	Auth      string `json:"-"`
	Optional  bool   `json:"optional,omitempty"`
}

type status struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    any    `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
}

type server struct {
	http                    *http.Client
	targets                 []target
	agentURL                string
	agentReadToken          string
	neuroforgeURL           string
	neuroforgeKey           string
	codebaseMemoryURL       string
	codebaseMemoryPublicURL string
	vectorMode              string
	neuroforgeSearchK       string
	failOpen                string
	controlledLearning      string
	goalLearning            string
	outcomeLearning         string
	outcomeRetrieval        string
	outcomeSearchK          string
	outcomeMinSimilarity    string
	outcomeFailOpen         string
	researchEnabled         string
	searxngEnabled          string
	autonomyEnabled         string
}

func env(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

func main() {
	agentURL := env("AGENT_URL", "http://agent:8080")
	nfURL := env("NEUROFORGE_URL", "http://neuroforge:8080")
	nfKeyRaw := strings.TrimSpace(os.Getenv("NEUROFORGE_API_KEY"))
	nfAuth := ""
	if nfKeyRaw != "" {
		nfAuth = "Bearer " + nfKeyRaw
	}
	s := &server{http: &http.Client{Timeout: 6 * time.Second}, agentURL: strings.TrimRight(agentURL, "/"), agentReadToken: strings.TrimSpace(os.Getenv("CONTROL_READ_TOKEN")), neuroforgeURL: strings.TrimRight(nfURL, "/"), neuroforgeKey: nfKeyRaw, codebaseMemoryURL: strings.TrimRight(strings.TrimSpace(os.Getenv("CODEBASE_MEMORY_URL")), "/"), codebaseMemoryPublicURL: strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_CODEBASE_MEMORY_URL")), "/"), vectorMode: env("KNOWLEDGE_VECTOR_BACKEND", "dual"), neuroforgeSearchK: env("NEUROFORGE_SEARCH_K", "128"), failOpen: env("NEUROFORGE_FAIL_OPEN", "true"), controlledLearning: env("NEUROFORGE_CONTROLLED_LEARNING", "true"), goalLearning: env("NEUROFORGE_GOAL_LEARNING_ENABLED", "false"), outcomeLearning: env("OUTCOME_LEARNING_ENABLED", "true"), outcomeRetrieval: env("OUTCOME_RETRIEVAL_ENABLED", "true"), outcomeSearchK: env("OUTCOME_RETRIEVAL_SEARCH_K", "6"), outcomeMinSimilarity: env("OUTCOME_RETRIEVAL_MIN_SIMILARITY", "0.58"), outcomeFailOpen: env("OUTCOME_RETRIEVAL_FAIL_OPEN", "true"), researchEnabled: env("NEUROFORGE_RESEARCH_ENABLED", "false"), searxngEnabled: env("NEUROFORGE_SEARXNG_ENABLED", "false"), autonomyEnabled: env("NEUROFORGE_AUTONOMY_ENABLED", "false")}
	s.targets = []target{
		{ID: "agent", Name: "GLPI AI Agent", URL: agentURL, PublicURL: env("PUBLIC_AGENT_URL", "http://localhost:8080"), Path: "/readyz"},
		{ID: "knowledge", Name: "Knowledgebase", URL: env("KNOWLEDGE_URL", "http://knowledge:8080"), PublicURL: env("PUBLIC_KNOWLEDGE_URL", "http://localhost:8081"), Path: "/api/health"},
		{ID: "neuroforge", Name: "NeuroForge Brain", URL: nfURL, PublicURL: env("PUBLIC_NEUROFORGE_URL", "http://localhost:8090/admin"), Path: "/api/v1/stats", Auth: nfAuth},
	}
	if s.codebaseMemoryURL != "" {
		s.targets = append(s.targets, target{ID: "codebase-memory", Name: "Codebase Memory MCP", URL: s.codebaseMemoryURL, PublicURL: s.codebaseMemoryPublicURL, Path: "/", Optional: true})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/graph/runtime", s.handleRuntimeGraph)
	mux.HandleFunc("GET /api/graph/runs", s.handleGraphRuns)
	mux.HandleFunc("GET /api/graph/ticket", s.handleTicketGraph)
	mux.HandleFunc("GET /api/graph/learning", s.handleLearningGraph)
	mux.HandleFunc("GET /api/graph/research", s.handleResearchGraph)
	mux.HandleFunc("GET /api/graph/brain", s.handleBrainGraph)
	mux.HandleFunc("GET /api/graph/engineering", s.handleEngineeringGraph)
	mux.HandleFunc("GET /api/graph/impact", s.handleEngineeringImpact)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := web.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	addr := env("CONTROL_ADDR", ":8070")
	srv := &http.Server{Addr: addr, Handler: secure(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("mega control listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"vector_backend": s.vectorMode, "neuroforge_search_k": s.neuroforgeSearchK, "neuroforge_fail_open": s.failOpen, "controlled_learning": s.controlledLearning, "goal_learning": s.goalLearning, "outcome_learning": s.outcomeLearning, "outcome_retrieval": s.outcomeRetrieval, "outcome_retrieval_search_k": s.outcomeSearchK, "outcome_retrieval_min_similarity": s.outcomeMinSimilarity, "outcome_retrieval_fail_open": s.outcomeFailOpen, "quality_replay": "available-on-agent", "research_enabled": s.researchEnabled, "searxng_enabled": s.searxngEnabled, "autonomy_enabled": s.autonomyEnabled, "control_plane": "read-only", "policy_owner": "glpi-agent", "unified_graph": true, "engineering_graph": "embedded-ast", "codebase_memory_url": s.codebaseMemoryPublicURL})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := s.statusSnapshot(ctx)
	all := true
	for _, st := range out {
		if !st.OK && !st.Optional {
			all = false
		}
	}
	code := 200
	if !all {
		code = 207
	}
	writeJSON(w, code, map[string]any{"ok": all, "checked_at": time.Now().UTC(), "services": out})
}

func (s *server) statusSnapshot(ctx context.Context) []status {
	ch := make(chan status, len(s.targets))
	for _, t := range s.targets {
		go func(t target) { ch <- s.check(ctx, t) }(t)
	}
	out := make([]status, 0, len(s.targets))
	for range s.targets {
		out = append(out, <-ch)
	}
	return out
}

func (s *server) check(ctx context.Context, t target) status {
	started := time.Now()
	st := status{Name: t.Name, PublicURL: t.PublicURL, Optional: t.Optional}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.URL, "/")+t.Path, nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if t.Auth != "" {
		req.Header.Set("Authorization", t.Auth)
	}
	resp, err := s.http.Do(req)
	st.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	defer resp.Body.Close()
	st.Status = resp.StatusCode
	st.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if len(b) > 0 {
		var v any
		if json.Unmarshal(b, &v) == nil {
			st.Detail = v
		} else {
			st.Detail = string(b)
		}
	}
	if !st.OK && st.Error == "" {
		st.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return st
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

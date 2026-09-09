package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/vector"
)

const workerVersion = "1.6.0"

type relinkPayload struct {
	TargetID          string    `json:"target_id"`
	TargetVersion     int64     `json:"target_version"`
	TargetFingerprint string    `json:"target_fingerprint"`
	Target            []float32 `json:"target"`
	Candidates        []struct {
		ID          string    `json:"id"`
		Version     int64     `json:"version"`
		Fingerprint string    `json:"fingerprint"`
		Vector      []float32 `json:"vector"`
	} `json:"candidates"`
	K             int     `json:"k"`
	MinSimilarity float64 `json:"min_similarity"`
}
type neighbor struct {
	ID         string  `json:"id"`
	Similarity float64 `json:"similarity"`
}
type relinkResult struct {
	TargetID  string     `json:"target_id"`
	Neighbors []neighbor `json:"neighbors"`
}

type modelEmbedPayload struct {
	Text  string `json:"text"`
	Model string `json:"model,omitempty"`
}

type modelChatPayload struct {
	Instructions string `json:"instructions,omitempty"`
	Input        string `json:"input"`
	Model        string `json:"model,omitempty"`
	MaxOutput    int    `json:"max_output,omitempty"`
	JSONMode     bool   `json:"json_mode,omitempty"`
}

type modelUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens"`
}

type modelResult struct {
	Text     string     `json:"text,omitempty"`
	Vector   []float32  `json:"vector,omitempty"`
	Usage    modelUsage `json:"usage"`
	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	NodeID   string     `json:"node_id"`
}

type workerConfig struct {
	ID               string
	Server           string
	Token            string
	Interval         time.Duration
	Heartbeat        time.Duration
	ResourceClass    string
	Capabilities     []string
	MaxConcurrency   int
	Hostname         string
	OllamaURL        string
	OllamaAPIKey     string
	OllamaChatModel  string
	OllamaEmbedModel string
	OllamaNumCtx     int
	OllamaKeepAlive  string
}

type leaseSet struct {
	mu sync.RWMutex
	m  map[string]string
}

func (l *leaseSet) add(id, token string) {
	l.mu.Lock()
	l.m[id] = token
	l.mu.Unlock()
}
func (l *leaseSet) del(id string) {
	l.mu.Lock()
	delete(l.m, id)
	l.mu.Unlock()
}
func (l *leaseSet) snapshot() map[string]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]string, len(l.m))
	for k, v := range l.m {
		out[k] = v
	}
	return out
}

func main() {
	server := flag.String("server", "http://localhost:8080", "NeuroForge master/orchestrator server")
	id := flag.String("id", hostname(), "subagent worker id")
	interval := flag.Duration("interval", 2*time.Second, "poll interval")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("NEUROFORGE_WORKER_TOKEN"))
	if token == "" {
		log.Fatal("NEUROFORGE_WORKER_TOKEN is required")
	}
	resource := lowerDefault(os.Getenv("NEUROFORGE_WORKER_RESOURCE_CLASS"), "cpu")
	caps := csv(os.Getenv("NEUROFORGE_WORKER_CAPABILITIES"))
	if len(caps) == 0 {
		if resource == "gpu" {
			caps = []string{"gpu", "model.chat", "model.embed"}
		} else {
			caps = []string{"cpu", "vector.relink"}
		}
	}
	maxConcurrency := envInt("NEUROFORGE_WORKER_MAX_CONCURRENCY", 1)
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	heartbeat := envDuration("NEUROFORGE_WORKER_HEARTBEAT_INTERVAL", 15*time.Second)
	cfg := workerConfig{
		ID: *id, Server: strings.TrimRight(*server, "/"), Token: token, Interval: *interval,
		Heartbeat: heartbeat, ResourceClass: resource, Capabilities: caps,
		MaxConcurrency: maxConcurrency, Hostname: hostname(),
		OllamaURL:        strings.TrimRight(firstNonEmpty(os.Getenv("NEUROFORGE_WORKER_OLLAMA_URL"), os.Getenv("OLLAMA_BASE_URL"), os.Getenv("OLLAMA_URL")), "/"),
		OllamaAPIKey:     firstNonEmpty(os.Getenv("NEUROFORGE_WORKER_OLLAMA_API_KEY"), os.Getenv("OLLAMA_API_KEY")),
		OllamaChatModel:  firstNonEmpty(os.Getenv("NEUROFORGE_WORKER_OLLAMA_CHAT_MODEL"), os.Getenv("OLLAMA_MODEL")),
		OllamaEmbedModel: firstNonEmpty(os.Getenv("NEUROFORGE_WORKER_OLLAMA_EMBEDDING_MODEL"), os.Getenv("OLLAMA_EMBEDDING_MODEL")),
		OllamaNumCtx:     envInt("NEUROFORGE_WORKER_OLLAMA_NUM_CTX", 8192),
		OllamaKeepAlive:  firstNonEmpty(os.Getenv("NEUROFORGE_WORKER_OLLAMA_KEEP_ALIVE"), "10m"),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Control-plane requests must never hang forever on a dead master. Inference
	// uses its own client and is bounded by the durable job timeout/context.
	controlClient := &http.Client{Timeout: 30 * time.Second}
	inferenceClient := &http.Client{Timeout: 0}
	leases := &leaseSet{m: map[string]string{}}
	if err := register(controlClient, cfg); err != nil {
		log.Printf("initial register failed: %v", err)
	}
	go heartbeatLoop(ctx, controlClient, cfg, leases)
	log.Printf("subagent %s polling %s resource=%s caps=%s concurrency=%d", cfg.ID, cfg.Server, cfg.ResourceClass, strings.Join(cfg.Capabilities, ","), cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for i := 0; i < cfg.MaxConcurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			pollLoop(ctx, controlClient, inferenceClient, cfg, leases, slot)
		}(i)
	}
	<-ctx.Done()
	log.Printf("subagent shutdown requested; waiting for active slots")
	wg.Wait()
}

func waitOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func pollLoop(root context.Context, controlClient, inferenceClient *http.Client, cfg workerConfig, leases *leaseSet, slot int) {
	for root.Err() == nil {
		job, err := claim(controlClient, cfg)
		if err != nil {
			log.Printf("slot=%d claim: %v", slot, err)
			if !waitOrDone(root, cfg.Interval) {
				return
			}
			continue
		}
		if job == nil {
			if !waitOrDone(root, cfg.Interval) {
				return
			}
			continue
		}
		leases.add(job.ID, job.LeaseToken)
		jobCtx := root
		cancel := func() {}
		if job.TimeoutSeconds > 0 {
			jobCtx, cancel = context.WithTimeout(root, time.Duration(job.TimeoutSeconds)*time.Second)
		}
		res, jobErr := run(jobCtx, inferenceClient, cfg, job)
		cancel()
		if root.Err() != nil && jobErr == "" {
			jobErr = root.Err().Error()
		}
		if err := complete(controlClient, cfg, job.ID, job.LeaseToken, res, jobErr); err != nil {
			log.Printf("slot=%d complete %s: %v", slot, job.ID, err)
		} else if jobErr != "" {
			log.Printf("slot=%d job %s %s returned error: %s", slot, job.ID, job.Type, jobErr)
		} else {
			log.Printf("slot=%d job %s %s complete", slot, job.ID, job.Type)
		}
		leases.del(job.ID)
	}
}

func workerBody(cfg workerConfig, leases map[string]string) map[string]any {
	return map[string]any{
		"worker_id": cfg.ID, "resource_class": cfg.ResourceClass, "capabilities": cfg.Capabilities,
		"max_concurrency": cfg.MaxConcurrency, "version": workerVersion, "hostname": cfg.Hostname,
		"active_leases": leases,
	}
}

func register(c *http.Client, cfg workerConfig) error {
	return postJSON(c, cfg.Server+"/api/v1/worker/register", cfg.Token, workerBody(cfg, nil), nil, http.StatusOK)
}

func heartbeatLoop(ctx context.Context, c *http.Client, cfg workerConfig, leases *leaseSet) {
	t := time.NewTicker(cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var state core.WorkerState
			if err := postJSON(c, cfg.Server+"/api/v1/worker/heartbeat", cfg.Token, workerBody(cfg, leases.snapshot()), &state, http.StatusOK); err != nil {
				log.Printf("heartbeat: %v", err)
			}
		}
	}
}

func claim(c *http.Client, cfg workerConfig) (*core.Job, error) {
	body, _ := json.Marshal(workerBody(cfg, nil))
	req, _ := http.NewRequest("POST", cfg.Server+"/api/v1/worker/claim", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	var j core.Job
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func run(ctx context.Context, c *http.Client, cfg workerConfig, j *core.Job) (json.RawMessage, string) {
	switch j.Type {
	case "vector.relink":
		var p relinkPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err.Error()
		}
		out := relinkResult{TargetID: p.TargetID}
		for _, candidate := range p.Candidates {
			sim := vector.Cosine(p.Target, candidate.Vector)
			if sim >= p.MinSimilarity {
				out.Neighbors = append(out.Neighbors, neighbor{ID: candidate.ID, Similarity: sim})
			}
		}
		sort.Slice(out.Neighbors, func(i, k int) bool { return out.Neighbors[i].Similarity > out.Neighbors[k].Similarity })
		if p.K > 0 && len(out.Neighbors) > p.K {
			out.Neighbors = out.Neighbors[:p.K]
		}
		b, _ := json.Marshal(out)
		return b, ""
	case "model.embed":
		if !hasCap(cfg.Capabilities, "model.embed") {
			return nil, "worker lacks model.embed capability"
		}
		var p modelEmbedPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err.Error()
		}
		out, err := ollamaEmbed(ctx, c, cfg, p)
		if err != nil {
			return nil, err.Error()
		}
		b, _ := json.Marshal(out)
		return b, ""
	case "model.chat":
		if !hasCap(cfg.Capabilities, "model.chat") {
			return nil, "worker lacks model.chat capability"
		}
		var p modelChatPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err.Error()
		}
		out, err := ollamaChat(ctx, c, cfg, p)
		if err != nil {
			return nil, err.Error()
		}
		b, _ := json.Marshal(out)
		return b, ""
	default:
		return nil, "unsupported job type: " + j.Type
	}
}

func ollamaEmbed(ctx context.Context, c *http.Client, cfg workerConfig, p modelEmbedPayload) (modelResult, error) {
	if cfg.OllamaURL == "" {
		return modelResult{}, fmt.Errorf("NEUROFORGE_WORKER_OLLAMA_URL is required for model.embed")
	}
	model := firstNonEmpty(p.Model, cfg.OllamaEmbedModel)
	if model == "" || strings.TrimSpace(p.Text) == "" {
		return modelResult{}, fmt.Errorf("embedding model and text are required")
	}
	body := map[string]any{"model": model, "input": p.Text, "keep_alive": cfg.OllamaKeepAlive}
	var resp struct {
		Embeddings      [][]float32 `json:"embeddings"`
		PromptEvalCount int64       `json:"prompt_eval_count"`
	}
	if err := postOllama(ctx, c, cfg.OllamaURL+"/api/embed", cfg.OllamaAPIKey, body, &resp); err != nil {
		return modelResult{}, err
	}
	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
		return modelResult{}, fmt.Errorf("ollama embedding response is empty")
	}
	return modelResult{Vector: resp.Embeddings[0], Usage: modelUsage{InputTokens: resp.PromptEvalCount}, Provider: "ollama", Model: model, NodeID: cfg.ID}, nil
}

func ollamaChat(ctx context.Context, c *http.Client, cfg workerConfig, p modelChatPayload) (modelResult, error) {
	if cfg.OllamaURL == "" {
		return modelResult{}, fmt.Errorf("NEUROFORGE_WORKER_OLLAMA_URL is required for model.chat")
	}
	model := firstNonEmpty(p.Model, cfg.OllamaChatModel)
	if model == "" || strings.TrimSpace(p.Input) == "" {
		return modelResult{}, fmt.Errorf("chat model and input are required")
	}
	messages := []map[string]string{}
	if strings.TrimSpace(p.Instructions) != "" {
		messages = append(messages, map[string]string{"role": "system", "content": p.Instructions})
	}
	messages = append(messages, map[string]string{"role": "user", "content": p.Input})
	body := map[string]any{"model": model, "messages": messages, "stream": false, "keep_alive": cfg.OllamaKeepAlive}
	if p.JSONMode {
		body["format"] = "json"
	}
	options := map[string]any{}
	if cfg.OllamaNumCtx > 0 {
		options["num_ctx"] = cfg.OllamaNumCtx
	}
	if p.MaxOutput > 0 {
		options["num_predict"] = p.MaxOutput
	}
	if len(options) > 0 {
		body["options"] = options
	}
	var resp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int64 `json:"prompt_eval_count"`
		EvalCount       int64 `json:"eval_count"`
	}
	if err := postOllama(ctx, c, cfg.OllamaURL+"/api/chat", cfg.OllamaAPIKey, body, &resp); err != nil {
		return modelResult{}, err
	}
	return modelResult{Text: strings.TrimSpace(resp.Message.Content), Usage: modelUsage{InputTokens: resp.PromptEvalCount, OutputTokens: resp.EvalCount}, Provider: "ollama", Model: model, NodeID: cfg.ID}, nil
}

func postOllama(ctx context.Context, c *http.Client, url, apiKey string, body any, out any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(apiKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode ollama response: %w", err)
	}
	return nil
}

func complete(c *http.Client, cfg workerConfig, jobID, leaseToken string, result json.RawMessage, jobErr string) error {
	body := map[string]any{"worker_id": cfg.ID, "job_id": jobID, "lease_token": leaseToken, "result": result, "error": jobErr}
	return postJSON(c, cfg.Server+"/api/v1/worker/complete", cfg.Token, body, nil, http.StatusOK)
}

func postJSON(c *http.Client, url, token string, body any, out any, want int) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != want {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil && len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return err
		}
	}
	return nil
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "worker"
	}
	return h
}

func csv(v string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range strings.Split(v, ",") {
		x = strings.ToLower(strings.TrimSpace(x))
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func hasCap(caps []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, x := range caps {
		if strings.ToLower(strings.TrimSpace(x)) == want {
			return true
		}
	}
	return false
}

func lowerDefault(v, def string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return def
	}
	return v
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

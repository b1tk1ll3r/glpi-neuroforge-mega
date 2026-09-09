package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

const providerName = "ollama-pool"

type NodeConfig struct {
	Name   string
	URL    string
	Weight int
}

type PoolConfig struct {
	APIKey                 string
	Nodes                  []NodeConfig
	RoutingMode            string
	NodeMaxInflight        int
	HealthInterval         time.Duration
	FailureCooldown        time.Duration
	NodeRequestTimeout     time.Duration
	FailoverEnabled        bool
	FailoverAttempts       int
	RequireSameModelDigest bool
	RequireEmbeddingModel  bool
	Model                  string
	EmbeddingModel         string
}

type requestMeta struct {
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int64  `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int64  `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	DoneReason         string `json:"done_reason"`
}

type poolNode struct {
	name     string
	baseURL  string
	weight   int
	sem      chan struct{}
	inflight atomic.Int64
	requests atomic.Uint64
	failures atomic.Uint64

	mu                    sync.RWMutex
	healthy               bool
	compatible            bool
	chatDigest            string
	embeddingDigest       string
	lastCheck             time.Time
	lastSuccess           time.Time
	cooldownUntil         time.Time
	consecutiveFailures   int
	lastError             string
	averageDurationMS     float64
	lastRequestDurationMS int64
}

func (n *poolNode) acquire() (int64, bool) {
	select {
	case n.sem <- struct{}{}:
		return n.inflight.Add(1), true
	default:
		return n.inflight.Load(), false
	}
}

func (n *poolNode) release() {
	n.inflight.Add(-1)
	<-n.sem
}

func (n *poolNode) isEligible(now time.Time, stage string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if !n.healthy || !n.compatible || now.Before(n.cooldownUntil) {
		return false
	}
	if stage == "embedding" && n.embeddingDigest == "" {
		return false
	}
	return true
}

func (n *poolNode) recordRequest(duration time.Duration, ok bool, retryable bool, err error) {
	n.requests.Add(1)
	ms := duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	n.mu.Lock()
	n.lastRequestDurationMS = ms
	if ok {
		if n.averageDurationMS == 0 {
			n.averageDurationMS = float64(ms)
		} else {
			n.averageDurationMS = n.averageDurationMS*0.8 + float64(ms)*0.2
		}
		n.lastSuccess = time.Now()
		n.consecutiveFailures = 0
		n.lastError = ""
		n.cooldownUntil = time.Time{}
	} else {
		n.failures.Add(1)
		n.consecutiveFailures++
		if err != nil {
			n.lastError = err.Error()
		}
		if retryable {
			// The caller applies the configured cooldown after releasing the lock.
		}
	}
	n.mu.Unlock()
}

func (n *poolNode) status(maxInflight int) model.OllamaNodeStatus {
	n.mu.RLock()
	defer n.mu.RUnlock()
	now := time.Now()
	return model.OllamaNodeStatus{
		Name: n.name, URL: n.baseURL, Weight: n.weight,
		Healthy: n.healthy, Compatible: n.compatible,
		Available: n.healthy && n.compatible && !now.Before(n.cooldownUntil) && n.inflight.Load() < int64(maxInflight),
		InFlight:  n.inflight.Load(), MaxInFlight: maxInflight,
		ChatModelDigest: n.chatDigest, EmbeddingModelDigest: n.embeddingDigest,
		LastCheck: n.lastCheck, LastSuccess: n.lastSuccess, CooldownUntil: n.cooldownUntil,
		ConsecutiveFailures: n.consecutiveFailures, LastError: n.lastError,
		Requests: n.requests.Load(), Failures: n.failures.Load(),
		AverageDurationMS: n.averageDurationMS, LastRequestDurationMS: n.lastRequestDurationMS,
	}
}

type Pool struct {
	cfg     PoolConfig
	nodes   []*poolNode
	http    *http.Client
	rr      atomic.Uint64
	refresh sync.Mutex
	started atomic.Bool
}

func newPool(cfg PoolConfig) (*Pool, error) {
	if len(cfg.Nodes) == 0 {
		return nil, errors.New("at least one Ollama node is required")
	}
	if len(cfg.Nodes) > 64 {
		return nil, errors.New("at most 64 Ollama nodes are supported")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("Ollama chat model must not be empty")
	}
	if cfg.RequireEmbeddingModel && strings.TrimSpace(cfg.EmbeddingModel) == "" {
		return nil, errors.New("Ollama embedding model must not be empty when it is required")
	}
	if cfg.NodeMaxInflight <= 0 {
		cfg.NodeMaxInflight = 1
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = 15 * time.Second
	}
	if cfg.FailureCooldown < 0 {
		cfg.FailureCooldown = 0
	}
	if cfg.NodeRequestTimeout <= 0 {
		cfg.NodeRequestTimeout = 10 * time.Minute
	}
	if cfg.FailoverAttempts <= 0 {
		cfg.FailoverAttempts = len(cfg.Nodes)
	}
	if cfg.FailoverAttempts > len(cfg.Nodes) {
		cfg.FailoverAttempts = len(cfg.Nodes)
	}
	cfg.RoutingMode = strings.ToLower(strings.TrimSpace(cfg.RoutingMode))
	if cfg.RoutingMode == "" {
		cfg.RoutingMode = "least_inflight"
	}
	switch cfg.RoutingMode {
	case "least_inflight", "round_robin", "weighted", "fastest_recent":
	default:
		return nil, fmt.Errorf("unsupported Ollama routing mode %q", cfg.RoutingMode)
	}
	p := &Pool{cfg: cfg, http: &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: cfg.NodeRequestTimeout,
	}}}
	seenNames := map[string]struct{}{}
	seenURLs := map[string]struct{}{}
	for i, c := range cfg.Nodes {
		base := strings.TrimRight(strings.TrimSpace(c.URL), "/")
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid Ollama node URL %q", c.URL)
		}
		if _, ok := seenURLs[base]; ok {
			return nil, fmt.Errorf("duplicate Ollama node URL %q", base)
		}
		seenURLs[base] = struct{}{}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = u.Hostname()
			if name == "" {
				name = fmt.Sprintf("ollama-%d", i+1)
			}
		}
		if _, ok := seenNames[name]; ok {
			name = fmt.Sprintf("%s-%d", name, i+1)
		}
		seenNames[name] = struct{}{}
		weight := c.Weight
		if weight <= 0 {
			weight = 1
		}
		if weight > 100 {
			return nil, fmt.Errorf("Ollama node weight for %q must be between 1 and 100", name)
		}
		p.nodes = append(p.nodes, &poolNode{name: name, baseURL: base, weight: weight, sem: make(chan struct{}, cfg.NodeMaxInflight)})
	}
	return p, nil
}

func (p *Pool) Start(ctx context.Context) {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	go func() {
		p.refreshAll(ctx)
		ticker := time.NewTicker(p.cfg.HealthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.refreshAll(ctx)
			}
		}
	}()
}

func (p *Pool) Ping(ctx context.Context) error {
	p.refreshAll(ctx)
	for _, n := range p.nodes {
		if n.isEligible(time.Now(), "") {
			return nil
		}
	}
	return p.unavailableError()
}

func (p *Pool) NodeStatuses() []model.OllamaNodeStatus {
	out := make([]model.OllamaNodeStatus, 0, len(p.nodes))
	for _, n := range p.nodes {
		out = append(out, n.status(p.cfg.NodeMaxInflight))
	}
	return out
}

func (p *Pool) refreshAll(ctx context.Context) {
	// The first request may arrive while Start is still performing the initial
	// health scan. Serialize callers instead of returning early, otherwise a
	// healthy pool can briefly look empty during startup.
	p.refresh.Lock()
	defer p.refresh.Unlock()

	type result struct {
		node            *poolNode
		healthy         bool
		chatDigest      string
		embeddingDigest string
		err             error
	}
	ch := make(chan result, len(p.nodes))
	for _, n := range p.nodes {
		go func(n *poolNode) {
			checkCtx := ctx
			cancel := func() {}
			timeout := p.cfg.NodeRequestTimeout
			if timeout <= 0 || timeout > 10*time.Second {
				timeout = 10 * time.Second
			}
			checkCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
			chatDigest, embeddingDigest, err := p.checkNode(checkCtx, n)
			ch <- result{node: n, healthy: err == nil, chatDigest: chatDigest, embeddingDigest: embeddingDigest, err: err}
		}(n)
	}
	results := make([]result, 0, len(p.nodes))
	for range p.nodes {
		results = append(results, <-ch)
	}

	chatDigest, chatConflict := commonDigest(results, func(r result) (string, bool) { return r.chatDigest, r.healthy })
	embedDigest, embedConflict := commonDigest(results, func(r result) (string, bool) { return r.embeddingDigest, r.healthy && r.embeddingDigest != "" })
	now := time.Now()
	for _, r := range results {
		n := r.node
		n.mu.Lock()
		previousHealthy := n.healthy
		previousCompatible := n.compatible
		previousError := n.lastError
		n.lastCheck = now
		n.healthy = r.healthy
		n.chatDigest = r.chatDigest
		n.embeddingDigest = r.embeddingDigest
		n.compatible = r.healthy
		if r.healthy && p.cfg.RequireSameModelDigest {
			switch {
			case chatConflict:
				n.compatible = false
				r.err = fmt.Errorf("chat model digests differ inside the pool; node digest=%s", r.chatDigest)
			case chatDigest != "" && r.chatDigest != chatDigest:
				n.compatible = false
				r.err = fmt.Errorf("chat model digest differs from pool digest: node=%s pool=%s", r.chatDigest, chatDigest)
			}
			// Even when chat-only nodes are allowed, all nodes that can serve
			// embeddings must expose the same embedding model digest.
			switch {
			case embedConflict:
				n.compatible = false
				r.err = fmt.Errorf("embedding model digests differ inside the pool; node digest=%s", r.embeddingDigest)
			case r.embeddingDigest != "" && embedDigest != "" && r.embeddingDigest != embedDigest:
				n.compatible = false
				r.err = fmt.Errorf("embedding model digest differs from pool digest: node=%s pool=%s", r.embeddingDigest, embedDigest)
			}
		}
		if r.err != nil {
			n.lastError = r.err.Error()
		} else {
			n.lastError = ""
			n.lastSuccess = now
			n.consecutiveFailures = 0
		}
		currentHealthy := n.healthy
		currentCompatible := n.compatible
		currentError := n.lastError
		n.mu.Unlock()
		if previousHealthy != currentHealthy || previousCompatible != currentCompatible || previousError != currentError {
			if currentHealthy && currentCompatible {
				slog.Info("Ollama node available", "node", n.name, "url", n.baseURL, "chat_digest", r.chatDigest, "embedding_digest", r.embeddingDigest)
			} else {
				slog.Warn("Ollama node unavailable", "node", n.name, "url", n.baseURL, "healthy", currentHealthy, "compatible", currentCompatible, "error", currentError)
			}
		}
	}
}

func commonDigest[T any](results []T, getter func(T) (string, bool)) (string, bool) {
	common := ""
	for _, r := range results {
		digest, include := getter(r)
		digest = strings.TrimSpace(digest)
		if !include || digest == "" {
			continue
		}
		if common == "" {
			common = digest
			continue
		}
		if digest != common {
			return "", true
		}
	}
	return common, false
}

func (p *Pool) checkNode(ctx context.Context, n *poolNode) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.baseURL+"/api/tags", nil)
	if err != nil {
		return "", "", err
	}
	setBearer(req, p.cfg.APIKey)
	resp, err := p.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return "", "", fmt.Errorf("decode /api/tags: %w", err)
	}
	find := func(wanted string) string {
		wanted = strings.TrimSpace(wanted)
		for _, m := range tags.Models {
			if modelNameMatches(wanted, m.Name) || modelNameMatches(wanted, m.Model) {
				return strings.TrimSpace(m.Digest)
			}
		}
		return ""
	}
	chat := find(p.cfg.Model)
	if chat == "" {
		return "", "", fmt.Errorf("model %q is not installed", p.cfg.Model)
	}
	embed := ""
	if strings.TrimSpace(p.cfg.EmbeddingModel) != "" {
		embed = find(p.cfg.EmbeddingModel)
		if p.cfg.RequireEmbeddingModel && embed == "" {
			return "", "", fmt.Errorf("embedding model %q is not installed", p.cfg.EmbeddingModel)
		}
	}
	return chat, embed, nil
}

func modelNameMatches(wanted, got string) bool {
	wanted = strings.TrimSpace(wanted)
	got = strings.TrimSpace(got)
	if wanted == got {
		return true
	}
	if !strings.Contains(wanted, ":") && strings.TrimSuffix(got, ":latest") == wanted {
		return true
	}
	if !strings.Contains(got, ":") && strings.TrimSuffix(wanted, ":latest") == got {
		return true
	}
	return false
}

func (p *Pool) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if !p.anyKnownHealthy() {
		p.refreshAll(ctx)
	}
	attemptLimit := 1
	if p.cfg.FailoverEnabled {
		attemptLimit = p.cfg.FailoverAttempts
		if attemptLimit <= 0 || attemptLimit > len(p.nodes) {
			attemptLimit = len(p.nodes)
		}
	}
	attempted := map[string]struct{}{}
	var errs []error
	for attempt := 1; attempt <= attemptLimit; attempt++ {
		n, inflight, selectErr := p.selectNode(ctx, attempted, requestStage(ctx))
		if selectErr != nil {
			errs = append(errs, selectErr)
			break
		}
		attempted[n.name] = struct{}{}
		started := time.Now()
		stage := requestStage(ctx)
		status, raw, reqErr := p.doPost(ctx, n, path, body)
		if reqErr == nil && len(raw) > 0 {
			var envelope struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(raw, &envelope) == nil && strings.TrimSpace(envelope.Error) != "" {
				reqErr = fmt.Errorf("Ollama %s returned an error from %s: %s", path, n.name, strings.TrimSpace(envelope.Error))
			}
		}
		if reqErr == nil && out != nil {
			if len(raw) == 0 {
				reqErr = errors.New("empty Ollama response")
			} else if decodeErr := json.Unmarshal(raw, out); decodeErr != nil {
				reqErr = fmt.Errorf("decode Ollama %s response from %s: %w", path, n.name, decodeErr)
			}
		}
		duration := time.Since(started)
		n.release()
		retryable := isRetryable(reqErr, status)
		// A 2xx response that cannot be decoded is safe to fail over because no
		// application decision was accepted from this node.
		if reqErr != nil && status/100 == 2 {
			retryable = true
		}
		ok := reqErr == nil
		n.recordRequest(duration, ok, retryable, reqErr)
		if !ok && retryable && p.cfg.FailureCooldown > 0 {
			n.mu.Lock()
			n.cooldownUntil = time.Now().Add(p.cfg.FailureCooldown)
			n.mu.Unlock()
		}
		meta := requestMeta{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &meta)
		}
		recordTraceAttempt(ctx, model.OllamaRequestAttempt{
			Attempt: attempt, Stage: stage, Path: path, NodeName: n.name, NodeURL: n.baseURL,
			ModelDigest: n.digestForStage(stage), StartedAt: started, DurationMS: duration.Milliseconds(),
			InflightAtStart: inflight, HTTPStatus: status, Outcome: outcomeText(reqErr), Retryable: retryable,
			Error: errorText(reqErr), TotalDurationNS: meta.TotalDuration, LoadDurationNS: meta.LoadDuration,
			PromptEvalCount: meta.PromptEvalCount, PromptEvalDuration: meta.PromptEvalDuration,
			EvalCount: meta.EvalCount, EvalDuration: meta.EvalDuration, DoneReason: meta.DoneReason,
		})
		if reqErr == nil {
			markTraceSuccess(ctx, n.name, n.baseURL)
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", n.name, reqErr))
		if retryable && p.cfg.FailoverEnabled && attempt < attemptLimit && ctx.Err() == nil {
			slog.Warn("Ollama request failed; trying another node", "stage", stage, "path", path, "failed_node", n.name, "attempt", attempt, "max_attempts", attemptLimit, "error", reqErr)
		}
		if !retryable || !p.cfg.FailoverEnabled || attempt == attemptLimit || ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return p.unavailableError()
	}
	return errors.Join(errs...)
}

func (n *poolNode) digestForStage(stage string) string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if stage == "embedding" {
		return n.embeddingDigest
	}
	return n.chatDigest
}

func (p *Pool) doPost(ctx context.Context, n *poolNode, path string, body []byte) (int, []byte, error) {
	reqCtx := ctx
	cancel := func() {}
	if p.cfg.NodeRequestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, p.cfg.NodeRequestTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, n.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setBearer(req, p.cfg.APIKey)
	resp, err := p.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, raw, fmt.Errorf("Ollama %s failed: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return resp.StatusCode, raw, nil
}

func setBearer(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (p *Pool) selectNode(ctx context.Context, excluded map[string]struct{}, stage string) (*poolNode, int64, error) {
	for {
		nodes := p.orderedCandidates(excluded, stage)
		if len(nodes) == 0 {
			return nil, 0, p.unavailableError()
		}
		for _, n := range nodes {
			if inflight, ok := n.acquire(); ok {
				return n, inflight, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(15 * time.Millisecond):
		}
	}
}

func (p *Pool) orderedCandidates(excluded map[string]struct{}, stage string) []*poolNode {
	now := time.Now()
	out := make([]*poolNode, 0, len(p.nodes))
	for _, n := range p.nodes {
		if _, skip := excluded[n.name]; skip {
			continue
		}
		if n.isEligible(now, stage) {
			out = append(out, n)
		}
	}
	if len(out) <= 1 {
		return out
	}
	switch p.cfg.RoutingMode {
	case "round_robin":
		start := int(p.rr.Add(1)-1) % len(out)
		rotated := append([]*poolNode(nil), out[start:]...)
		rotated = append(rotated, out[:start]...)
		return rotated
	case "fastest_recent":
		sort.SliceStable(out, func(i, j int) bool {
			ai, aj := out[i].status(p.cfg.NodeMaxInflight), out[j].status(p.cfg.NodeMaxInflight)
			// Probe nodes without measurements before permanently preferring a
			// known node. This prevents new/recovered nodes from starving.
			if ai.AverageDurationMS == 0 && aj.AverageDurationMS != 0 {
				return true
			}
			if aj.AverageDurationMS == 0 && ai.AverageDurationMS != 0 {
				return false
			}
			if ai.AverageDurationMS == aj.AverageDurationMS {
				if ai.InFlight == aj.InFlight {
					return ai.Requests < aj.Requests
				}
				return ai.InFlight < aj.InFlight
			}
			return ai.AverageDurationMS < aj.AverageDurationMS
		})
	case "weighted":
		sort.SliceStable(out, func(i, j int) bool {
			// Weighted least-request scheduling also works for serial traffic;
			// using only current inflight values would permanently select the
			// highest-weight node whenever requests do not overlap.
			si := float64(out[i].requests.Load()+uint64(out[i].inflight.Load())+1) / float64(maxInt(out[i].weight, 1))
			sj := float64(out[j].requests.Load()+uint64(out[j].inflight.Load())+1) / float64(maxInt(out[j].weight, 1))
			if math.Abs(si-sj) < 1e-9 {
				return out[i].name < out[j].name
			}
			return si < sj
		})
	default: // least_inflight
		sort.SliceStable(out, func(i, j int) bool {
			ii, ij := out[i].inflight.Load(), out[j].inflight.Load()
			if ii == ij {
				ri, rj := out[i].requests.Load(), out[j].requests.Load()
				if ri != rj {
					return ri < rj
				}
				si, sj := out[i].status(p.cfg.NodeMaxInflight), out[j].status(p.cfg.NodeMaxInflight)
				if si.AverageDurationMS != sj.AverageDurationMS && si.AverageDurationMS > 0 && sj.AverageDurationMS > 0 {
					return si.AverageDurationMS < sj.AverageDurationMS
				}
				return out[i].name < out[j].name
			}
			return ii < ij
		})
	}
	return out
}

func (p *Pool) anyKnownHealthy() bool {
	for _, n := range p.nodes {
		n.mu.RLock()
		known := !n.lastCheck.IsZero()
		healthy := n.healthy && n.compatible
		n.mu.RUnlock()
		if known && healthy {
			return true
		}
	}
	return false
}

func (p *Pool) unavailableError() error {
	statuses := p.NodeStatuses()
	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		detail := s.LastError
		if detail == "" {
			detail = "not available"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", s.Name, detail))
	}
	return fmt.Errorf("no compatible Ollama node available (%s)", strings.Join(parts, "; "))
}

func isRetryable(err error, status int) bool {
	if err == nil {
		return false
	}
	if status == 0 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return true
	}
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout || status >= 500
}

func outcomeText(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Trace is a concurrency-safe collector attached to one logical analysis context.
type Trace struct {
	mu   sync.Mutex
	data model.OllamaProviderTrace
}

type traceKey struct{}
type stageKey struct{}

func WithTrace(ctx context.Context, routingMode string) (context.Context, *Trace) {
	t := &Trace{data: model.OllamaProviderTrace{Provider: providerName, RoutingMode: routingMode}}
	return context.WithValue(ctx, traceKey{}, t), t
}

func withStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, stageKey{}, strings.TrimSpace(stage))
}

func requestStage(ctx context.Context) string {
	v, _ := ctx.Value(stageKey{}).(string)
	return v
}

func recordTraceAttempt(ctx context.Context, a model.OllamaRequestAttempt) {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	if t == nil {
		return
	}
	t.mu.Lock()
	a.Attempt = len(t.data.Attempts) + 1
	if len(t.data.Attempts) > 0 {
		previous := t.data.Attempts[len(t.data.Attempts)-1]
		if previous.NodeName != a.NodeName && previous.Retryable && previous.Outcome == "error" {
			t.data.FailoverUsed = true
		}
	}
	t.data.Attempts = append(t.data.Attempts, a)
	t.data.AttemptCount = len(t.data.Attempts)
	t.mu.Unlock()
}

func markTraceSuccess(ctx context.Context, node, nodeURL string) {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	if t == nil {
		return
	}
	t.mu.Lock()
	t.data.SelectedNode = node
	t.data.SelectedURL = nodeURL
	t.mu.Unlock()
}

func (t *Trace) Snapshot() model.OllamaProviderTrace {
	if t == nil {
		return model.OllamaProviderTrace{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.data
	out.Attempts = append([]model.OllamaRequestAttempt(nil), t.data.Attempts...)
	return out
}

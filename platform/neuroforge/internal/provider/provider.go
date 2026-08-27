package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

type Usage struct {
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
}

type ChatResult struct {
	Text     string
	Usage    Usage
	Provider string
	Model    string
	NodeID   string
}

type EmbedResult struct {
	Vector   []float32
	Usage    Usage
	Provider string
	Model    string
	NodeID   string
}

type Router struct {
	store *store.Store
	http  *http.Client
	rr    atomic.Uint64
}

func NewRouter(s *store.Store) *Router {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	// Do not set ResponseHeaderTimeout here. With Ollama stream=false the response
	// headers may arrive only after a long model generation. Inference deadlines
	// are controlled explicitly per Ollama node; 0 means unlimited.
	tr.ResponseHeaderTimeout = 0
	return &Router{store: s, http: &http.Client{Transport: tr}}
}

func cleanBase(v string) string { return strings.TrimRight(strings.TrimSpace(v), "/") }

func (r *Router) ollamaCandidates() []core.OllamaServer {
	c := r.store.Config()
	out := []core.OllamaServer{}
	for _, o := range c.Ollama {
		if o.Enabled {
			if o.Weight < 1 {
				o.Weight = 1
			}
			for i := 0; i < o.Weight; i++ {
				out = append(out, o)
			}
		}
	}
	return out
}

func (r *Router) ollamaOrder() []core.OllamaServer {
	all := r.ollamaCandidates()
	if len(all) == 0 {
		return nil
	}
	start := int(r.rr.Add(1)-1) % len(all)
	seen := map[string]bool{}
	out := make([]core.OllamaServer, 0, len(all))
	for i := 0; i < len(all); i++ {
		o := all[(start+i)%len(all)]
		key := o.ID + "|" + o.BaseURL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out
}

func (r *Router) ollamaOrderFor(nodeID string) []core.OllamaServer {
	if strings.TrimSpace(nodeID) == "" {
		return r.ollamaOrder()
	}
	for _, o := range r.store.Config().Ollama {
		if o.Enabled && o.ID == nodeID {
			return []core.OllamaServer{o}
		}
	}
	return nil
}

func (r *Router) Chat(ctx context.Context, providerName, model, instructions, input string, maxOutput int) (ChatResult, error) {
	cfg := r.store.Config()
	nodeID := ""
	if providerName == "" || providerName == "auto" {
		if model == "" {
			model = cfg.Routing.ChatModel
		}
		nodeID = cfg.Routing.ChatNodeID
	}
	return r.ChatOn(ctx, providerName, model, nodeID, instructions, input, maxOutput)
}

// ChatOn behaves like Chat but can pin Ollama inference to one configured node.
// A non-empty nodeID is strict: NeuroForge will not silently use another Ollama
// server for that role. OpenAI ignores nodeID.
func (r *Router) ChatOn(ctx context.Context, providerName, model, nodeID, instructions, input string, maxOutput int) (ChatResult, error) {
	return r.chatOn(ctx, providerName, model, nodeID, instructions, input, maxOutput, false)
}

// ChatJSONOn requests provider-native JSON output where the provider supports it.
// Ollama's /api/chat "format":"json" keeps structured-output calls syntactically
// constrained before NeuroForge applies its own strict schema and evidence gates.
// Providers without a native mode continue through the normal transport and are
// still validated by the caller's strict JSON decoder.
func (r *Router) ChatJSONOn(ctx context.Context, providerName, model, nodeID, instructions, input string, maxOutput int) (ChatResult, error) {
	return r.chatOn(ctx, providerName, model, nodeID, instructions, input, maxOutput, true)
}

func (r *Router) chatOn(ctx context.Context, providerName, model, nodeID, instructions, input string, maxOutput int, jsonMode bool) (ChatResult, error) {
	cfg := r.store.Config()
	if providerName == "" || providerName == "auto" {
		providerName = cfg.Routing.ChatProvider
	}
	if providerName == "" {
		providerName = "auto"
	}
	if providerName == "ollama" || providerName == "auto" {
		var lastErr error
		order := r.ollamaOrderFor(nodeID)
		if nodeID != "" && len(order) == 0 {
			lastErr = fmt.Errorf("configured Ollama node %q is missing or disabled", nodeID)
		}
		for _, o := range order {
			m := model
			if m == "" {
				m = o.ChatModel
			}
			if strings.TrimSpace(m) == "" {
				lastErr = fmt.Errorf("ollama %s has no chat_model configured", o.Name)
				continue
			}
			res, err := r.chatOllama(ctx, o, m, instructions, input, maxOutput, jsonMode)
			if err == nil {
				return res, nil
			}
			lastErr = err
		}
		if providerName == "ollama" || nodeID != "" {
			if lastErr == nil {
				lastErr = errors.New("no enabled Ollama server")
			}
			return ChatResult{}, lastErr
		}
	}
	if providerName == "openai" || providerName == "auto" {
		if !cfg.OpenAI.Enabled {
			return ChatResult{}, errors.New("OpenAI disabled and no Ollama route succeeded")
		}
		m := model
		if m == "" {
			m = cfg.OpenAI.ChatModel
		}
		return r.chatOpenAI(ctx, m, instructions, input, maxOutput)
	}
	return ChatResult{}, fmt.Errorf("unknown chat provider %q", providerName)
}

func (r *Router) Embed(ctx context.Context, providerName, model, text string) (EmbedResult, error) {
	cfg := r.store.Config()
	nodeID := ""
	if providerName == "" || providerName == "auto" {
		if model == "" {
			model = cfg.Routing.EmbeddingModel
		}
		nodeID = cfg.Routing.EmbeddingNodeID
	}
	return r.EmbedOn(ctx, providerName, model, nodeID, text)
}

// EmbedOn pins an embedding request to a configured Ollama node when nodeID is
// set. This is useful because a knowledge base must keep one embedding space.
func (r *Router) EmbedOn(ctx context.Context, providerName, model, nodeID, text string) (EmbedResult, error) {
	cfg := r.store.Config()
	if providerName == "" || providerName == "auto" {
		providerName = cfg.Routing.EmbeddingProvider
	}
	if providerName == "" {
		providerName = "auto"
	}
	if providerName == "ollama" || providerName == "auto" {
		var lastErr error
		order := r.ollamaOrderFor(nodeID)
		if nodeID != "" && len(order) == 0 {
			lastErr = fmt.Errorf("configured Ollama node %q is missing or disabled", nodeID)
		}
		for _, o := range order {
			m := model
			if m == "" {
				m = o.EmbeddingModel
			}
			if strings.TrimSpace(m) == "" {
				lastErr = fmt.Errorf("ollama %s has no embedding_model configured", o.Name)
				continue
			}
			res, err := r.embedOllama(ctx, o, m, text)
			if err == nil {
				return res, nil
			}
			lastErr = err
		}
		if providerName == "ollama" || nodeID != "" {
			if lastErr == nil {
				lastErr = errors.New("no enabled Ollama server")
			}
			return EmbedResult{}, lastErr
		}
	}
	if providerName == "openai" || providerName == "auto" {
		if !cfg.OpenAI.Enabled {
			return EmbedResult{}, errors.New("OpenAI disabled and no Ollama embedding route succeeded")
		}
		m := model
		if m == "" {
			m = cfg.OpenAI.EmbeddingModel
		}
		return r.embedOpenAI(ctx, m, text)
	}
	return EmbedResult{}, fmt.Errorf("unknown embedding provider %q", providerName)
}

func optionalTimeout(ctx context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
}

func ollamaThinkValue(v string) (any, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return nil, false
	case "off", "false", "0", "no":
		return false, true
	case "on", "true", "1", "yes":
		return true, true
	case "low", "medium", "high", "max":
		return strings.ToLower(strings.TrimSpace(v)), true
	default:
		return nil, false
	}
}

func (r *Router) chatOllama(ctx context.Context, o core.OllamaServer, model, instructions, input string, maxOutput int, jsonMode bool) (ChatResult, error) {
	messages := []map[string]string{}
	if instructions != "" {
		messages = append(messages, map[string]string{"role": "system", "content": instructions})
	}
	messages = append(messages, map[string]string{"role": "user", "content": input})
	body := map[string]any{"model": model, "messages": messages, "stream": false}
	if jsonMode {
		body["format"] = "json"
	}
	if strings.TrimSpace(o.ChatKeepAlive) != "" {
		body["keep_alive"] = strings.TrimSpace(o.ChatKeepAlive)
	}
	if think, ok := ollamaThinkValue(o.Think); ok {
		body["think"] = think
	}
	options := map[string]any{}
	if o.NumCtx > 0 {
		options["num_ctx"] = o.NumCtx
	}
	numPredict := o.NumPredict
	if numPredict <= 0 {
		numPredict = maxOutput
	}
	if numPredict > 0 {
		options["num_predict"] = numPredict
	}
	if len(options) > 0 {
		body["options"] = options
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int64 `json:"prompt_eval_count"`
		EvalCount       int64 `json:"eval_count"`
	}
	requestCtx, cancel := optionalTimeout(ctx, o.RequestTimeoutSeconds)
	defer cancel()
	if err := r.doJSON(requestCtx, "POST", cleanBase(o.BaseURL)+"/api/chat", "", body, &out); err != nil {
		return ChatResult{}, fmt.Errorf("ollama %s: %w", o.Name, err)
	}
	if strings.TrimSpace(out.Message.Content) == "" {
		return ChatResult{}, errors.New("ollama returned empty message")
	}
	return ChatResult{Text: out.Message.Content, Usage: Usage{InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount}, Provider: "ollama", Model: model, NodeID: o.ID}, nil
}

func (r *Router) embedOllama(ctx context.Context, o core.OllamaServer, model, text string) (EmbedResult, error) {
	body := map[string]any{"model": model, "input": text}
	if strings.TrimSpace(o.EmbeddingKeepAlive) != "" {
		body["keep_alive"] = strings.TrimSpace(o.EmbeddingKeepAlive)
	}
	var out struct {
		Embeddings      [][]float32 `json:"embeddings"`
		PromptEvalCount int64       `json:"prompt_eval_count"`
	}
	requestCtx, cancel := optionalTimeout(ctx, o.RequestTimeoutSeconds)
	defer cancel()
	if err := r.doJSON(requestCtx, "POST", cleanBase(o.BaseURL)+"/api/embed", "", body, &out); err != nil {
		return EmbedResult{}, fmt.Errorf("ollama %s: %w", o.Name, err)
	}
	if len(out.Embeddings) == 0 || len(out.Embeddings[0]) == 0 {
		return EmbedResult{}, errors.New("ollama returned no embedding")
	}
	return EmbedResult{Vector: out.Embeddings[0], Usage: Usage{InputTokens: out.PromptEvalCount}, Provider: "ollama", Model: model, NodeID: o.ID}, nil
}

func (r *Router) chatOpenAI(ctx context.Context, model, instructions, input string, maxOutput int) (ChatResult, error) {
	sec := r.store.Secrets()
	if sec.OpenAIAPIKey == "" {
		return ChatResult{}, errors.New("OpenAI API key missing")
	}
	cfg := r.store.Config()
	if maxOutput <= 0 {
		maxOutput = cfg.OpenAI.MaxOutputTokens
	}
	body := map[string]any{"model": model, "instructions": instructions, "input": input, "store": false}
	if maxOutput > 0 {
		body["max_output_tokens"] = maxOutput
	}
	var out struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			InputDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := r.doJSON(ctx, "POST", cleanBase(cfg.OpenAI.BaseURL)+"/v1/responses", sec.OpenAIAPIKey, body, &out); err != nil {
		return ChatResult{}, err
	}
	var parts []string
	for _, item := range out.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" && c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		return ChatResult{}, errors.New("OpenAI returned no output_text")
	}
	return ChatResult{Text: text, Usage: Usage{InputTokens: out.Usage.InputTokens, CachedTokens: out.Usage.InputDetails.CachedTokens, OutputTokens: out.Usage.OutputTokens}, Provider: "openai", Model: model, NodeID: "openai"}, nil
}

func (r *Router) embedOpenAI(ctx context.Context, model, text string) (EmbedResult, error) {
	sec := r.store.Secrets()
	if sec.OpenAIAPIKey == "" {
		return EmbedResult{}, errors.New("OpenAI API key missing")
	}
	cfg := r.store.Config()
	body := map[string]any{"model": model, "input": text, "encoding_format": "float"}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := r.doJSON(ctx, "POST", cleanBase(cfg.OpenAI.BaseURL)+"/v1/embeddings", sec.OpenAIAPIKey, body, &out); err != nil {
		return EmbedResult{}, err
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return EmbedResult{}, errors.New("OpenAI returned no embedding")
	}
	tokens := out.Usage.PromptTokens
	if tokens == 0 {
		tokens = out.Usage.TotalTokens
	}
	return EmbedResult{Vector: out.Data[0].Embedding, Usage: Usage{InputTokens: tokens}, Provider: "openai", Model: model, NodeID: "openai"}, nil
}

func (r *Router) doJSON(ctx context.Context, method, url, key string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (r *Router) Health(ctx context.Context) []map[string]any {
	cfg := r.store.Config()
	out := make([]map[string]any, 0, len(cfg.Ollama)+1)
	for _, o := range cfg.Ollama {
		entry := map[string]any{"provider": "ollama", "id": o.ID, "name": o.Name, "url": o.BaseURL, "enabled": o.Enabled}
		if !o.Enabled {
			entry["ok"] = false
			entry["error"] = "disabled"
			out = append(out, entry)
			continue
		}
		healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, _ := http.NewRequestWithContext(healthCtx, "GET", cleanBase(o.BaseURL)+"/api/tags", nil)
		resp, err := r.http.Do(req)
		if err != nil {
			cancel()
			entry["ok"] = false
			entry["error"] = err.Error()
		} else {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			cancel()
			entry["ok"] = resp.StatusCode >= 200 && resp.StatusCode < 300
			entry["status"] = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var tags struct {
					Models []struct {
						Name  string `json:"name"`
						Model string `json:"model"`
					} `json:"models"`
				}
				if json.Unmarshal(raw, &tags) == nil {
					models := make([]string, 0, len(tags.Models))
					for _, m := range tags.Models {
						name := m.Name
						if name == "" {
							name = m.Model
						}
						if name != "" {
							models = append(models, name)
						}
					}
					entry["models"] = models
				}
			}
		}
		out = append(out, entry)
	}
	sec := r.store.Secrets()
	out = append(out, map[string]any{"provider": "openai", "enabled": cfg.OpenAI.Enabled, "configured": sec.OpenAIAPIKey != "", "model": cfg.OpenAI.ChatModel})
	return out
}

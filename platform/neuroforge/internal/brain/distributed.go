package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

type distributedModelResult struct {
	Text   string    `json:"text,omitempty"`
	Vector []float32 `json:"vector,omitempty"`
	Usage  struct {
		InputTokens  int64 `json:"input_tokens"`
		CachedTokens int64 `json:"cached_tokens,omitempty"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	NodeID   string `json:"node_id"`
}

func (e *Engine) waitDistributedJob(ctx context.Context, jID string) (*distributedModelResult, error) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.store.CancelJob(jID, "caller context ended: "+ctx.Err().Error())
			return nil, ctx.Err()
		case <-t.C:
			j, ok := e.store.Job(jID)
			if !ok {
				return nil, errors.New("distributed job disappeared")
			}
			switch j.Status {
			case "done":
				var out distributedModelResult
				if err := json.Unmarshal(j.Result, &out); err != nil {
					return nil, fmt.Errorf("decode distributed model result: %w", err)
				}
				return &out, nil
			case "failed", "canceled":
				return nil, fmt.Errorf("distributed job %s: %s", j.Status, j.Error)
			}
		}
	}
}

func (e *Engine) distributedEmbed(ctx context.Context, model, text string) (provider.EmbedResult, bool, error) {
	cfg := e.store.Config()
	if !cfg.Worker.OffloadEmbeddings || !e.store.HasLiveWorker("gpu", "gpu", "model.embed") {
		return provider.EmbedResult{}, false, nil
	}
	wait := cfg.Worker.DistributedInferenceWaitS
	if wait <= 0 {
		wait = 180
	}
	j, err := e.store.EnqueueJobSpec(store.JobSpec{
		Type: "model.embed", Payload: map[string]any{"text": text, "model": model},
		Priority: 100, ResourceClass: "gpu", RequiredCapabilities: []string{"gpu", "model.embed"},
		MaxAttempts: 2, TimeoutSeconds: wait,
	})
	if err != nil {
		return provider.EmbedResult{}, true, err
	}
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(wait)*time.Second)
	defer cancel()
	out, err := e.waitDistributedJob(jobCtx, j.ID)
	if err != nil {
		return provider.EmbedResult{}, true, err
	}
	if len(out.Vector) == 0 {
		return provider.EmbedResult{}, true, errors.New("distributed embedding returned empty vector")
	}
	return provider.EmbedResult{Vector: out.Vector, Provider: out.Provider, Model: out.Model, NodeID: out.NodeID, Usage: provider.Usage{InputTokens: out.Usage.InputTokens, CachedTokens: out.Usage.CachedTokens, OutputTokens: out.Usage.OutputTokens}}, true, nil
}

func (e *Engine) distributedChat(ctx context.Context, model, instructions, input string, maxOutput int, jsonMode bool) (provider.ChatResult, bool, error) {
	cfg := e.store.Config()
	if !cfg.Worker.OffloadChat || !e.store.HasLiveWorker("gpu", "gpu", "model.chat") {
		return provider.ChatResult{}, false, nil
	}
	wait := cfg.Worker.DistributedInferenceWaitS
	if wait <= 0 {
		wait = 180
	}
	j, err := e.store.EnqueueJobSpec(store.JobSpec{
		Type: "model.chat", Payload: map[string]any{"instructions": instructions, "input": input, "model": model, "max_output": maxOutput, "json_mode": jsonMode},
		Priority: 100, ResourceClass: "gpu", RequiredCapabilities: []string{"gpu", "model.chat"},
		MaxAttempts: 2, TimeoutSeconds: wait,
	})
	if err != nil {
		return provider.ChatResult{}, true, err
	}
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(wait)*time.Second)
	defer cancel()
	out, err := e.waitDistributedJob(jobCtx, j.ID)
	if err != nil {
		return provider.ChatResult{}, true, err
	}
	return provider.ChatResult{Text: out.Text, Provider: out.Provider, Model: out.Model, NodeID: out.NodeID, Usage: provider.Usage{InputTokens: out.Usage.InputTokens, CachedTokens: out.Usage.CachedTokens, OutputTokens: out.Usage.OutputTokens}}, true, nil
}

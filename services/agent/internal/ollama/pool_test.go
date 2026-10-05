package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

func tagsResponse(w http.ResponseWriter, chatDigest, embedDigest string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
		{"name": "m:latest", "model": "m:latest", "digest": chatDigest},
		{"name": "e:latest", "model": "e:latest", "digest": embedDigest},
	}})
}

func categoryResponse(w http.ResponseWriter, id int64) {
	_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": `{"category":{"id":` + jsonNumber(id) + `,"confidence":0.9},"reason":"ok"}`}})
}

func jsonNumber(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newTestPool(t *testing.T, nodes []NodeConfig, routing string) *Client {
	t.Helper()
	c, err := NewPool(PoolConfig{
		Nodes: nodes, RoutingMode: routing, NodeMaxInflight: 1,
		HealthInterval: time.Minute, FailureCooldown: time.Second,
		NodeRequestTimeout: 2 * time.Second, FailoverEnabled: true, FailoverAttempts: len(nodes),
		RequireSameModelDigest: true, RequireEmbeddingModel: true, Model: "m", EmbeddingModel: "e",
	}, "m", "e", "de-DE", "formal", 128, time.Minute, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPoolFailsOverAndRecordsTrace(t *testing.T) {
	var badCalls atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		badCalls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	var goodCalls atomic.Int64
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		goodCalls.Add(1)
		categoryResponse(w, 1)
	}))
	defer good.Close()

	c := newTestPool(t, []NodeConfig{{Name: "a-bad", URL: bad.URL}, {Name: "b-good", URL: good.URL}}, "least_inflight")
	ctx, trace := WithTrace(context.Background(), c.RoutingMode())
	d, err := c.AnalyseCategory(ctx, model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Category.ID != 1 || badCalls.Load() != 1 || goodCalls.Load() != 1 {
		t.Fatalf("decision=%+v bad=%d good=%d", d, badCalls.Load(), goodCalls.Load())
	}
	got := trace.Snapshot()
	if !got.FailoverUsed || got.SelectedNode != "b-good" || len(got.Attempts) != 2 {
		t.Fatalf("unexpected trace: %+v", got)
	}
	if got.Attempts[0].HTTPStatus != http.StatusServiceUnavailable || !got.Attempts[0].Retryable {
		t.Fatalf("unexpected first attempt: %+v", got.Attempts[0])
	}
}

func TestPoolLeastInflightUsesFreeNode(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var callsA, callsB atomic.Int64
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		callsA.Add(1)
		once.Do(func() { close(started) })
		<-release
		categoryResponse(w, 1)
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		callsB.Add(1)
		categoryResponse(w, 1)
	}))
	defer b.Close()

	c := newTestPool(t, []NodeConfig{{Name: "a", URL: a.URL}, {Name: "b", URL: b.URL}}, "least_inflight")
	errCh := make(chan error, 2)
	go func() {
		_, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{})
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first node did not start")
	}
	go func() {
		_, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: 2}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{})
		errCh <- err
	}()
	deadline := time.Now().Add(time.Second)
	for callsB.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("least-inflight distribution a=%d b=%d", callsA.Load(), callsB.Load())
	}
}

func TestPoolRejectsMismatchedDigest(t *testing.T) {
	server := func(chatDigest string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tagsResponse(w, chatDigest, "embed-digest")
		}))
	}
	a := server("aaa")
	defer a.Close()
	b := server("bbb")
	defer b.Close()
	c, err := NewPool(PoolConfig{
		Nodes: []NodeConfig{{Name: "a", URL: a.URL}, {Name: "b", URL: b.URL}}, RoutingMode: "least_inflight", NodeMaxInflight: 1,
		HealthInterval: time.Minute, NodeRequestTimeout: time.Second, FailoverEnabled: true, FailoverAttempts: 2,
		RequireSameModelDigest: true, RequireEmbeddingModel: true, Model: "m", EmbeddingModel: "e",
	}, "m", "e", "de-DE", "formal", 128, time.Minute, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected pool with divergent model digests to fail closed")
	}
	statuses := c.NodeStatuses()
	for _, status := range statuses {
		if status.Compatible {
			t.Fatalf("mismatched node must be incompatible: %+v", statuses)
		}
	}
}

func TestPoolFirstRequestWaitsForInitialHealthScan(t *testing.T) {
	healthStarted := make(chan struct{})
	releaseHealth := make(chan struct{})
	var healthCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			if healthCalls.Add(1) == 1 {
				close(healthStarted)
				<-releaseHealth
			}
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		categoryResponse(w, 1)
	}))
	defer server.Close()

	c, err := NewPool(PoolConfig{
		Nodes: []NodeConfig{{Name: "node-1", URL: server.URL}}, RoutingMode: "least_inflight", NodeMaxInflight: 1,
		HealthInterval: time.Minute, NodeRequestTimeout: 2 * time.Second, FailoverEnabled: false, FailoverAttempts: 1,
		RequireSameModelDigest: true, RequireEmbeddingModel: true, Model: "m", EmbeddingModel: "e",
	}, "m", "e", "de-DE", "formal", 128, time.Minute, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	select {
	case <-healthStarted:
	case <-time.After(time.Second):
		t.Fatal("initial health scan did not start")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{})
		errCh <- err
	}()
	select {
	case err := <-errCh:
		t.Fatalf("request returned before health scan completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseHealth)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not continue after health scan")
	}
}

func TestPoolFailsOverOnInvalidOuterJSON(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":`))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		categoryResponse(w, 1)
	}))
	defer good.Close()

	c := newTestPool(t, []NodeConfig{{Name: "a-bad-json", URL: bad.URL}, {Name: "b-good", URL: good.URL}}, "least_inflight")
	ctx, trace := WithTrace(context.Background(), c.RoutingMode())
	if _, err := c.AnalyseCategory(ctx, model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{}); err != nil {
		t.Fatal(err)
	}
	got := trace.Snapshot()
	if !got.FailoverUsed || got.SelectedNode != "b-good" || len(got.Attempts) != 2 {
		t.Fatalf("unexpected trace: %+v", got)
	}
	if got.Attempts[0].Outcome != "error" || !got.Attempts[0].Retryable || got.Attempts[0].Error == "" {
		t.Fatalf("invalid JSON must be recorded as retryable error: %+v", got.Attempts[0])
	}
}

func TestPoolAllowsChatOnlyNodeButRoutesEmbeddingsToCapableNode(t *testing.T) {
	var chatOnlyEmbedCalls atomic.Int64
	chatOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "m:latest", "model": "m:latest", "digest": "chat-digest"}}})
			return
		}
		if r.URL.Path == "/api/embed" {
			chatOnlyEmbedCalls.Add(1)
		}
		categoryResponse(w, 1)
	}))
	defer chatOnly.Close()
	var embeddingCalls atomic.Int64
	embeddingNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		if r.URL.Path == "/api/embed" {
			embeddingCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{0.1, 0.2}}})
			return
		}
		categoryResponse(w, 1)
	}))
	defer embeddingNode.Close()

	c, err := NewPool(PoolConfig{
		Nodes:       []NodeConfig{{Name: "chat-only", URL: chatOnly.URL}, {Name: "embedding", URL: embeddingNode.URL}},
		RoutingMode: "least_inflight", NodeMaxInflight: 1, HealthInterval: time.Minute,
		NodeRequestTimeout: time.Second, FailoverEnabled: true, FailoverAttempts: 2,
		RequireSameModelDigest: true, RequireEmbeddingModel: false, Model: "m", EmbeddingModel: "e",
	}, "m", "e", "de-DE", "formal", 128, time.Minute, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	vectors, err := c.Embed(context.Background(), []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || embeddingCalls.Load() != 1 || chatOnlyEmbedCalls.Load() != 0 {
		t.Fatalf("vectors=%v embedding_calls=%d chat_only_embed_calls=%d", vectors, embeddingCalls.Load(), chatOnlyEmbedCalls.Load())
	}
}

func TestPoolLeastInflightBalancesSerialRequests(t *testing.T) {
	var callsA, callsB atomic.Int64
	server := func(calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/tags" {
				tagsResponse(w, "chat-digest", "embed-digest")
				return
			}
			calls.Add(1)
			categoryResponse(w, 1)
		}))
	}
	a := server(&callsA)
	defer a.Close()
	b := server(&callsB)
	defer b.Close()
	c := newTestPool(t, []NodeConfig{{Name: "a", URL: a.URL}, {Name: "b", URL: b.URL}}, "least_inflight")
	for i := 0; i < 4; i++ {
		if _, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: int64(i + 1)}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{}); err != nil {
			t.Fatal(err)
		}
	}
	if callsA.Load() != 2 || callsB.Load() != 2 {
		t.Fatalf("serial least-inflight distribution a=%d b=%d", callsA.Load(), callsB.Load())
	}
}

func TestPoolWeightedBalancesSerialRequestsByWeight(t *testing.T) {
	var callsA, callsB atomic.Int64
	server := func(calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/tags" {
				tagsResponse(w, "chat-digest", "embed-digest")
				return
			}
			calls.Add(1)
			categoryResponse(w, 1)
		}))
	}
	a := server(&callsA)
	defer a.Close()
	b := server(&callsB)
	defer b.Close()
	c := newTestPool(t, []NodeConfig{{Name: "a", URL: a.URL, Weight: 1}, {Name: "b", URL: b.URL, Weight: 3}}, "weighted")
	for i := 0; i < 8; i++ {
		if _, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: int64(i + 1)}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{}); err != nil {
			t.Fatal(err)
		}
	}
	if callsA.Load() == 0 || callsB.Load() <= callsA.Load() {
		t.Fatalf("weighted distribution must use both nodes and prefer weight 3: a=%d b=%d", callsA.Load(), callsB.Load())
	}
}

func TestPoolFastestRecentProbesUnmeasuredNodes(t *testing.T) {
	var callsA, callsB atomic.Int64
	server := func(calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/tags" {
				tagsResponse(w, "chat-digest", "embed-digest")
				return
			}
			calls.Add(1)
			categoryResponse(w, 1)
		}))
	}
	a := server(&callsA)
	defer a.Close()
	b := server(&callsB)
	defer b.Close()
	c := newTestPool(t, []NodeConfig{{Name: "a", URL: a.URL}, {Name: "b", URL: b.URL}}, "fastest_recent")
	for i := 0; i < 2; i++ {
		if _, err := c.AnalyseCategory(context.Background(), model.Ticket{ID: int64(i + 1)}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{}); err != nil {
			t.Fatal(err)
		}
	}
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("fastest_recent must measure both nodes before preferring one: a=%d b=%d", callsA.Load(), callsB.Load())
	}
}

func TestPoolSendsBearerToHealthAndInference(t *testing.T) {
	const token = "ollama-secret"
	var tagsAuth, embedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			tagsAuth = r.Header.Get("Authorization")
			tagsResponse(w, "chat-digest", "embed-digest")
		case "/api/embed":
			embedAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := NewPool(PoolConfig{
		APIKey: token, Nodes: []NodeConfig{{Name: "secure", URL: srv.URL, Weight: 1}}, RoutingMode: "least_inflight",
		NodeMaxInflight: 1, HealthInterval: time.Minute, FailureCooldown: time.Second, NodeRequestTimeout: time.Second,
		FailoverEnabled: false, FailoverAttempts: 1, RequireSameModelDigest: true, RequireEmbeddingModel: true, Model: "m", EmbeddingModel: "e",
	}, "m", "e", "de-DE", "formal", 128, time.Minute, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if tagsAuth != "Bearer "+token {
		t.Fatalf("tags authorization=%q", tagsAuth)
	}
	if embedAuth != "Bearer "+token {
		t.Fatalf("embed authorization=%q", embedAuth)
	}
}

func TestPoolCallerCancelDoesNotPenalizeNodeOrFailOver(t *testing.T) {
	started := make(chan struct{}, 1)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		// Drain the body so the server notices the client disconnect.
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer slow.Close()
	var otherCalls atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			tagsResponse(w, "chat-digest", "embed-digest")
			return
		}
		otherCalls.Add(1)
		categoryResponse(w, 1)
	}))
	defer other.Close()

	c := newTestPool(t, []NodeConfig{{Name: "a-slow", URL: slow.URL, Weight: 100}, {Name: "b-other", URL: other.URL, Weight: 1}}, "weighted")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	if _, err := c.AnalyseCategory(ctx, model.Ticket{ID: 1}, []model.Category{{ID: 1}}, nil, model.ContextSnapshot{}); err == nil {
		t.Fatal("expected error after caller cancel")
	}
	if otherCalls.Load() != 0 {
		t.Fatalf("failed over after caller cancel: other calls=%d", otherCalls.Load())
	}
	for _, st := range c.NodeStatuses() {
		if st.ConsecutiveFailures != 0 || !st.CooldownUntil.IsZero() {
			t.Fatalf("node penalized after caller cancel: %+v", st)
		}
	}
}

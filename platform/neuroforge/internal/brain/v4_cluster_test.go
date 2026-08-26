package brain_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"neuroforge/internal/brain"
	"neuroforge/internal/core"
	"neuroforge/internal/cost"
	"neuroforge/internal/httpapi"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

type node struct {
	s  *store.Store
	b  *brain.Engine
	h  http.Handler
	ts *httptest.Server
}

func newNode(t *testing.T, id string) *node {
	t.Helper()
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Config()
	cfg.Sharding.LocalShardID = id
	cfg.Cluster.NodeID = id
	if err := s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := provider.NewRouter(s)
	c := cost.New(s)
	b := brain.New(s, r, c)
	h := httpapi.New(s, b, r, c).Handler()
	return &node{s: s, b: b, h: h}
}

func setSharedToken(t *testing.T, nodes []*node, token string) {
	t.Helper()
	for _, n := range nodes {
		sec := n.s.Secrets()
		sec.ClusterToken = token
		if err := n.s.UpdateSecrets(sec); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClusterQuorumCommitAndRepair(t *testing.T) {
	leader := newNode(t, "n1")
	f2 := newNode(t, "n2")
	f3 := newNode(t, "n3")
	defer leader.s.Close()
	defer f2.s.Close()
	defer f3.s.Close()

	leader.ts = httptest.NewServer(leader.h)
	defer leader.ts.Close()
	f2.ts = httptest.NewServer(f2.h)
	defer f2.ts.Close()
	var failCommit atomic.Bool
	failCommit.Store(true)
	f3.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failCommit.Load() && r.URL.Path == "/internal/v1/cluster/commit" {
			http.Error(w, "simulated commit delivery failure", http.StatusServiceUnavailable)
			return
		}
		f3.h.ServeHTTP(w, r)
	}))
	defer f3.ts.Close()

	setSharedToken(t, []*node{leader, f2, f3}, "shared-cluster-secret")

	lc := leader.s.Config()
	lc.Cluster.Enabled = true
	lc.Cluster.NodeID = "n1"
	lc.Cluster.LeaderID = "n1"
	lc.Cluster.Term = 7
	lc.Cluster.Quorum = 2
	lc.Cluster.Peers = []core.ClusterPeer{{ID: "n2", BaseURL: f2.ts.URL, Enabled: true, Voting: true}, {ID: "n3", BaseURL: f3.ts.URL, Enabled: true, Voting: true}}
	if err := leader.s.UpdateConfig(lc); err != nil {
		t.Fatal(err)
	}
	for id, n := range map[string]*node{"n2": f2, "n3": f3} {
		cfg := n.s.Config()
		cfg.Cluster.Enabled = true
		cfg.Cluster.NodeID = id
		cfg.Cluster.LeaderID = "n1"
		cfg.Cluster.Term = 7
		cfg.Cluster.Quorum = 2
		cfg.Cluster.Peers = []core.ClusterPeer{{ID: "n1", BaseURL: leader.ts.URL, Enabled: true, Voting: true}}
		if err := n.s.UpdateConfig(cfg); err != nil {
			t.Fatal(err)
		}
	}

	m := &core.Memory{ID: "cluster_memory", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "quorum durable", Vector: []float32{1, 0, 0}, Salience: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := leader.b.ClusterProposeMemory(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, ok := leader.s.GetMemory(m.ID); !ok {
		t.Fatal("leader missing committed memory")
	}
	if _, ok := f2.s.GetMemory(m.ID); !ok {
		t.Fatal("healthy follower missing committed memory")
	}
	if _, ok := f3.s.GetMemory(m.ID); ok {
		t.Fatal("flaky follower should still have prepared but uncommitted entry")
	}
	if len(f3.s.PendingClusterEntries()) != 1 {
		t.Fatalf("expected one pending entry on flaky follower, got %d", len(f3.s.PendingClusterEntries()))
	}

	failCommit.Store(false)
	repair := f3.b.RepairCluster(ctx)
	if repair.Committed != 1 || repair.Deferred != 0 {
		t.Fatalf("unexpected repair result: %#v", repair)
	}
	if _, ok := f3.s.GetMemory(m.ID); !ok {
		t.Fatal("repaired follower missing memory")
	}
	if f3.s.ClusterState().CommitIndex == 0 {
		t.Fatal("follower commit index not advanced")
	}
}

func TestClusterRejectsWriteWithoutQuorum(t *testing.T) {
	leader := newNode(t, "leader")
	defer leader.s.Close()
	// Two dead peers make a 3-voter cluster; quorum=2 cannot be reached.
	cfg := leader.s.Config()
	cfg.Cluster.Enabled = true
	cfg.Cluster.NodeID = "leader"
	cfg.Cluster.LeaderID = "leader"
	cfg.Cluster.Term = 2
	cfg.Cluster.Quorum = 2
	cfg.Cluster.RequestTimeoutS = 1
	cfg.Cluster.Peers = []core.ClusterPeer{{ID: "dead1", BaseURL: "http://127.0.0.1:1", Enabled: true, Voting: true}, {ID: "dead2", BaseURL: "http://127.0.0.1:2", Enabled: true, Voting: true}}
	if err := leader.s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := &core.Memory{ID: "must_not_commit", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "no quorum", Vector: []float32{1, 0}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := leader.b.ClusterProposeMemory(ctx, m); err == nil {
		t.Fatal("expected quorum failure")
	}
	if _, ok := leader.s.GetMemory(m.ID); ok {
		t.Fatal("memory became visible without quorum")
	}
}

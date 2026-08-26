package brain_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/store"
)

func newUnstartedServer(h http.Handler) *httptest.Server { return httptest.NewUnstartedServer(h) }

func configureElectionNode(t *testing.T, n *node, all map[string]*node) {
	t.Helper()
	cfg := n.s.Config()
	cfg.Cluster.Enabled = true
	cfg.Cluster.AutoElection = true
	cfg.Cluster.NodeID = ""
	for id, x := range all {
		if x == n {
			cfg.Cluster.NodeID = id
			break
		}
	}
	cfg.Cluster.LeaderID = ""
	cfg.Cluster.Term = 1
	cfg.Cluster.Quorum = 0
	cfg.Cluster.ElectionMinMS = 250
	cfg.Cluster.ElectionMaxMS = 500
	cfg.Cluster.HeartbeatMS = 80
	cfg.Cluster.RequestTimeoutS = 1
	cfg.Cluster.LogSegmentBytes = 1 << 20
	cfg.Cluster.Peers = nil
	for id, x := range all {
		if x != n {
			cfg.Cluster.Peers = append(cfg.Cluster.Peers, core.ClusterPeer{ID: id, BaseURL: x.ts.URL, Enabled: true, Voting: true})
		}
	}
	if err := n.s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func waitLeader(t *testing.T, nodes map[string]*node, excluded string, timeout time.Duration) (string, uint64) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		leaders := map[string]uint64{}
		for id, n := range nodes {
			if id == excluded {
				continue
			}
			st := n.s.ClusterState()
			if st.Role == store.ClusterLeader && st.LeaderID == id {
				leaders[id] = st.Term
			}
		}
		if len(leaders) == 1 {
			for id, term := range leaders {
				return id, term
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	for id, n := range nodes {
		t.Logf("%s state=%#v", id, n.s.ClusterState())
	}
	t.Fatal("no stable elected leader")
	return "", 0
}

func TestAutomaticElectionFailoverAndQuorumWrite(t *testing.T) {
	n1, n2, n3 := newNode(t, "n1"), newNode(t, "n2"), newNode(t, "n3")
	defer n1.s.Close()
	defer n2.s.Close()
	defer n3.s.Close()
	n1.ts = newUnstartedServer(n1.h)
	n2.ts = newUnstartedServer(n2.h)
	n3.ts = newUnstartedServer(n3.h)
	n1.ts.Start()
	n2.ts.Start()
	n3.ts.Start()
	defer func() {
		if n1.ts != nil {
			n1.ts.Close()
		}
		if n2.ts != nil {
			n2.ts.Close()
		}
		if n3.ts != nil {
			n3.ts.Close()
		}
	}()
	nodes := map[string]*node{"n1": n1, "n2": n2, "n3": n3}
	setSharedToken(t, []*node{n1, n2, n3}, "election-secret")
	for _, n := range nodes {
		configureElectionNode(t, n, nodes)
	}
	ctxs := map[string]context.CancelFunc{}
	for id, n := range nodes {
		ctx, cancel := context.WithCancel(context.Background())
		ctxs[id] = cancel
		go n.b.RunV5Maintenance(ctx)
	}
	defer func() {
		for _, c := range ctxs {
			c()
		}
	}()
	leaderID, term := waitLeader(t, nodes, "", 5*time.Second)
	leader := nodes[leaderID]
	m := &core.Memory{ID: "elected_write_1", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "first elected write", Vector: []float32{1, 0, 0}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := leader.b.ClusterProposeMemory(ctx, m); err != nil {
		t.Fatalf("leader write failed: %v", err)
	}
	for id, n := range nodes {
		if _, ok := n.s.GetMemory(m.ID); !ok {
			t.Fatalf("%s missing first committed memory", id)
		}
	}
	for id, n := range nodes {
		ls := n.s.ClusterLogStats()
		if ls.Entries < 1 || ls.Decisions < 1 {
			t.Fatalf("%s missing replicated log entry/decision: %#v", id, ls)
		}
	}
	// Remove the elected leader from the network and maintenance loop. The two remaining voters must elect a successor.
	ctxs[leaderID]()
	leader.ts.Close()
	leader.ts = nil
	newLeaderID, newTerm := waitLeader(t, nodes, leaderID, 6*time.Second)
	if newLeaderID == leaderID || newTerm <= term {
		t.Fatalf("failover did not advance leadership: old=%s/%d new=%s/%d", leaderID, term, newLeaderID, newTerm)
	}
	m2 := &core.Memory{ID: "elected_write_2", Kind: "knowledge", MemoryType: core.MemorySemantic, Text: "after failover", Vector: []float32{0, 1, 0}}
	if err := nodes[newLeaderID].b.ClusterProposeMemory(ctx, m2); err != nil {
		t.Fatalf("failover leader write failed: %v", err)
	}
	for id, n := range nodes {
		if id == leaderID {
			continue
		}
		if _, ok := n.s.GetMemory(m2.ID); !ok {
			t.Fatalf("survivor %s missing failover memory", id)
		}
	}
}

func TestAutomaticElectionCannotWinFromMinority(t *testing.T) {
	n := newNode(t, "solo")
	defer n.s.Close()
	cfg := n.s.Config()
	cfg.Cluster.Enabled = true
	cfg.Cluster.AutoElection = true
	cfg.Cluster.NodeID = "solo"
	cfg.Cluster.LeaderID = ""
	cfg.Cluster.Term = 1
	cfg.Cluster.Quorum = 0
	cfg.Cluster.ElectionMinMS = 220
	cfg.Cluster.ElectionMaxMS = 400
	cfg.Cluster.HeartbeatMS = 70
	cfg.Cluster.RequestTimeoutS = 1
	cfg.Cluster.LogSegmentBytes = 1 << 20
	cfg.Cluster.Peers = []core.ClusterPeer{
		{ID: "dead-a", BaseURL: "http://127.0.0.1:1", Enabled: true, Voting: true},
		{ID: "dead-b", BaseURL: "http://127.0.0.1:2", Enabled: true, Voting: true},
	}
	if err := n.s.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.b.RunV5Maintenance(ctx)
	time.Sleep(1200 * time.Millisecond)
	st := n.s.ClusterState()
	if st.Role == store.ClusterLeader {
		t.Fatalf("minority node elected itself leader: %#v", st)
	}
}

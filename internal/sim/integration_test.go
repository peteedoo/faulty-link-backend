package sim_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/peteedoo/faulty-link-backend/api"
	"github.com/peteedoo/faulty-link-backend/internal/mesh"
	"github.com/peteedoo/faulty-link-backend/internal/sim"
)

type nodeJSON struct {
	NodeID    string    `json:"node_id"`
	LongName  string    `json:"long_name"`
	ShortName string    `json:"short_name"`
	LastHeard time.Time `json:"last_heard"`
}

// startStack wires a live sim listener -> bridge client+store -> HTTP API and
// returns the test HTTP server plus a cleanup func.
func startStack(t *testing.T, interval time.Duration, dropNode uint32, dropAfter time.Duration) (*httptest.Server, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	stop := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					return
				}
			}
			go sim.Serve(conn, sim.DefaultNodes(), interval, dropNode, dropAfter)
		}
	}()

	store := mesh.NewStore(10 * time.Minute)
	client := mesh.NewClient(ln.Addr().String(), store)
	go func() { _ = client.Run() }()

	mux := http.NewServeMux()
	api.RegisterHandlers(mux, &api.HandlerDeps{Store: store, Client: client})
	srv := httptest.NewServer(mux)

	cleanup := func() {
		srv.Close()
		client.Stop()
		store.Close()
		close(stop)
		ln.Close()
	}
	return srv, cleanup
}

func getNodes(t *testing.T, srv *httptest.Server) []nodeJSON {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatalf("GET /nodes: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Nodes []nodeJSON `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Nodes
}

// TestEndToEndNodesAppear is the core MVP criterion: with the simulator
// emitting, the bridge must expose >=3 nodes over HTTP within 10 seconds.
func TestEndToEndNodesAppear(t *testing.T) {
	srv, cleanup := startStack(t, 200*time.Millisecond, 0, 0)
	defer cleanup()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(getNodes(t, srv)) >= 3 {
			nodes := getNodes(t, srv)
			seen := map[string]bool{}
			for _, n := range nodes {
				seen[n.NodeID] = true
			}
			for _, want := range []string{"!000000a1", "!000000a2", "!000000a3"} {
				if !seen[want] {
					t.Fatalf("expected node %s in %v", want, seen)
				}
			}
			return // success
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("did not see 3 nodes within 10s")
}

// TestDroppedNodeGoesStale verifies the drop mechanism: after the drop window,
// the dropped node's last_heard stops advancing while a live node's keeps
// advancing. This is the signal the CLI monitor uses to flag a node DOWN.
func TestDroppedNodeGoesStale(t *testing.T) {
	srv, cleanup := startStack(t, 150*time.Millisecond, 0xa3, 600*time.Millisecond)
	defer cleanup()

	// Wait until all three nodes are present.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(getNodes(t, srv)) < 3 {
		time.Sleep(100 * time.Millisecond)
	}
	if len(getNodes(t, srv)) < 3 {
		t.Fatalf("nodes never reached 3")
	}

	// Let the drop window pass plus a few emit cycles.
	time.Sleep(1500 * time.Millisecond)

	lastHeard := map[string]time.Time{}
	for _, n := range getNodes(t, srv) {
		lastHeard[n.NodeID] = n.LastHeard
	}
	droppedFirst := lastHeard["!000000a3"]
	liveFirst := lastHeard["!000000a1"]

	// Observe another window; the live node must advance, the dropped must not.
	time.Sleep(900 * time.Millisecond)
	lastHeard = map[string]time.Time{}
	for _, n := range getNodes(t, srv) {
		lastHeard[n.NodeID] = n.LastHeard
	}

	if !lastHeard["!000000a1"].After(liveFirst) {
		t.Errorf("live node !000000a1 last_heard did not advance (%v -> %v)", liveFirst, lastHeard["!000000a1"])
	}
	if lastHeard["!000000a3"].After(droppedFirst) {
		t.Errorf("dropped node !000000a3 last_heard advanced but should be frozen (%v -> %v)", droppedFirst, lastHeard["!000000a3"])
	}
}

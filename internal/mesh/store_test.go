package mesh

import (
	"testing"
	"time"
)

func TestStorePutAndGetNode(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	n := &NodeInfo{NodeID: "!abc1234", LongName: "Test Node"}
	s.PutNode(n)

	got := s.GetNode("!abc1234")
	if got == nil {
		t.Fatal("expected node, got nil")
	}
	if got.LongName != "Test Node" {
		t.Errorf("expected LongName 'Test Node', got %q", got.LongName)
	}
}

func TestStoreAllNodes(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	s.PutNode(&NodeInfo{NodeID: "!a", LongName: "Node A"})
	s.PutNode(&NodeInfo{NodeID: "!b", LongName: "Node B"})

	nodes := s.AllNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestStorePutAndGetTelemetry(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	s.PutTelemetry(&Telemetry{NodeID: "!a", BatteryLevel: 42})

	latest, ok := s.LatestTelemetry("!a")
	if !ok {
		t.Fatal("expected telemetry, got none")
	}
	if latest.BatteryLevel != 42 {
		t.Errorf("expected battery 42, got %d", latest.BatteryLevel)
	}
}

func TestStoreTelemetryRingBuffer(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	for i := 0; i < 70; i++ {
		s.PutTelemetry(&Telemetry{NodeID: "!a", BatteryLevel: i})
	}

	all := s.AllTelemetry("!a")
	if len(all) != 64 {
		t.Fatalf("expected 64 samples, got %d", len(all))
	}

	// Verify oldest sample is 6 (70 - 64)
	if all[0].BatteryLevel != 6 {
		t.Errorf("expected oldest sample 6, got %d", all[0].BatteryLevel)
	}

	// Verify newest sample is 69
	latest, ok := s.LatestTelemetry("!a")
	if !ok || latest.BatteryLevel != 69 {
		t.Errorf("expected latest sample 69, got %d", latest.BatteryLevel)
	}
}

func TestStoreGetNodeMissing(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	if got := s.GetNode("!missing"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestStoreStats(t *testing.T) {
	s := NewStore(5 * time.Minute)
	defer s.Close()

	s.PutNode(&NodeInfo{NodeID: "!a"})
	s.PutTelemetry(&Telemetry{NodeID: "!a", BatteryLevel: 100})
	s.PutPosition(&Position{NodeID: "!a", LatitudeI: 123})

	n, te, p := s.Stats()
	if n != 1 || te != 1 || p != 1 {
		t.Errorf("expected stats (1,1,1), got (%d,%d,%d)", n, te, p)
	}
}

func TestStoreTTLEviction(t *testing.T) {
	s := NewStore(100 * time.Millisecond)
	defer s.Close()

	s.PutNode(&NodeInfo{NodeID: "!a"})

	// Should exist immediately
	if s.GetNode("!a") == nil {
		t.Fatal("expected node before TTL expiry")
	}

	// Wait for TTL + cleanup interval
	time.Sleep(300 * time.Millisecond)

	if s.GetNode("!a") != nil {
		t.Fatal("expected node to be evicted after TTL")
	}
}

func TestTelemetryRingEmpty(t *testing.T) {
	r := NewTelemetryRing(4)
	_, ok := r.Latest()
	if ok {
		t.Error("expected Latest to return false for empty ring")
	}
	all := r.All()
	if len(all) != 0 {
		t.Errorf("expected empty slice, got %d items", len(all))
	}
}

func TestTelemetryRingAppendAndLatest(t *testing.T) {
	r := NewTelemetryRing(4)
	r.Append(Telemetry{BatteryLevel: 10})
	r.Append(Telemetry{BatteryLevel: 20})

	latest, ok := r.Latest()
	if !ok {
		t.Fatal("expected latest")
	}
	if latest.BatteryLevel != 20 {
		t.Errorf("expected 20, got %d", latest.BatteryLevel)
	}
}

func TestTelemetryRingOverwrite(t *testing.T) {
	r := NewTelemetryRing(3)
	for i := 1; i <= 5; i++ {
		r.Append(Telemetry{BatteryLevel: i})
	}

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 items, got %d", len(all))
	}
	// Should contain 3,4,5
	for i, want := range []int{3, 4, 5} {
		if all[i].BatteryLevel != want {
			t.Errorf("all[%d] = %d, want %d", i, all[i].BatteryLevel, want)
		}
	}
}

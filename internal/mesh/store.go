// Package mesh handles Meshtastic TCP connectivity and telemetry ingestion.
package mesh

import (
	"sync"
	"time"
)

// TelemetryRing is a circular buffer for per-node telemetry samples.
type TelemetryRing struct {
	mu       sync.RWMutex
	buf      []Telemetry
	head     int
	count    int
	capacity int
}

// NewTelemetryRing creates a ring buffer with the given capacity.
func NewTelemetryRing(capacity int) *TelemetryRing {
	if capacity <= 0 {
		capacity = 64
	}
	return &TelemetryRing{
		buf:      make([]Telemetry, capacity),
		capacity: capacity,
	}
}

// Append adds a sample to the ring, overwriting the oldest if full.
func (r *TelemetryRing) Append(t Telemetry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.head] = t
	r.head = (r.head + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
}

// Latest returns the most recent sample and true, or zero value and false if empty.
func (r *TelemetryRing) Latest() (Telemetry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return Telemetry{}, false
	}
	idx := (r.head - 1 + r.capacity) % r.capacity
	return r.buf[idx], true
}

// All returns a slice of all samples ordered oldest to newest.
func (r *TelemetryRing) All() []Telemetry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Telemetry, r.count)
	if r.count == 0 {
		return out
	}
	start := (r.head - r.count + r.capacity) % r.capacity
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(start+i)%r.capacity]
	}
	return out
}

// Store is a thread-safe in-memory data store for mesh state with TTL eviction.
type Store struct {
	mu          sync.RWMutex
	nodes       map[string]*NodeInfo
	telemetry   map[string]*TelemetryRing
	positions   map[string]*Position
	ttl         time.Duration
	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

// NewStore creates a Store with the given TTL and default ring capacity.
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	s := &Store{
		nodes:       make(map[string]*NodeInfo),
		telemetry:   make(map[string]*TelemetryRing),
		positions:   make(map[string]*Position),
		ttl:         ttl,
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// Close stops the background cleanup goroutine.
func (s *Store) Close() {
	close(s.stopCleanup)
	<-s.cleanupDone
}

// PutNode stores or updates a NodeInfo record.
func (s *Store) PutNode(n *NodeInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.LastUpdate = time.Now()
	s.nodes[n.NodeID] = n
}

// GetNode returns a NodeInfo by ID, or nil if not found or expired.
func (s *Store) GetNode(nodeID string) *NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes[nodeID]
	if !ok {
		return nil
	}
	// Return a copy to avoid external mutation
	cp := *n
	return &cp
}

// AllNodes returns a slice of all known (non-expired) nodes.
func (s *Store) AllNodes() []*NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*NodeInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		cp := *n
		out = append(out, &cp)
	}
	return out
}

// PutTelemetry appends a telemetry sample for the given node.
func (s *Store) PutTelemetry(t *Telemetry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t.LastUpdate = time.Now()
	r, ok := s.telemetry[t.NodeID]
	if !ok {
		r = NewTelemetryRing(64)
		s.telemetry[t.NodeID] = r
	}
	r.Append(*t)
}

// GetTelemetry returns the telemetry ring for a node, or nil.
func (s *Store) GetTelemetry(nodeID string) *TelemetryRing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.telemetry[nodeID]
}

// LatestTelemetry returns the most recent telemetry sample for a node.
func (s *Store) LatestTelemetry(nodeID string) (Telemetry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.telemetry[nodeID]
	if !ok {
		return Telemetry{}, false
	}
	return r.Latest()
}

// AllTelemetry returns all telemetry samples for a node.
func (s *Store) AllTelemetry(nodeID string) []Telemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.telemetry[nodeID]
	if !ok {
		return nil
	}
	return r.All()
}

// PutPosition stores or updates a Position record.
func (s *Store) PutPosition(p *Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.LastUpdate = time.Now()
	s.positions[p.NodeID] = p
}

// GetPosition returns a Position by ID, or nil.
func (s *Store) GetPosition(nodeID string) *Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.positions[nodeID]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// Stats returns store statistics for health checks.
func (s *Store) Stats() (nodeCount, telemetryCount, positionCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes), len(s.telemetry), len(s.positions)
}

// cleanupLoop periodically evicts stale entries.
func (s *Store) cleanupLoop() {
	defer close(s.cleanupDone)
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.evictStale()
		case <-s.stopCleanup:
			return
		}
	}
}

// evictStale removes entries older than the TTL.
func (s *Store) evictStale() {
	cutoff := time.Now().Add(-s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, n := range s.nodes {
		if n.LastUpdate.Before(cutoff) {
			delete(s.nodes, id)
		}
	}
	for id, p := range s.positions {
		if p.LastUpdate.Before(cutoff) {
			delete(s.positions, id)
		}
	}
	for id, r := range s.telemetry {
		latest, ok := r.Latest()
		if !ok || latest.LastUpdate.Before(cutoff) {
			delete(s.telemetry, id)
		}
	}
}

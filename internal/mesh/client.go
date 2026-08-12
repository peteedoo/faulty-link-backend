// Package mesh handles Meshtastic TCP connectivity and telemetry ingestion.
package mesh

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	meshpb "github.com/peteedoo/faulty-link-backend/third_party/protobufs/meshtastic"
	"google.golang.org/protobuf/proto"
)

// Client manages the TCP connection to a Meshtastic node with auto-reconnect.
type Client struct {
	addr    string
	conn    net.Conn
	connMu  sync.RWMutex
	store   *Store
	decoder *Decoder

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Config
	connectTimeout    time.Duration
	heartbeatInterval time.Duration
	staleTimeout      time.Duration
	maxMessageSize    int

	// State
	connected atomic.Bool
	lastRead  atomic.Value // time.Time
}

// NewClient creates a new mesh client for the given address.
func NewClient(addr string, store *Store) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		addr:              addr,
		store:             store,
		ctx:               ctx,
		cancel:            cancel,
		connectTimeout:    10 * time.Second,
		heartbeatInterval: 60 * time.Second,
		staleTimeout:      180 * time.Second,
		maxMessageSize:    1 << 20, // 1 MB
	}
}

// Run starts the connection loop. It blocks until the context is cancelled.
func (c *Client) Run() error {
	c.wg.Add(1)
	go c.connectLoop()

	// Wait for context cancellation
	<-c.ctx.Done()
	c.wg.Wait()
	return c.ctx.Err()
}

// Stop gracefully shuts down the client.
func (c *Client) Stop() {
	c.cancel()
}

// IsConnected reports whether the client currently has an active TCP connection.
func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

// connectLoop dials the Meshtastic node with exponential backoff.
func (c *Client) connectLoop() {
	defer c.wg.Done()

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		if err := c.dial(); err != nil {
			log.Printf("mesh connect failed: %v, retrying in %v", err, backoff)
			select {
			case <-time.After(backoff):
				backoff = nextBackoff(backoff, maxBackoff)
				continue
			case <-c.ctx.Done():
				return
			}
		}

		// Reset backoff on successful connection
		backoff = time.Second
		c.connected.Store(true)
		log.Printf("mesh connected to %s", c.addr)

		// Start sub-loops
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); c.readLoop() }()
		go func() { defer wg.Done(); c.heartbeatLoop() }()
		go func() { defer wg.Done(); c.staleCheckLoop() }()

		// Wait for any loop to exit (connection lost)
		wg.Wait()

		c.connected.Store(false)
		c.closeConn()
		log.Printf("mesh disconnected, reconnecting...")
	}
}

// dial attempts a single TCP connection.
func (c *Client) dial() error {
	dialer := net.Dialer{Timeout: c.connectTimeout}
	conn, err := dialer.DialContext(c.ctx, "tcp", c.addr)
	if err != nil {
		return err
	}

	// Enable TCP keepalive
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(3 * time.Minute)
	}

	c.connMu.Lock()
	c.conn = conn
	c.decoder = NewDecoder(conn, c.maxMessageSize)
	c.lastRead.Store(time.Now())
	c.connMu.Unlock()

	return nil
}

// closeConn closes the current connection under lock.
func (c *Client) closeConn() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.decoder = nil
	}
}

// readLoop continuously decodes messages from the TCP stream.
func (c *Client) readLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.connMu.RLock()
		decoder := c.decoder
		c.connMu.RUnlock()

		if decoder == nil {
			return
		}

		msg, err := decoder.DecodeMessage()
		if err != nil {
			log.Printf("mesh decode error: %v", err)
			return // triggers reconnect
		}

		c.lastRead.Store(time.Now())
		c.dispatch(msg)
	}
}

// heartbeatLoop sends periodic heartbeat messages.
func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := c.sendHeartbeat(); err != nil {
				log.Printf("mesh heartbeat failed: %v", err)
				return // triggers reconnect
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// staleCheckLoop monitors the last read time and forces reconnect if stale.
func (c *Client) staleCheckLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			last, ok := c.lastRead.Load().(time.Time)
			if ok && time.Since(last) > c.staleTimeout {
				log.Printf("mesh connection stale (no data for %v), forcing reconnect", c.staleTimeout)
				c.closeConn()
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// sendHeartbeat writes a ToRadio protobuf heartbeat frame to keep the
// connection alive. Uses a random nonce per heartbeat.
func (c *Client) sendHeartbeat() error {
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return fmt.Errorf("no connection")
	}

	// Build ToRadio with Heartbeat payload
	hb := &meshpb.Heartbeat{Nonce: rand.Uint32()}
	msg := &meshpb.ToRadio{
		PayloadVariant: &meshpb.ToRadio_Heartbeat{Heartbeat: hb},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	// Write Meshtastic stream frame: [START1][START2][len_hi][len_lo][payload].
	frame := make([]byte, 0, 4+len(data))
	frame = append(frame, start1, start2, byte(len(data)>>8), byte(len(data)))
	frame = append(frame, data...)

	_, err = conn.Write(frame)
	return err
}

// dispatch routes a decoded message to the store.
func (c *Client) dispatch(msg Message) {
	switch m := msg.(type) {
	case *NodeInfo:
		m.LastHeard = time.Now()
		c.store.PutNode(m)
	case *Telemetry:
		m.Timestamp = time.Now()
		c.store.PutTelemetry(m)
	case *Position:
		m.Timestamp = time.Now()
		c.store.PutPosition(m)
	default:
		log.Printf("mesh: unknown message type %T", msg)
	}
}

// nextBackoff computes the next backoff duration with jitter.
func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		next = max
	}
	// Add ±25% jitter
	jitter := time.Duration(rand.Int63n(int64(next)/2) - int64(next)/4)
	return next + jitter
}

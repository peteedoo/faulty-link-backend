# Faulty Link — Meshtastic TCP Client Architecture Design

## Overview

The Go bridge service connects to a Meshtastic mesh node via TCP (port 4403) and exposes a REST JSON API. This document describes the architecture for the TCP client, protobuf message handling, and in-memory data store.

## 1. TCP Connection Lifecycle

### Connection
- Dial TCP to the configured Meshtastic node address (default `:4403`).
- Set TCP keepalive via `net.TCPConn.SetKeepAlive(true)` and `SetKeepAlivePeriod(3m)`.
- Connection attempts are wrapped with a configurable timeout (default 10s).

### Auto-Reconnect with Exponential Backoff
- On any read/write error or EOF, the connection is closed and a reconnect loop begins.
- Backoff strategy: exponential with jitter, capped at a maximum interval.
  - Base delay: 1s
  - Multiplier: 2
  - Cap: 30s
  - Jitter: ±25% randomization to prevent thundering herd
- The reconnect loop runs in a dedicated goroutine and can be cancelled via `context.Context`.
- A `sync.RWMutex` protects the `net.Conn` reference so callers can check `IsConnected()` safely.

### Heartbeat / Keepalive
- Meshtastic TCP API expects periodic `ToRadio` heartbeat messages (empty or `want_config_id`).
- The client sends a heartbeat every 60 seconds when connected.
- If no data (including heartbeats) is received for 180 seconds, the connection is considered stale and triggers reconnect.
- Heartbeat and read-timeout are managed by separate goroutines with `time.Ticker` and `time.Timer`.

## 2. Protobuf Message Framing

Meshtastic uses **length-delimited** protobuf framing over TCP:

```
[ varint: message length ] [ bytes: protobuf payload ]
```

- The length prefix is a base-128 varint (same encoding as protobuf wire format).
- The decoder reads the varint, then reads exactly that many bytes into a buffer.
- Large messages (>1 MB) are rejected as a DoS mitigation.
- The decoder is implemented as a state machine (`Decoder`) wrapping an `io.Reader`.

### Decoder State Machine

```
READ_LENGTH ──(varint decoded)──► READ_PAYLOAD ──(payload complete)──► MESSAGE_READY
     ▲                                                                    │
     └────────────────────────────────────────────────────────────────────┘
```

### Message Types to Support

| Protobuf Type | PortNum | Maps To | Fields |
|---------------|---------|---------|--------|
| `NodeInfo` | `NODEINFO_APP` (4) | `mesh.NodeInfo` | `node_id`, `long_name`, `short_name`, `hw_model`, `role`, `last_heard` |
| `Telemetry` | `TELEMETRY_APP` (67) | `mesh.Telemetry` | `node_id`, `timestamp`, `battery_level`, `voltage`, `temperature`, `air_util_tx`, `channel_utilization`, `pressure`, `relative_humidity` |
| `Position` | `POSITION_APP` (3) | `mesh.Position` | `node_id`, `timestamp`, `latitude_i`, `longitude_i`, `altitude`, `precision_bits` |

> **Note:** Actual protobuf deserialization is stubbed. When the protobuf library is integrated, `Decoder` will pass the raw payload to `proto.Unmarshal`. For now, the decoder produces stub structs for testing the pipeline.

## 3. In-Memory Store Design

### Requirements
- Thread-safe: all operations guarded by `sync.RWMutex`.
- TTL eviction: nodes and telemetry expire after a configurable duration (default 10 minutes).
- Telemetry history: per-node ring buffer (circular slice) retaining the last N samples (default 64).
- No external dependencies: pure standard library.

### Data Structures

```go
// Store is the thread-safe in-memory mesh data store.
type Store struct {
    mu           sync.RWMutex
    nodes        map[string]*NodeInfo      // key: node_id
    telemetry    map[string]*TelemetryRing  // key: node_id
    positions    map[string]*Position       // key: node_id
    ttl          time.Duration
    ringCapacity int
    stopCleanup  chan struct{}
}
```

### TTL Eviction
- A background goroutine runs every `ttl/2` seconds.
- It scans all maps and removes entries where `time.Since(lastUpdated) > ttl`.
- The cleanup goroutine is started in `NewStore` and stopped via `Close()`.

### Telemetry Ring Buffer
- Implemented as a circular slice with `head` and `count` indices.
- `Append(sample)` overwrites the oldest entry when full.
- `Latest()` returns the most recent sample without allocation.
- `All()` returns a copy slice ordered oldest→newest.

## 4. Handler Wiring

```
┌─────────────┐    TCP 4403    ┌──────────┐    decoded    ┌───────┐    HTTP 8080    ┌────────┐
│ Meshtastic  │ ◄────────────► │  Client  │ ────────────► │ Store │ ◄─────────────► │ API    │
│ Node        │  protobuf      │          │  mesh.Msg     │       │   REST JSON     │ Handlers│
└─────────────┘                └──────────┘               └───────┘                 └────────┘
```

- `Client` owns the TCP connection and `Decoder`.
- Decoded messages are dispatched to `Store` via type switch.
- `api/handlers.go` receives a `*mesh.Store` at registration time.
- Handlers read from the store under `RLock` and return JSON.

### Handler → Store Mapping

| Handler | Store Method | Description |
|---------|-------------|-------------|
| `GET /health` | `Store.Stats()` | Returns node count, connection state |
| `GET /api/v1/nodes` | `Store.AllNodes()` | Returns all known nodes |
| `GET /api/v1/telemetry` | `Store.LatestTelemetry(nodeID)` | Returns latest or all telemetry |

## 5. Concurrency Model

```
main goroutine
    └── http.ListenAndServe

mesh.Client goroutines (managed by Client.Run):
    ├── connectLoop      // dial + backoff
    ├── readLoop         // decode protobuf stream
    ├── heartbeatLoop    // send periodic heartbeats
    └── staleCheckLoop   // detect dead connections

mesh.Store goroutines:
    └── cleanupLoop      // TTL eviction
```

All channels between goroutines are buffered (size 16) to prevent blocking under load. Shutdown uses `context.Cancel` + `sync.WaitGroup`.

## 6. Future Protobuf Integration

When `google.golang.org/protobuf` is added:

1. Replace `Decoder.decodeStub()` with `proto.Unmarshal(payload, &meshpb.FromRadio)`.
2. Map `meshpb.FromRadio.payload_variant` to Go structs via a generated switch.
3. Keep the framing decoder unchanged — it only cares about length-delimited bytes.
4. Add `internal/mesh/pb/` for generated `.pb.go` files.

## 7. Error Handling Philosophy

- **Transient errors** (network blips, timeouts): log at `Info`, trigger reconnect.
- **Permanent errors** (bad address, refused): log at `Error`, still retry with backoff.
- **Decode errors** (bad varint, oversized message): log at `Warn`, skip message, continue reading.
- **Store errors**: none — store operations are in-memory and infallible.

## 8. Configuration

```go
type Config struct {
    MeshAddr         string        // default "localhost:4403"
    ConnectTimeout   time.Duration // default 10s
    HeartbeatInterval time.Duration // default 60s
    StaleTimeout     time.Duration // default 180s
    StoreTTL         time.Duration // default 10m
    RingCapacity     int           // default 64
    MaxMessageSize   int           // default 1 << 20 (1 MB)
}
```

## 9. File Layout

```
internal/mesh/
├── client.go       // TCP connection, reconnect, heartbeat
├── decoder.go      // Length-delimited protobuf framing
├── store.go        // Thread-safe in-memory store with TTL
├── models.go       // Go structs for NodeInfo, Telemetry, Position
└── client_test.go  // Connection/reconnect tests
├── decoder_test.go // Framing decoder tests
└── store_test.go   // Store and ring buffer tests
```

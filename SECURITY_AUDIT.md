# Faulty Link — Security & Architecture Audit Report
*Generated: 2026-05-29*

## Executive Summary

This report covers a deep technical analysis of the Faulty Link mesh network project, including the Go backend bridge service, Python CLI, and static frontend (JS/CSS/HTML). The codebase is well-structured for an early-stage project but has significant security gaps, architectural limitations, and operational risks that must be addressed before any production or field deployment.

**Overall Risk Rating: MEDIUM-HIGH**
- Critical gaps in API security (no auth, no TLS, no rate limiting)
- Python CLI is non-functional (stub commands only)
- Frontend has no CSP, no integrity checks, and leaks internal email addresses
- Backend lacks observability, persistence, and input validation
- Docker image uses `root` user and `alpine:latest` (non-reproducible)

---

## 1. Backend (Go) Analysis

### 1.1 Architecture Overview

```
Meshtastic Node (TCP 4403)  <--->  Go Bridge (cmd/bridge)  <--->  HTTP API (:8080)
                                         |
                                    internal/mesh/
                                    - client.go (TCP reconnect)
                                    - decoder.go (protobuf framing)
                                    - store.go (in-memory TTL)
                                    - models.go (structs)
                                    api/
                                    - handlers.go (REST JSON)
```

**Strengths:**
- Clean separation of concerns (`internal/mesh/` vs `api/`)
- Context-aware lifecycle management (`context.CancelFunc` + `sync.WaitGroup`)
- Thread-safe in-memory store with TTL eviction
- Per-node telemetry ring buffers (circular, 64-sample default)
- Exponential backoff with jitter for TCP reconnects
- Graceful shutdown on SIGINT/SIGTERM
- Comprehensive test coverage (decoder, store, handlers, benchmarks)

**Weaknesses:**
- No persistence layer (all data lost on restart)
- No structured logging (uses stdlib `log` only)
- No metrics or health check endpoints beyond basic counts
- No request logging or tracing
- HTTP server has no timeouts, no max header size, no request size limits

### 1.2 Security Gaps

#### CRITICAL: No Authentication or Authorization
- **File:** `api/handlers.go`
- **Issue:** All endpoints (`/health`, `/api/v1/nodes`, `/api/v1/telemetry`) are completely open. Anyone on the network can enumerate nodes, query telemetry, and infer network topology.
- **Impact:** Information disclosure; attacker can map mesh node locations, battery levels, and hardware models.
- **Fix:** Add API key middleware or mTLS. At minimum, require a `X-API-Key` header.

#### CRITICAL: No TLS
- **File:** `cmd/bridge/main.go:53`
- **Issue:** `http.ListenAndServe` serves plaintext HTTP. In a mesh network context, the bridge may run on a Pi Zero on a home network. Without TLS, credentials and telemetry are exposed to LAN sniffing.
- **Impact:** MITM, credential theft, telemetry interception.
- **Fix:** Use `http.ListenAndServeTLS` with auto-generated or Let's Encrypt certs. Support ACME for automatic cert provisioning.

#### HIGH: No Input Validation
- **File:** `api/handlers.go:47`
- **Issue:** `node_id` query parameter is passed directly to `Store.LatestTelemetry()` without sanitization. While the current store implementation is safe, future persistence layers (SQL, Redis) could be vulnerable to injection if this pattern continues.
- **Impact:** Potential injection attacks if backend changes.
- **Fix:** Validate `node_id` against the Meshtastic format (`^![0-9a-fA-F]{8}$`). Reject malformed IDs with 400 Bad Request.

#### HIGH: No Rate Limiting
- **File:** `api/handlers.go`
- **Issue:** No rate limiting on any endpoint. An attacker can poll `/api/v1/telemetry` in a tight loop, causing CPU spikes from JSON encoding and lock contention in the store.
- **Impact:** DoS via resource exhaustion.
- **Fix:** Implement per-IP token bucket rate limiting (e.g., `golang.org/x/time/rate`).

#### MEDIUM: Information Leakage in `/health`
- **File:** `api/handlers.go:25`
- **Issue:** `/health` exposes internal counts (`node_count`, `telemetry_count`, `position_count`) and connection state to unauthenticated callers.
- **Impact:** Reconnaissance aid for attackers.
- **Fix:** Require auth for detailed stats. Return minimal `{"status":"ok"}` for public health checks.

#### MEDIUM: CORS Not Configured
- **File:** `api/handlers.go`
- **Issue:** No CORS headers are set. If a web dashboard is served from a different origin, browsers will block requests. Conversely, if CORS is later added as `*`, it opens cross-site request risks.
- **Fix:** Explicitly configure CORS with an allowlist of origins. Reject all others.

### 1.3 Code Quality Issues

#### MEDIUM: `respondJSON` Ignores Encoding Errors
- **File:** `api/handlers.go:71`
- **Issue:** `_ = json.NewEncoder(w).Encode(payload)` silently drops encoding errors. If the payload contains a channel or function (which it shouldn't, but could via future changes), the client gets a truncated response.
- **Fix:** Check the error and log it. Consider writing to a buffer first to avoid partial writes.

#### MEDIUM: Store `evictStale` Holds Write Lock for Extended Period
- **File:** `internal/mesh/store.go:224`
- **Issue:** `evictStale` acquires `s.mu.Lock()` and iterates all three maps. With thousands of nodes, this blocks all readers and writers.
- **Impact:** Latency spikes on HTTP requests during cleanup.
- **Fix:** Use a generational or sharded approach. Or evict in smaller batches.

#### LOW: `nodeIDFromUint` Uses Lowercase Hex
- **File:** `internal/mesh/decoder.go:224`
- **Issue:** `fmt.Sprintf("!%08x", num)` produces lowercase hex. Meshtastic convention is typically uppercase (`!DEADBEEF`). This inconsistency could cause lookup mismatches if clients send uppercase IDs.
- **Fix:** Use `%08X` or normalize all IDs to lowercase in the store.

#### LOW: Missing `client_test.go`
- **File:** Referenced in README but does not exist.
- **Issue:** `client.go` has no unit tests. The reconnect logic, heartbeat, and stale detection are untested.
- **Fix:** Add `client_test.go` with a mock TCP server.

#### LOW: Dockerfile Uses `alpine:latest`
- **File:** `Dockerfile:10`
- **Issue:** `alpine:latest` is a moving target. Rebuilds may pull different base images with different package versions or vulnerabilities.
- **Fix:** Pin to a specific digest, e.g., `alpine:3.20@sha256:...`

#### LOW: Dockerfile Runs as `root`
- **File:** `Dockerfile:13`
- **Issue:** The bridge binary runs as root in the container.
- **Fix:** Add `RUN adduser -D -u 1000 bridge` and `USER bridge`.

### 1.4 Data Model Issues

#### MEDIUM: `Position` Struct Lacks Validation
- **File:** `internal/mesh/models.go:32`
- **Issue:** `LatitudeI` and `LongitudeI` are raw `int32` values from protobuf. No bounds checking (lat: -90M to +90M, lon: -180M to +180M in Meshtastic's 1e-7 degree format). Invalid values could break map rendering.
- **Fix:** Add validation in `decodePosition` or a `Normalize()` method.

#### MEDIUM: `Telemetry` Mixes Device and Environment Metrics
- **File:** `internal/mesh/models.go:18`
- **Issue:** A single `Telemetry` struct holds both `BatteryLevel`/`Voltage` (device) and `Temperature`/`Pressure` (environment). If a node sends both variants in separate packets, the store overwrites the previous variant's fields because `PutTelemetry` appends the whole struct.
- **Impact:** A telemetry sample may show stale environment data paired with fresh battery data, or vice versa.
- **Fix:** Separate `DeviceMetrics` and `EnvironmentMetrics` into distinct types, or use a union pattern. The ring buffer should store the variant type.

#### LOW: `NodeInfo` Position is Ignored
- **File:** `internal/mesh/decoder.go:212`
- **Issue:** `protoToNodeInfo` acknowledges that `NodeInfo` may carry an embedded position but does nothing with it (`_ = ni.Position`). This means position updates delivered inside `NodeInfo` packets are lost.
- **Fix:** Extract the embedded position and dispatch it as a `Position` message.

---

## 2. Python CLI Analysis

### 2.1 Critical Issue: CLI is Completely Non-Functional
- **File:** `cli/faulty_link_cli/main.py`
- **Issue:** Every command (`health`, `nodes`, `telemetry`) is a stub that only prints the URL. No actual HTTP requests are made. The `requests` library is imported but never used.
- **Impact:** The CLI is unusable. The README and Makefile both reference it as a working tool, which is misleading.
- **Fix:** Implement actual `requests.get()` calls with error handling, JSON parsing, and pretty-printing.

### 2.2 Missing Features
- No timeout configuration for HTTP requests.
- No retry logic.
- No output formatting (table, JSON, CSV).
- No configuration file support.
- No tests (the Makefile runs `pytest` but there are no test files).

### 2.3 Dependency Risk
- **File:** `cli/requirements.txt`
- **Issue:** Only pins `requests>=2.31.0`. No upper bound. A future major version of `requests` could break the CLI.
- **Fix:** Pin to a known good range: `requests>=2.31.0,<3.0.0`.

---

## 3. Frontend (JS/CSS/HTML) Analysis

### 3.1 Security Gaps

#### HIGH: No Content Security Policy (CSP)
- **File:** All HTML files
- **Issue:** No `<meta http-equiv="Content-Security-Policy">` header. The site uses inline event handlers (none currently, but the modal injection uses `innerHTML`). A compromised CDN or XSS could inject scripts.
- **Impact:** XSS attacks if any user input is rendered without escaping.
- **Fix:** Add a strict CSP. Since the site is static with no external scripts, a strong policy is feasible:
  ```html
  <meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' https:;">
  ```

#### MEDIUM: Email Address Hardcoded in Config
- **File:** `js/config.js:46`
- **Issue:** `to: 'info@iamfaulty.com'` is exposed in client-side JS. Scrapers can harvest this.
- **Impact:** Spam, phishing targeting.
- **Fix:** Use a contact form backend or obfuscate the email. At minimum, encode it.

#### MEDIUM: `postJson` Endpoint Vulnerable to CSRF / Arbitrary POST
- **File:** `js/site.js:183`
- **Issue:** If `submitMode` is set to `postJson`, the frontend POSTs JSON to any configured endpoint. There is no CSRF token, no origin validation, and no credential handling. If an attacker tricks a user into visiting a malicious page that sets `CONFIG.signupForm.endpoint`, they could exfiltrate data or cause unwanted submissions.
- **Impact:** Data exfiltration, unwanted signups, potential CORS bypass depending on endpoint configuration.
- **Fix:** Do not allow arbitrary endpoints from client-side config. Validate endpoints against an allowlist. Add CORS preflight handling on the backend.

#### LOW: No Subresource Integrity (SRI)
- **File:** All HTML files
- **Issue:** No `integrity` attributes on `<script>` or `<link>` tags. If the hosting provider is compromised, malicious JS/CSS could be served.
- **Fix:** Add SRI hashes for all static assets.

### 3.2 Code Quality Issues

#### MEDIUM: `innerHTML` Used for Modal Injection
- **File:** `js/site.js:105`
- **Issue:** `document.body.insertAdjacentHTML('beforeend', buildModalHtml())` injects HTML built from `CONFIG`. While `escapeHtml` is used, any future change that forgets to escape a field introduces XSS.
- **Fix:** Use `document.createElement` and `textContent` for dynamic DOM construction instead of HTML strings.

#### MEDIUM: No Error Handling for `fetch` in `postJson`
- **File:** `js/site.js:189`
- **Issue:** The `fetch` call catches errors but only shows a generic toast. Network failures, CORS blocks, or 5xx responses are not differentiated to the user.
- **Fix:** Distinguish network errors (offline) from server errors (5xx) from client errors (4xx). Provide actionable messages.

#### LOW: `normalizePath` is Fragile
- **File:** `js/site.js:33`
- **Issue:** `normalizePath` strips trailing slashes, but Meshtastic-style paths or query strings could break active-state highlighting.
- **Fix:** Use `URL` API for robust path comparison.

### 3.3 Accessibility (Good Practices Found)
- Modal has `role="dialog"`, `aria-modal="true"`, `aria-labelledby`
- Focus trap implemented correctly
- ESC key closes modal and returns focus
- Form labels are properly associated
- Required fields marked with `aria-label="required"`
- Focus outlines visible (2–3px)

### 3.4 Performance
- No lazy loading for images (though the site is image-light)
- CSS is small (~4KB) and not minified (acceptable for static site)
- JS is modular but not bundled (fine for small site)

---

## 4. Build & Deployment Issues

### 4.1 Go Module
- **File:** `go.mod`
- **Issue:** Only depends on `google.golang.org/protobuf`. No dependency management for HTTP middleware, rate limiting, logging, or metrics.
- **Fix:** Add dependencies as needed (see Improvement Plan).

### 4.2 Makefile
- **File:** `Makefile`
- **Issue:** `test-py` runs `pytest -q || true`, which silently ignores test failures.
- **Fix:** Remove `|| true` so CI fails on test failures.

### 4.3 Missing CI/CD
- No GitHub Actions workflow for Go tests, linting, or Docker builds.
- No automated security scanning (Dependabot, Snyk, Trivy).

### 4.4 Missing Observability
- No Prometheus metrics.
- No structured logging (JSON).
- No distributed tracing.
- No pprof endpoints for profiling.

---

## 5. Improvement Plan

### Phase 1: Security Hardening (Must Do Before Any Deployment)

| Priority | Task | Files to Modify | Effort |
|----------|------|-----------------|--------|
| CRITICAL | Add API key middleware | `api/handlers.go`, `cmd/bridge/main.go` | 2h |
| CRITICAL | Enable TLS / auto-certs | `cmd/bridge/main.go` | 3h |
| HIGH | Add rate limiting | `api/handlers.go` | 2h |
| HIGH | Validate `node_id` parameter | `api/handlers.go` | 1h |
| HIGH | Add CSP to all HTML pages | All `index.html` | 1h |
| MEDIUM | Obfuscate or remove hardcoded email | `js/config.js` | 30m |
| MEDIUM | Add CORS allowlist | `api/handlers.go` | 1h |
| MEDIUM | Run bridge as non-root in Docker | `Dockerfile` | 30m |
| LOW | Pin alpine base image digest | `Dockerfile` | 15m |

### Phase 2: Backend Robustness

| Priority | Task | Files to Modify | Effort |
|----------|------|-----------------|--------|
| HIGH | Implement Python CLI properly | `cli/faulty_link_cli/main.py` | 3h |
| HIGH | Add SQLite persistence for telemetry | `internal/mesh/store.go` | 4h |
| MEDIUM | Add structured logging (slog/zap) | All Go files | 3h |
| MEDIUM | Add Prometheus metrics endpoint | `api/handlers.go` | 2h |
| MEDIUM | Add request logging middleware | `api/handlers.go` | 1h |
| MEDIUM | Separate device vs environment telemetry | `internal/mesh/models.go`, `internal/mesh/decoder.go` | 2h |
| LOW | Extract embedded position from NodeInfo | `internal/mesh/decoder.go` | 1h |
| LOW | Add `client_test.go` with mock TCP server | `internal/mesh/client_test.go` | 3h |

### Phase 3: Frontend & DevOps

| Priority | Task | Files to Modify | Effort |
|----------|------|-----------------|--------|
| MEDIUM | Replace `innerHTML` modal injection with DOM API | `js/site.js` | 2h |
| MEDIUM | Add SRI hashes to static assets | All `index.html` | 1h |
| MEDIUM | Add GitHub Actions CI (Go tests, lint, build) | `.github/workflows/ci.yml` | 2h |
| LOW | Add Dependabot config | `.github/dependabot.yml` | 30m |
| LOW | Minify CSS/JS for production | Build script or Makefile | 1h |

---

## 6. Specific Code Changes

### 6.1 API Key Middleware (`api/handlers.go`)

```go
func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Header.Get("X-API-Key") != key {
                respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### 6.2 TLS Support (`cmd/bridge/main.go`)

Replace `http.ListenAndServe` with:
```go
if certFile != "" && keyFile != "" {
    err = http.ListenAndServeTLS(httpAddr, certFile, keyFile, mux)
} else {
    err = http.ListenAndServe(httpAddr, mux)
}
```

Consider integrating `golang.org/x/crypto/acme/autocert` for automatic Let's Encrypt.

### 6.3 Rate Limiting (`api/handlers.go`)

```go
import "golang.org/x/time/rate"

var limiter = rate.NewLimiter(rate.Limit(10), 20) // 10 r/s burst 20

func rateLimit(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !limiter.Allow() {
            respondJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### 6.4 Node ID Validation

```go
import "regexp"

var nodeIDRe = regexp.MustCompile(`^![0-9a-fA-F]{8}$`)

func isValidNodeID(id string) bool {
    return nodeIDRe.MatchString(id)
}
```

### 6.5 Python CLI Implementation (`cli/faulty_link_cli/main.py`)

Replace stub commands with actual HTTP calls:

```python
def cmd_health(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/health"
    try:
        resp = requests.get(url, timeout=10)
        resp.raise_for_status()
        data = resp.json()
        print(f"Status: {data.get('status')}")
        print(f"Connected: {data.get('connected')}")
        print(f"Nodes: {data.get('node_count')}")
    except requests.RequestException as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1
    return 0
```

### 6.6 Dockerfile Security

```dockerfile
FROM alpine:3.20
RUN apk --no-cache add ca-certificates && \
    adduser -D -u 1000 bridge
WORKDIR /home/bridge
COPY --from=builder /app/bridge .
USER bridge
EXPOSE 8080
CMD ["./bridge"]
```

### 6.7 CSP Header (add to all HTML `<head>`)

```html
<meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self' https:; img-src 'self' data:;">
```

---

## 7. Testing Recommendations

1. **Add fuzz tests** for the protobuf decoder to catch malformed packets.
2. **Add integration tests** that spin up a mock Meshtastic TCP server and verify end-to-end flow.
3. **Add load tests** for the HTTP API (e.g., ` vegeta attack`).
4. **Run `gosec`** for static security analysis:
   ```bash
   go install github.com/securego/gosec/v2/cmd/gosec@latest
   gosec ./...
   ```
5. **Run `nancy`** for dependency vulnerability scanning:
   ```bash
   go install github.com/sonatypecommunity/nancy@latest
   go list -json -deps ./... | nancy sleuth
   ```

---

## 8. Conclusion

The Faulty Link project demonstrates solid Go fundamentals and a pragmatic architecture for a mesh network bridge. However, it is currently in a "developer demo" state with multiple security and operational gaps that make it unsuitable for any real-world deployment without remediation.

**Immediate actions required:**
1. Implement API authentication and TLS.
2. Fix the Python CLI so it actually works.
3. Add CSP and remove hardcoded emails from the frontend.
4. Add CI/CD and dependency scanning.

**Medium-term actions:**
1. Add persistence (SQLite or embedded DB).
2. Add structured logging and Prometheus metrics.
3. Separate telemetry variants properly.
4. Add integration and load tests.

With the above improvements, the project will be ready for field testing in the Denver pilot.

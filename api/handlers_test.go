package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/peteedoo/faulty-link-backend/internal/mesh"
)

func setupTestDeps() *HandlerDeps {
	store := mesh.NewStore(5 * time.Minute)
	// Client is nil for handler tests; IsConnected will panic on nil pointer.
	// We use a minimal stub client that reports disconnected.
	return &HandlerDeps{
		Store:  store,
		Client: &mesh.Client{}, // zero value; IsConnected returns false
	}
}

func TestHealthHandler(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	deps.healthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body["status"] != "degraded" {
		t.Errorf("expected status 'degraded' (disconnected), got %q", body["status"])
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", contentType)
	}
}

func TestNodesHandler(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	deps.Store.PutNode(&mesh.NodeInfo{NodeID: "!abc", LongName: "Test Node"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	rr := httptest.NewRecorder()

	deps.nodesHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	nodes, ok := body["nodes"].([]any)
	if !ok {
		t.Fatalf("expected nodes to be an array, got %T", body["nodes"])
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d items", len(nodes))
	}
}

func TestTelemetryHandlerEmpty(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry", nil)
	rr := httptest.NewRecorder()

	deps.telemetryHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	telemetry, ok := body["telemetry"].([]any)
	if !ok {
		t.Fatalf("expected telemetry to be an array, got %T", body["telemetry"])
	}
	if len(telemetry) != 0 {
		t.Errorf("expected empty telemetry list, got %d items", len(telemetry))
	}
}

func TestTelemetryHandlerWithNodeID(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	deps.Store.PutTelemetry(&mesh.Telemetry{NodeID: "!abc", BatteryLevel: 87})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry?node_id=!abc", nil)
	rr := httptest.NewRecorder()

	deps.telemetryHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	telem, ok := body["telemetry"].(map[string]any)
	if !ok {
		t.Fatalf("expected telemetry object, got %T", body["telemetry"])
	}
	if telem["battery_level"] != float64(87) {
		t.Errorf("expected battery_level 87, got %v", telem["battery_level"])
	}
}

func TestTelemetryHandlerWithMissingNodeID(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry?node_id=!missing", nil)
	rr := httptest.NewRecorder()

	deps.telemetryHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestRegisterHandlers(t *testing.T) {
	deps := setupTestDeps()
	defer deps.Store.Close()

	mux := http.NewServeMux()
	RegisterHandlers(mux, deps)

	tests := []struct {
		path   string
		method string
	}{
		{"/health", http.MethodGet},
		{"/api/v1/nodes", http.MethodGet},
		{"/api/v1/telemetry", http.MethodGet},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code == http.StatusNotFound {
				t.Errorf("route %s not registered", tt.path)
			}
		})
	}
}

// Package api exposes REST JSON handlers for the bridge service.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/peteedoo/faulty-link-backend/internal/mesh"
)

// HandlerDeps holds runtime dependencies for HTTP handlers.
type HandlerDeps struct {
	Store  *mesh.Store
	Client *mesh.Client
}

// RegisterHandlers mounts all API routes on the provided ServeMux.
func RegisterHandlers(mux *http.ServeMux, deps *HandlerDeps) {
	mux.HandleFunc("/health", deps.healthHandler)
	mux.HandleFunc("/api/v1/nodes", deps.nodesHandler)
	mux.HandleFunc("/api/v1/telemetry", deps.telemetryHandler)
}

func (d *HandlerDeps) healthHandler(w http.ResponseWriter, r *http.Request) {
	nodeCount, telemCount, posCount := d.Store.Stats()
	status := "ok"
	if !d.Client.IsConnected() {
		status = "degraded"
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"status":          status,
		"connected":       d.Client.IsConnected(),
		"node_count":      nodeCount,
		"telemetry_count": telemCount,
		"position_count":  posCount,
		"timestamp":       time.Now().UTC(),
	})
}

func (d *HandlerDeps) nodesHandler(w http.ResponseWriter, r *http.Request) {
	nodes := d.Store.AllNodes()
	respondJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (d *HandlerDeps) telemetryHandler(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node_id")
	if nodeID != "" {
		latest, ok := d.Store.LatestTelemetry(nodeID)
		if !ok {
			respondJSON(w, http.StatusNotFound, map[string]string{
				"error": "no telemetry found for node",
			})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"telemetry": latest})
		return
	}

	// Return latest telemetry for all known nodes
	allNodes := d.Store.AllNodes()
	result := make([]mesh.Telemetry, 0, len(allNodes))
	for _, n := range allNodes {
		if t, ok := d.Store.LatestTelemetry(n.NodeID); ok {
			result = append(result, t)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"telemetry": result})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

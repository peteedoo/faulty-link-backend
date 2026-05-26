// bridge is the Go HTTP bridge service that connects to a Meshtastic
// mesh network node via TCP (port 4403) and exposes a REST JSON API.
package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/peteedoo/faulty-link-backend/api"
	"github.com/peteedoo/faulty-link-backend/internal/mesh"
)

func main() {
	meshAddr := getEnv("MESH_ADDR", "localhost:4403")
	httpAddr := getEnv("HTTP_ADDR", ":8080")

	// Initialize the in-memory store with a 10-minute TTL.
	store := mesh.NewStore(10 * time.Minute)
	defer store.Close()

	// Initialize the Meshtastic TCP client.
	client := mesh.NewClient(meshAddr, store)

	// Start the client connection loop in the background.
	go func() {
		if err := client.Run(); err != nil {
			log.Printf("mesh client exited: %v", err)
		}
	}()

	// Wire HTTP handlers with store and client dependencies.
	deps := &api.HandlerDeps{
		Store:  store,
		Client: client,
	}
	mux := http.NewServeMux()
	api.RegisterHandlers(mux, deps)

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		client.Stop()
	}()

	log.Printf("Bridge listening on %s", httpAddr)
	if err := http.ListenAndServe(httpAddr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

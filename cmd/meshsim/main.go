// meshsim is a Meshtastic node simulator binary. It listens on TCP (default
// :4403) and streams real FromRadio protobuf frames for a set of virtual nodes,
// letting the full Faulty Link stack (sim -> bridge -> CLI monitor) run
// end-to-end with no LoRa hardware. It can also DROP a node mid-run to
// demonstrate offline detection.
//
// Env:
//
//	SIM_ADDR       listen address           (default ":4403")
//	SIM_INTERVAL   per-node emit interval   (default "2s")
//	SIM_DROP_NODE  node to drop, hex or !id (e.g. "a3" or "!000000a3"; default none)
//	SIM_DROP_AFTER stop emitting drop node after this delay (e.g. "12s"; default none)
package main

import (
	"log"
	"net"
	"os"
	"time"

	"github.com/peteedoo/faulty-link-backend/internal/sim"
)

func main() {
	addr := getEnv("SIM_ADDR", ":4403")
	interval := getDuration("SIM_INTERVAL", 2*time.Second)
	dropNode := sim.ParseNodeNum(os.Getenv("SIM_DROP_NODE"))
	dropAfter := getDuration("SIM_DROP_AFTER", 0)

	nodes := sim.DefaultNodes()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("meshsim listen %s: %v", addr, err)
	}
	log.Printf("meshsim listening on %s, %d nodes, interval %v", addr, len(nodes), interval)
	if dropNode != 0 {
		log.Printf("meshsim will DROP node %s after %v", sim.NodeID(dropNode), dropAfter)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("meshsim accept: %v", err)
			continue
		}
		log.Printf("meshsim client connected: %s", conn.RemoteAddr())
		go sim.Serve(conn, nodes, interval, dropNode, dropAfter)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("meshsim: bad duration %s=%q, using %v", key, v, fallback)
	}
	return fallback
}

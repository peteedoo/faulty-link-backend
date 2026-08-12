#!/usr/bin/env bash
#
# demo.sh — Faulty Link Phase-1 MVP, end-to-end, no LoRa hardware.
#
# Boots the mesh simulator (3 nodes, drops the handset after a delay), the Go
# bridge, then the Python CLI monitor. You watch the handset go DOWN with a
# red alert + terminal bell — proving the full sim -> bridge -> monitor path.
#
# Usage: scripts/demo.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

SIM_PORT="${SIM_PORT:-4403}"
HTTP_PORT="${HTTP_PORT:-8080}"
DROP_AFTER="${DROP_AFTER:-18s}"

pids=()
cleanup() {
  echo
  echo ">>> shutting down demo..."
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo ">>> building bridge + meshsim..."
go build -o /tmp/faulty-bridge ./cmd/bridge
go build -o /tmp/faulty-meshsim ./cmd/meshsim

echo ">>> starting simulator on :$SIM_PORT (handset-1 drops after $DROP_AFTER)..."
SIM_ADDR=":$SIM_PORT" SIM_INTERVAL=2s SIM_DROP_NODE=a3 SIM_DROP_AFTER="$DROP_AFTER" \
  /tmp/faulty-meshsim &
pids+=($!)

echo ">>> starting bridge on :$HTTP_PORT (short TTL so eviction is visible)..."
MESH_ADDR="localhost:$SIM_PORT" HTTP_ADDR=":$HTTP_PORT" MESH_TTL=60s \
  /tmp/faulty-bridge &
pids+=($!)

echo ">>> waiting for bridge to populate nodes..."
for _ in $(seq 1 20); do
  count=$(curl -s "http://localhost:$HTTP_PORT/api/v1/nodes" | grep -o '"node_id"' | wc -l | tr -d ' ')
  if [ "${count:-0}" -ge 3 ]; then break; fi
  sleep 1
done

echo ">>> monitor starting — handset-1 will flip to DOWN ~${DROP_AFTER} in (ctrl-c to stop):"
echo
cd "$REPO/cli"
exec python -m faulty_link_cli.main --base-url "http://localhost:$HTTP_PORT" \
  monitor --baseline baseline.yaml

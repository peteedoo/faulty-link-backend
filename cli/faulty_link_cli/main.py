"""Entry point for the Faulty Link CLI."""

import argparse
import sys

import requests

DEFAULT_BASE_URL = "http://localhost:8080"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="faulty-link-cli",
        description="Poll the Faulty Link Bridge REST API.",
    )
    parser.add_argument(
        "--base-url",
        default=DEFAULT_BASE_URL,
        help="Base URL of the bridge API (default: http://localhost:8080).",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    health = sub.add_parser("health", help="Check bridge health.")
    health.set_defaults(func=cmd_health)

    nodes = sub.add_parser("nodes", help="List mesh nodes.")
    nodes.set_defaults(func=cmd_nodes)

    telemetry = sub.add_parser("telemetry", help="Get node telemetry.")
    telemetry.add_argument("--node-id", help="Filter by node ID.")
    telemetry.set_defaults(func=cmd_telemetry)

    return parser


def _get(url: str) -> dict | None:
    try:
        resp = requests.get(url, timeout=10)
        resp.raise_for_status()
        return resp.json()
    except requests.exceptions.ConnectionError:
        print(f"error: could not connect to {url}", file=sys.stderr)
        return None
    except requests.exceptions.Timeout:
        print(f"error: timeout connecting to {url}", file=sys.stderr)
        return None
    except requests.exceptions.HTTPError as e:
        print(f"error: HTTP {e.response.status_code} from {url}", file=sys.stderr)
        return None


def cmd_health(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/health"
    data = _get(url)
    if data is None:
        return 1
    status = data.get("status", "unknown")
    print(f"bridge status: {status}")
    return 0 if status == "ok" else 1


def cmd_nodes(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/api/v1/nodes"
    data = _get(url)
    if data is None:
        return 1
    nodes = data.get("nodes", [])
    if not nodes:
        print("no nodes found")
        return 0
    print(f"{'node_id':<20} {'last_seen':<20} {'rssi'}")
    print("-" * 50)
    for node in nodes:
        nid = node.get("node_id", "?")
        seen = node.get("last_seen", "?")
        rssi = node.get("rssi", "?")
        print(f"{nid:<20} {seen:<20} {rssi}")
    return 0


def cmd_telemetry(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/api/v1/telemetry"
    params = {}
    if args.node_id:
        params["node_id"] = args.node_id
    try:
        resp = requests.get(url, params=params, timeout=10)
        resp.raise_for_status()
        data = resp.json()
    except requests.exceptions.RequestException as e:
        print(f"error: {e}", file=sys.stderr)
        return 1

    telemetry_list = data.get("telemetry", [])
    if not telemetry_list:
        print("no telemetry found")
        return 0

    for t in telemetry_list:
        nid = t.get("node_id", "?")
        batt = t.get("battery_level", "?")
        temp = t.get("temperature", "?")
        print(f"node: {nid}  battery: {batt}%  temp: {temp}C")
    return 0


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())

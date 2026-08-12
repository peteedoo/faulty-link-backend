"""Entry point for the Faulty Link CLI."""

import argparse
import datetime
import sys
import time

import requests
import yaml

DEFAULT_BASE_URL = "http://localhost:8080"

# ANSI colours
_GREEN = "\033[92m"
_RED = "\033[91m"
_RESET = "\033[0m"


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

    monitor = sub.add_parser("monitor", help="Monitor baseline nodes.")
    monitor.add_argument("--baseline", required=True, help="Path to baseline YAML.")
    monitor.add_argument("--once", action="store_true", help="Single poll then exit.")
    monitor.set_defaults(func=cmd_monitor)

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


def _load_baseline(path: str) -> dict:
    with open(path, "r") as fh:
        return yaml.safe_load(fh)


def _parse_last_heard(raw: str) -> datetime.datetime:
    """Parse RFC3339; treat zero-time as epoch-like never-heard."""
    if raw == "0001-01-01T00:00:00Z":
        return datetime.datetime.min.replace(tzinfo=datetime.timezone.utc)
    return datetime.datetime.fromisoformat(raw.replace("Z", "+00:00"))


def _compute_status_and_age(
    baseline_node: dict,
    live_nodes: dict[str, dict],
    now_utc: datetime.datetime,
    offline_threshold_seconds: int,
) -> tuple[str, int]:
    nid = baseline_node["node_id"]
    live = live_nodes.get(nid)
    if live is None:
        return "DOWN", -1
    last_heard_raw = live.get("last_heard", "0001-01-01T00:00:00Z")
    last_heard = _parse_last_heard(last_heard_raw)
    age_seconds = int((now_utc - last_heard).total_seconds())
    if last_heard_raw == "0001-01-01T00:00:00Z" or age_seconds > offline_threshold_seconds:
        return "DOWN", age_seconds
    return "OK", age_seconds


def _render_table(rows: list[tuple[str, str, str, str]]) -> None:
    print(f"{'NAME':<15} {'NODE_ID':<12} {'STATUS':<8} {'AGE_s'}")
    print("-" * 50)
    for name, nid, status, age in rows:
        if status == "OK":
            status_coloured = f"{_GREEN}{status:<8}{_RESET}"
        else:
            status_coloured = f"{_RED}{status:<8}{_RESET}"
        print(f"{name:<15} {nid:<12} {status_coloured} {age}")


def cmd_monitor(args: argparse.Namespace) -> int:
    baseline = _load_baseline(args.baseline)
    poll_interval = baseline.get("poll_interval_seconds", 5)
    offline_threshold = baseline.get("offline_threshold_seconds", 15)
    baseline_nodes = baseline.get("nodes", [])

    prev_status: dict[str, str] = {}

    while True:
        url = f"{args.base_url}/api/v1/nodes"
        data = _get(url)
        if data is None:
            if args.once:
                return 1
            time.sleep(poll_interval)
            continue

        live_nodes: dict[str, dict] = {}
        for node in data.get("nodes", []):
            nid = node.get("node_id")
            if nid:
                live_nodes[nid] = node

        now_utc = datetime.datetime.now(datetime.timezone.utc)
        rows: list[tuple[str, str, str, str]] = []
        any_down = False
        alerts: list[str] = []

        for bnode in baseline_nodes:
            nid = bnode["node_id"]
            name = bnode.get("name", nid)
            status, age = _compute_status_and_age(
                bnode, live_nodes, now_utc, offline_threshold
            )
            if status == "DOWN":
                any_down = True
                age_str = str(age) if age >= 0 else "N/A"
                if prev_status.get(nid) == "OK":
                    alerts.append(f"{_RED}ALERT: {name} ({nid}) is DOWN{_RESET}\a")
            else:
                age_str = str(age)
            rows.append((name, nid, status, age_str))
            prev_status[nid] = status

        for alert in alerts:
            print(alert)
        _render_table(rows)

        if args.once:
            return 1 if any_down else 0

        time.sleep(poll_interval)


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())

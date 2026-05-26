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


def cmd_health(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/health"
    # TODO: implement GET /health and pretty-print response
    print(f"GET {url}")
    return 0


def cmd_nodes(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/api/v1/nodes"
    # TODO: implement GET /api/v1/nodes and pretty-print response
    print(f"GET {url}")
    return 0


def cmd_telemetry(args: argparse.Namespace) -> int:
    url = f"{args.base_url}/api/v1/telemetry"
    # TODO: implement GET /api/v1/telemetry and pretty-print response
    print(f"GET {url}")
    return 0


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())

"""Tests for cli.faulty_link_cli.main using mocked requests."""

import argparse
import sys
from io import StringIO
from unittest.mock import MagicMock, patch

import pytest

from faulty_link_cli.main import (
    build_parser,
    cmd_health,
    cmd_nodes,
    cmd_telemetry,
    _get,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def make_args(**kwargs):
    """Build an argparse.Namespace with sensible defaults."""
    defaults = {"base_url": "http://localhost:8080", "node_id": None}
    defaults.update(kwargs)
    return argparse.Namespace(**defaults)


# ---------------------------------------------------------------------------
# _get
# ---------------------------------------------------------------------------

@patch("faulty_link_cli.main.requests.get")
def test_get_success(mock_get):
    mock_get.return_value.json.return_value = {"status": "ok"}
    mock_get.return_value.raise_for_status = MagicMock()
    assert _get("http://localhost:8080/health") == {"status": "ok"}


@patch("faulty_link_cli.main.requests.get")
def test_get_connection_error(mock_get, capsys):
    import requests
    mock_get.side_effect = requests.exceptions.ConnectionError()
    assert _get("http://localhost:8080/health") is None
    captured = capsys.readouterr()
    assert "could not connect" in captured.err


@patch("faulty_link_cli.main.requests.get")
def test_get_timeout(mock_get, capsys):
    import requests
    mock_get.side_effect = requests.exceptions.Timeout()
    assert _get("http://localhost:8080/health") is None
    captured = capsys.readouterr()
    assert "timeout connecting" in captured.err


@patch("faulty_link_cli.main.requests.get")
def test_get_http_error(mock_get, capsys):
    import requests
    resp = MagicMock()
    resp.status_code = 500
    mock_get.side_effect = requests.exceptions.HTTPError(response=resp)
    assert _get("http://localhost:8080/health") is None
    captured = capsys.readouterr()
    assert "HTTP 500" in captured.err


# ---------------------------------------------------------------------------
# cmd_health
# ---------------------------------------------------------------------------

@patch("faulty_link_cli.main._get")
def test_cmd_health_ok(mock_get, capsys):
    mock_get.return_value = {"status": "ok"}
    args = make_args()
    assert cmd_health(args) == 0
    captured = capsys.readouterr()
    assert "bridge status: ok" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_health_degraded(mock_get, capsys):
    mock_get.return_value = {"status": "degraded"}
    args = make_args()
    assert cmd_health(args) == 1
    captured = capsys.readouterr()
    assert "bridge status: degraded" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_health_request_failure(mock_get, capsys):
    mock_get.return_value = None
    args = make_args()
    assert cmd_health(args) == 1


# ---------------------------------------------------------------------------
# cmd_nodes
# ---------------------------------------------------------------------------

@patch("faulty_link_cli.main._get")
def test_cmd_nodes_empty(mock_get, capsys):
    mock_get.return_value = {"nodes": []}
    args = make_args()
    assert cmd_nodes(args) == 0
    captured = capsys.readouterr()
    assert "no nodes found" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_nodes_list(mock_get, capsys):
    mock_get.return_value = {
        "nodes": [
            {"node_id": "!abc", "last_seen": "2024-01-01T00:00:00Z", "rssi": -45},
            {"node_id": "!def", "last_seen": "2024-01-01T01:00:00Z", "rssi": -60},
        ]
    }
    args = make_args()
    assert cmd_nodes(args) == 0
    captured = capsys.readouterr()
    assert "!abc" in captured.out
    assert "!def" in captured.out
    assert "-45" in captured.out
    assert "-60" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_nodes_missing_fields(mock_get, capsys):
    mock_get.return_value = {"nodes": [{}]}
    args = make_args()
    assert cmd_nodes(args) == 0
    captured = capsys.readouterr()
    assert "?" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_nodes_request_failure(mock_get):
    mock_get.return_value = None
    args = make_args()
    assert cmd_nodes(args) == 1


# ---------------------------------------------------------------------------
# cmd_telemetry
# ---------------------------------------------------------------------------

@patch("faulty_link_cli.main.requests.get")
def test_cmd_telemetry_empty(mock_get, capsys):
    mock_get.return_value.json.return_value = {"telemetry": []}
    mock_get.return_value.raise_for_status = MagicMock()
    args = make_args()
    assert cmd_telemetry(args) == 0
    captured = capsys.readouterr()
    assert "no telemetry found" in captured.out


@patch("faulty_link_cli.main.requests.get")
def test_cmd_telemetry_list(mock_get, capsys):
    mock_get.return_value.json.return_value = {
        "telemetry": [
            {"node_id": "!abc", "battery_level": 87, "temperature": 22.5},
            {"node_id": "!def", "battery_level": 42, "temperature": 18.0},
        ]
    }
    mock_get.return_value.raise_for_status = MagicMock()
    args = make_args()
    assert cmd_telemetry(args) == 0
    captured = capsys.readouterr()
    assert "node: !abc" in captured.out
    assert "battery: 87%" in captured.out
    assert "temp: 22.5C" in captured.out
    assert "node: !def" in captured.out


@patch("faulty_link_cli.main.requests.get")
def test_cmd_telemetry_with_node_id(mock_get, capsys):
    mock_get.return_value.json.return_value = {
        "telemetry": [
            {"node_id": "!abc", "battery_level": 99, "temperature": 25.0},
        ]
    }
    mock_get.return_value.raise_for_status = MagicMock()
    args = make_args(node_id="!abc")
    assert cmd_telemetry(args) == 0
    mock_get.assert_called_once_with(
        "http://localhost:8080/api/v1/telemetry",
        params={"node_id": "!abc"},
        timeout=10,
    )


@patch("faulty_link_cli.main.requests.get")
def test_cmd_telemetry_request_exception(mock_get, capsys):
    import requests
    mock_get.side_effect = requests.exceptions.ConnectionError("boom")
    args = make_args()
    assert cmd_telemetry(args) == 1
    captured = capsys.readouterr()
    assert "error:" in captured.err


# ---------------------------------------------------------------------------
# build_parser / main entrypoint sanity
# ---------------------------------------------------------------------------

def test_build_parser_subcommands():
    parser = build_parser()
    # health
    args = parser.parse_args(["health"])
    assert args.command == "health"
    assert args.func == cmd_health
    # nodes
    args = parser.parse_args(["nodes"])
    assert args.command == "nodes"
    assert args.func == cmd_nodes
    # telemetry
    args = parser.parse_args(["telemetry"])
    assert args.command == "telemetry"
    assert args.func == cmd_telemetry
    # telemetry with --node-id
    args = parser.parse_args(["telemetry", "--node-id", "!abc"])
    assert args.node_id == "!abc"

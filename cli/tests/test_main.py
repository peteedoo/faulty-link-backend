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
    _parse_last_heard,
    _compute_status_and_age,
    cmd_monitor,
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


# ---------------------------------------------------------------------------
# Monitor subcommand tests
# ---------------------------------------------------------------------------

import datetime
import tempfile
import os


BASELINE_YAML = """
poll_interval_seconds: 5
offline_threshold_seconds: 15
nodes:
  - node_id: "!000000a1"
    name: gateway-1
    role: gateway
  - node_id: "!000000a2"
    name: repeater-1
    role: repeater
  - node_id: "!000000a3"
    name: handset-1
    role: handset
"""


def _write_baseline(tmpdir: str) -> str:
    path = os.path.join(tmpdir, "baseline.yaml")
    with open(path, "w") as fh:
        fh.write(BASELINE_YAML)
    return path


@patch("faulty_link_cli.main._get")
def test_cmd_monitor_all_up(mock_get, capsys, tmp_path):
    now = datetime.datetime.now(datetime.timezone.utc)
    mock_get.return_value = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": now.isoformat().replace("+00:00", "Z")},
            {"node_id": "!000000a2", "last_heard": now.isoformat().replace("+00:00", "Z")},
            {"node_id": "!000000a3", "last_heard": now.isoformat().replace("+00:00", "Z")},
        ]
    }
    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=True)
    assert cmd_monitor(args) == 0
    captured = capsys.readouterr()
    assert "gateway-1" in captured.out
    assert "repeater-1" in captured.out
    assert "handset-1" in captured.out
    assert "OK" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_monitor_down_by_absence(mock_get, capsys, tmp_path):
    now = datetime.datetime.now(datetime.timezone.utc)
    mock_get.return_value = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": now.isoformat().replace("+00:00", "Z")},
            # repeater-1 absent
            {"node_id": "!000000a3", "last_heard": now.isoformat().replace("+00:00", "Z")},
        ]
    }
    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=True)
    assert cmd_monitor(args) == 1
    captured = capsys.readouterr()
    assert "DOWN" in captured.out
    assert "repeater-1" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_monitor_down_by_staleness(mock_get, capsys, tmp_path):
    now = datetime.datetime.now(datetime.timezone.utc)
    old = (now - datetime.timedelta(seconds=30)).isoformat().replace("+00:00", "Z")
    mock_get.return_value = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": old},
            {"node_id": "!000000a2", "last_heard": old},
            {"node_id": "!000000a3", "last_heard": old},
        ]
    }
    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=True)
    assert cmd_monitor(args) == 1
    captured = capsys.readouterr()
    assert "DOWN" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_monitor_age_computation(mock_get, capsys, tmp_path):
    now = datetime.datetime.now(datetime.timezone.utc)
    age_10 = (now - datetime.timedelta(seconds=10)).isoformat().replace("+00:00", "Z")
    age_20 = (now - datetime.timedelta(seconds=20)).isoformat().replace("+00:00", "Z")
    mock_get.return_value = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": age_10},
            {"node_id": "!000000a2", "last_heard": age_20},
            {"node_id": "!000000a3", "last_heard": "0001-01-01T00:00:00Z"},
        ]
    }
    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=True)
    assert cmd_monitor(args) == 1
    captured = capsys.readouterr()
    out = captured.out
    # gateway-1 age ~10s -> OK
    assert "gateway-1" in out
    # repeater-1 age ~20s -> DOWN
    assert "repeater-1" in out
    # handset-1 zero-time -> DOWN
    assert "handset-1" in out
    assert out.count("DOWN") == 2
    assert out.count("OK") == 1


def test_cmd_monitor_alert_on_transition(capsys, tmp_path):
    """OK -> DOWN transition emits alert line with bell."""
    from unittest.mock import patch
    now = datetime.datetime.now(datetime.timezone.utc)
    fresh = now.isoformat().replace("+00:00", "Z")
    stale = (now - datetime.timedelta(seconds=30)).isoformat().replace("+00:00", "Z")

    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=False)

    poll1 = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": fresh},
            {"node_id": "!000000a2", "last_heard": fresh},
            {"node_id": "!000000a3", "last_heard": fresh},
        ]
    }
    poll2 = {
        "nodes": [
            {"node_id": "!000000a1", "last_heard": stale},
            {"node_id": "!000000a2", "last_heard": fresh},
            {"node_id": "!000000a3", "last_heard": fresh},
        ]
    }

    with patch("faulty_link_cli.main._get", side_effect=[poll1, poll2]) as mock_get:
        with patch("faulty_link_cli.main.time.sleep", side_effect=[None, Exception("break")]):
            try:
                cmd_monitor(args)
            except Exception:
                pass

    captured = capsys.readouterr()
    # No alert on first poll, then alert on second
    assert "ALERT" in captured.out
    assert "gateway-1" in captured.out
    assert "\a" in captured.out


@patch("faulty_link_cli.main._get")
def test_cmd_monitor_request_failure(mock_get, capsys, tmp_path):
    mock_get.return_value = None
    baseline_path = _write_baseline(str(tmp_path))
    args = make_args(baseline=baseline_path, once=True)
    assert cmd_monitor(args) == 1


# ---------------------------------------------------------------------------
# _parse_last_heard unit tests
# ---------------------------------------------------------------------------

def test_parse_last_heard_zero_time():
    dt = _parse_last_heard("0001-01-01T00:00:00Z")
    assert dt == datetime.datetime.min.replace(tzinfo=datetime.timezone.utc)


def test_parse_last_heard_normal():
    dt = _parse_last_heard("2026-06-24T12:00:00Z")
    expected = datetime.datetime(2026, 6, 24, 12, 0, 0, tzinfo=datetime.timezone.utc)
    assert dt == expected


# ---------------------------------------------------------------------------
# _compute_status_and_age unit tests
# ---------------------------------------------------------------------------

def test_compute_status_and_age_ok():
    now = datetime.datetime.now(datetime.timezone.utc)
    baseline_node = {"node_id": "!000000a1"}
    live_nodes = {"!000000a1": {"last_heard": now.isoformat().replace("+00:00", "Z")}}
    status, age = _compute_status_and_age(baseline_node, live_nodes, now, 15)
    assert status == "OK"
    assert age == 0


def test_compute_status_and_age_down_by_absence():
    now = datetime.datetime.now(datetime.timezone.utc)
    baseline_node = {"node_id": "!000000a1"}
    live_nodes = {}
    status, age = _compute_status_and_age(baseline_node, live_nodes, now, 15)
    assert status == "DOWN"
    assert age == -1


def test_compute_status_and_age_down_by_staleness():
    now = datetime.datetime.now(datetime.timezone.utc)
    old = now - datetime.timedelta(seconds=30)
    baseline_node = {"node_id": "!000000a1"}
    live_nodes = {"!000000a1": {"last_heard": old.isoformat().replace("+00:00", "Z")}}
    status, age = _compute_status_and_age(baseline_node, live_nodes, now, 15)
    assert status == "DOWN"
    assert age == 30


def test_compute_status_and_age_never_heard():
    now = datetime.datetime.now(datetime.timezone.utc)
    baseline_node = {"node_id": "!000000a1"}
    live_nodes = {"!000000a1": {"last_heard": "0001-01-01T00:00:00Z"}}
    status, age = _compute_status_and_age(baseline_node, live_nodes, now, 15)
    assert status == "DOWN"
    # age will be huge but positive
    assert age > 0

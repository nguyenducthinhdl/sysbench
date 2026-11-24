#!/usr/bin/env python3
"""Snapshot on-disk size for every ClickHouse and Elasticsearch arm.

    python3 columnar-fulltext/driver/storage.py --size 2

Writes results/columnar-fulltext-storage-{size}gb.json. Buckets match
report_CH_ES_1GB.md: inverted, all columns / doc values, body, remainder.
"""

from __future__ import annotations

import argparse
import json
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

import gate
import templates

ROOT = Path(__file__).resolve().parents[2]
CH_TABLES = gate.CH_TABLES
ARM_OF_TABLE = {
    "ch_col": "ch-col",
    "ch_ft": "ch-ft",
    "ch_ng": "ch-ng",
    "ch_scan": "ch-scan",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--size", type=int, required=True)
    return parser.parse_args()


def as_int(value) -> int:
    if value is None or value is False:
        return 0
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, (int, float)):
        return int(value)
    if isinstance(value, dict):
        for key in ("total_in_bytes", "size_in_bytes"):
            if key in value and value[key] is not None:
                return as_int(value[key])
        return 0
    if isinstance(value, str):
        stripped = value.strip()
        if stripped.isdigit() or (stripped.startswith("-") and stripped[1:].isdigit()):
            return int(stripped)
        return 0
    return 0


def pick_bytes(node: dict, *keys) -> int:
    for key in keys:
        if key in node and node[key] is not None:
            parsed = as_int(node[key])
            if parsed or node[key] == 0 or node[key] == "0":
                return parsed
    return 0


def wait_ch_merges(timeout: float = 3600.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        n = gate.ch_count("SELECT count() FROM system.merges WHERE database = 'sysbench'")
        if n == 0:
            return
        gate.log(f"waiting for {n} ClickHouse merges")
        time.sleep(10)
    gate.log("ClickHouse merges still running; snapshot anyway")


def wait_es_idle(timeout: float = 180.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        health = gate.es("GET", "/_cluster/health")
        if int(health.get("relocating_shards") or 0) == 0 and int(health.get("initializing_shards") or 0) == 0:
            gate.es("POST", "/_flush")
            return
        time.sleep(5)
    gate.log("Elasticsearch still relocating; snapshot anyway")


def ch_json(sql: str) -> list[dict]:
    raw = gate.ch(sql + "\nFORMAT JSON")
    return json.loads(raw)["data"]


def collect_clickhouse() -> dict[str, dict]:
    wait_ch_merges()
    parts = {
        row["table"]: row
        for row in ch_json(
            "SELECT table, sum(rows) AS rows, count() AS parts, "
            "sum(bytes_on_disk) AS bytes_on_disk, "
            "sum(data_uncompressed_bytes) AS uncompressed "
            "FROM system.parts WHERE database = 'sysbench' AND active "
            "GROUP BY table"
        )
    }
    inverted_rows = ch_json("SELECT * FROM system.data_skipping_indices WHERE database = 'sysbench'")
    inverted: dict[str, int] = {}
    inverted_detail: dict[str, list[dict]] = {}
    for row in inverted_rows:
        table = row["table"]
        compressed = as_int(row.get("data_compressed_bytes"))
        marks = as_int(row.get("marks_bytes") or row.get("marks"))
        inverted[table] = inverted.get(table, 0) + compressed + marks
        inverted_detail.setdefault(table, []).append(
            {
                "name": row.get("name"),
                "type": row.get("type") or row.get("type_of_index"),
                "data_compressed_bytes": compressed,
                "marks_bytes": marks,
            }
        )
    columns_raw = ch_json(
        "SELECT table, column, "
        "sum(column_bytes_on_disk) AS bytes_on_disk, "
        "sum(column_data_compressed_bytes) AS compressed "
        "FROM system.parts_columns WHERE database = 'sysbench' AND active "
        "GROUP BY table, column"
    )
    columns: dict[str, dict[str, int]] = {}
    for row in columns_raw:
        columns.setdefault(row["table"], {})[row["column"]] = as_int(row.get("bytes_on_disk") or row.get("compressed"))
    out: dict[str, dict] = {}
    for table in CH_TABLES:
        part = parts.get(table)
        if part is None:
            gate.fail(f"missing ClickHouse parts for {table}")
        total = as_int(part["bytes_on_disk"])
        inv = inverted.get(table, 0)
        col_map = columns.get(table, {})
        all_cols = sum(col_map.values())
        out[ARM_OF_TABLE[table]] = {
            "table": table,
            "rows": as_int(part["rows"]),
            "parts": as_int(part["parts"]),
            "uncompressed_bytes": as_int(part["uncompressed"]),
            "total_bytes": total,
            "inverted_bytes": inv,
            "inverted_detail": inverted_detail.get(table, []),
            "all_columns_bytes": all_cols,
            "body_bytes": col_map.get("Body", 0),
            "remainder_bytes": max(0, total - inv - all_cols),
            "columns_bytes": col_map,
        }
    return out


def first_index_usage(payload: dict) -> dict:
    indices = payload.get("indices")
    if isinstance(indices, dict) and indices:
        return next(iter(indices.values()))
    for key, value in payload.items():
        if key.startswith("_"):
            continue
        if isinstance(value, dict) and ("all_fields" in value or "store_size_in_bytes" in value or "fields" in value):
            return value
    gate.fail("elasticsearch _disk_usage had no index body")
    raise AssertionError("unreachable")


def collect_elasticsearch() -> dict[str, dict]:
    wait_es_idle()
    out: dict[str, dict] = {}
    for arm, stream in templates.ARMS.items():
        stats = gate.es("GET", f"/{stream}/_stats")
        indices = stats.get("indices") or {}
        if not indices:
            gate.fail(f"missing Elasticsearch stats for {stream}")
        prim = next(iter(indices.values()))["primaries"]
        store = as_int(prim["store"]["size_in_bytes"])
        docs = as_int(prim["docs"]["count"])
        usage = gate.es("POST", f"/{stream}/_disk_usage?run_expensive_tasks=true&flush=true", timeout=1800)
        idx = first_index_usage(usage)
        all_fields = idx.get("all_fields") or {}
        fields = idx.get("fields") or {}
        body = fields.get("body") or {}
        out[arm] = {
            "stream": stream,
            "docs": docs,
            "store_bytes": store,
            "inverted_bytes": as_int(all_fields.get("inverted_index")),
            "stored_fields_bytes": pick_bytes(all_fields, "stored_fields_in_bytes"),
            "doc_values_bytes": pick_bytes(all_fields, "doc_values_in_bytes"),
            "points_bytes": pick_bytes(all_fields, "points_in_bytes"),
            "norms_bytes": pick_bytes(all_fields, "norms_in_bytes"),
            "body_inverted_bytes": as_int(body.get("inverted_index")),
            "body_stored_bytes": pick_bytes(body, "stored_fields_in_bytes"),
            "body_doc_values_bytes": pick_bytes(body, "doc_values_in_bytes"),
        }
    return out


def ping_url(url: str) -> bool:
    try:
        with urllib.request.urlopen(url, timeout=3) as response:
            return response.status < 500
    except Exception:
        return False


def ping_clickhouse() -> bool:
    return ping_url(f"{gate.CH}/ping")


def ping_elasticsearch() -> bool:
    return ping_url(f"{gate.ES}/_cluster/health")


def main() -> int:
    args = parse_args()
    payload = {
        "captured_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "size_gb": args.size,
        "host": "macos",
        "clickhouse": {},
        "elasticsearch": {},
    }
    try:
        if ping_clickhouse():
            payload["clickhouse"] = collect_clickhouse()
            payload["clickhouse_version"] = gate.ch("SELECT version()").strip()
        else:
            gate.log("ClickHouse is down; starting it for storage")
            gate.engine("clickhouse")
            payload["clickhouse"] = collect_clickhouse()
            payload["clickhouse_version"] = gate.ch("SELECT version()").strip()
    finally:
        partial = ROOT / "results" / f"columnar-fulltext-storage-{args.size}gb.partial.json"
        partial.write_text(json.dumps(payload, indent=2) + "\n")
    gate.log("switching to Elasticsearch for storage")
    gate.engine("elasticsearch")
    payload["elasticsearch"] = collect_elasticsearch()
    payload["elasticsearch_version"] = gate.es("GET", "/")["version"]["number"]
    out = ROOT / "results" / f"columnar-fulltext-storage-{args.size}gb.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(payload, indent=2) + "\n")
    gate.log(f"wrote {out}")
    print(
        json.dumps(
            {
                "wrote": str(out),
                "size_gb": args.size,
                "ch_arms": {arm: row["total_bytes"] for arm, row in payload["clickhouse"].items()},
                "es_arms": {arm: row["store_bytes"] for arm, row in payload["elasticsearch"].items()},
            },
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

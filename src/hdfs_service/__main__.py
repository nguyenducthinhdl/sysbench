"""Create the HDFS service schema and a JSONL fixture from the sample log.

    python src/create_schema.py
    python src/create_schema.py --log data/HDFS_2k.log --structure data/HDFS_2k_structure.txt --out schema
"""

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter
from pathlib import Path

from hdfs_service.parse import Service, line_fields, load_log, load_structure, to_event
from hdfs_service.schema import (
    clickhouse_ddl,
    dumps,
    elasticsearch_template,
    event_schema,
    loki_mapping,
    victorialogs_mapping,
)


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    root = repo_root()
    parser = argparse.ArgumentParser(
        description=(
            "Create a production schema for the HDFS sample, "
            "with the instance IP stored as the host."
        )
    )
    parser.add_argument("--log", type=Path, default=root / "data" / "HDFS_2k.log")
    parser.add_argument(
        "--structure",
        type=Path,
        default=root / "data" / "HDFS_2k_structure.txt",
    )
    parser.add_argument("--out", type=Path, default=root / "schema")
    parser.add_argument("--cluster", default="dc1")
    parser.add_argument("--namespace", default="hdfs")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        example = load_structure(args.structure)
        lines = load_log(args.log)
    except (OSError, ValueError) as exc:
        print(exc, file=sys.stderr)
        return 1

    if example not in (line_fields(line) for line in lines):
        print(
            f"{args.structure}: example row does not match any line in {args.log}",
            file=sys.stderr,
        )
        return 1

    service = Service(cluster=args.cluster, namespace=args.namespace)
    events = [to_event(line, service) for line in lines]

    out: Path = args.out
    out.mkdir(parents=True, exist_ok=True)
    (out / "clickhouse.sql").write_text(clickhouse_ddl(), encoding="utf-8")
    (out / "event.schema.json").write_text(dumps(event_schema()), encoding="utf-8")
    (out / "elasticsearch.json").write_text(dumps(elasticsearch_template()), encoding="utf-8")
    (out / "loki.json").write_text(dumps(loki_mapping()), encoding="utf-8")
    (out / "victorialogs.json").write_text(dumps(victorialogs_mapping()), encoding="utf-8")

    jsonl = out / "hdfs_2k.jsonl"
    with jsonl.open("w", encoding="utf-8") as handle:
        for event in events:
            handle.write(json.dumps(event, ensure_ascii=False, separators=(",", ":")))
            handle.write("\n")

    hosts = Counter(event["host"]["name"] for event in events)
    attributed = sum(count for name, count in hosts.items() if name)
    print(f"events: {len(events)}")
    print(f"with instance ip: {attributed}")
    print(f"unattributed: {len(events) - attributed}")
    print(f"distinct instance ips: {len(hosts) - (1 if '' in hosts else 0)}")
    for name in (
        "clickhouse.sql",
        "event.schema.json",
        "elasticsearch.json",
        "loki.json",
        "victorialogs.json",
        "hdfs_2k.jsonl",
    ):
        print(f"wrote {out / name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

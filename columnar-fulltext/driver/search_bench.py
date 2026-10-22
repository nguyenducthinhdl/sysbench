#!/usr/bin/env python3
"""Time the same search on ClickHouse and Elasticsearch.

One engine at a time. Three repetitions. The page cache is not dropped, so
these times are a macOS smoke test and cannot fill spec section 13.

The query text is the frozen text in queries.py. A query is kept only when
every arm that claims that meaning returns the same count.
"""

from __future__ import annotations

import json
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "elasticsearch"))

import gate  # noqa: E402
import queries  # noqa: E402
import templates  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "results" / "columnar-fulltext-search.json"
REPS = 3
T0, T1 = queries.WINDOWS["7d"]
CH_T0, CH_T1 = queries.ch_time(T0), queries.ch_time(T1)
ES_T0, ES_T1 = queries.es_time(T0), queries.es_time(T1)


def ch_sql(builder, table: str) -> str:
    return builder(table, CH_T0, CH_T1)


def es_body(builder) -> dict:
    return builder(ES_T0, ES_T1)


# Same meaning on every arm of a query. Scan arms implement that meaning by
# reading the body column or doc values, not by an inverted index.
SUITE = [
    {
        "id": "C1",
        "semantic": "columnar",
        "statement": "count events in 7 days with cluster = dc1",
        "arms": [
            ("ch-col", "clickhouse", "ch_col", lambda: ch_sql(queries.ch_c1, "ch_col")),
            ("es-col", "elasticsearch", templates.ARMS["es-col"], lambda: es_body(queries.es_c1)),
        ],
    },
    {
        "id": "F1",
        "semantic": "TOKEN",
        "statement": "count zzq_s5 in 7 days, cluster = dc1",
        "arms": [
            ("ch-ft", "clickhouse", "ch_ft", lambda: ch_sql(queries.ch_f1, "ch_ft")),
            ("ch-scan", "clickhouse", "ch_scan", lambda: ch_sql(queries.ch_f1, "ch_scan")),
            ("es-ft", "elasticsearch", templates.ARMS["es-ft"], lambda: es_body(queries.es_f1)),
            ("es-scan", "elasticsearch", templates.ARMS["es-scan"], lambda: es_body(queries.es_scan_f1)),
        ],
    },
    {
        "id": "F5",
        "semantic": "phrase",
        "statement": "count the phrase connection refused in 7 days",
        "arms": [
            ("ch-ft", "clickhouse", "ch_ft", lambda: ch_sql(queries.ch_f5, "ch_ft")),
            ("es-ft", "elasticsearch", templates.ARMS["es-ft"], lambda: es_body(queries.es_f5)),
            ("es-phrase", "elasticsearch", templates.ARMS["es-phrase"], lambda: es_body(queries.es_f5)),
        ],
    },
    {
        "id": "F6",
        "semantic": "SUBSTRING",
        "statement": "count the substring abc in 7 days",
        "arms": [
            ("ch-ng", "clickhouse", "ch_ng", lambda: ch_sql(queries.ch_f6, "ch_ng")),
            ("ch-scan", "clickhouse", "ch_scan", lambda: ch_sql(queries.ch_f6, "ch_scan")),
            ("es-ng", "elasticsearch", templates.ARMS["es-ng"], lambda: es_body(queries.es_f6)),
            ("es-scan", "elasticsearch", templates.ARMS["es-scan"], lambda: es_body(queries.es_scan_f6)),
        ],
    },
    {
        "id": "F6-token",
        "semantic": "TOKEN",
        "statement": "count the token abc in 7 days; the decoy is not a token",
        "arms": [
            ("ch-ft", "clickhouse", "ch_ft", lambda: ch_sql(queries.ch_f6_token, "ch_ft")),
            ("es-ft", "elasticsearch", templates.ARMS["es-ft"], lambda: es_body(queries.es_f6_token)),
        ],
    },
]


def time_clickhouse(sql: str, comment: str) -> dict:
    started = time.perf_counter()
    raw = gate.ch(sql + f"\nSETTINGS log_comment = '{comment}'\nFORMAT TabSeparated")
    wall_ms = (time.perf_counter() - started) * 1000
    gate.ch("SYSTEM FLUSH LOGS")
    logged = gate.ch(
        "SELECT query_duration_ms, "
        "ProfileEvents['UserTimeMicroseconds'], "
        "ProfileEvents['SystemTimeMicroseconds'], "
        "read_rows, read_bytes "
        "FROM system.query_log "
        f"WHERE type = 'QueryFinish' AND log_comment = '{comment}' "
        "ORDER BY event_time DESC LIMIT 1 FORMAT TabSeparated"
    ).strip()
    duration_ms, user_us, system_us, read_rows, read_bytes = logged.split("\t")
    return {
        "count": int(raw.strip()),
        "client_wall_ms": round(wall_ms, 1),
        "engine_wall_ms": int(duration_ms),
        "cpu_seconds": round((int(user_us) + int(system_us)) / 1e6, 4),
        "read_rows": int(read_rows),
        "read_bytes": int(read_bytes),
    }


def time_elasticsearch(stream: str, body: dict) -> dict:
    started = time.perf_counter()
    result = gate.es("POST", f"/{stream}/_search?request_cache=false", body)
    wall_ms = (time.perf_counter() - started) * 1000
    return {
        "count": int(result["hits"]["total"]["value"]),
        "client_wall_ms": round(wall_ms, 1),
        "engine_wall_ms": int(result["took"]),
    }


def explain_clickhouse(sql: str) -> str:
    return gate.ch("EXPLAIN indexes = 1\n" + sql + "\nFORMAT TSVRaw").strip()


def profile_elasticsearch(stream: str, body: dict) -> dict:
    profiled = dict(body)
    profiled["profile"] = True
    result = gate.es("POST", f"/{stream}/_search?request_cache=false", profiled)
    leaves = []

    def walk(node: dict) -> None:
        children = node.get("children") or []
        if not children:
            leaves.append({"type": node.get("type"), "description": node.get("description")})
        for child in children:
            walk(child)

    for shard in result.get("profile", {}).get("shards", []):
        for search in shard.get("searches", []):
            for query in search.get("query", []):
                walk(query)
    return {"took": result.get("took"), "leaves": leaves}


def text_index_used(engine: str, proof) -> str:
    if engine == "clickhouse":
        if "idx_body" in proof or "idx_ng" in proof:
            return "yes"
        if "Skip" in proof:
            return "other-skip"
        return "no"
    text_leaves = [leaf for leaf in proof["leaves"] if leaf["type"] in ("TermQuery", "PhraseQuery", "WildcardQuery")]
    scan_leaves = [leaf for leaf in proof["leaves"] if leaf["type"] not in ("TermQuery", "PhraseQuery", "WildcardQuery", "BooleanQuery", "ConstantScoreQuery")]
    if text_leaves and not any(leaf["type"] == "MatchAllDocsQuery" for leaf in proof["leaves"]):
        return "yes" if not scan_leaves else "text-plus-" + ",".join(sorted({leaf["type"] for leaf in scan_leaves}))
    if scan_leaves:
        return "no (" + ",".join(sorted({leaf["type"] for leaf in scan_leaves})) + ")"
    return "no"


def run_engine(engine: str, rows: list[dict]) -> None:
    gate.engine(engine)
    for query in SUITE:
        for arm, arm_engine, target, build in query["arms"]:
            if arm_engine != engine:
                continue
            built = build()
            reps = []
            for rep in range(1, REPS + 1):
                gate.log(f"{query['id']} {arm} rep {rep}")
                if engine == "clickhouse":
                    reps.append(time_clickhouse(built, f"bench-{query['id']}-{arm}-{rep}"))
                else:
                    reps.append(time_elasticsearch(target, built))
            if engine == "clickhouse":
                proof = explain_clickhouse(built)
            else:
                proof = profile_elasticsearch(target, built)
            counts = {item["count"] for item in reps}
            rows.append(
                {
                    "query_id": query["id"],
                    "semantic": query["semantic"],
                    "statement": query["statement"],
                    "arm": arm,
                    "engine": engine,
                    "repetitions": reps,
                    "count": reps[-1]["count"],
                    "counts_stable": len(counts) == 1,
                    "full_text_index_used": text_index_used(engine, proof),
                    "proof": proof if engine == "elasticsearch" else proof[:2000],
                }
            )


def main() -> int:
    rows: list[dict] = []
    run_engine("elasticsearch", rows)
    run_engine("clickhouse", rows)
    mismatches = []
    for query in SUITE:
        counts = {row["arm"]: row["count"] for row in rows if row["query_id"] == query["id"]}
        if len(set(counts.values())) != 1:
            mismatches.append({"query_id": query["id"], "counts": counts})
    document = {
        "host": "macos",
        "page_cache_dropped": False,
        "repetitions": REPS,
        "window": "7d",
        "t0": ES_T0,
        "t1": ES_T1,
        "note": "Repetition 1 follows an engine restart. The host page cache was not dropped.",
        "rows": rows,
        "count_mismatches": mismatches,
    }
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(document, indent=2) + "\n")
    gate.log(f"wrote {OUT}")
    if mismatches:
        gate.log(f"count mismatches: {mismatches}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

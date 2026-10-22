#!/usr/bin/env python3
"""1 GB correctness gate.

One engine process at a time. The tokenizer fixture runs against an empty
ClickHouse table and an empty Elasticsearch index before either load. Counts
that disagree with the manifest, or with each other, stop the run. On a match
the query text is frozen into queries/columnar-fulltext.md.
"""

from __future__ import annotations

import base64
import json
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "columnar-fulltext" / "elasticsearch"))
sys.path.insert(0, str(ROOT / "columnar-fulltext" / "driver"))

import queries  # noqa: E402
import templates  # noqa: E402

COMPOSE = ROOT / "columnar-fulltext" / "docker-compose.yml"
CH_PASSWORD = "columnar"
CH = "http://127.0.0.1:18123"
CH_AUTH = base64.b64encode(f"default:{CH_PASSWORD}".encode()).decode()
ES = "http://127.0.0.1:19200"
TOKEN = re.compile(r"[0-9A-Za-z]+")
CH_TABLES = ("ch_col", "ch_ft", "ch_ng", "ch_scan")


def log(message: str) -> None:
    print(f"[{time.strftime('%H:%M:%S')}] {message}", file=sys.stderr, flush=True)


def fail(message: str) -> None:
    print(f"gate failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def compose_base() -> list[str]:
    probe = subprocess.run(
        ["docker", "compose", "version"],
        check=False,
        capture_output=True,
        text=True,
    )
    if probe.returncode == 0:
        return ["docker", "compose", "-f", str(COMPOSE)]
    return ["docker-compose", "-f", str(COMPOSE)]


def compose(profile: str, *args: str, timeout: int = 180) -> None:
    cmd = compose_base() + ["--profile", profile, *args]
    result = subprocess.run(cmd, check=False, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        fail(f"{' '.join(cmd)}\n{result.stderr.strip()}")


def http(method: str, url: str, body: bytes | None = None, content_type: str | None = None, timeout: int = 600) -> bytes:
    req = urllib.request.Request(url, data=body, method=method)
    if url.startswith(CH):
        req.add_header("Authorization", f"Basic {CH_AUTH}")
    if content_type:
        req.add_header("Content-Type", content_type)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return response.read()
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode(errors="replace")[:2000]
        fail(f"{method} {url} -> {exc.code}: {detail}")
    except urllib.error.URLError as exc:
        fail(f"{method} {url} -> {exc}")
    raise AssertionError("unreachable")


def wait_until(label: str, url: str, seconds: int = 180) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=8) as response:
                if response.status < 500:
                    response.read()
                    return
        except Exception:
            pass
        time.sleep(2)
    fail(f"{label} did not become ready")


def engine(which: str) -> None:
    other = "elasticsearch" if which == "clickhouse" else "clickhouse"
    log(f"stopping {other}")
    compose(other, "stop")
    log(f"starting {which}")
    compose(which, "up", "-d")
    if which == "clickhouse":
        wait_until("clickhouse", f"{CH}/ping")
    else:
        wait_until("elasticsearch", f"{ES}/_cluster/health?wait_for_status=yellow&timeout=5s")


def ch(sql: str, timeout: int = 3600) -> str:
    url = f"{CH}/?max_execution_time=0&http_receive_timeout=3600&send_timeout=3600"
    return http("POST", url, sql.encode(), "text/plain", timeout).decode()


def es(method: str, path: str, body: dict | None = None, timeout: int = 600) -> dict:
    payload = None if body is None else json.dumps(body).encode()
    content = None if payload is None else "application/json"
    raw = http(method, ES + path, payload, content, timeout)
    if not raw:
        return {}
    return json.loads(raw)


def ch_count(sql: str) -> int:
    return int(ch(sql + "\nFORMAT TabSeparated").strip())


def es_count(stream: str, body: dict) -> int:
    result = es("POST", f"/{stream}/_search?request_cache=false", body)
    return int(result["hits"]["total"]["value"])


def ch_files(*names: str) -> None:
    for name in names:
        cmd = compose_base() + [
            "--profile",
            "clickhouse",
            "exec",
            "-T",
            "clickhouse",
            "clickhouse-client",
            "--user",
            "default",
            "--password",
            CH_PASSWORD,
            "--multiquery",
            "--queries-file",
            f"/sql/{name}",
        ]
        result = subprocess.run(cmd, check=False, capture_output=True, text=True, timeout=120)
        if result.returncode != 0:
            fail(result.stderr.strip() or result.stdout.strip() or f"clickhouse-client {name} failed")


def apply_clickhouse() -> None:
    ch_files("reset.sql", "ddl.sql")
    for table in CH_TABLES:
        count = ch_count(f"SELECT count() FROM sysbench.{table}")
        if count != 0:
            fail(f"{table} is not empty before the load ({count})")


def apply_elasticsearch() -> None:
    es(
        "PUT",
        "/_cluster/settings",
        {"transient": {"search.allow_expensive_queries": True}},
    )
    for arm, stream in templates.ARMS.items():
        delete_data_stream(stream)
        es("PUT", f"/_index_template/{arm}", templates.template(arm))
        es("PUT", f"/_data_stream/{stream}")
        count = es("GET", f"/{stream}/_count").get("count", 0)
        if count != 0:
            fail(f"{stream} is not empty before the load ({count})")


def delete_data_stream(stream: str) -> None:
    req = urllib.request.Request(ES + f"/_data_stream/{stream}", method="DELETE")
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            response.read()
    except urllib.error.HTTPError as exc:
        if exc.code != 404:
            detail = exc.read().decode(errors="replace")[:1000]
            fail(f"delete {stream} -> {exc.code}: {detail}")


def tokenize_clickhouse(lines: list[dict]) -> dict[str, list[str]]:
    found = {}
    for line in lines:
        encoded = line["text"].encode().hex()
        raw = ch(
            f"SELECT toJSONString(tokens(unhex('{encoded}'), 'splitByNonAlpha')) FORMAT TabSeparated"
        ).strip()
        found[line["id"]] = json.loads(raw)
    return found


def tokenize_elasticsearch(lines: list[dict]) -> dict[str, list[str]]:
    info = es("GET", f"/_data_stream/{templates.ARMS['es-ft']}")
    index = info["data_streams"][0]["indices"][-1]["index_name"]
    found = {}
    for line in lines:
        result = es(
            "POST",
            f"/{index}/_analyze",
            {"field": "body", "text": line["text"]},
        )
        found[line["id"]] = [token["token"] for token in result["tokens"]]
    return found


def check_tokens(clickhouse: dict[str, list[str]], elastic: dict[str, list[str]], lines: list[dict]) -> None:
    if len(lines) != 10:
        fail(f"tokenizer fixture has {len(lines)} lines, want 10")
    for line in lines:
        ident = line["id"]
        python_tokens = TOKEN.findall(line["text"])
        if clickhouse[ident] != python_tokens:
            fail(f"ClickHouse tokens for {ident}: {clickhouse[ident]} != {python_tokens}")
        if elastic[ident] != clickhouse[ident]:
            fail(f"tokenizer mismatch on {ident}: es={elastic[ident]} ch={clickhouse[ident]}")
    log("tokenizer fixture matched on 10 lines")


def insert_sql(table: str, limit: str = "") -> str:
    source = "file('/var/lib/clickhouse/user_files/corpus/1gb/events.jsonl', 'LineAsString', 'line String')"
    if limit:
        source = f"(SELECT line FROM {source} LIMIT {limit})"
    return f"""
INSERT INTO sysbench.{table}
    (Timestamp, Cluster, Namespace, App, Pod, Container, HostName, Level,
     TraceId, SpanId, HttpMethod, HttpPath, HttpStatus, DurationMs,
     Attributes, Status, Body)
SELECT
    parseDateTime64BestEffort(JSONExtractString(line, 'timestamp'), 6, 'UTC'),
    JSONExtractString(line, 'cluster'),
    JSONExtractString(line, 'namespace'),
    JSONExtractString(line, 'app'),
    JSONExtractString(line, 'pod'),
    JSONExtractString(line, 'container'),
    JSONExtractString(line, 'host', 'name'),
    JSONExtractString(line, 'level'),
    JSONExtractString(line, 'trace_id'),
    JSONExtractString(line, 'span_id'),
    JSONExtractString(line, 'http', 'method'),
    JSONExtractString(line, 'http', 'path'),
    toUInt16(JSONExtractUInt(line, 'http', 'status')),
    toUInt32(JSONExtractUInt(line, 'duration_ms')),
    CAST(JSONExtract(line, 'attributes', 'Map(String, String)'), 'Map(LowCardinality(String), String)'),
    JSONExtractString(line, 'status'),
    JSONExtractString(line, 'body')
FROM {source}
SETTINGS max_insert_block_size = 65536, max_insert_threads = 1, max_threads = 2
"""


def load_clickhouse(expected_events: int) -> None:
    counts = {table: ch_count(f"SELECT count() FROM sysbench.{table}") for table in CH_TABLES}
    if all(count == expected_events for count in counts.values()):
        log("clickhouse tables already match the manifest")
        return
    if all(count == 0 for count in counts.values()):
        log("clickhouse round-trip of one event")
        ch(insert_sql("ch_col", "1"))
        got = ch("SELECT Body FROM sysbench.ch_col LIMIT 1 FORMAT JSONEachRow").strip()
        parsed = json.loads(got.splitlines()[0]) if got else {}
        if not parsed.get("Body"):
            fail(f"clickhouse did not return the sample body: {got[:200]}")
        for table in CH_TABLES:
            ch(f"TRUNCATE TABLE sysbench.{table}")
    path = ROOT / "data" / "corpus" / "1gb" / "events.jsonl"
    for table in CH_TABLES:
        have = counts[table] if not all(count == 0 for count in counts.values()) else 0
        if have == expected_events:
            log(f"{table} already has {have} rows")
            continue
        if have:
            log(f"{table} has {have} rows; reloading")
            ch(f"TRUNCATE TABLE sysbench.{table}")
        log(f"loading {table}")
        started = time.monotonic()
        stream_insert(table, path)
        count = ch_count(f"SELECT count() FROM sysbench.{table}")
        log(f"{table} rows={count} in {time.monotonic() - started:.0f}s")
        if count != expected_events:
            fail(f"{table} loaded {count} rows, manifest has {expected_events}")


def stream_insert(table: str, path: Path, chunk_lines: int = 50000) -> None:
    """Insert in chunks. One insert of the whole prefix builds a text index that does not fit in 2 GB."""
    sql = insert_sql(table).replace(
        "FROM file('/var/lib/clickhouse/user_files/corpus/1gb/events.jsonl', 'LineAsString', 'line String')\n"
        "SETTINGS max_insert_block_size = 65536, max_insert_threads = 1, max_threads = 2",
        "FROM input('line String')\n"
        "SETTINGS max_memory_usage = 700000000, max_insert_block_size = 65536, max_threads = 1\n"
        "FORMAT LineAsString",
    )
    if "FROM input('line String')" not in sql:
        fail("stream insert SQL was not rewritten")
    cmd = compose_base() + [
        "--profile",
        "clickhouse",
        "exec",
        "-T",
        "clickhouse",
        "clickhouse-client",
        "--user",
        "default",
        "--password",
        CH_PASSWORD,
        "--query",
        sql,
    ]
    sent = 0
    with path.open("rb") as handle:
        while True:
            chunk = b"".join(line for _, line in zip(range(chunk_lines), handle))
            if not chunk:
                break
            result = subprocess.run(cmd, input=chunk, capture_output=True, timeout=600)
            if result.returncode != 0:
                detail = (result.stderr or b"").decode(errors="replace")[-1500:]
                fail(f"insert into {table} failed at row {sent} ({result.returncode}): {detail}")
            sent += chunk.count(b"\n")
            if sent % 500000 == 0:
                log(f"{table} inserted {sent}")
            if sent % 200000 == 0:
                # RSS climbs across text-index inserts until the 2 GB VM kills the server.
                log(f"restarting clickhouse after {table} {sent}")
                compose("clickhouse", "restart")
                wait_until("clickhouse", f"{CH}/ping")


def bulk(stream: str, path: Path, batch_rows: int = 1000) -> None:
    import http.client

    prefix = b'{"timestamp":'
    replacement = b'{"@timestamp":'
    sent = 0
    conn = http.client.HTTPConnection("127.0.0.1", 19200, timeout=180)
    batch: list[bytes] = []

    def flush() -> None:
        nonlocal conn
        if not batch:
            return
        payload = b"".join(batch)
        for attempt in range(4):
            try:
                conn.request(
                    "POST",
                    "/_bulk",
                    body=payload,
                    headers={"Content-Type": "application/x-ndjson"},
                )
                response = conn.getresponse()
                raw = response.read()
            except (http.client.HTTPException, OSError, TimeoutError):
                conn.close()
                conn = http.client.HTTPConnection("127.0.0.1", 19200, timeout=180)
                if attempt == 3:
                    fail(f"bulk to {stream} dropped the connection at row {sent}")
                time.sleep(2)
                continue
            if response.status >= 300:
                fail(f"bulk to {stream} -> {response.status}: {raw[:500]!r}")
            parsed = json.loads(raw)
            if parsed.get("errors"):
                for item in parsed["items"]:
                    error = item.get("create", {}).get("error")
                    if error:
                        fail(f"bulk error on {stream}: {error}")
            batch.clear()
            return
        fail(f"bulk to {stream} did not flush")

    meta = ('{"create":{"_index":"%s"}}\n' % stream).encode()
    with path.open("rb") as handle:
        for line in handle:
            if not line.startswith(prefix):
                fail("corpus line does not start with timestamp")
            batch.append(meta)
            batch.append(replacement + line[len(prefix):])
            sent += 1
            if len(batch) >= batch_rows * 2:
                flush()
                if sent % 100000 == 0:
                    log(f"{stream} bulk {sent}")
    flush()
    conn.close()
    log(f"{stream} bulk rows={sent}")


def load_elasticsearch(path: Path, expected_events: int, sample_body: str) -> None:
    first = path.open("rb").readline()
    doc = json.loads(first)
    doc["@timestamp"] = doc.pop("timestamp")
    for stream in (
        templates.ARMS["es-col"],
        templates.ARMS["es-ft"],
        templates.ARMS["es-scan"],
    ):
        payload = (
            json.dumps({"create": {"_index": stream}}) + "\n" + json.dumps(doc) + "\n"
        ).encode()
        http("POST", f"{ES}/_bulk", payload, "application/x-ndjson")
        es("POST", f"/{stream}/_refresh")
        fetched = es(
            "POST",
            f"/{stream}/_search",
            {"size": 1, "query": {"term": {"trace_id": doc["trace_id"]}}},
        )
        hits = fetched["hits"]["hits"]
        if not hits or hits[0]["_source"].get("body") != doc["body"]:
            fail(f"{stream} did not round-trip body")
    del sample_body
    log("elasticsearch round-trip kept body; recreating empty streams")
    for stream in templates.ARMS.values():
        delete_data_stream(stream)
        es("PUT", f"/_data_stream/{stream}")
    for arm, stream in templates.ARMS.items():
        log(f"loading {arm}")
        started = time.monotonic()
        es("PUT", f"/{stream}/_settings", {"index": {"refresh_interval": "-1"}})
        bulk(stream, path)
        es("PUT", f"/{stream}/_settings", {"index": {"refresh_interval": "1s"}})
        es("POST", f"/{stream}/_refresh")
        count = es("GET", f"/{stream}/_count")["count"]
        log(f"{arm} docs={count} in {time.monotonic() - started:.0f}s")
        if count != expected_events:
            fail(f"{arm} loaded {count} docs, manifest has {expected_events}")


def window_sum(manifest: dict, key: str, t0, t1) -> int:
    start = int((t0 - queries.START).total_seconds() // 3600)
    end = int((t1 - queries.START).total_seconds() // 3600)
    return sum(bucket[key] for bucket in manifest["by_hour"][start:end])


def hour_key(value: str) -> str:
    return value[:13].replace(" ", "T")


def ch_c6(t0: str, t1: str) -> dict[str, tuple[int, int]]:
    sql = queries.ch_c6("ch_col", t0, t1) + "\nFORMAT JSONEachRow"
    rows = {}
    for line in ch(sql).splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        rows[hour_key(row["h"])] = (int(row["total"]), int(row["rejected"]))
    return rows


def es_c6(t0: str, t1: str) -> dict[str, tuple[int, int]]:
    result = es("POST", f"/{templates.ARMS['es-col']}/_search?request_cache=false", queries.es_c6(t0, t1))
    rows = {}
    for bucket in result["aggregations"]["per_hour"]["buckets"]:
        rows[hour_key(bucket["key_as_string"])] = (
            int(bucket["doc_count"]),
            int(bucket["rejected"]["doc_count"]),
        )
    return rows


def bound(fmt, window: str) -> tuple[str, str]:
    t0, t1 = queries.WINDOWS[window]
    return fmt(t0), fmt(t1)


def write_freeze(path: Path, manifest: dict, versions: dict, counts: dict) -> None:
    lines = [
        "# Columnar and full-text queries",
        "",
        "Frozen by the 1 GB correctness gate. Later sizes use this text.",
        "Do not edit a query without re-running the gate.",
        "",
        f"Corpus sha256 `{manifest['sha256']}`, seed `{manifest['seed']}`, "
        f"events `{manifest['event_count']}`, bytes `{manifest['actual_bytes']}`.",
        f"ClickHouse `{versions['clickhouse']}`. Elasticsearch `{versions['elasticsearch']}`.",
        "Host `macos`. This run does not fill spec §13.",
        "",
        "Two substitutions from [matrix.md](matrix.md), required on the pinned ClickHouse 26.2 image:",
        "",
        "- `hasToken(Body, 'zzq_s5')` is rejected: underscore is a `splitByNonAlpha` separator. "
        "`hasAllTokens(Body, 'zzq_s5')` is the token predicate, and Elasticsearch `match` with "
        "`operator: and` asks the same question.",
        "- `hasPhrase` was added in ClickHouse 26.4. The phrase predicate on this image is "
        "`position(Body, 'connection refused') > 0`. `es-ft` still runs `match_phrase` and is "
        "`verify-from-source`.",
        "- Elasticsearch 9.2 `match_only_text` rejects a field analyzer. `es-ft` uses that type "
        "and the index default analyzer, which is the same `splitByNonAlpha` split.",
        "- `es-scan` stores `body` as an unindexed keyword and scans it from doc values. "
        "Query scripts on this version cannot see `_source`.",
        "",
        "`$T0` and `$T1` are the window bounds. `$TRACE` is one trace id from the corpus.",
        "",
    ]
    for query_id, semantic, summary, arms in queries.FROZEN:
        lines.append(f"## {query_id}")
        lines.append("")
        lines.append(f"{summary}. Semantic: `{semantic}`.")
        lines.append("")
        for arm, language, text in arms:
            lines.append(f"### `{arm}`")
            lines.append("")
            lines.append(f"```{language}")
            lines.append(text)
            lines.append("```")
            lines.append("")
    lines.append("## Gate counts at 1 GB")
    lines.append("")
    lines.append("| Check | Expected | Arms |")
    lines.append("| --- | --- | --- |")
    for name, row in counts.items():
        expected = row["expected"]
        arms = ", ".join(f"{arm}={value}" for arm, value in row.items() if arm != "expected")
        lines.append(f"| {name} | {expected} | {arms} |")
    lines.append("")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(lines))


def main() -> int:
    reuse_es = "--reuse-elasticsearch" in sys.argv
    corpus = ROOT / "data" / "corpus" / "1gb"
    manifest_path = corpus / "manifest.json"
    events = corpus / "events.jsonl"
    if not manifest_path.is_file() or not events.is_file():
        fail(f"missing {corpus}; run tools/gen-corpus/gen_corpus.py first")
    manifest = json.loads(manifest_path.read_text())
    lines = json.loads((ROOT / "columnar-fulltext" / "fixture" / "lines.json").read_text())
    log(f"corpus events={manifest['event_count']} bytes={manifest['actual_bytes']}")

    versions = {"clickhouse": "", "elasticsearch": ""}
    clickhouse_tokens = None
    if not reuse_es:
        engine("clickhouse")
        versions["clickhouse"] = ch("SELECT version()").strip()
        log(f"clickhouse {versions['clickhouse']}")
        apply_clickhouse()
        clickhouse_tokens = tokenize_clickhouse(lines)

    engine("elasticsearch")
    versions["elasticsearch"] = es("GET", "/")["version"]["number"]
    log(f"elasticsearch {versions['elasticsearch']}")
    loaded = es("GET", f"/{templates.ARMS['es-ft']}/_count").get("count")
    if reuse_es and loaded == manifest["event_count"]:
        log("reusing the elasticsearch load")
    else:
        if reuse_es:
            log(f"elasticsearch has {loaded} docs; loading again")
        apply_elasticsearch()
        if clickhouse_tokens is None:
            fail("tokenizer check needs a ClickHouse pass before the first load")
        elastic_tokens = tokenize_elasticsearch(lines)
        check_tokens(clickhouse_tokens, elastic_tokens, lines)
        load_elasticsearch(events, manifest["event_count"], manifest.get("sample_stack_body") or "")
    if clickhouse_tokens is not None and reuse_es and loaded == manifest["event_count"]:
        elastic_tokens = tokenize_elasticsearch(lines)
        check_tokens(clickhouse_tokens, elastic_tokens, lines)
    log("querying elasticsearch before stopping it")
    es_results = collect_elasticsearch(manifest)
    check_multiline_es(manifest)
    es_c6_rows = es_c6(*bound(queries.es_time, "48h"))
    k1_es = k1_elasticsearch(manifest)

    engine("clickhouse")
    versions["clickhouse"] = ch("SELECT version()").strip()
    log(f"clickhouse {versions['clickhouse']}")
    existing_rows = ch_count("SELECT count() FROM system.tables WHERE database = 'sysbench' AND name = 'ch_col'")
    if existing_rows == 0:
        apply_clickhouse()
    else:
        log("keeping the clickhouse tables already on disk")
    load_clickhouse(manifest["event_count"])
    ch_results = collect_clickhouse(manifest)
    check_multiline_ch(manifest)
    ch_c6_rows = ch_c6(*bound(queries.ch_time, "48h"))
    k1_ch = k1_clickhouse(manifest)

    counts = merge_counts(manifest, ch_results, es_results)
    compare_c6(manifest, ch_c6_rows, es_c6_rows)
    if k1_ch != k1_es:
        fail(f"K1 disagreed: ch={k1_ch!r} es={k1_es!r}")
    log("K1 matched")

    freeze = ROOT / "queries" / "columnar-fulltext.md"
    result = ROOT / "results" / "columnar-fulltext-gate.json"
    result.parent.mkdir(parents=True, exist_ok=True)
    result.write_text(
        json.dumps(
            {
                "passed": True,
                "host": "macos",
                "versions": versions,
                "manifest_sha256": manifest["sha256"],
                "event_count": manifest["event_count"],
                "counts": counts,
            },
            indent=2,
        )
        + "\n"
    )
    write_freeze(freeze, manifest, versions, counts)
    log(f"froze {freeze}")
    return 0


def collect_clickhouse(manifest: dict) -> dict[str, dict[str, int]]:
    out: dict[str, dict[str, int]] = {}
    events = manifest["event_count"]
    for table in CH_TABLES:
        out[f"rows:{table}"] = {"clickhouse": ch_count(f"SELECT count() FROM sysbench.{table}"), "expected_rows": events}
    for window in ("7d", "24h", "1h"):
        t0, t1 = queries.WINDOWS[window]
        ct0, ct1 = bound(queries.ch_time, window)
        out[f"C1:{window}"] = {"ch-col": ch_count(queries.ch_c1("ch_col", ct0, ct1))}
        out[f"C2:{window}"] = {"ch-col": ch_count(queries.ch_c2("ch_col", ct0, ct1))}
        out[f"F1:{window}"] = {
            "ch-ft": ch_count(queries.ch_f1("ch_ft", ct0, ct1)),
            "ch-scan": ch_count(queries.ch_f1("ch_scan", ct0, ct1)),
        }
    ct0, ct1 = bound(queries.ch_time, "7d")
    out["F2:7d"] = {
        "ch-ft": ch_count(queries.ch_f2("ch_ft", ct0, ct1)),
        "ch-scan": ch_count(queries.ch_f2("ch_scan", ct0, ct1)),
    }
    out["F4:7d"] = {"ch-ft": ch_count(queries.ch_f4("ch_ft", ct0, ct1))}
    out["F6-token:7d"] = {"ch-ft": ch_count(queries.ch_f6_token("ch_ft", ct0, ct1))}
    for window in ("7d", "24h"):
        ct0, ct1 = bound(queries.ch_time, window)
        out[f"F5:{window}"] = {"ch-ft": ch_count(queries.ch_f5("ch_ft", ct0, ct1))}
        out[f"F6:{window}"] = {
            "ch-ng": ch_count(queries.ch_f6("ch_ng", ct0, ct1)),
            "ch-scan": ch_count(queries.ch_f6("ch_scan", ct0, ct1)),
        }
    return out


def collect_elasticsearch(manifest: dict) -> dict[str, dict[str, int]]:
    out: dict[str, dict[str, int]] = {}
    for arm, stream in templates.ARMS.items():
        out[f"rows:{arm}"] = {"elasticsearch": es("GET", f"/{stream}/_count")["count"]}
    for window in ("7d", "24h", "1h"):
        et0, et1 = bound(queries.es_time, window)
        out[f"C1:{window}"] = {"es-col": es_count(templates.ARMS["es-col"], queries.es_c1(et0, et1))}
        out[f"C2:{window}"] = {"es-col": es_count(templates.ARMS["es-col"], queries.es_c2(et0, et1))}
        out[f"F1:{window}"] = {
            "es-ft": es_count(templates.ARMS["es-ft"], queries.es_f1(et0, et1)),
            "es-scan": es_count(templates.ARMS["es-scan"], queries.es_scan_f1(et0, et1)),
        }
    et0, et1 = bound(queries.es_time, "7d")
    out["F2:7d"] = {
        "es-ft": es_count(templates.ARMS["es-ft"], queries.es_f2(et0, et1)),
        "es-scan": es_count(templates.ARMS["es-scan"], queries.es_scan_f2(et0, et1)),
    }
    out["F4:7d"] = {"es-ft": es_count(templates.ARMS["es-ft"], queries.es_f4(et0, et1))}
    out["F6-token:7d"] = {"es-ft": es_count(templates.ARMS["es-ft"], queries.es_f6_token(et0, et1))}
    for window in ("7d", "24h"):
        et0, et1 = bound(queries.es_time, window)
        out[f"F5:{window}"] = {
            "es-ft": es_count(templates.ARMS["es-ft"], queries.es_f5(et0, et1)),
            "es-phrase": es_count(templates.ARMS["es-phrase"], queries.es_f5(et0, et1)),
        }
        out[f"F6:{window}"] = {
            "es-ng": es_count(templates.ARMS["es-ng"], queries.es_f6(et0, et1)),
            "es-scan": es_count(templates.ARMS["es-scan"], queries.es_scan_f6(et0, et1)),
        }
    return out


def merge_counts(manifest: dict, clickhouse: dict, elastic: dict) -> dict[str, dict[str, int]]:
    events = manifest["event_count"]
    merged: dict[str, dict[str, int]] = {}

    def take(name: str, expected: int) -> None:
        actual = {}
        actual.update(clickhouse.get(name, {}))
        actual.update(elastic.get(name, {}))
        actual.pop("expected_rows", None)
        for arm, value in actual.items():
            if value != expected:
                fail(f"{name} {arm}={value} expected={expected}")
        merged[name] = {"expected": expected, **actual}

    for table in CH_TABLES:
        take(f"rows:{table}", events)
    for arm in templates.ARMS:
        take(f"rows:{arm}", events)
    for window in ("7d", "24h", "1h"):
        t0, t1 = queries.WINDOWS[window]
        take(f"C1:{window}", window_sum(manifest, "dc1", t0, t1))
        take(f"C2:{window}", window_sum(manifest, "error_checkout", t0, t1))
        take(f"F1:{window}", window_sum(manifest, "zzq_s5_dc1", t0, t1))
    t0, t1 = queries.WINDOWS["7d"]
    take("F2:7d", window_sum(manifest, "zzq_both", t0, t1))
    take(
        "F4:7d",
        window_sum(manifest, "zzq_s5", t0, t1) - window_sum(manifest, "s5_health", t0, t1),
    )
    take("F6-token:7d", 0)
    for window in ("7d", "24h"):
        t0, t1 = queries.WINDOWS[window]
        take(f"F5:{window}", window_sum(manifest, "phrase", t0, t1))
        take(f"F6:{window}", window_sum(manifest, "abc", t0, t1))
    return merged


def check_multiline_ch(manifest: dict) -> None:
    trace = manifest["sample_stack_trace_id"]
    raw = ch(
        f"SELECT Body FROM sysbench.ch_col WHERE TraceId = '{trace}' LIMIT 1 FORMAT JSONEachRow"
    ).strip()
    got = json.loads(raw)["Body"]
    if got != manifest["sample_stack_body"] or got.count("\n") < 19:
        fail("clickhouse did not return the stack trace as one event")


def check_multiline_es(manifest: dict) -> None:
    trace = manifest["sample_stack_trace_id"]
    fetched = es(
        "POST",
        f"/{templates.ARMS['es-col']}/_search",
        {"size": 1, "query": {"term": {"trace_id": trace}}},
    )
    hits = fetched["hits"]["hits"]
    if len(hits) != 1 or hits[0]["_source"].get("body") != manifest["sample_stack_body"]:
        fail("elasticsearch did not return the stack trace as one event")
    log("stack trace round-tripped on elasticsearch")


def compare_c6(manifest_ignored: dict, clickhouse: dict, elastic: dict) -> None:
    incident_path = ROOT / "data" / "corpus" / "1gb" / "incidents.json"
    incident = json.loads(incident_path.read_text())["incidents"][0]
    for hour in incident["hours"]:
        key = hour_key(hour["hour"])
        expected = (hour["total"], hour["rejected"])
        if clickhouse.get(key, (0, 0)) != expected or elastic.get(key, (0, 0)) != expected:
            fail(f"C6 {key}: ch={clickhouse.get(key)} es={elastic.get(key)} manifest={expected}")
    log("C6 hourly rejection counts match INC-2")


def k1_clickhouse(manifest: dict) -> str:
    trace = manifest["sample_trace_id"]
    ct0, ct1 = bound(queries.ch_time, "7d")
    rows = [
        json.loads(line)
        for line in ch(queries.ch_k1("ch_col", trace, ct0, ct1) + "\nFORMAT JSONEachRow").splitlines()
        if line.strip()
    ]
    if len(rows) != 1:
        fail(f"K1 clickhouse returned {len(rows)} rows")
    return rows[0]["Body"]


def k1_elasticsearch(manifest: dict) -> str:
    trace = manifest["sample_trace_id"]
    et0, et1 = bound(queries.es_time, "7d")
    fetched = es(
        "POST",
        f"/{templates.ARMS['es-col']}/_search?request_cache=false",
        queries.es_k1(trace, et0, et1),
    )
    if fetched["hits"]["total"]["value"] != 1:
        fail(f"K1 elasticsearch returned {fetched['hits']['total']['value']}")
    return fetched["hits"]["hits"][0]["_source"]["body"]


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Rebuild report_CH_ES_1GB.md from the size JSON files."""

from __future__ import annotations

import json
import statistics
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORT = ROOT / "columnar-fulltext" / "report_CH_ES_1GB.md"
# 100 GB stays out of the published report until that run has storage + gate JSON.
SIZES = (1, 2, 5, 10, 20)
CH_ARMS = ("ch-col", "ch-ft", "ch-ng", "ch-scan")
ES_ARMS = ("es-col", "es-ft", "es-phrase", "es-ng", "es-scan")
PAIR_ARMS = ("col", "ft", "phrase", "ng", "scan")


def load_json(path: Path) -> dict | None:
    if not path.is_file():
        return None
    return json.loads(path.read_text())


def mb(n: int | float | None) -> str:
    if n is None:
        return "—"
    return f"{n / 1e6:.1f} MB"


def bytes_cell(n: int | None) -> str:
    if n is None:
        return "—"
    return f"{n:,}"


def ratio(es: int | None, ch: int | None) -> str:
    if not es or not ch:
        return "—"
    return f"{es / ch:.2f}×"


def warm_ms(reps: list[dict]) -> float | None:
    if len(reps) < 3:
        return None
    return statistics.median([reps[1]["engine_wall_ms"], reps[2]["engine_wall_ms"]])


def warm_cpu(reps: list[dict]) -> float | None:
    values = [row.get("cpu_seconds") for row in reps[1:3] if row.get("cpu_seconds") is not None]
    if len(values) != 2:
        return None
    return statistics.median(values)


def corpus_meta(size: int) -> dict | None:
    path = ROOT / "data" / "corpus" / f"{size}gb" / "manifest.json"
    if not path.is_file():
        return None
    manifest = json.loads(path.read_text())
    return {
        "events": manifest["event_count"],
        "bytes": manifest["actual_bytes"],
        "sha": manifest["sha256"],
        "seed": manifest["seed"],
    }


def storage_for(size: int) -> dict | None:
    return load_json(ROOT / "results" / f"columnar-fulltext-storage-{size}gb.json")


def gate_for(size: int) -> dict | None:
    if size == 1:
        path = ROOT / "results" / "columnar-fulltext-gate.json"
    else:
        path = ROOT / "results" / f"columnar-fulltext-gate-{size}gb.json"
    return load_json(path)


def search_for(size: int) -> dict | None:
    if size == 1:
        path = ROOT / "results" / "columnar-fulltext-search.json"
    else:
        path = ROOT / "results" / f"columnar-fulltext-search-{size}gb.json"
    return load_json(path)


def ch_total(storage: dict, arm: str) -> int | None:
    row = storage.get("clickhouse", {}).get(f"ch-{arm}")
    return None if row is None else int(row["total_bytes"])


def es_total(storage: dict, arm: str) -> int | None:
    row = storage.get("elasticsearch", {}).get(f"es-{arm}")
    return None if row is None else int(row["store_bytes"])


def mermaid_size(size: int, storage: dict) -> str:
    labels = []
    ch_vals = []
    es_vals = []
    for arm in PAIR_ARMS:
        labels.append(arm)
        ch = ch_total(storage, arm)
        es = es_total(storage, arm)
        ch_vals.append("0" if ch is None else f"{ch / 1e6:.1f}")
        es_vals.append("0" if es is None else f"{es / 1e6:.1f}")
    return "\n".join(
        [
            "```mermaid",
            "xychart-beta",
            f'    title "On-disk size at {size} GB (MB)"',
            f"    x-axis [{', '.join(labels)}]",
            "    y-axis \"MB\"",
            f"    bar [{', '.join(ch_vals)}]",
            f"    bar [{', '.join(es_vals)}]",
            "```",
            "",
            "First bar is ClickHouse, second bar is Elasticsearch. `phrase` has no ClickHouse table; that query runs on `ch-ft`.",
        ]
    )


def verification_block(size: int, gate: dict, meta: dict) -> list[str]:
    counts = gate.get("counts") or {}
    wanted = [
        ("rows:ch_col", "ClickHouse col row count"),
        ("rows:ch_ft", "ClickHouse token-index row count"),
        ("rows:ch_ng", "ClickHouse n-gram row count"),
        ("rows:ch_scan", "ClickHouse scan row count"),
        ("rows:es-col", "Elasticsearch col doc count"),
        ("rows:es-ft", "Elasticsearch token-index doc count"),
        ("rows:es-phrase", "Elasticsearch phrase doc count"),
        ("rows:es-ng", "Elasticsearch n-gram doc count"),
        ("rows:es-scan", "Elasticsearch scan doc count"),
        ("C1:7d", "cluster=dc1 in 7 days"),
        ("F1:7d", "token zzq_s5 in dc1, 7 days"),
        ("F5:7d", "phrase connection refused, 7 days"),
        ("F6:7d", "substring abc, 7 days"),
        ("F6-token:7d", "token abc, 7 days"),
    ]
    lines = [
        f"### {size} GB",
        "",
        f"Corpus sha256 `{meta['sha']}`, {meta['events']:,} events, {meta['bytes']:,} raw JSONL bytes. "
        f"Gate `passed={gate.get('passed')}`.",
        "",
        "| Check | Expected | Got |",
        "| --- | ---: | --- |",
    ]
    for key, label in wanted:
        row = counts.get(key)
        if not row:
            lines.append(f"| {label} | — | missing |")
            continue
        expected = row.get("expected")
        got = ", ".join(
            f"{arm}={value:,}" for arm, value in row.items() if arm != "expected"
        )
        lines.append(f"| {label} | {expected:,} | {got} |")
    lines.append("")
    lines.append(
        "Every arm in a row had to equal the expected value or the run stopped. "
        "C6 hourly rejection counts and the sample stack-trace body were checked as well."
    )
    lines.append("")
    return lines


def search_rows(search: dict) -> list[str]:
    order = ["C1", "F1", "F5", "F6", "F6-token"]
    rows = list(search.get("rows") or [])
    rows.sort(
        key=lambda row: (
            order.index(row["query_id"]) if row["query_id"] in order else 99,
            0 if row["engine"] == "clickhouse" else 1,
            row["arm"],
        )
    )
    lines = [
        "| Query | Meaning | Count | Arm | Wall ms, 3 reps | Warm wall ms | Warm CPU-seconds | Full-text index |",
        "| --- | --- | ---: | --- | --- | ---: | ---: | --- |",
    ]
    for row in rows:
        reps = row["repetitions"]
        wall = ", ".join(str(item["engine_wall_ms"]) for item in reps)
        warm = warm_ms(reps)
        cpu = warm_cpu(reps)
        cpu_cell = "—" if cpu is None else f"{cpu:.3f}"
        warm_cell = "—" if warm is None else (str(int(warm)) if float(warm).is_integer() else f"{warm:.1f}")
        lines.append(
            f"| {row['query_id']} | {row['statement']} | {row['count']:,} | {row['arm']} | {wall} | "
            f"{warm_cell} | {cpu_cell} | {row.get('full_text_index_used')} |"
        )
    lines.append("")
    return lines


def storage_total_table(size: int, storage: dict, raw_bytes: int) -> list[str]:
    lines = [
        f"### {size} GB total size",
        "",
        "| Arm | ClickHouse bytes | ClickHouse per raw GB | Elasticsearch bytes | Elasticsearch per raw GB | ES / CH |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    scale = raw_bytes / 1e9
    for arm in PAIR_ARMS:
        ch = ch_total(storage, arm)
        es = es_total(storage, arm)
        ch_per = "—" if ch is None else mb(ch / scale)
        es_per = "—" if es is None else mb(es / scale)
        lines.append(
            f"| {arm} | {bytes_cell(ch)} | {ch_per} | {bytes_cell(es)} | {es_per} | {ratio(es, ch)} |"
        )
    lines.append("")
    return lines


def storage_buckets(size: int, storage: dict) -> list[str]:
    lines = [
        f"#### ClickHouse at {size} GB",
        "",
        "| Arm | Inverted | All columns | Body column | Remainder | Parts |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for arm in CH_ARMS:
        row = storage.get("clickhouse", {}).get(arm)
        if not row:
            lines.append(f"| {arm} | — | — | — | — | — |")
            continue
        lines.append(
            f"| {arm} | {row['inverted_bytes']:,} | {row['all_columns_bytes']:,} | "
            f"{row['body_bytes']:,} | {row['remainder_bytes']:,} | {row.get('parts', '—')} |"
        )
    lines += [
        "",
        f"#### Elasticsearch at {size} GB",
        "",
        "| Arm | Inverted | Doc values | Points | Stored fields | Store size |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for arm in ES_ARMS:
        row = storage.get("elasticsearch", {}).get(arm)
        if not row:
            lines.append(f"| {arm} | — | — | — | — | — |")
            continue
        lines.append(
            f"| {arm} | {row['inverted_bytes']:,} | {row['doc_values_bytes']:,} | "
            f"{row.get('points_bytes', 0):,} | {row.get('stored_fields_bytes', 0):,} | {row['store_bytes']:,} |"
        )
    lines += [
        "",
        f"#### Body field at {size} GB",
        "",
        "| Arm | Body inverted | Body doc values | ClickHouse Body column |",
        "| --- | ---: | ---: | ---: |",
    ]
    for short in PAIR_ARMS:
        es = storage.get("elasticsearch", {}).get(f"es-{short}")
        ch = storage.get("clickhouse", {}).get(f"ch-{short}")
        lines.append(
            f"| {short} | {bytes_cell(None if es is None else es.get('body_inverted_bytes'))} | "
            f"{bytes_cell(None if es is None else es.get('body_doc_values_bytes'))} | "
            f"{bytes_cell(None if ch is None else ch.get('body_bytes'))} |"
        )
    lines.append("")
    return lines


def build() -> str:
    available = []
    for size in SIZES:
        storage = storage_for(size)
        gate = gate_for(size)
        search = search_for(size)
        if storage or gate or search:
            available.append(size)

    lines = [
        "# ClickHouse and Elasticsearch columnar / full-text, 1–20 GB",
        "",
        "Elasticsearch has [columnar storage](https://www.elastic.co/search-labs/blog/elasticsearch-columnar-storage) "
        "that stores field values together, the same idea as ClickHouse columns. This run loads one synthetic log "
        "corpus into both engines, in several layouts, and asks two questions: how much disk each layout uses, and "
        "whether a word search actually uses a word index.",
        "",
        "The 1 GB prefix was measured on Colima at 2 CPU / 2 GB. The 2, 5, and 10 GB prefixes were measured on "
        "4 CPU / 12 GB. The 20 GB prefix was measured on 6 CPU / 18 GB, 300 GB disk. Storage bytes can be compared "
        "across sizes. Search times from 1 GB sit on a smaller VM and are not a hardware-fair scaling curve. None of "
        "these numbers fill [columnar-fulltext-spec.md](../columnar-fulltext-spec.md) §13 (that table wants 16 cores / 64 GB).",
        "",
        "## TL;DR",
        "",
        "| | 1 GB | 2 GB | 5 GB | 10 GB | 20 GB |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
        "| Events | 2,016,000 | 4,024,000 | 10,064,000 | 20,120,000 | 40,232,000 |",
        "| Gate | passed | passed | passed | passed | passed |",
        "| Closest ES/CH arm | ng 1.02× | ng 1.14× | ng 1.02× | ng 1.07× | ng 1.09× |",
        "| Widest ES/CH arm | scan 1.53× | scan 1.59× | scan 1.60× | scan 1.55× | scan 1.56× |",
        "| Token `zzq_s5` in dc1 | 16 | 31 | 79 | 157 | 312 |",
        "| Substring `abc` / token `abc` | 20 / 0 | 40 / 0 | 101 / 0 | 201 / 0 | 403 / 0 |",
        "",
        "N-gram is the closest on-disk layout at every size. Scan is the widest, because Elasticsearch keeps a "
        "doc-values copy of every typed field. A word search uses the word index; a character search uses the n-gram "
        "index; the same character sequence inside a longer token does not hit the word index.",
        "",
        "## In plain language",
        "",
        "Each **arm** is the same pile of log lines stored a different way. `col` keeps typed fields (cluster, "
        "status, duration) easy to filter and does not index the words in the message. `ft` builds a **word index**: "
        "a dictionary of tokens that points at the lines containing each word. `phrase` (Elasticsearch only) also "
        "stores word order, so \"connection refused\" is two words next to each other, not just both present. `ng` "
        "indexes every overlapping 3-character slice, which finds `abc` inside `traceabc`. `scan` stores the message "
        "and reads it; it is the control that shows what the indexes are saving you from.",
        "",
        "A **token** is a word after splitting on anything that is not `0-9A-Za-z`. `zzq_s5` becomes `zzq` and `s5`. "
        "A **substring** search looks at characters, not words. **Doc values** are Elasticsearch's column-like copy "
        "of a field. **Inverted** bytes are the word/n-gram dictionary. **Wall time** is how long you wait. "
        "**CPU-seconds** is how much processor time ClickHouse used; Elasticsearch CPU was not sampled on this host.",
        "",
        "| Arm | What ClickHouse stores | What Elasticsearch stores | What a search does |",
        "| --- | --- | --- | --- |",
        "| col | Columns plus a bloom filter on `TraceId`. No index on the log line. | `body` is a keyword that is not inverted. Values live in doc values. | Filters on typed fields. Reading the message is extra. |",
        "| ft | `idx_body`, a skip index of tokens (`splitByNonAlpha`). | `body` is `match_only_text` (tokens, no word positions). | \"Does this line contain these words?\" |",
        "| phrase | No extra table. The phrase query runs on `ch-ft` by scanning the line. | `body` is `text` with positions. | \"Are these words next to each other?\" |",
        "| ng | `idx_ng`, every overlapping 3-character slice. | `body` is a `wildcard` field. | \"Does this character sequence appear, even inside a longer token?\" |",
        "| scan | The `Body` column only. | `body` is an unindexed keyword; a script reads doc values. | Same question as `ft` / `ng`, answered by reading the raw line. |",
        "",
        "## How the test is checked",
        "",
        "The 2, 5, 10, and 20 GB files are **nested prefixes** of one 7-day corpus: the 2 GB file starts with the 1 GB "
        "file, the 5 GB file starts with the 2 GB file, and so on. Same time window, more events per hour. The "
        "needles (`zzq_s5`, `connection refused`, `abc` inside `traceabc`) are planted at a known rate, so the "
        "expected hit count is in `manifest.json` and `incidents.json` for that size.",
        "",
        "A size is accepted only when all of this holds:",
        "",
        "1. Tokenizer: 10 fixture lines produce the same tokens in Python, ClickHouse `splitByNonAlpha`, and the Elasticsearch analyzer.",
        "2. Load: every arm has exactly `manifest.event_count` rows.",
        "3. Multiline: the sample stack-trace body comes back as one event, with the original newlines.",
        "4. Point lookup: one known `trace_id` returns the same body on both engines.",
        "5. Query counts: C1, C2, F1, F2, F4, F5, F6, F6-token match the per-hour totals in the manifest, on every arm that claims that meaning.",
        "6. Incident C6: hourly `payment-api` rejected counts match INC-2.",
        "7. Search: the timed repetitions of a query all return that same count.",
        "",
        "The query text is frozen at 1 GB in `driver/queries.py`. Larger sizes reuse that text; they do not rewrite it.",
        "",
    ]

    lines.append("## Corpus")
    lines.append("")
    lines.append("| Size | Events | Raw JSONL bytes | sha256 |")
    lines.append("| --- | ---: | ---: | --- |")
    for size in SIZES:
        meta = corpus_meta(size)
        if not meta:
            lines.append(f"| {size} GB | — | — | not generated yet |")
            continue
        lines.append(f"| {size} GB | {meta['events']:,} | {meta['bytes']:,} | `{meta['sha']}` |")
    lines.append("")
    lines.append("Engines: ClickHouse 26.2.19.43, Elasticsearch 9.5.5 `logsdb_columnar`, one shard, zero replicas. One engine process at a time. Merges idle. No `force_merge`. Search window is 7 days, `2026-09-01` through `2026-09-08`. Three repetitions after that engine restarted; page cache left in place. Warm time is the median of repetitions 2 and 3.")
    lines.append("")

    lines.append("## Storage by arm")
    lines.append("")
    lines.append("Disk after the load, with merges idle. `phrase` exists only on Elasticsearch; ClickHouse answers the phrase query from `ch-ft`.")
    lines.append("")
    storages = {size: storage_for(size) for size in SIZES}
    if any(storages.values()):
        lines.append("### ClickHouse total bytes")
        lines.append("")
        header = "| Arm |" + "".join(f" {size} GB |" for size in SIZES)
        lines.append(header)
        lines.append("| --- |" + "".join(" ---: |" for _ in SIZES))
        for arm in ("col", "ft", "ng", "scan"):
            cells = []
            for size in SIZES:
                storage = storages[size]
                cells.append("—" if not storage else bytes_cell(ch_total(storage, arm)))
            lines.append(f"| ch-{arm} | " + " | ".join(cells) + " |")
        lines.append("")
        lines.append("### Elasticsearch total bytes")
        lines.append("")
        lines.append(header)
        lines.append("| --- |" + "".join(" ---: |" for _ in SIZES))
        for arm in PAIR_ARMS:
            cells = []
            for size in SIZES:
                storage = storages[size]
                cells.append("—" if not storage else bytes_cell(es_total(storage, arm)))
            lines.append(f"| es-{arm} | " + " | ".join(cells) + " |")
        lines.append("")
        lines.append("### ES / CH at each size")
        lines.append("")
        lines.append(header)
        lines.append("| --- |" + "".join(" ---: |" for _ in SIZES))
        for arm in ("col", "ft", "ng", "scan"):
            cells = []
            for size in SIZES:
                storage = storages[size]
                if not storage:
                    cells.append("—")
                else:
                    cells.append(ratio(es_total(storage, arm), ch_total(storage, arm)))
            lines.append(f"| {arm} | " + " | ".join(cells) + " |")
        lines.append("")
    lines.append("")

    for size in SIZES:
        storage = storage_for(size)
        if not storage:
            lines.append(f"_{size} GB storage snapshot is not on disk yet._")
            lines.append("")
            continue
        meta = corpus_meta(size)
        if not meta:
            lines.append(f"_{size} GB corpus manifest is missing._")
            lines.append("")
            continue
        lines.extend(storage_total_table(size, storage, meta["bytes"]))
        lines.append(mermaid_size(size, storage))
        lines.append("")
        lines.extend(storage_buckets(size, storage))

    lines.append("## Verification")
    lines.append("")
    for size in SIZES:
        gate = gate_for(size)
        if not gate:
            lines.append(f"_{size} GB gate result is not on disk yet._")
            lines.append("")
            continue
        meta = corpus_meta(size)
        if not meta:
            lines.append(f"_{size} GB corpus manifest is missing._")
            lines.append("")
            continue
        lines.extend(verification_block(size, gate, meta))

    lines.append("## Search")
    lines.append("")
    lines.append("Timed only after the counts above matched. 1 GB search ran on the 2 GB Colima VM. 2/5/10 GB search ran on 4 CPU / 12 GB. 20 GB search ran on 6 CPU / 18 GB.")
    lines.append("")
    for size in SIZES:
        search = search_for(size)
        if not search:
            lines.append(f"_{size} GB search result is not on disk yet._")
            lines.append("")
            continue
        lines.append(f"### {size} GB")
        lines.append("")
        lines.extend(search_rows(search))

    lines += [
        "## What the sizes say",
        "",
        "Disk scales close to linearly through 20 GB. ClickHouse `col` is 141 MB at 1 GB of JSONL and 2.65 GB at 20 GB "
        "of JSONL (132–141 MB per raw GB). Elasticsearch `col` is 190–214 MB per raw GB. The ES/CH ratio is stable: "
        "n-gram stays the closest pair (1.02×–1.14×), scan the widest (1.53×–1.60×), tokens in the middle (1.29×–1.47×). "
        "At 20 GB those ratios are ng 1.09×, ft 1.36×, col 1.44×, scan 1.56×.",
        "",
        "The extra Elasticsearch bytes are mostly doc values of the typed fields, not a second copy of `_source` "
        "(`stored_fields` is 0 on every arm). The word index is the increment on top of that base: at 20 GB, "
        "`es-ft` body postings are 273 MB and `ch-ft` `idx_body` is 469 MB; `es-ng` body postings are 810 MB and "
        "`ch-ng` `idx_ng` is 1.73 GB. Positions on `es-phrase` take the body postings to 596 MB.",
        "",
        "Verification holds at every size: row counts match the manifest, token `zzq_s5` in `dc1` is "
        "16 / 31 / 79 / 157 / 312, the phrase is 8,064 / 16,096 / 40,256 / 80,480 / 160,928, substring `abc` is "
        "20 / 40 / 101 / 201 / 403, and token `abc` is 0. That last zero is the point of the decoy: `traceabc` "
        "contains the characters `abc` but is one token, so the word index correctly ignores it.",
        "",
        "Indexed search stays in the tens of milliseconds on this host through 20 GB (`es-phrase` 3.5 ms warm, "
        "`es-ft` token 9 ms, `ch-ft` token 11 ms, `ch-ng` 32.5 ms). The scan arms do not: `es-scan` token search is "
        "4.5 s warm at 20 GB, `ch-scan` 280 ms. ClickHouse 26.2 still has no phrase index, so `ch-ft` answers F5 by "
        "scanning `Body` (144 ms warm at 20 GB) while `es-phrase` uses positions (3.5 ms). These times are a macOS "
        "smoke test with the page cache in place; they are not spec §13.",
        "",
        "## Notes",
        "",
        "Warm CPU-seconds is empty on every `es*` row: Elasticsearch `took` is waiting time, and this host does not "
        "sample the container cgroup. ClickHouse CPU comes from `system.query_log`. These runs do not fill spec §13. "
        "The 20 GB load used a 14 GB container limit (Elasticsearch heap 6 GB); ClickHouse `max_server_memory_usage` "
        "was 12 GB.",
        "",
        "Raw JSON: [results/](../results/). Query text: [driver/queries.py](driver/queries.py). Layouts: "
        "[clickhouse/ddl.sql](clickhouse/ddl.sql), [elasticsearch/templates.py](elasticsearch/templates.py).",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    REPORT.write_text(build())
    print(f"wrote {REPORT}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

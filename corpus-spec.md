# Corpus specification

One dataset, generated once, replayed byte-identically to all four engines. If the engines
see different bytes or different schema intent, we are comparing schema decisions rather
than engines — the reason this is a separate document with its own acceptance checks.

Used by [benchmark-plan.md](benchmark-plan.md) and [queries/matrix.md](queries/matrix.md).

---

## 1. Shape and volume

| Property | Value | Rationale |
| --- | --- | --- |
| Raw size | 500 GB uncompressed JSONL | agreed target |
| Average event | ~500 bytes | typical Kubernetes JSON app log |
| Event count | ~1.0 x 10^9 | 500 GB / 500 B |
| Logical time span | 7 days | makes 1h / 24h / 7d query windows meaningful |
| Shard files | 500 x 1 GB, gzip-compressed at rest | parallel replay, cheap to store |

Logical timestamps span seven days; replay is as fast as the engine will accept. So the
corpus represents roughly 71 GB/day (~1,650 events/s) of production traffic, while the rig
ingests it in under an hour. Keep those two numbers distinct in the report — the first is
what we are modelling, the second is a throughput measurement.

---

## 2. Event schema

The canonical record. One JSON object per line, no trailing whitespace, UTC timestamps with
microsecond precision.

```json
{
  "timestamp": "2026-09-01T04:17:22.481913Z",
  "cluster": "dc1",
  "namespace": "payments",
  "app": "checkout-api",
  "pod": "checkout-api-7d9f4b8c6-x2k9p",
  "container": "app",
  "host": { "name": "node-014" },
  "level": "error",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "http": { "method": "POST", "path": "/v1/charge", "status": 500 },
  "duration_ms": 1284,
  "attributes": { "tenant": "acme", "retry": "2" },
  "body": "charge failed for order 88213: upstream returned 503 zzq7f3a91c"
}
```

Field roles:

| Field | Role | Cardinality |
| --- | --- | --- |
| `timestamp` | time dimension, all engines | — |
| `cluster`, `namespace`, `app`, `pod` | stream / label identity | see §3 |
| `host.name` | first sort key for Elasticsearch `logsdb` | 60 |
| `level` | low-cardinality filter, drives the alerting scenario | 5 |
| `trace_id` | high-cardinality exact lookup (Q4, Q13) | ~10^8 |
| `http.*`, `duration_ms` | numeric aggregation (Q10) | path ~200 |
| `attributes` | sparse long tail, stresses schema flexibility | see §5 |
| `body` | the full-text target for every search query | — |

`host.name` is not decoration. Elasticsearch `logsdb` derives most of its storage advantage
from sorting on `host.name` before `@timestamp` (up to ~40% versus under 10% for
timestamp-first), so omitting a host dimension would quietly understate ES
([explanation.md §1.7](explanation.md)).

---

## 3. Stream cardinality

Two arms, because label cardinality is the variable that most often decides these
comparisons and the realistic case and the pathological case give opposite answers.

**Baseline arm — 200 streams.** 2 clusters x 5 namespaces x 4 apps x 5 pods. Deliberately
modest: this is a well-behaved platform where pods are stable and labels are curated.

**Pathological arm — ~50,000 streams.** Same cluster/namespace/app structure, but 1,250
pod identities per app to simulate pod churn over the seven days, with each pod alive for a
bounded window. This is what a real platform with frequent rollouts looks like, and it is
where Loki's index and ClickHouse's primary-key sort order behave very differently.

Event distribution is skewed, not uniform: the top 10% of streams carry ~60% of volume
(Zipf-like). Uniform distribution is the single most common way a synthetic log benchmark
becomes useless, because it makes every pruning strategy look equally good.

---

## 4. Content mix

| Share | Content | Purpose |
| --- | --- | --- |
| 55% | structured application events — templated messages with variable ids and values | the common case |
| 15% | nginx/envoy-style access lines inside `body` | high-volume, highly repetitive, compresses well |
| 20% | free-form text: human-written messages, no template | full-text search with no schema help |
| 10% | multi-line Java stack traces (20-60 lines, `\n` inside `body`) | the hardest real-world case |

Level distribution: 78% info, 14% warn, 6% error, 1.5% debug, 0.5% fatal. Errors cluster in
bursts rather than arriving uniformly, which matters for the alerting scenario — a uniform
error rate would make Q8's `> 100 per minute` threshold either always or never true. Baseline
bursts are causeless noise; the six planted incidents in §7 are the ones with a known cause
and a recorded onset.

Traffic volume follows a diurnal curve with a 3x peak-to-trough ratio rather than being flat
in time. This is load-bearing for `INC-2`: without varying traffic, a rising defect *ratio*
and a rising defect *count* would be indistinguishable, and the whole point of that incident
would be lost.

**Multi-line events** are emitted as a single JSONL record with `\n` inside `body`, matching
the common Kubernetes JSON-logging setup. This is deliberate: joining multi-line output is a
*collector* problem, and we are benchmarking engines. Phase 0 still verifies each engine
returns the stack trace as one logical event rather than 40 fragments.

---

## 5. Sparse attributes (the schema-flexibility stress)

`attributes` carries 1-6 keys per event, drawn from a pool of ~2,000 distinct keys with a
long tail: 20 keys appear on most events, the remaining ~1,980 appear on fewer than 0.1%.

This is the realistic shape of OpenTelemetry attributes and it is where the four engines
diverge structurally:

- **ClickHouse:** `Map(LowCardinality(String), String)` per ClickStack's default guidance,
  plus a `JSON`-typed arm on a subset to compare. Watch `max_dynamic_paths` (default 1024)
  against our ~2,000 keys — paths beyond the threshold fall into a shared overflow file
  that is queryable but slower. Surfacing that boundary is the point.
- **Elasticsearch:** `index.mapping.total_fields.limit` defaults to 1,000. A 2,000-key tail
  will hit it. Whether we raise the limit, use `flattened`, or accept mapping rejection is a
  **capability finding to record**, not a configuration detail to quietly fix.
- **Loki / VictoriaLogs:** attributes live in the line body or as structured
  metadata/fields; the cost shows up at query time rather than as a mapping limit.

---

## 6. Planted needles

Search cost depends overwhelmingly on selectivity, so selectivity is a controlled variable
rather than something we discover afterwards. Needles are synthetic tokens with the prefix
`zzq` followed by hex, chosen so they cannot occur naturally in generated text, and the
generator asserts the exact planted count for each.

| Needle | Planted occurrences | Selectivity | Used by |
| --- | --- | --- | --- |
| `zzq_s9` | 1 | 1 in 10^9 | Q1 best case — exact expected answer is 1 |
| `zzq_s7` | 100 | 1 in 10^7 | Q1, Q3 |
| `zzq_s5` | 10,000 | 1 in 10^5 | Q1, Q12 |
| `zzq_pa` | 10,000 | 1 in 10^5 | Q2 term A |
| `zzq_pb` | 10,000 | 1 in 10^5 | Q2 term B |
| `zzq_pa` + `zzq_pb` co-occurring | 100 | 1 in 10^7 | Q2 — the AND result |

The Q2 pair is designed so the AND result (100) is two orders of magnitude smaller than
either term alone (10,000 each). An engine that evaluates the terms independently and
intersects late will show it, and a broken query that accidentally means OR is immediately
obvious from the count.

**Delimiter safety.** Every needle is surrounded by non-alphanumeric characters wherever it
is planted, so token matching and substring matching agree. That is what lets Loki's
substring `|=` be compared fairly against ClickHouse `hasAllTokens` and Elasticsearch
`match` ([queries/matrix.md §1](queries/matrix.md)).

**Deliberate decoys for Q5.** A separate token `abc` is planted 10,000 times *inside* larger
tokens (`traceabc=1`, `xxabcyy`) and never as a standalone word. Substring engines find all
10,000; token engines find zero without an n-gram index. The gap between those two results
is exactly the measurement Q5 exists for.

Needle placement is uniform across time and streams so the same needle is usable at 1h, 24h
and 7d windows without changing its density.

---

## 7. Planted incidents (ground truth)

Needles measure how fast an engine *counts* something we already know to look for. Incidents
measure whether an engine helps us *find* something we do not. Both are needed, and the
second is what the original note was reaching for when it listed log exploration — "if we
don't know which part to search in future" — as Elasticsearch's luxury feature.

Without this section the corpus is statistically flat: errors are bursty but causeless, so no
query can be scored on whether it led an engineer to an answer. With it, we know the exact
start time, shape, blast radius and cause of every anomaly in 500 GB, which is something no
production dataset can give us.

Six incidents, deliberately few, so the generator stays tractable and each one is
individually traceable in results.

| ID | Type | Shape | Detectable by |
| --- | --- | --- | --- |
| `INC-1` | Correlated crash cascade | sharp: 50x connection errors in 90s | absolute threshold |
| `INC-2` | Gradual ratio drift | slow: 2% to 6% over 48h | ratio + trend, **not** a static threshold |
| `INC-3` | Single-pod outlier | 1 of 50 pods failing, steady | per-pod grouping only |
| `INC-4` | Silent failure | error rate drops to zero | absence-of-signal detection |
| `INC-5` | Novel log shape | a template never seen before | pattern/categorization |
| `INC-6` | Latency degradation | p95 doubles, error count flat | numeric percentile only |

### INC-1 — correlated crash cascade (the "system dumping" case)

At a fixed timestamp, `checkout-api` pods begin emitting connection errors at roughly 50x
baseline for 90 seconds. One pod also emits an OOM-kill and a JVM crash-dump signature with a
multi-line stack trace. Within 5-20 seconds, its two downstream callers (`orders-api`,
`web-bff`) begin emitting timeouts naming the failed upstream, and a subset of the events
share `trace_id` values with the originating failures.

This requires the generator to carry a small service dependency graph — the main added
complexity in this section, and the reason the incident count is capped at six. The causal
lag is what makes the incident *investigable* rather than merely visible: the symptom appears
first in the caller, and the cause is upstream and slightly earlier.

Ground truth recorded: onset timestamp, the 90s window, the affected pod set, the root-cause
pod, the shared trace ids, and the expected event counts per service.

### INC-2 — gradual ratio drift (the payment case)

Over 48 hours, `payment-api` rejection rate rises from 2% to 6% in small increments, while
total payment traffic follows a normal diurnal curve with a 3x peak-to-trough ratio.

The diurnal curve is the point. Because traffic varies more than the defect rate does, the
**absolute count of rejections is not monotonic** — it falls overnight even as the rate
climbs. Any alert built on rejection count rather than rejection ratio will both miss this
and fire spuriously at the next traffic peak. This is why Q14 exists as a separate query
class: no aggregation currently in the suite can express it
([queries/matrix.md §16](queries/matrix.md)).

Ground truth recorded: the per-hour intended rate, the per-hour total and rejected counts, and
the hour at which the rate first exceeds 3%, 4% and 5%.

### INC-3 — single-pod outlier

One pod out of fifty in `inventory-api` has a 30% error rate for six hours while the other
forty-nine sit at baseline. Aggregate service error rate rises only to about 0.6%, which is
inside normal noise, so the incident is invisible to any service-level aggregate and obvious
the moment you group by pod. Tests whether a group-by at realistic cardinality is cheap enough
that an engineer would actually run it.

### INC-4 — silent failure

For four hours, one app stops logging entirely: not errors, not anything. The absence of a
signal is the signal. Included because it is the incident class that rule-based alerting
reliably misses, and because detecting it requires a query over expected-but-absent streams
rather than over events.

### INC-5 — novel log shape

A log template that appears nowhere else in the corpus starts appearing at low volume. No
needle token, no error level, nothing to filter on unless you are clustering log lines by
shape. This is what the pattern-detection capability row in
[notebook/logging-decision-handbook.md](notebook/logging-decision-handbook.md) is scored
against.

### INC-6 — latency degradation with flat error count

`duration_ms` for one endpoint doubles at p95 while p50 and the error count stay flat. Only a
numeric percentile aggregation finds it (Q10). Included because it is the most common real
degradation that log *counting* cannot see at all.

### Rules for planted incidents

1. Incidents are placed in the middle 5 days of the 7-day span, so every incident has at least a day of clean baseline either side for comparison queries.
2. No two incidents overlap in time **and** share an app, so each can be scored independently.
3. Incident events are otherwise ordinary corpus events — same schema, same fields. Nothing marks them as planted except the ground-truth manifest.
4. `incidents.json` is emitted alongside `manifest.json`: id, type, onset, end, affected streams, root cause, expected counts per bucket, and the earliest timestamp at which each is *theoretically* detectable. That last field is the denominator for detection-delay measurement.
5. The generator asserts each incident's event counts, exactly as it asserts needle counts.

### Why this is worth the generator complexity

Two returns beyond the query suite.

It gives **detection quality** a ground truth. Detection delay against a known onset is a
real number; detection delay against "when we noticed in production" is not. We can score a
static threshold rule, an anomaly rule and an LLM on the same six incidents and compare them
directly — the experiment §4.7 of [revise-syscon.txt](revise-syscon.txt) calls for. `INC-2`
is the decisive case: no static threshold handles a 2%-to-6% drift well, since 3% pages on
noise and 5% means you have been degrading for a day. `INC-4` and `INC-5` are the cases hard
rules miss structurally.

Caveat to keep in the report: planted incidents are synthetic, so strong LLM performance here
is weaker evidence than strong performance on real history. Treat this as the controlled
experiment and an alert replay as the reality check, and do not let the clean result stand
alone.

---

## 8. Determinism and integrity

1. Single seeded PRNG; the seed is recorded in the manifest. Same seed produces byte-identical output.
2. Generation is sharded by output file, each shard seeded as `seed + shard_index`, so generation parallelises without affecting reproducibility.
3. `manifest.json` records: generator version and git commit, seed, shard count, per-shard SHA-256 and byte count, total bytes, event count, and the asserted planted-needle counts. `incidents.json` records the incident ground truth per §7.
4. Post-generation assertions, all of which must pass before the corpus is usable:
   - every planted needle count matches its target exactly
   - no needle token occurs accidentally outside its planted positions
   - every planted incident's event counts match its target, and its onset falls inside the middle 5 days
   - no two incidents overlap in both time and app
   - stream count matches the arm (200 or ~50,000)
   - every line is valid JSON with a monotonic-per-stream timestamp
   - total bytes within 1% of 500 GB
5. Every benchmark result record carries the corpus manifest hash, so a number can always be traced back to the exact bytes that produced it.

---

## 9. Replay

The driver reads shards in timestamp order and pushes to the engine's native ingest path.
Timestamps are baked into the corpus and are **never rewritten**, so all four engines index
the same logical time range.

| Engine | Ingest path |
| --- | --- |
| Elasticsearch | `_bulk` against the `logs-sysbench-default` data stream |
| Loki | `/loki/api/v1/push`, labels extracted per §10 |
| ClickHouse | native protocol, `JSONEachRow`, explicit batch size |
| VictoriaLogs | `/insert/jsonline` (or `vlagent` for the two-cluster tier-2 arm) |

Two configuration traps that will otherwise waste a day each:

- **Loki rejects old samples by default.** Our corpus spans seven days of logical time, so
  set `reject_old_samples: false` (or raise `reject_old_samples_max_age` beyond 7d), and
  raise the per-stream and per-tenant ingestion rate limits. Otherwise Loki silently drops
  most of the corpus and posts an excellent throughput number.
- **ClickHouse batch size is a tuning parameter, not an implementation detail.** Tiny
  batches produce too many parts and trigger merge pressure; huge batches inflate
  ingest-to-visible latency. Record the batch size used, and keep it at whatever we would
  genuinely run in production, since Q11 measures the visibility latency it causes.

The driver enforces a shared rate limiter so phase 2's background ingest can be pinned at
50% of the slowest engine's knee, and records acked vs attempted counts per shard for the
durability accounting in C3.

---

## 10. Per-engine schema translation

Same intent, four expressions. Each is reviewed by whoever knows that engine best, per
fairness rule 6 in [benchmark-plan.md](benchmark-plan.md).

**Loki** — stream labels `{cluster, namespace, app, pod}` only: 200 streams in the baseline
arm. The full JSON document is the log line, so field access goes through `| json`.
`level`, `trace_id` and `http.status` are shipped as **structured metadata** in the
bloom/structured-metadata arm only, never in the baseline arm — mixing the two would make
Loki's headline number unreproducible.

**Elasticsearch** — data stream `logs-sysbench-default`, `index.mode: logsdb`, sort
`host.name` then `@timestamp`. `body` mapped as `match_only_text` (the log-oriented text
type), identity fields as `keyword`, `duration_ms` and `http.status` as numerics,
`attributes` per §5.

**ClickHouse**

```sql
CREATE TABLE logs
(
    Timestamp    DateTime64(6, 'UTC'),
    Cluster      LowCardinality(String),
    Namespace    LowCardinality(String),
    App          LowCardinality(String),
    Pod          String,
    HostName     LowCardinality(String),
    Level        LowCardinality(String),
    TraceId      String,
    SpanId       String,
    HttpMethod   LowCardinality(String),
    HttpPath     LowCardinality(String),
    HttpStatus   UInt16,
    DurationMs   UInt32,
    Attributes   Map(LowCardinality(String), String),
    Body         String,

    INDEX idx_body  Body    TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64,
    INDEX idx_trace TraceId TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Cluster, Namespace, App, Timestamp);
```

`PARTITION BY toDate()` is explicit because MergeTree has no default partitioning — the
correction in [explanation.md §2.2](explanation.md) — and because day partitions are what
make retention a partition drop instead of a TTL mutation. The `GRANULARITY 64` on the text
index is a starting point to tune within the engine's tuning budget, and a second n-gram
index is added only for the Q5 arm.

**VictoriaLogs** — `_time` from `timestamp`, `_msg` from `body`, stream fields
`cluster, namespace, app, pod`, everything else as regular fields.

---

## 10.1 Sanity check before phase 1

Load one 1 GB shard into each engine and confirm: event counts match across all four, a
known needle returns its exact planted count, one stack trace comes back whole, and on-disk
size per engine is within the expected order of magnitude. Catching a schema mistake here
costs an hour; catching it after a 500 GB load costs a day per engine.

---

## 11. Generator

A small Go or Python program in `tools/gen-corpus/`. Requirements rather than an
implementation, since the implementation is trivial and the requirements are what matter:

- Deterministic from a seed; parallel by shard; streams output (never materialises 500 GB in memory).
- Emits `manifest.json` per §8 and `incidents.json` per §7, and exits non-zero if any assertion in §8.4 fails.
- Carries a small service dependency graph (who calls whom, with a latency budget) so `INC-1` can produce causally ordered downstream failures. This is the only structural addition the incidents require.
- `--arm baseline|pathological` selects stream cardinality; `--size` allows a small slice (the 5 GB phase 0 slice is the same generator with the same seed and proportionally scaled needle counts).
- Needle counts are asserted, not sampled.

The 5 GB phase 0 slice keeps the same needle *selectivities* rather than the same absolute
counts, so `zzq_s9` becomes unusable at that size — phase 0 correctness checks use `zzq_s5`
and the Q2 pair, which still plant hundreds of occurrences in 5 GB.

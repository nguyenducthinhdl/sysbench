# Logging decision handbook

> **STATUS: SKELETON.** Every cell marked `TBD` is filled from benchmark results. Nothing
> here is a recommendation until the phase that produces it has run. Cells that stay `TBD`
> after phase 4 are gaps we are choosing to live with, and they must be listed in §9 rather
> than left blank.

This is the deliverable the 20 Sept meeting asked for: not a benchmark report, but the
document an engineer reads when deciding where a new log stream should go. Benchmark numbers
are the evidence underneath it.

Source documents: [../revise-syscon.txt](../revise-syscon.txt) (decisions),
[../benchmark-plan.md](../benchmark-plan.md) (method),
[../explanation.md](../explanation.md) (why the original assumptions changed),
[../queries/matrix.md](../queries/matrix.md) (query definitions).

---

## 1. Recommendation in one paragraph

> Fill last. Name the engine (or the pair), the workload it is for, the one number that
> decided it, and the strongest argument against it. If this paragraph needs more than five
> sentences, we have not finished thinking.

`TBD`

---

## 2. Scenario to engine

The main table. A reader who gets no further than this page should still make the right
call.

| Scenario | Recommended | Why | Avoid | Evidence |
| --- | --- | --- | --- | --- |
| Incident triage: unknown search terms, last 24h | `TBD` | `TBD` | `TBD` | Q1, Q2, Q5 |
| Trace correlation: known trace id, full retention | `TBD` | `TBD` | `TBD` | Q4 |
| Alerting on log patterns (errors/min) | `TBD` | `TBD` | `TBD` | Q8 |
| Gradual degradation (rate drifting over days) | `TBD` | `TBD` | `TBD` | Q14, `INC-2` |
| Correlated multi-service incident, root cause | `TBD` | `TBD` | `TBD` | Q15, `INC-1` |
| Finding a problem nobody filtered for | `TBD` | `TBD` | `TBD` | Q16, `INC-5` |
| Dashboards: top-K, repeated every 30s | `TBD` | `TBD` | `TBD` | Q9 |
| Business/product analytics over log fields | `TBD` | `TBD` | `TBD` | Q10, Q13 |
| Compliance archive: write once, query rarely | `TBD` | `TBD` | `TBD` | C3 storage, Q12 |
| Very high volume, low query rate (access logs) | `TBD` | `TBD` | `TBD` | C3 |
| Agent-driven exploratory querying | `TBD` | `TBD` | `TBD` | Q12, C5 quotas |

---

## 3. Capability matrix (phase 0)

Filled before any performance number exists. `Y` / `N` / `Y*` where the asterisk means
"possible, with a cost recorded in the notes".

| Capability | Elasticsearch | Loki | ClickHouse | VictoriaLogs |
| --- | --- | --- | --- | --- |
| Unordered multi-term AND, indexed (Q2) | `TBD` | `TBD` | `TBD` | `TBD` |
| Mid-token substring (Q5) | `TBD` | `TBD` | `TBD` | `TBD` |
| Ordered phrase (Q7) | `TBD` | `TBD` | `TBD` | `TBD` |
| High-cardinality exact field (Q4) | `TBD` | `TBD` | `TBD` | `TBD` |
| Multi-line events as one event | `TBD` | `TBD` | `TBD` | `TBD` |
| Continuous alerting rules (Q8) | `TBD` | `TBD` | `TBD` | `TBD` |
| Ratio / rate-of-change aggregation (Q14) | `TBD` | `TBD` | `TBD` | `TBD` |
| Automatic log pattern detection (Q16) | `TBD` | `TBD` | `TBD` | `TBD` |
| Numeric percentiles on parsed fields (Q10) | `TBD` | `TBD` | `TBD` | `TBD` |
| Sparse attribute tail without mapping failure | `TBD` | `TBD` | `TBD` | `TBD` |
| Per-tenant query quotas | `TBD` | `TBD` | `TBD` | `TBD` |
| In-cluster replication | `TBD` | `TBD` | `TBD` | **N** (by design) |

The VictoriaLogs cell is pre-filled because it is settled by documentation, not measurement:
`vlinsert` shards without replicating and the maintainers have confirmed in-cluster
replication is not planned ([explanation.md §1.6](../explanation.md)).

**Semantic pinned for this round:** `TBD` (TOKEN or SUBSTRING — see
[queries/matrix.md §1](../queries/matrix.md)). Record the answer here prominently, because
every latency number in §4 is only valid for the semantic actually tested.

---

## 4. Query cost (phase 2)

Cost per query, at durability tier T2, concurrency 1, cold cache. Warm and higher
concurrency in `results/`.

| Query | Metric | ES | Loki | CH | VL |
| --- | --- | --- | --- | --- | --- |
| Q1 @ 1 in 10^7, 24h | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q2 @ 24h | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q2 @ 7d | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q4 @ 7d | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q5 @ 24h | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q9 @ 24h | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q12 @ 7d | p95 wall / CPU-s, or refused | `TBD` | `TBD` | `TBD` | `TBD` |
| Q14 @ 48h | p95 wall / CPU-s | `TBD` | `TBD` | `TBD` | `TBD` |
| Q15 session | total wall time, steps 1-7 | `TBD` | `TBD` | `TBD` | `TBD` |
| Q15 session | reached root cause? | `TBD` | `TBD` | `TBD` | `TBD` |
| Q15 session | expert vs non-expert gap | `TBD` | `TBD` | `TBD` | `TBD` |

Scaling behaviour, which generalises better than any single latency:

| Engine | Cost vs time window | Cost vs selectivity | Cost vs concurrency | Cold/warm ratio |
| --- | --- | --- | --- | --- |
| Elasticsearch | `TBD` | `TBD` | `TBD` | `TBD` |
| Loki | `TBD` | `TBD` | `TBD` | `TBD` |
| ClickHouse | `TBD` | `TBD` | `TBD` | `TBD` |
| VictoriaLogs | `TBD` | `TBD` | `TBD` | `TBD` |

---

## 5. Ingest, storage and durability (phase 1 and 3)

| Metric | ES | Loki | CH | VL |
| --- | --- | --- | --- | --- |
| Ingest MB/s per core, T1 | `TBD` | `TBD` | `TBD` | `TBD` |
| Ingest MB/s per core, T2 | `TBD` | `TBD` | `TBD` | `TBD` |
| On-disk GB per 100 GB raw | `TBD` | `TBD` | `TBD` | `TBD` |
| Compression ratio | `TBD` | `TBD` | `TBD` | `TBD` |
| Ingest-to-visible p99 | `TBD` | `TBD` | `TBD` | `TBD` |
| Behaviour past the knee | `TBD` | `TBD` | `TBD` | `TBD` |
| Acked-but-lost on `kill -9` | `TBD` | `TBD` | `TBD` | `TBD` |
| Query availability during node loss | `TBD` | `TBD` | `TBD` | `TBD` |
| **HA hardware multiplier (T2/T1 nodes)** | `TBD` | `TBD` | `TBD` | `TBD` |

The last row is the one most likely to overturn a decision made on the rows above it. An
engine that is 30% more efficient but needs twice the hardware for HA is 40% more expensive.

Kafka TLS constant (measured once, applies to any design with Kafka in the path):

| Measurement | Value |
| --- | --- |
| Broker CPU per MB/s, TLS off | `TBD` |
| Broker CPU per MB/s, mTLS on | `TBD` |
| Overhead | `TBD` |

This exists because the previous benchmark was distorted by it. Kafka cannot use `sendfile`
under TLS and there is no kTLS path for it, so the overhead is a constant to budget rather
than a bug to fix ([explanation.md §3.4](../explanation.md)).

---

## 6. Alerting cost (phase 2, C4)

| Metric | ES | Loki | CH (ad-hoc) | CH (materialized view) | VL |
| --- | --- | --- | --- | --- | --- |
| CPU-s per rule evaluation | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |
| Cost of 50 concurrent rules | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |
| Detection delay p95 | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |

The ClickHouse materialized-view column has no equivalent in the other engines and should be
read as a different mechanism, not a better score.

### Incident detection against ground truth

Each of the six planted incidents ([corpus-spec.md §7](../corpus-spec.md)) is scored found or
missed, with delay measured from its recorded onset rather than from when someone noticed.

| Incident | What it tests | Found by | Delay from onset | Missed by |
| --- | --- | --- | --- | --- |
| `INC-1` crash cascade | sharp spike + causal pivot | `TBD` | `TBD` | `TBD` |
| `INC-2` ratio drift 2%→6% | ratio over long window | `TBD` | `TBD` | `TBD` |
| `INC-3` single-pod outlier | per-pod grouping | `TBD` | `TBD` | `TBD` |
| `INC-4` silent failure | absence of signal | `TBD` | `TBD` | `TBD` |
| `INC-5` novel log shape | pattern detection | `TBD` | `TBD` | `TBD` |
| `INC-6` latency degradation | numeric percentile | `TBD` | `TBD` | `TBD` |

### Detection mechanism comparison (feeds the AI decision)

Same six incidents, three mechanisms, one corpus. This is the experiment
[revise-syscon.txt §4.7](../revise-syscon.txt) calls for, and it is nearly free once the rig
exists.

| Mechanism | Incidents found | False positives over 7d | Median detection delay |
| --- | --- | --- | --- |
| Static threshold rules | `TBD` | `TBD` | `TBD` |
| Anomaly / statistical rules | `TBD` | `TBD` | `TBD` |
| LLM triage | `TBD` | `TBD` | `TBD` |

`INC-2` is the decisive row: no static threshold handles a 2%-to-6% drift well, because 3%
pages on noise and 5% means we have been degrading for a day. `INC-4` and `INC-5` are the ones
hard rules miss structurally rather than by tuning.

**Caveat that must survive into any summary of this table:** these incidents are synthetic, so
a strong LLM result here is weaker evidence than a strong result on real alert history. Treat
this as the controlled experiment and an alert replay as the reality check.

---

## 7. Sizing guidance

The part of this document that gets used most often after the decision is made. Derived from
CPU-seconds per query and MB/s per core, not extrapolated from wall-clock times.

| Daily volume | Retention | Engine | Nodes | Cores | RAM | Disk | Assumptions |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 TB/day | 30d | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |
| 5 TB/day | 30d | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |
| 5 TB/day | 90d | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |
| 20 TB/day | 30d | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` | `TBD` |

Always state the query load the sizing assumes. A cluster sized for ingest alone will fall
over the first time someone runs Q12 — which, per
[revise-syscon.txt §4.5](../revise-syscon.txt), an agent eventually will.

---

## 8. Retention tier crossover

Replaces the meeting's "within 1 day use Elastic, beyond that use Loki" heuristic with a
measured curve. Note that Elasticsearch `logsdb` may have moved this substantially, which is
the whole reason we are re-measuring it.

| Age of data | Cheapest engine meeting p95 < 5s | Cheapest engine meeting p95 < 60s | Cost per GB-day |
| --- | --- | --- | --- |
| 0-1 day | `TBD` | `TBD` | `TBD` |
| 1-7 days | `TBD` | `TBD` | `TBD` |
| 7-30 days | `TBD` | `TBD` | `TBD` |
| 30+ days | `TBD` | `TBD` | `TBD` |

**Crossover conclusion:** `TBD`

---

## 9. Architecture recommendation

### Single engine or tiered?

Decision trigger from [revise-syscon.txt §3.1](../revise-syscon.txt): what percentage of log
volume needs indexed search within 24h? Below roughly 10-20% a tiered design wins; above
that, one well-tuned engine is simpler and cheaper.

- Measured/reported percentage: `TBD`
- Recommendation: `TBD`
- If tiered: dual-write or promotion, and the routing rule: `TBD`

### Object storage or local disk?

From the phase 3 isolation arm, settling the disagreement in the original note:

| Metric | MinIO | Local filesystem |
| --- | --- | --- |
| Q2 @ 7d p95 | `TBD` | `TBD` |
| Bytes fetched per query | `TBD` | `TBD` |
| Object-store requests per query | `TBD` | `TBD` |
| p99 TTFB | `TBD` | `TBD` |

**Conclusion:** `TBD`

### Known gaps and caveats

Everything we did not test, and everything a reader should distrust. Fill honestly — this
section is what makes the rest of the document credible.

- Storage tiering (hot/warm/cold) was out of scope for round 1; all numbers are single-tier.
- Loki's Kafka + dataobj path is experimental and was benchmarked as a separate arm, if at all: `TBD`
- Loki bloom-filter arm applies only to structured metadata, never to free-text search, and its numbers must not be read as Loki's baseline.
- `TBD` — add every deviation from [benchmark-plan.md](../benchmark-plan.md) discovered during execution.

---

## 10. Operational notes per engine

The "what nobody tells you until week three" section. Filled during execution, not at the
end — these are cheap to write down when discovered and expensive to reconstruct.

**Elasticsearch** — `logsdb` and sort order matter enormously; `total_fields.limit` defaults
to 1,000 and our sparse attribute tail exceeds it; `track_total_hits` must be set or counts
silently cap at 10,000. Plus: `TBD`

**Loki** — not stateless: ingesters hold in-memory chunks plus WAL. `reject_old_samples`
must be disabled to replay a historical corpus, or most of it is silently dropped. SSD mode
is removed in 4.0, so benchmark and deploy distributed. Plus: `TBD`

**ClickHouse** — the text index is silently skipped when whole tokens cannot be extracted,
so `EXPLAIN indexes=1` is mandatory; requires >= 26.2 for GA text indexes; no default
partitioning, state `PARTITION BY` explicitly; batch size is a real tuning parameter that
trades merge pressure against visibility latency. Plus: `TBD`

**VictoriaLogs** — no in-cluster replication, so HA means two clusters fed by `vlagent`; a
downed `vlstorage` node makes its shard unqueryable while writes continue to healthy nodes;
`vlagent` buffer sizing (`-remoteWrite.maxDiskUsagePerURL`) governs how long an outage it can
absorb. Plus: `TBD`

---

## 11. How to reproduce

- Corpus: [corpus-spec.md](../corpus-spec.md), manifest hash `TBD`
- Queries: [queries/matrix.md](../queries/matrix.md), frozen suite commit `TBD`
- Raw results: `results/` (newline-delimited JSON, one file per phase)
- Engine configurations: `configs/<engine>/`, including every non-default setting
- Rig: `TBD` (node models, kernel, NIC, disk models)

Any number in this handbook must be traceable to a result record carrying its engine
version, durability tier, cache state and corpus manifest hash. If it is not traceable, it
does not belong here.

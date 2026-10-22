# Columnar and full-text specification

ClickHouse and Elasticsearch only. This document freezes the comparison between columnar
access and full-text search: what each mechanism is, which arm exercises it, which queries
are allowed to speak for it, and which numbers are allowed to move a decision.

Companion documents:

- [corpus-spec.md](corpus-spec.md) — the dataset. Binding here. Do not invent a second corpus.
- [queries/matrix.md](queries/matrix.md) — the four-engine suite. Families below reuse its semantics and, where noted, its query ids.
- [benchmark-plan.md](benchmark-plan.md) — the four-engine logwar. This spec does not replace it.
- [columnar-fulltext-plan.md](columnar-fulltext-plan.md) — how to execute this spec.

If this document and the plan disagree, this document wins.

---

## 1. Question

A logging query does two different kinds of work.

**Columnar access** reads a typed field: time, level, status, duration, app, pod. Values of
one field sit together, so a filter or an aggregate on that field reads that column
(ClickHouse) or its doc-values and points representation (Elasticsearch `logsdb`). The text
of the log line is not tokenized and is not read.

**Full-text access** resolves a predicate over words in the log line through an inverted
structure. Elasticsearch's primary structure for `body` is the Lucene inverted index.
ClickHouse's primary structure is still the column; its text index is a skip index that
drops granules, after which the surviving granules are read as columns.

**String scan** is the third path, and it is mandatory to measure. A predicate over `body`
that never consults the inverted structure reads the raw text. A fast string scan reported
as full text makes the inverted index look better or worse than it is.

**Keyword point lookup** is a fourth path, kept separate so it cannot be quoted as evidence
for either of the other two. An exact match on `trace_id` uses Elasticsearch's keyword term
dictionary. ClickHouse keeps `TraceId` out of the sort key and uses a bloom skip index plus
a column read.

The structural asymmetry is the reason to measure both engines on both paths:

| | Columnar structure | Inverted structure |
| --- | --- | --- |
| ClickHouse | primary (MergeTree columns, sort order) | secondary (text skip index prunes granules) |
| Elasticsearch | secondary (doc values, points, `logsdb` codecs) | primary for `body` (Lucene postings) |

A wall-time comparison of "ClickHouse versus Elasticsearch" adds these two structures
together. This spec reports them apart, then reports the mixed query that production
actually runs: a token predicate followed by a columnar aggregate.

---

## 2. Decisions this spec is allowed to support

Six questions. Each has one headline cell in §13. No other cell may be cited as the answer.

| Id | Question |
| --- | --- |
| Q-FT | At a pinned token semantic, how do CPU-seconds scale with input size and time window for the ClickHouse text index versus the Elasticsearch inverted index? |
| Q-COL | For filters and aggregates that never touch `body`, how do CPU-seconds scale with input size? |
| Q-MIX | After a token predicate, what fraction of a grouped aggregate is the inverted step, and what fraction is the columnar step? |
| Q-CROSS | At 1 in 10^5 and a 24h window, at which input sizes does the ClickHouse text index cost more CPU-seconds than scanning the text column? |
| Q-STORE | Per raw gigabyte, how many on-disk bytes are inverted structure, how many are columnar structure, and how many are stored source? |
| Q-VIS | At the batch size we would actually ship, what is ingest-to-visible latency for a token query? |

Q-VIS exists because the 20 Sept note claimed Elasticsearch is real-time and ClickHouse
becomes visible only after a batch. [explanation.md §4.2](explanation.md) says that claim
is backwards and must be measured. One probe. It is not an ingest bake-off.

---

## 3. Scope

In scope: the two engines, the arms in §5, the families in §7, one machine, one copy of
the data, local disk.

Owned by [benchmark-plan.md](benchmark-plan.md), and untouched here: Loki, VictoriaLogs,
replication, `kill -9`, object storage, per-tenant quotas, alerting-product behaviour,
and the 500 GB ingest knee.

Also out of this spec: BM25 ranking, highlighting, suggestions, and fuzzy search. Log
search here is a filter. Elasticsearch runs in filter context. Counts use `size: 0` and
`track_total_hits: true`.

---

## 4. Engines

| Engine | Version floor | Configuration that is part of the result |
| --- | --- | --- |
| ClickHouse | >= 26.2 | `MergeTree`, `PARTITION BY toDate(Timestamp)`, text index GA with no experimental flag |
| Elasticsearch | 9.x | data stream `logs-sysbench-default`, `index.mode: logsdb`, sort `host.name` then `@timestamp` |

ClickHouse 25.x text indexes are beta behind `enable_full_text_index`. Elasticsearch
without `logsdb` is a different storage layout. Either substitution voids the run.
Version pinning rationale: [explanation.md §1.4](explanation.md) and
[explanation.md §1.7](explanation.md).

---

## 5. Arms

An arm is one physical layout. A query is timed on the arm it names. If the proof in §10
shows the inverted structure was not used, the run is stored as `scan` and stays on that
arm. It is not quietly reissued against a different arm and reported under the original name.

### 5.1 ClickHouse

Same columns and the same sort on every arm:
`ORDER BY (Cluster, Namespace, App, Timestamp)`, matching
[corpus-spec.md §10](corpus-spec.md). Separate tables, so a query cannot silently pick up
the other index.

| Arm | Index | What it is allowed to speak for |
| --- | --- | --- |
| `ch-col` | no index on `Body`. Bloom on `TraceId` for family K only | Q-COL, keyword lookup |
| `ch-ft` | `INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64` | Q-FT, phrase, Q-MIX |
| `ch-ng` | `INDEX idx_ng Body TYPE text(tokenizer = 'ngrams(3)') GRANULARITY 64` | substring |
| `ch-scan` | no index on `Body`. Predicates are `LIKE` or `hasToken` issued against this table | string-scan ceiling |

`GRANULARITY 64` is the baseline, inherited from the corpus spec. One other granularity may
be run inside the tuning budget. Its arm id is `ch-ft-g<N>`. Its numbers stay out of the
`ch-ft` cells.

`ch-ft` and `ch-ng` are different tables. Building both indexes on one table would let the
planner answer a substring from the token index, or the reverse, and the storage split
would be unreadable.

### 5.2 Elasticsearch

One index template per arm. Every arm uses `logsdb` and the host-then-timestamp sort.
The analyzer for every indexed text field is pinned: split on non-alphanumeric characters,
case-sensitive, no lowercase, no stemming, no stop words. That is the Elasticsearch
expression of ClickHouse `splitByNonAlpha`, which is the tokenizer
[queries/matrix.md §1](queries/matrix.md) already requires.

| Arm | `body` | What it is allowed to speak for |
| --- | --- | --- |
| `es-col` | `index: false`, `doc_values: false`. Typed fields stay keyword or numeric with doc values on | Q-COL, keyword lookup |
| `es-ft` | `match_only_text` with the pinned analyzer | Q-FT, Q-MIX |
| `es-phrase` | `text`, `index_options: positions`, same analyzer | phrase |
| `es-ng` | `wildcard`, or a `text` subfield `body.ng` with an n-gram tokenizer | substring |
| `es-scan` | `body` stored and searchable only by a leading wildcard or a script that has no inverted entry | string-scan ceiling |

`es-col` leaves `body` unindexed so the columnar storage number does not contain an
inverted index. The field may still be reconstructed for the fetch arm; that cost is the
stored-source bucket in §9, not the columnar bucket.

`match_only_text` does not store positions. A phrase query on `es-ft` verifies candidates
from source. That run is labelled `verify-from-source` and is never reported as `es-phrase`.

---

## 6. Dataset

[corpus-spec.md](corpus-spec.md) is binding: event schema, stream cardinality, content mix,
planted needles, the `abc` decoy, delimiter safety, and the manifest hash.

The size axis is five uncompressed inputs. Every query family in §7 is timed at every size.
The published result is the curve across these five points.

| Size | Role |
| --- | --- |
| 1 GB | smallest point; the size at which query text is frozen |
| 2 GB | second point |
| 5 GB | third point |
| 10 GB | fourth point |
| 20 GB | largest point |

The five sizes are nested prefixes of one generation, one seed. The 1 GB file is a
timestamp-ordered prefix of the 2 GB file, and the same relation holds up to 20 GB. Each
size has its own manifest hash and its own asserted needle counts. Each size is loaded
into an empty table or index. Appending the next prefix onto parts left over from the
previous size is a different physical layout and does not count as that size.

Raw input of 20 GB fits in the 64 GB cgroup before compression. Cold and warm in §12 are
page-cache states on that resident working set. Out-of-core behaviour on the 500 GB corpus
stays with the logwar plan.

**Hit floor.** A timing cell requires the manifest to assert at least 20 hits for that
needle at that size. Fewer than 20 is stored as `unmeasured` and is not filled by scaling.
At about 500 bytes an event, 1 GB is about 2×10^6 events and 20 GB is about 4×10^7.

| Selectivity | Hits at 1 GB | Hits at 20 GB | Timed in this series? |
| --- | --- | --- | --- |
| 1 in 10^5 (`zzq_s5`) | ~20 | ~400 | yes, at every size |
| 1 in 10^7 (`zzq_s7`, and the F2 AND) | < 1 | ~4 | count check only |
| 1 in 10^9 (`zzq_s9`) | < 1 | < 1 | count check only |

So every timed full-text cell uses 1 in 10^5. The rarer needles stay in the manifest,
because corpus-spec defines them, and they do not become latency numbers at or below 20 GB.
A selectivity crossover needs those rarer needles with at least 20 hits, which means a
corpus past this series.

One extra measurement, taken after each load, is allowed: the document frequency of one
natural token near 1 in 10^5. It is used only by §8.4. It is not planted, and it is not a
second corpus.

---

## 7. Query families

Every query below runs at each size in §6. TOKEN versus SUBSTRING is pinned per query, and
counts must match across engines inside one semantic before any timing is kept. The rule
and the reason are [queries/matrix.md §1](queries/matrix.md): the engine that returns fewer
rows looks faster.

Time bounds are `$T0` and `$T1`. Needles are the `zzq_*` tokens from corpus-spec §6.
Every count query is `SELECT count()` or an Elasticsearch search with `"size": 0` and
`"track_total_hits": true`.

### 7.1 Family C — columnar

The predicate and the aggregate read typed columns only. `Body` is neither filtered nor
projected. A query that looks for the word `error` inside the line is family F or M, even
if the author meant "errors".

| Id | Statement | Windows | Matrix cousin |
| --- | --- | --- | --- |
| C1 | count events in the window with `cluster = dc1` | 1h, 24h, 7d | — |
| C2 | count where `level = error` and `app = checkout-api` | 1h, 24h, 7d | — |
| C3 | count of `level = error` grouped by minute and app | 24h | Q8, column form |
| C4 | top 10 pods by that error count | 1h, 24h, 7d | Q9, column form |
| C5 | p95 of `duration_ms` by `http.path` | 24h | Q10 |
| C6 | hourly rejection ratio for `payment-api` | 48h | Q14, column form |

ClickHouse runs family C on `ch-col`. Elasticsearch runs it on `es-col`.

C6 needs a typed rejection flag. The corpus records it on the payment events that
[corpus-spec.md §7](corpus-spec.md) already requires for `INC-2`. The column is the
source of the ratio. A text match on `rejected` is a different query and is not C6.

### 7.2 Family K — keyword point lookup

| Id | Statement | Window | Arm |
| --- | --- | --- | --- |
| K1 | 100 events for one `trace_id`, ordered by time | 7d | `ch-col` with the bloom index, `es-col` with a `term` on keyword `trace_id` |

This is the matrix Q4 shape. It speaks for keyword lookup only.

### 7.3 Family F — full text

| Id | Statement | Semantic | Arms | Windows |
| --- | --- | --- | --- | --- |
| F1 | one planted token at 1 in 10^5 | TOKEN | `ch-ft`, `es-ft`, both scan arms | 1h, 24h, 7d |
| F2 | both `zzq_pa` and `zzq_pb`, any order | TOKEN | `ch-ft`, `es-ft` | 1h, 24h, 7d |
| F3 | F1 narrowed to `namespace = payments` and `app = checkout-api` | TOKEN | `ch-ft`, `es-ft` | 24h |
| F4 | token present and the token `healthcheck` absent | TOKEN | `ch-ft`, `es-ft` | 24h |
| F5 | the phrase `connection refused` | phrase | `ch-ft`, `es-ft`, `es-phrase` | 24h |
| F6 | the decoy `abc` inside a larger token | SUBSTRING | `ch-ng`, `es-ng`, both scan arms | 24h |

Expressions are the ClickHouse and Elasticsearch texts already written for Q1, Q2, Q3, Q5,
Q6, and Q7 in [queries/matrix.md](queries/matrix.md). Phase 0 of the plan freezes the final
text once counts match; until then those texts are the starting point, as the matrix itself
says.

F1-fetch is F1 over 24h, returning 100 bodies. It is the materialization cost. It does not
replace the count.

F2 is a count check at every size. Its AND result is the 1 in 10^7 needle, which stays
under the hit floor through 20 GB, so F2 has no timing cell in this series.

F3 still returns a count. It is the prefilter. Grouping after a token predicate is family M.

F5 on `es-ft` carries the label `verify-from-source`. F5 on `es-phrase` is the positions
number. Both counts must equal `ch-ft`.

F6 on a token index is expected to miss the decoy or to fall through to a scan. That
outcome is the result. It is recorded, and it is not repaired by adding the n-gram index
to `ch-ft` or `es-ft`.

### 7.4 Family M — token predicate, then a columnar aggregate

All three use `zzq_s5` (1 in 10^5) and a 24h window, on `ch-ft` and `es-ft`, at every size.

| Id | Statement |
| --- | --- |
| M1 | count grouped by app |
| M2 | p95 of `duration_ms` |
| M3 | top 10 pods by count |

The profile has to split the inverted step from the aggregate step. A run without that
split can support Q-FT and cannot support Q-MIX.

### 7.5 Family V — visibility

During a sustained ingest, insert one canary event whose body contains a fresh token, and
issue F1 for that token until it returns 1.

| Engine | Batch the headline arm uses |
| --- | --- |
| ClickHouse | the batch size selected by phase 1A. If phase 1A has not finished, synchronous inserts of 100,000 rows, arm label `provisional` |
| Elasticsearch | `refresh_interval: 1s`, bulk bodies of the same uncompressed size as the ClickHouse batch |

A second Elasticsearch arm at `refresh_interval: 30s` is allowed only when that is the
production setting, and it is labelled `es-ft-r30`. The headline arm remains 1s.

Record ack time and first-visible time. p50 and p99 over at least 100 canaries. Family V
runs during the 20 GB load. Visibility is a function of batch and refresh, and size is not
its axis.

---

## 8. Sweeps

### 8.1 Size

The size sweep is the five inputs in §6. Families C, K, F, and M run at each of them,
concurrency 1. A cell under the hit floor is `unmeasured` at that size.

### 8.2 Window

F1 at 1 in 10^5, and C2, at 1h, 24h, and 7d, at each size. Every size spans the same seven
logical days, so the window is a fraction of the events and not a stand-in for the size
axis. This pair is the measurement behind the "search within a day / search beyond a day"
heuristic in the 20 Sept note.

### 8.3 Concurrency

Concurrency 1 is the headline, at every size. After those runs are inside the repetition
tolerance, run concurrency 10 on F1 (1 in 10^5, 24h) and on C5 at 1 GB and at 20 GB.
Concurrency 50 belongs to the logwar plan.

### 8.4 Natural-token check

After each load, measure document frequency and choose one token already present at that
size whose frequency is near 1 in 10^5. Run F1 only. When the CPU-seconds ratio against
`zzq_s5` at that size differs by more than 2×, the natural token is the headline for that
size and the planted-needle figure is reported beside it.

---

## 9. Metrics

One JSON object per run, in `results/columnar-fulltext.ndjson`.

Identity: `engine`, `version`, `arm`, `family`, `query_id`, `semantic`, `size_gb`,
`selectivity`, `window`, `concurrency`, `cache` (`cold` or `warm`), `repetition`, corpus
manifest hash, non-default settings.

Result: `count`, client wall-time p50 / p95 / p99, engine CPU-seconds over the query
interval, rows or docs examined, bytes read, `index_used`, and the proof excerpt from §10.

ClickHouse, from `system.query_log`: `read_rows`, `read_bytes`. From `EXPLAIN indexes = 1`:
granules selected and granules total.

Elasticsearch: `took`, and the `profile` breakdown summed into four buckets — inverted,
doc values, fetch, aggregation. The record states whether the request cache and the query
cache were on.

Storage, one record per arm per size after merges have settled, `type: storage`:

| Bucket | ClickHouse source | Elasticsearch source |
| --- | --- | --- |
| Inverted | `system.data_skipping_indices` for the text index | `_disk_usage` `inverted_index` |
| Columnar | `system.parts_columns` for every column except the text-index bytes | `_disk_usage` `doc_values` plus `points` |
| Stored source | `Body` column alone, from `system.parts_columns` | `_disk_usage` `stored_fields` (synthetic `_source` included) |
| Total | on-disk table size | store size |

The published storage figure is bytes in each bucket per raw input gigabyte. A total
without the split does not answer Q-STORE.

`_disk_usage` is requested with `run_expensive_tasks=true` once per arm, after merges, on
a quiet index.

---

## 10. Proof the inverted structure was used

**ClickHouse.** `EXPLAIN indexes = 1`. `index_used` is true only when a `Skip` node names
the text index of that arm and selected granules are at most half of total granules.
Otherwise the run is reclassified `scan`. A tokenizer mismatch fails this way and raises
no error, which is why the proof is part of the result rather than a log to check later.

**Elasticsearch.** `"profile": true`. On `es-ft` and `es-phrase`, `index_used` is true
only when every text leaf is a `TermQuery`, a `PhraseQuery`, or a `BooleanQuery` of those.
On `es-ng`, a `WildcardQuery` or an n-gram `TermQuery` satisfies the proof. A
`MatchAllDocsQuery` plus a script is a scan.

A warm page cache does not satisfy the proof.

---

## 11. Correctness gate

The tokenizer check runs once, before any load. The other four checks run at every size,
against that size's asserted counts. Timing of a size starts after they hold for that size.
Query text freezes when they first hold, at 1 GB.

1. Ten fixture lines — a needle line, a stack trace, a line with the `abc` decoy — produce
   the same token list from Elasticsearch `_analyze` and from ClickHouse token extraction
   under `splitByNonAlpha`.
2. F1, F2, and F6 counts match across every arm that claims that semantic, including the
   scan arms.
3. C1 and C2 counts match between `ch-col` and `es-col`.
4. A multi-line stack trace comes back as one event.
5. F5 counts match across `ch-ft`, `es-ft`, and `es-phrase`.

A count mismatch stops the work. It is a semantic bug, and a latency delta taken from it
is void.

---

## 12. Fairness

The rules in [benchmark-plan.md §3](benchmark-plan.md) apply, bound as follows.

1. One engine process at a time. The other is stopped.
2. One machine. Cgroup of 16 cores and 64 GB. Data on one disk. Clustering is the logwar rig's job.
3. Cold is repetition 1, after a restart, `sync`, and a dropped page cache. Warm is the median of repetitions 2 and 3.
4. Three repetitions. A spread above 20% is investigated and stored as unstable. It is not averaged into a headline cell.
5. One corpus checksum, replayed byte-identically.
6. Filter context, no scores. Elasticsearch `request_cache` and `search.allow_expensive_queries` are set explicitly and written on the record.
7. Families C, K, F, and M run against a quiet index: ingest stopped, merges settled. Family V is the only phase with ingest in flight.
8. Tuning budget is half a day per engine. Every non-default setting is on the record. Tuned runs use a new arm id.
9. `force_merge` to one segment is forbidden on the baseline arms. One labelled arm `es-ft-forcemerge` may be run as a ceiling. It cannot fill the FT headline.
10. ClickHouse `max_threads` equals the cgroup core count. The Elasticsearch search thread pool stays at the default for that heap, and the default is recorded. CPU-seconds is the figure that crosses the threshold in §13. Wall time is reported beside it.

---

## 13. Headline cells and decision thresholds

Warm cache, concurrency 1, `index_used: true` (except the scan side of Q-CROSS). Each cell
is reported at each size. Ratios are not averaged across sizes.

| Cell | Run | Figure |
| --- | --- | --- |
| FT | F1, selectivity 1 in 10^5, window 24h | CPU-seconds `es-ft` / `ch-ft`, per size |
| COL | C5 | CPU-seconds `es-col` / `ch-col`, per size |
| MIX | M2 | inverted CPU-seconds and doc-values or aggregate CPU-seconds, each engine, per size |
| CROSS | F1 at 24h, selectivity 1 in 10^5, `ch-ft` against `ch-scan` | the sizes at which `ch-ft` CPU-seconds exceeds `ch-scan`. When `ch-ft` stays cheaper at every size, the cell is `not reached` |
| STORE | storage records | inverted, columnar, and stored-source bytes per raw GB, per baseline arm, per size |
| VIS | family V headline arms, during the 20 GB load | p50 and p99 ingest-to-visible |

Thresholds, applied to FT and COL at each size:

| Outcome | What the handbook paragraph is allowed to say |
| --- | --- |
| FT ≤ 2 and COL ≥ 3 | Full text is not a reason to add Elasticsearch for this workload. The columnar engine covers both paths. |
| FT ≥ 3 and COL ≤ 2 | The inverted index wins on-call token search, and the columnar gap is small. Elasticsearch stays the search engine. |
| FT ≥ 3 at 1h and FT ≤ 2 at 7d | The window heuristic survives. The recommendation names the measured window. |
| Both ratios inside 2× | Stop. Operability decides. Further query tuning is outside this spec. |

When the winning row differs across sizes, the curve is the answer. One size does not
outvote another.

CROSS, STORE, MIX, and VIS are published next to that paragraph. They do not move the
thresholds. Wall time does not move the thresholds.

---

## 14. Acceptance

This spec is executable when every arm has a mapping, every headline query has a text, and
every decision cell has a proof rule. The plan may order the work. It may not add a query,
a mapping, or a success condition.

# Query matrix

The frozen query suite. Every logical query is expressed once per engine, with the search
semantic pinned so the four engines are asked the same question, plus the command that
proves the engine used an index rather than scanning.

Depends on the schema in [../corpus-spec.md](../corpus-spec.md). Read
[../benchmark-plan.md](../benchmark-plan.md) for how these are run.

> Syntax for every engine must be validated against the deployed version during phase 0.
> Treat the expressions below as the intent plus a starting point, not as verified output.
> LogsQL and ES|QL in particular move quickly.

---

## 1. The pinned semantic

The meeting framed the requirement as "unordered search: a b = b a". That phrase hides a
fork the four engines take differently:

| Engine | Idiomatic multi-term AND | Matches inside a word? |
| --- | --- | --- |
| Loki | `\|= "abc" \|= "def"` | **yes** — substring |
| Elasticsearch | `match` + `operator: and` | no — token, post-analyzer |
| ClickHouse | `hasAllTokens(body, ['abc','def'])` | no — token, per index tokenizer |
| VictoriaLogs | `abc def` (implicit AND) | no — word |

A line containing `traceabc=1 svcdef=2` matches under substring semantics and does not
match under token semantics. So the same "query" can legitimately return different row
counts, and the engine returning fewer rows will look faster.

**Rule: every query below declares its semantic as TOKEN or SUBSTRING, and result counts
must match across engines within that semantic before any timing is recorded.**

Q1, Q2, Q3, Q4, Q6, Q7 are **TOKEN** — chosen as the default because it is the semantic all
four can index, and because our needles are whole tokens (request ids, error codes, trace
ids). Q5 is deliberately **SUBSTRING**, to expose what each engine costs when the needle is
not a whole token. Q9–Q14 and Q16 operate on typed fields rather than on log text, so the
semantic does not apply to them; Q15 mixes both because a real investigation does. That single query is where the engines diverge most, and it is why the
[open question in the revised note](../revise-syscon.txt) about which semantic on-call
actually needs has to be answered before phase 0.

To keep the token semantic honest we also pin the tokenizer: split on non-alphanumeric
characters, case-sensitive. ClickHouse `tokenizer = 'splitByNonAlpha'`, Elasticsearch a
custom analyzer on `body` using the `standard`-equivalent split without lowercasing,
VictoriaLogs' default word tokenization, and Loki emulating it via exact substring on
needles that are surrounded by delimiters in the corpus by construction
([corpus-spec.md](../corpus-spec.md) plants them that way precisely so Loki's substring
match and the others' token match agree).

---

## 2. Query classes

Placeholders: `$T0`/`$T1` time bounds, `$NEEDLE_A`/`$NEEDLE_B` planted needles at a chosen
selectivity, `$TRACE` a specific trace id.

| ID | Class | Semantic | Windows | Axis it measures |
| --- | --- | --- | --- | --- |
| Q1 | Single rare token | TOKEN | 1h, 24h, 7d | needle-in-haystack, all selectivities |
| Q2 | Unordered multi-term AND | TOKEN | 1h, 24h, 7d | the meeting's headline requirement |
| Q3 | Token + stream prefilter | TOKEN | 24h | value of the label/stream index |
| Q4 | High-cardinality exact field | TOKEN | 7d | trace lookup, the most common on-call query |
| Q5 | Mid-token substring | SUBSTRING | 24h | tokenizer limits, where token engines fall over |
| Q6 | AND + NOT | TOKEN | 24h | negation cost |
| Q7 | Ordered phrase | TOKEN | 24h | phrase support, contrast with Q2 |
| Q8 | Errors per minute (the alert) | TOKEN | 24h | C4 rule cost and detection delay |
| Q9 | Top-K group-by | n/a | 1h, 24h, 7d | aggregation scaling |
| Q10 | Numeric percentile by field | n/a | 24h | parsed-field aggregation |
| Q11 | Recent tail | TOKEN | 5m | freshness, ingest-to-visible |
| Q12 | Full-corpus unselective scan | TOKEN | 7d | cost ceiling, worst case |
| Q13 | High-cardinality group-by | n/a | 1h | stress; expected to degrade somewhere |
| Q14 | Ratio over a long window | n/a | 48h, 7d | gradual drift; absolute counts mislead |
| Q15 | Investigation session | mixed | 24h | time-to-answer across a query sequence |
| Q16 | Log pattern / categorization | n/a | 24h | finding a shape nobody filtered for |

Q1–Q4, Q8 and Q14 are **required**: failing one eliminates the engine. The rest are
**informational**: failure is a recorded capability gap, weighed in C5.

Q14–Q16 are scored against the planted incidents in
[corpus-spec.md §7](../corpus-spec.md), which is what gives them a ground truth to be right
or wrong about. Q1–Q13 ask how fast an engine counts something we already know to look for;
Q14–Q16 ask whether it helps us find something we do not.

---

## 3. Q1 — single rare token

Find occurrences of one rare token in a time window. Run at all three planted
selectivities (1 in 10^9, 10^7, 10^5).

**Loki**

```logql
count_over_time({cluster="dc1"} |= "$NEEDLE_A" [1h])
```

**Elasticsearch**

```json
POST /logs-sysbench-default/_search
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        { "range": { "@timestamp": { "gte": "$T0", "lt": "$T1" } } },
        { "term": { "cluster": "dc1" } },
        { "match": { "body": { "query": "$NEEDLE_A", "operator": "and" } } }
      ]
    }
  }
}
```

**ClickHouse**

```sql
SELECT count()
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Cluster = 'dc1'
  AND hasToken(Body, '$NEEDLE_A');
```

**VictoriaLogs**

```logsql
_time:[$T0, $T1] cluster:dc1 "$NEEDLE_A" | stats count() as hits
```

`track_total_hits: true` in Elasticsearch is not optional here. Without it ES stops counting
at 10,000 and would report a cheaper query than the others actually ran.

---

## 4. Q2 — unordered multi-term AND (the headline requirement)

Both needles present in the same event, in any order. This is the query the meeting opened
with.

**Loki**

```logql
count_over_time({cluster="dc1"} |= "$NEEDLE_A" |= "$NEEDLE_B" [1h])
```

Order of the filters does not affect results but does affect cost: Loki applies them
left to right, so put the more selective needle first and record which order was used.

**Elasticsearch**

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        { "range": { "@timestamp": { "gte": "$T0", "lt": "$T1" } } },
        { "match": { "body": { "query": "$NEEDLE_A $NEEDLE_B", "operator": "and" } } }
      ]
    }
  }
}
```

**ClickHouse**

```sql
SELECT count()
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, ['$NEEDLE_A', '$NEEDLE_B']);
```

**VictoriaLogs**

```logsql
_time:[$T0, $T1] "$NEEDLE_A" "$NEEDLE_B" | stats count() as hits
```

---

## 5. Q3 — token plus stream prefilter

Same needle, narrowed to one application. Isolates how much the label/stream index is
worth: comparing Q3 against Q1 at equal selectivity shows whether the engine prunes by
metadata before touching log content.

**Loki**

```logql
count_over_time({cluster="dc1", namespace="payments", app="checkout-api"} |= "$NEEDLE_A" [24h])
```

**Elasticsearch** — add to the `filter` array of Q1:

```json
{ "term": { "namespace": "payments" } },
{ "term": { "app": "checkout-api" } }
```

**ClickHouse**

```sql
SELECT count()
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Namespace = 'payments' AND App = 'checkout-api'
  AND hasToken(Body, '$NEEDLE_A');
```

The `ORDER BY (Cluster, Namespace, App, Timestamp)` primary key means this should prune via
the primary index before the text index is consulted. Confirm in `EXPLAIN indexes=1` that
both the PrimaryKey condition and the skip index appear.

**VictoriaLogs**

```logsql
_time:[$T0, $T1] cluster:dc1 namespace:payments app:checkout-api "$NEEDLE_A" | stats count() as hits
```

---

## 6. Q4 — high-cardinality exact field lookup

Retrieve every event for one trace id across the full retention window. This is the query
on-call actually runs during an incident, and it is the hardest case for engines that index
by stream rather than by value.

**Loki**

```logql
{cluster="dc1"} | json | trace_id = "$TRACE"
```

Baseline arm: no acceleration; Loki scans every chunk in the 7d window for `cluster="dc1"`.
Structured-metadata arm (run and reported separately): with `trace_id` shipped as structured
metadata and blooms enabled, the filter must appear **before** any parser stage to be
accelerated:

```logql
{cluster="dc1"} | trace_id = "$TRACE"
```

**Elasticsearch**

```json
{
  "size": 100,
  "query": {
    "bool": {
      "filter": [
        { "range": { "@timestamp": { "gte": "$T0", "lt": "$T1" } } },
        { "term": { "trace_id": "$TRACE" } }
      ]
    }
  }
}
```

**ClickHouse**

```sql
SELECT Timestamp, App, Body
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND TraceId = '$TRACE'
ORDER BY Timestamp
LIMIT 100;
```

`TraceId` is not in the primary key (it would destroy the sort order that makes everything
else cheap), so add a bloom filter skip index and verify it is used:
`INDEX idx_trace TraceId TYPE bloom_filter(0.01) GRANULARITY 1`.

**VictoriaLogs**

```logsql
_time:[$T0, $T1] trace_id:"$TRACE" | limit 100
```

---

## 7. Q5 — mid-token substring (the deliberate tokenizer stress)

Find `abc` where it appears **inside** a larger token, e.g. the line `traceabc=1`. Every
engine can do this; the interesting result is what it costs and what extra storage it
requires.

**Loki** — native, this is what `|=` already does:

```logql
count_over_time({cluster="dc1"} |= "abc" [24h])
```

**Elasticsearch** — `match` cannot do this. Two options, both with a cost:

```json
{ "wildcard": { "body_wildcard": { "value": "*abc*" } } }
```

requires a companion `wildcard`-type field, which adds storage. The alternative is an
n-gram analyzed field, which adds more. Either way, record the extra on-disk bytes as part
of the result — the capability is not free.

**ClickHouse** — the `splitByNonAlpha` text index will **not** serve this;
`Body LIKE '%abc%'` silently full-scans. With a second index using an n-gram tokenizer the
`like` path can be served:

```sql
-- requires: INDEX idx_body_ng Body TYPE text(tokenizer = 'ngrams(3)') GRANULARITY 64
SELECT count()
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Body LIKE '%abc%';
```

Run both: with the n-gram index (fast, more storage) and without (full scan). The pair is
the actual answer about ClickHouse and substring search.

**VictoriaLogs**

```logsql
_time:[$T0, $T1] ~"abc" | stats count() as hits
```

---

## 8. Q6 — AND with negation

Both needles present, a third token absent. Negation is where scan-based engines usually
keep their advantage, since an index gives less help.

**Loki**

```logql
count_over_time({cluster="dc1"} |= "$NEEDLE_A" != "healthcheck" [24h])
```

**Elasticsearch**

```json
{
  "bool": {
    "filter": [
      { "range": { "@timestamp": { "gte": "$T0", "lt": "$T1" } } },
      { "match": { "body": { "query": "$NEEDLE_A", "operator": "and" } } }
    ],
    "must_not": [ { "match": { "body": { "query": "healthcheck" } } } ]
  }
}
```

**ClickHouse**

```sql
SELECT count()
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasToken(Body, '$NEEDLE_A')
  AND NOT hasToken(Body, 'healthcheck');
```

**VictoriaLogs**

```logsql
_time:[$T0, $T1] "$NEEDLE_A" -"healthcheck" | stats count() as hits
```

---

## 9. Q7 — ordered phrase

`connection refused` as a phrase, not as two independent tokens. The contrast with Q2 shows
what ordering costs each engine.

| Engine | Expression |
| --- | --- |
| Loki | `{cluster="dc1"} \|= "connection refused"` (substring, so phrase comes free) |
| Elasticsearch | `{ "match_phrase": { "body": "connection refused" } }` |
| ClickHouse | `hasPhrase(Body, 'connection refused')` |
| VictoriaLogs | `"connection refused"` (quoted = phrase filter) |

Two engine-specific caveats, both of which would otherwise be misread as engine defects:

- ClickHouse `hasPhrase` requires a compatible tokenizer (`splitByNonAlpha`, `splitByString`,
  `ngrams`, `asciiCJK`, `icu`) — `sparseGrams` and `array` will not serve it.
- Elasticsearch `match_only_text` does not store positions, so phrase queries are verified
  against `_source` and are materially slower than on a full `text` field. That is the
  deliberate trade for its storage saving. If Q7 matters to us, re-run it with `body` mapped as
  `text` and record the extra storage; otherwise report the `match_only_text` number with this
  caveat attached.

---

## 10. Q8 — errors per minute (absolute-threshold alert)

Scenario 1 from the meeting: alert when a service exceeds 100 errors per minute. Run in two
modes: as an ad-hoc range query, and as a rule evaluated continuously (50 rules
concurrently) with injected bursts to measure detection delay.

**Loki** — ruler rule, contradicting the note's "not easy to do":

```logql
sum(count_over_time({namespace="payments", app="checkout-api"} |= "error" [1m])) > 100
```

**Elasticsearch** — ES|QL:

```esql
FROM logs-sysbench-default
| WHERE @timestamp >= "$T0" AND @timestamp < "$T1" AND level == "error"
| STATS errors = COUNT(*) BY minute = BUCKET(@timestamp, 1 minute), app
| WHERE errors > 100
```

or the classic aggregation form, `date_histogram` with `fixed_interval: 1m` under a
`level: error` filter, wrapped in a Kibana alerting rule.

**ClickHouse** — ad-hoc:

```sql
SELECT toStartOfMinute(Timestamp) AS minute, App, count() AS errors
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1' AND Level = 'error'
GROUP BY minute, App
HAVING errors > 100
ORDER BY minute;
```

and the materialized-view arm, which is ClickHouse's real answer and has no equivalent
among the other three:

```sql
CREATE MATERIALIZED VIEW logs_errors_per_min_mv
TO logs_errors_per_min AS
SELECT toStartOfMinute(Timestamp) AS minute, Cluster, Namespace, App, count() AS errors
FROM logs
WHERE Level = 'error'
GROUP BY minute, Cluster, Namespace, App;
```

The alert then reads a tiny pre-aggregated table. Report separately from the ad-hoc number;
the comparison between the two is one of the more useful results in the whole exercise.

**VictoriaLogs**

```logsql
_time:[$T0, $T1] level:error | stats by (_time:1m, app) count() as errors | filter errors:>100
```

---

## 11. Q9 — top-K group-by

Top 10 pods by error count.

| Engine | Expression |
| --- | --- |
| Loki | `topk(10, sum by (pod) (count_over_time({namespace="payments"} \|= "error" [24h])))` |
| Elasticsearch | `terms` agg on `pod`, `size: 10`, ordered by `_count` desc, under a `level: error` filter |
| ClickHouse | `SELECT Pod, count() c FROM logs WHERE ... AND Level='error' GROUP BY Pod ORDER BY c DESC LIMIT 10` |
| VictoriaLogs | `_time:24h level:error \| stats by (pod) count() as c \| sort by (c desc) \| limit 10` |

Run at 1h, 24h and 7d. The scaling curve matters more than the absolute number: this is the
query a dashboard refreshes every 30 seconds.

---

## 12. Q10 — numeric percentile on a parsed field

p95 of `duration_ms` grouped by `http_path`. Requires extracting a number from the event,
which is where schema-on-read gets expensive.

**Loki**

```logql
quantile_over_time(0.95, {namespace="payments"} | json | unwrap duration_ms [24h]) by (http_path)
```

**Elasticsearch** — `terms` agg on `http.path` with a `percentiles` sub-aggregation on
`duration_ms` (`percents: [95]`). Note that ES percentiles are approximate (TDigest) —
record that, since ClickHouse `quantile` is also approximate but by a different method, and
`quantileExact` is not. Do not treat small differences in the value as an engine defect.

**ClickHouse**

```sql
SELECT HttpPath, quantile(0.95)(DurationMs) AS p95
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
GROUP BY HttpPath
ORDER BY p95 DESC;
```

**VictoriaLogs**

```logsql
_time:[$T0, $T1] | stats by (http_path) quantile(0.95, duration_ms) as p95 | sort by (p95 desc)
```

---

## 13. Q11 — recent tail (freshness)

Last 5 minutes for one app, newest first. Doubles as the ingest-to-visible probe: the
canary record carries its own write timestamp, so the gap between "written" and "returned
by this query" is measured directly rather than argued from architecture
([explanation.md §4.2](../explanation.md)).

| Engine | Expression |
| --- | --- |
| Loki | `{namespace="payments", app="checkout-api"}` over the last 5m, `direction=backward`, `limit=100` |
| Elasticsearch | `range` on `@timestamp` last 5m, sort `@timestamp` desc, `size: 100` |
| ClickHouse | `... WHERE Timestamp > now() - INTERVAL 5 MINUTE ORDER BY Timestamp DESC LIMIT 100` |
| VictoriaLogs | `_time:5m app:checkout-api \| sort by (_time desc) \| limit 100` |

---

## 14. Q12 — full-corpus unselective scan (the cost ceiling)

A needle with ~10^5 selectivity, no stream prefilter, across the full 7 days. Nobody should
run this in production, which is exactly why we measure it: it is the query that takes the
cluster down, and the number tells us what per-tenant quotas need to block
([revise-syscon.txt §4.5](../revise-syscon.txt) — agents will run exactly this shape).

Same form as Q1 with the stream filter removed and the window set to 7d. Record whether the
engine **refuses** it (a limit kicks in) rather than degrading — refusing is the better
behaviour and should be scored as such, not penalised as a failure.

---

## 15. Q13 — high-cardinality group-by (stress)

Count events grouped by `trace_id` over 1h — millions of groups. Expected to fail or
degrade badly somewhere; where and how it fails is the result.

| Engine | Expression |
| --- | --- |
| Loki | `sum by (trace_id) (count_over_time({cluster="dc1"} \| json [1h]))` |
| Elasticsearch | `terms` agg on `trace_id`, `size: 10000` (watch `max_buckets` / circuit breaker) |
| ClickHouse | `SELECT TraceId, count() FROM logs WHERE ... GROUP BY TraceId` (watch `max_memory_usage`, spill to disk) |
| VictoriaLogs | `_time:1h \| stats by (trace_id) count() as c` |

Record the failure mode verbatim: OOM, circuit breaker, query rejection, or spill to disk
with a completion time. An engine that degrades gracefully here is materially safer to hand
to 1000 agents.

---

## 16. Q14 — ratio over a long window (required)

Payment rejection rate per hour over 48 hours. Scored against `INC-2`, where the rate drifts
from 2% to 6% while traffic follows a 3x diurnal curve.

This is a genuinely new cost shape, not a variant of Q8 or Q9. Because traffic varies more
than the defect rate does, **the absolute count of rejections is not monotonic** — it falls
overnight while the rate climbs. An alert on rejection count both misses this incident and
fires spuriously at the next traffic peak. The query must compute a ratio, and the four
engines pay very different prices for it.

**ClickHouse** — single pass, one scan, conditional aggregation:

```sql
SELECT toStartOfHour(Timestamp) AS h,
       count() AS total,
       countIf(Status = 'rejected') AS rejected,
       countIf(Status = 'rejected') / count() AS reject_rate
FROM logs
WHERE App = 'payment-api' AND Timestamp >= '$T0' AND Timestamp < '$T1'
GROUP BY h
ORDER BY h;
```

**Loki** — two aggregations over the same data, divided. Loki scans the window **twice per
evaluation step**, which is the headline cost finding for this query class:

```logql
sum(count_over_time({app="payment-api"} | json | status="rejected" [1h]))
/
sum(count_over_time({app="payment-api"} | json [1h]))
```

**Elasticsearch** — `date_histogram` with a `filters` sub-aggregation and a `bucket_script` to
divide. Index-backed and cheap per bucket, but three layers of aggregation to express one
idea:

```json
{
  "size": 0,
  "query": { "bool": { "filter": [
    { "range": { "@timestamp": { "gte": "$T0", "lt": "$T1" } } },
    { "term": { "app": "payment-api" } }
  ] } },
  "aggs": {
    "per_hour": {
      "date_histogram": { "field": "@timestamp", "fixed_interval": "1h" },
      "aggs": {
        "rejected": { "filter": { "term": { "status": "rejected" } } },
        "reject_rate": {
          "bucket_script": {
            "buckets_path": { "r": "rejected._count", "t": "_count" },
            "script": "params.t == 0 ? 0 : (double)params.r / params.t"
          }
        }
      }
    }
  }
}
```

The ES|QL form using aggregate filtering is more readable if the deployed version supports it;
confirm in phase 0 rather than assuming.

**VictoriaLogs** — conditional stats plus a `math` pipe, one pass:

```logsql
_time:[$T0, $T1] app:payment-api
| stats by (_time:1h) count() as total, count() if (status:rejected) as rejected
| math rejected / total as reject_rate
```

Scoring, beyond latency and CPU-seconds:

- Does the computed hourly rate match `incidents.json` ground truth for `INC-2`?
- **Detection delay:** with the engine's own alerting mechanism, how long after onset does a rule on this ratio fire? Compare against the recorded hour at which the true rate crosses 3%, 4% and 5%.
- Cost of evaluating this continuously, not just once. This is the query class most likely to be a standing rule, and Loki's double scan compounds accordingly.

Also run at 7d to see how each engine scales a ratio across a wider window, since drift
detection wants context well beyond the drift itself.

---

## 17. Q15 — investigation session (time-to-answer)

Not one query: a scripted sequence, measured end to end. Scored against `INC-1`, the
correlated crash cascade, where the symptom surfaces in a downstream caller and the cause is
upstream and slightly earlier.

The suite otherwise measures query shapes in isolation, which says nothing about what on-call
actually experiences: a chain of queries where each depends on the previous answer. Fixed
seven steps, same logical sequence per engine:

1. Spot the anomaly: error count per minute across all apps, last 24h. (Expect `web-bff` and `orders-api` timeouts to surface first.)
2. Narrow to the affected app and confirm the window.
3. Group by pod to see whether it is the whole service or a subset.
4. Read a sample of the actual error lines.
5. Extract a `trace_id` from one of them.
6. Follow that trace across services to find the upstream failure. (This is the pivot that should lead to `checkout-api`.)
7. Retrieve the root-cause event: the OOM-kill and the multi-line crash dump.

Recorded per engine:

| Metric | Note |
| --- | --- |
| Total wall time, steps 1-7 | the number an engineer feels |
| Per-step latency | shows which step is the bottleneck |
| **Did it reach the root cause?** | pass/fail — step 7 returns the crash dump or it does not |
| Steps needing a workaround | e.g. the engine cannot pivot by trace without a full scan |
| Human-visible friction | query-language gymnastics, result truncation, timeouts |

Run it twice per engine: once by someone who knows that engine well, once by someone who does
not. The gap between those two times is a C5 input and often larger than any latency
difference in the whole suite.

Step 6 is the discriminator. Pivoting by `trace_id` across services over a 24h window is Q4's
cost profile, so an engine that is slow at Q4 will be painful here in a way that no
single-query benchmark conveys — the engineer pays that cost mid-investigation, repeatedly.

---

## 18. Q16 — log pattern / categorization

Cluster log lines by shape and surface templates by frequency, with no filter term supplied.
Scored against `INC-5`, a template that appears nowhere else in the corpus, arriving at low
volume, with no error level and no needle to search for. If an engine cannot group by shape,
this incident is invisible.

Capability differs structurally rather than by performance, so this is a survey plus a
measurement:

| Engine | Mechanism | Status |
| --- | --- | --- |
| Loki | pattern ingester (`pattern_ingester` config) behind Grafana's Patterns view | built in |
| Elasticsearch | `categorize_text` aggregation; ML categorization jobs | built in |
| ClickHouse | no built-in equivalent — normalize and hash templates yourself | DIY |
| VictoriaLogs | number-collapsing / normalization pipes can approximate grouping by shape | **confirm in phase 0** |

The ClickHouse DIY approach is worth measuring rather than scoring as absent, because it is
cheap to express and may well be fast:

```sql
SELECT replaceRegexpAll(Body, '[0-9a-f]{8,}|\\d+', '?') AS template,
       count() AS c, min(Timestamp) AS first_seen
FROM logs
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
GROUP BY template
ORDER BY c DESC
LIMIT 50;
```

`first_seen` is the column that actually finds `INC-5`: sort ascending by it instead of by
count, and a never-before-seen template appears at the top. Whether each engine can express
"templates that are new in this window" is the real question here, and it is a better test
than "list the top 50 patterns", which every candidate can do somehow.

Record: does the engine find `INC-5` at all, how long it takes, and whether the mechanism is
built in, approximated or hand-rolled. Informational, but a strong input to the log-exploration
column of the handbook.

---

## 19. Proving the index was used

A fast query that accidentally full-scanned a warm cache is not a result. Every phase 0
query captures one of these:

**ClickHouse**

```sql
EXPLAIN indexes = 1
SELECT count() FROM logs WHERE hasAllTokens(Body, ['$NEEDLE_A', '$NEEDLE_B']);
```

Look for a `Skip` node naming the text index with `Granules: X/Y` where X is far below Y.
If the skip index is absent, or `Granules` shows no reduction, the index was not used —
usually a tokenizer mismatch or a non-token search term, and it fails silently with no
error. This check is mandatory for ClickHouse, not optional.

**Elasticsearch**

Add `"profile": true` and confirm the rewritten query type is what you expect
(`TermQuery` / `BooleanQuery`, not a scan), then read the `breakdown` timings for where the
work went. `_search?explain=true` on a single document confirms the analyzer produced the
tokens you assumed. Cross-check `indices.stats` for `query_cache` and `request_cache` hits
so warm runs are not mistaken for index efficiency.

**Loki**

Read the `stats` block in the query response: `totalChunksRef` vs `totalChunksDownloaded`
shows how much was pruned, `totalLinesProcessed` and `totalBytesProcessed` show the real
work done. For the bloom arm, `chunkRefsFiltered` shows how many chunk references the
blooms eliminated — if it is zero, the query was not accelerated, which is the expected
outcome for any free-text filter
([explanation.md §1.2](../explanation.md)).

**VictoriaLogs**

Append `&trace=1` to the query request to get a query execution trace showing per-stage
timings and how much data each `vlstorage` node scanned. Confirm the exact parameter and
output shape against the deployed version during phase 0; if tracing is unavailable, fall
back to the delta in `vl_*` scanned-rows metrics across the query, which is coarser but
sufficient to tell pruning from full scans.

---

## 20. Phase 0 checklist per query

For each of Q1–Q16, on the 5GB slice:

- [ ] Expressible in all four engines, or the gap recorded.
- [ ] Result counts identical across engines within the pinned semantic, or the difference
      explained and accepted in writing.
- [ ] Index-usage proof captured and stored with the query id.
- [ ] Multi-line stack-trace events returned as single logical events, not fragments.
- [ ] Query recorded in the frozen suite with its exact final text per engine, so phases 1-3
      cannot silently drift.

The 5 GB slice cannot carry the planted incidents at full fidelity, so Q14–Q16 are gated
differently: verify only that each is **expressible** per engine and returns the right shape
of result. Their ground-truth scoring happens in phase 2 against the full corpus, where the
incidents exist.

# Syscon-bench contract

Phase 1 freeze for the scenario file, the event schema, the query mix, and the
Prometheus series. The generator and the query client are one Go process. Two
loops share one scenario clock: ingest workers and a query ticker.

The checked-in scenarios conform to this document:

- [test-scenario-01.yaml](../log-generator/scenarios/test-scenario-01.yaml) is a flat 100 events/s run for 3600 seconds.
- [test-scenario-02.yaml](../log-generator/scenarios/test-scenario-02.yaml) is the varied linear and spike shape on the same 3600-second clock.

The search pattern is the one in the [README](../README.md). Phase 1 runs the
three classes in [§7](#7-query-mix). Image pins stay in
[benchmark-plan.md](../../benchmark-plan.md). This document does not start an
engine or Vector. The scenario names the dashboard endpoint that receives the
client metrics.

## 1. Module layout

Module path `syscon-bench`, Go 1.22, rooted at `syscon-bench/`.

| Path | Responsibility |
| --- | --- |
| `cmd/syscon-bench` | Binary. Subcommand `run`. |
| `internal/scenario` | Parse and validate the YAML in §2. |
| `internal/generate` | Scenario clock, identity pool, message bodies, ingest workers. |
| `internal/query` | Query-class mix, ticker, latency. |
| `internal/storage` | `Sink` and `Querier`. One adapter per engine. |
| `internal/metrics` | Series in §8, pushed to the dashboard endpoint. |

```text
syscon-bench run --scenario <file>
    [--storage-name <id>] [--storage-endpoint <url>]
```

`--scenario` is required. The two storage flags are a pair: passing either one
requires the other. They replace the single storage entry's `name` and
`endpoint` for that process and set `protocol` from the URL scheme. Auth stays
as written in the file. The load shape is unchanged, so one scenario file can
be aimed at each engine. Those flags leave `dashboard` as written in the file.

`Sink` pushes one batch. `Querier` runs one search. Phase 1 calls the engine
HTTP API directly. A later phase may point `Sink` at a local agent and leave
`Querier` on the storage URL.

```go
type Sink interface {
    Push(ctx context.Context, batch []Event) error
}

type Querier interface {
    Search(ctx context.Context, q Query) (Result, error)
}
```

`Event` is the canonical record in §5. `Query` carries `Class` (`fulltext`,
`exact`, or `filter`) and the predicates in §7. `Result` carries the hit count
and the raw body. The hit count is logged. Phase 1 records it as a measurement.

## 2. Scenario file

YAML, one document. `storage` and `dashboard` are each a one-element list.
Phase 1 runs one storage target and sends client metrics to one dashboard.

```yaml
name: test-scenario-01          # Prometheus label "scenario"
storage:
  - name: blackhole             # Prometheus label "storage"
    endpoint: http://localhost:8000
    protocol: http              # matches the endpoint scheme
    auth:
      type: basic               # none | basic
      username: admin
      password: admin
dashboard:
  - name: dashboard-01           # receiver of the client series in §8
    endpoint: http://localhost:8001
    protocol: http               # matches the endpoint scheme
    auth:
      type: basic                # none | basic
      username: admin
      password: admin
configs:
  min_size: 100                 # message bytes, inclusive
  max_size: 200000              # message bytes, inclusive
  concurrent: 100               # ingest worker count
  duration: 3600                # seconds from the start; equals the last time_stamp
scenario:
  - name: linear-all
    time_stamp: 3600            # seconds from the start; this segment's end
    type: linear                # linear | spike
    rps_min_concurrent: 100     # offered events per second
    rps_max_concurrent: 100     # offered events per second
    error_rate: 0.01            # fraction of events marked as errors
    query_rate: 1               # queries per minute
```

Sketch names on the design drawing map onto these fields as follows.

| Sketch | Field | Meaning |
| --- | --- | --- |
| `min-len` | `configs.min_size` | Minimum `message` length in bytes. |
| `max-len` | `configs.max_size` | Maximum `message` length in bytes. |
| `from-time` | `0`, or the previous segment's `time_stamp` | Window start, seconds from the run start. |
| `to-time` | `scenario[].time_stamp` | Window end, seconds from the run start. |
| `from-rps` | `scenario[].rps_min_concurrent` | Offered events per second. |
| `to-rps` | `scenario[].rps_max_concurrent` | Offered events per second. |
| — | `configs.concurrent` | Worker count. The sketch has no separate name for it. |

`rps_min_concurrent` and `rps_max_concurrent` are the process-wide offered rate
in events per second. The `concurrent` workers share that rate. The field names
are the ones already used in the scenario files.

### Validation

The parser rejects a file that breaks any of these rules.

- `name`, every segment `name`, and `dashboard[0].name` match `^[a-z0-9][a-z0-9-]*$`. Segment names are unique. The top-level `name` is the Prometheus `scenario` label.
- `storage` has exactly one entry. `dashboard` has exactly one entry.
- `storage[0].name` is `blackhole`, `clickhouse`, `elasticsearch`, `loki`, or `victorialogs`. `dashboard[0].name` is the receiver id, not an engine id.
- On both entries, `endpoint` is an absolute `http` or `https` URL and `protocol` is that same scheme.
- On both entries, `auth.type` is `none` or `basic`. `basic` requires a non-empty `username` and `password`. `none` omits both.
- `min_size` is an integer in `[64, 1048576]`. `max_size` is an integer, `min_size <= max_size <= 1048576`. The floor of 64 leaves room for the prefix in §6.
- `concurrent` is an integer `>= 1`.
- `duration` and every `time_stamp` are numbers of seconds from the start of the run. `duration > 0`.
- There is at least one segment. The run follows one chain of boundaries, all in seconds: `0`, then each segment's `time_stamp` in order, and the last `time_stamp` equals `duration`. Each `time_stamp` is greater than the previous boundary. Segment 1 covers `[0, time_stamp)`. Each later segment covers `[previous time_stamp, time_stamp)`.
- `type` is `linear` or `spike`.
- Both RPS fields are numbers `>= 0`.
- `error_rate` is a number in `[0, 1]`.
- `query_rate` is a number `>= 0`.

Integers and fractional YAML numbers are both accepted. The checked-in files use integers for times and rates.

## 3. Clock, offered rate, errors, queries

`t = 0` is the start of the run. Elapsed time, `duration`, and every
`time_stamp` are seconds. The generator walks the segments in order:

`0` → `time_stamp` of segment 1 → … → `time_stamp` of segment n → `duration`

The last `time_stamp` is `duration`, so that final arrow lands on the same
second. The clock stops there. No new event and no new query starts after that.
Metrics include in-flight calls that finish after the clock stops.

A segment's start is `0` for the first segment and the previous `time_stamp`
for every segment after that. Its end is its own `time_stamp`. The window is
half-open: the instant `t` equals a `time_stamp`, the next segment is the one
running, and `t = duration` ends the run.

Workers share one process-wide offered rate. `concurrent` is how many workers
issue those events. The 1000 identities in §4 are field values on each event.

For a segment of length `time_stamp - start` seconds:

- `linear` offers `rps_min_concurrent` at the start and changes linearly toward `rps_max_concurrent` as `t` approaches `time_stamp`. Equal ends are a constant rate. Offered events equal `(rps_min_concurrent + rps_max_concurrent) / 2 * (time_stamp - start)`.
- `spike` offers `rps_max_concurrent` across the whole window. Offered events equal `rps_max_concurrent * (time_stamp - start)`. The held rate is `rps_max_concurrent` even when it is lower than `rps_min_concurrent`, which stays in the file as a required number. `spike-03` in scenario 02 is that case: hold 100 events/s after a segment that held 2000.

`query_rate` is queries per minute for the whole process, one ticker, not per
worker. The instantaneous query rate is `query_rate / 60` queries per second.
Expected queries in a segment equal `query_rate / 60 * (time_stamp - start)`.
A rate of `0` starts no queries in that segment.

`error_rate` is the fraction of generated events marked as errors: `log-severity`
`error` and `status-code` 500. Every other event is `log-severity` `info` and
`status-code` 200. The draw is independent per event. The HTTP client still sends
those events. Sink failures use a separate counter, defined in §8.

If the sink falls behind, the offered schedule still defines the attempt rate.
Accepted events and failed events are whatever the sink call returns.

Checked-in integrals, if the sink accepts every event:

| Scenario | Segment | Window (seconds) | Offered events | Expected queries |
| --- | --- | --- | ---: | ---: |
| test-scenario-01 | linear-all | 0 → 3600 | 360000 | 60 |
| test-scenario-02 | linear-01 | 0 → 60 | 6000 | 1 |
| test-scenario-02 | spike-01 | 60 → 240 | 180000 | 300 |
| test-scenario-02 | spike-02 | 240 → 1800 | 3120000 | 2600 |
| test-scenario-02 | linear-02 | 1800 → 3000 | 2400000 | 2000 |
| test-scenario-02 | spike-03 | 3000 → 3600 | 60000 | 1000 |

Scenario 02 offers 5766000 events and 5901 queries across 3600 seconds.
`linear-01` marks 1% of its events as errors. `spike-02` marks 10%. The other
segments mark 1%.

`time_stamp` is the scenario schedule. The event field `time` in §5 is the
wall-clock time on the record, separate from this chain.

## 4. Identities

The generator draws each event's identity uniformly from a fixed pool of 1000.
The pool size is the constant 1000 in `internal/generate`. Index `i` runs over
`0 .. 999`:

| Field | Value |
| --- | --- |
| `instance-id` | `instance-` plus `i` as four digits (`instance-0000` … `instance-0999`) |
| `host-name` | `host-` plus the same four digits |
| `ip-address` | `10.{i/256}.{i%256}.10` (`10.0.0.10` … `10.3.231.10`) |

The three values stay paired. Identity is independent of the source class and
the message format.

## 5. Event schema

Every event has these fields. Names are canonical, including the hyphens.
Adapters translate them into each engine's columns, labels, or JSON.

| Field | Type | Rule |
| --- | --- | --- |
| `message` | string | Body in the format drawn in §6. UTF-8 byte length is in `[min_size, max_size]`. |
| `uuid` | string | RFC 4122 UUID, lowercase, with hyphens. Unique per event. |
| `instance-id` | string | §4. |
| `ip-address` | string | §4. |
| `host-name` | string | §4. |
| `log-severity` | string | `info` or `error`. |
| `status-code` | int | `200` or `500`, `401`, `302`, `000`, `201`, `503` |
| `component` | string | Paired with `service` in §6. |
| `trace-id` | string | 32 lowercase hex digits. Unique per event. |
| `version` | string | `1.0.0`. |
| `service` | string | §6. |
| `time` | string | Wall-clock UTC when the event is built, RFC3339 milliseconds, `Z` suffix (`2026-11-02T11:24:00.000Z`). |
| `jira-id` | string |
| `group-id` | string |
| `external-01` | string |
| `external-02` | string |
| `external-03` | string |
| `binary-field` | binary |


`version` is a string. `status-code` is an int.

A full-text or exact-match query uses a value from an event the sink has
already accepted. The query client skips a tick when nothing has been accepted
yet, and that skip leaves the query error counter unchanged.

## 6. Message format and source

Each event draws a format and a source independently.

### Format (`message` body)

| Id | Share | Body |
| --- | ---: | --- |
| `json` | 60% | One JSON object encoded as text. |
| `access` | 10% | One access-log line. |
| `text` | 10% | One plain-text line. |
| `stacktrace` | 5% | One stack trace, with `\n` between frames. |
| `audit` | 5% | One audit line, leading token `audit`. |
| `logfmt` | 10% | One logfmt line of `key=value` pairs. |

The shares sum to 100%. `logfmt` is included so the mix in the README sums to
100%. The source mix is a separate roll and already sums to 100%.

### Source (`service` and `component`)

| Id | Share | `service` / `component` pairs, chosen uniformly inside the class |
| --- | ---: | --- |
| `application` | 60% | `checkout`/`api`, `payments`/`api`, `inventory`/`worker`, `auth`/`api`, `notifications`/`worker` |
| `web` | 20% | `edge`/`envoy`, `gateway`/`proxy`, `ingress`/`nginx` |
| `platform` | 10% | `kubelet`/`node`, `containerd`/`runtime`, `kernel`/`os` |
| `data` | 10% | `postgres`/`db`, `redis`/`cache`, `kafka`/`queue` |

### Body shape

Draw a target length uniformly from the integers in `[min_size, max_size]`.
Build a prefix no longer than 64 bytes that contains the `service` value and
the severity token (`info` or `error`) as whole alphanumeric tokens. Every
service name is a single alphanumeric word, so the field value is one token.
Then extend the body in that format until it reaches the target.

- Shorter than the target: append the ASCII sequence ` x` and cut to the target byte length.
- Longer than the target: keep the first target bytes. The prefix occupies the first 64 bytes, and `min_size >= 64`, so the service token and the severity token survive the cut.
- `stacktrace` uses `\n` inside a single `message`. The other formats are one line.

Tokens are the alphanumeric runs used in §7. The shortest service name is
`auth` or `edge` (4 characters). `info` is 4 characters and `error` is 5, so
both required tokens meet the full-text minimum.

Illustrative bodies before padding, with `checkout`, `api`, `info`, and `200`.
The required tokens sit in the first 64 bytes; the rest of the line is the
format extending past that prefix:

```text
json        {"sev":"info","service":"checkout","component":"api","msg":"request completed"}
access      10.0.0.10 - checkout info [02/Nov/2026:11:24:00 +0000] "GET /v1/checkout HTTP/1.1" 200 128 "-" "syscon-bench"
text        checkout info request completed component api
stacktrace  checkout info exception timeout\n\tat checkout.api.Worker.run(Worker.java:42)
audit       audit checkout info service=checkout actor=instance-0000 action=access result=200
logfmt      service=checkout component=api severity=info status=200 msg="request completed"
```

Error events use the same format distribution and substitute `error` and `500`.

## 7. Query mix

The mix is fixed in the query client. Scenario files do not set it. Each tick
draws one class.

| `query_class` | Share | Search |
| --- | ---: | --- |
| `fulltext` | 25% | One token from `message` of an accepted event. |
| `exact` | 25% | Equality on one of `service`, `trace-id`, `uuid`, `status-code`. |
| `filter` | 25% | A time window plus equality on one of `service`, `log-severity`, `status-code`. |
| `aggregation` | 25% | Sum of `message` lengths in the same 60-second window. |

Full-text splits `message` on non-alphanumeric characters and keeps the case.
The token has length at least 4 and comes from an accepted message. The query
is a single token. After 1000 accepted events, a token seen in more than 1% of
events is not searched. The token chosen is the one whose document rate is
closest to 0.01%.

Exact match picks the field uniformly from those four and uses a value from an
accepted event. No time predicate.

Filter uses the half-open window `[T-60s, T)` on event `time`, where `T` is
the query start. The lower bound is raised to the first accepted event time
when the run is younger than 60 seconds. It also includes one equality, the
field chosen uniformly from `service`, `log-severity`, and `status-code`, using a
value from an accepted event.

The adapter translates the class into that engine's search call. The client
reads the response body to the end before recording latency. The hit count is
written to the log, and the run continues at any count.

## 8. Metrics

Registered by `internal/metrics` on the default Prometheus registry, using a
classic histogram. Buckets for `bench_query_duration_seconds`, in seconds:

`0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60`.

| Series | Type | Labels | Counts |
| --- | --- | --- | --- |
| `bench_ingest_events_total` | counter | `storage`, `scenario` | Events the sink accepted. |
| `bench_ingest_bytes_total` | counter | `storage`, `scenario` | `message` UTF-8 bytes of those accepted events. |
| `bench_ingest_errors_total` | counter | `storage`, `scenario` | Events the sink did not accept: non-2xx, timeout, or transport error. A failed batch adds its event count. |
| `bench_query_duration_seconds` | histogram | `storage`, `scenario`, `query_class` | Seconds from request start until the body is fully read. |
| `bench_query_errors_total` | counter | `storage`, `scenario`, `query_class` | Queries that timed out or returned non-2xx. |

`storage` is the storage entry's `name`. `scenario` is the top-level scenario
`name`. `query_class` is `fulltext`, `exact`, `filter`, or `aggregation`. Ingest series omit
`query_class`.

A query whose body was fully read is observed in the histogram, including a
non-2xx response. A non-2xx response also increments `bench_query_errors_total`.
A timeout increments the error counter. The histogram records only calls whose
body was fully read.

`bench_ingest_bytes_total` is the message-byte total the post-run disk
comparison uses as the bytes the client sent. It sums accepted events only.
`bench_ingest_errors_total` counts sink failures. The `error_rate` severity
mark stays on the event and does not increment that counter.

The process sends these five series to `dashboard[0].endpoint`. The body is
Prometheus text exposition 0.0.4 for those series only. The request is
`POST`, with `Content-Type: text/plain; version=0.0.4; charset=utf-8`. When
`auth.type` is `basic`, the request carries that username and password. The
push runs every 15 seconds of scenario time, and once more after the clock
stops so in-flight results are included. Storage CPU and RAM are not in this
body. A failed push is logged. It does not increment `bench_ingest_errors_total`
or `bench_query_errors_total`.

## 9. Phase 1 boundary

The contract freezes the file, the record, the query mix, the series above,
and the dashboard push. Direct engine adapters and the Docker profile that
scrapes them consume this document as written. The board reads the client
series from the dashboard endpoint in the scenario.

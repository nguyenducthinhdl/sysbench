# Phase 3: default-config comparison

Phase 3 compares ClickHouse, Elasticsearch, Loki, and VictoriaLogs on their
default settings. The generator, the query client, and the four adapters
already exist. This phase adds two scenario files, a 1-million-row preload,
a runner on the Mac, extra Grafana panels, and a written report.

`VM01` runs the log generator, Prometheus, and Grafana. `VM02` runs ClickHouse
or Loki, one at a time. `VM03` runs Elasticsearch or VictoriaLogs, one at a
time. Those are the SSH names in `~/.ssh/config`. The three machines are on
the LAN. The Mac only orchestrates. Each storage VM has 8 GB of RAM,
so before a case both `VM02` and `VM03` have their database containers and
data removed. Only the engine under test is started after that.

The generator on `VM01` sends logs and queries to the storage VM. That
address is `tailscale ip -4` when Tailscale is installed. These hosts do not
have the `tailscale` command, so the runner uses the IPv4 the storage VM
uses to reach `VM01` (`192.168.100.21` on `VM02`, `192.168.100.22` on `VM03`).
Prometheus on `VM01` scrapes that same address. This is not
the single-VM Compose layout, and it is not the Phase 2 split documented in
[phase2.md](phase2.md), where only ClickHouse has a VM.

The contract still decides the scenario file, the pool of 1000 identities,
the event fields, the message corpus, the query classes, and the client
metrics push. See [contract.md](contract.md) sections 2, 4, 5, 6, 7, and 8.

## What stays

`query_rate` is queries per minute. The query mix is 25% full-text, 25%
exact, 25% filter, and 25% aggregation. Aggregation sums `message`
lengths in the last 60 seconds.

Full-text does not search the most frequent tokens. After 1000 accepted
events, a token seen in more than 1% of events is skipped. The token used is
the one whose document rate is closest to 0.01%. Each engine then counts that
one token:

- ClickHouse: `hasToken(message, token)`
- Elasticsearch: a `term` query on `message`, not a `match` query
- Loki: `{job="syscon-bench"} |= "token"`
- VictoriaLogs: `"token" | stats count() as hits`

Image pins stay the ones already in Compose: ClickHouse `26.2`,
Elasticsearch 9.x with `logsdb`, Loki `3.5.x`, VictoriaLogs current,
Prometheus `v2.55.1`, Grafana `11.3.1`.

Default configuration means the stock image plus the minimum schema or index
required to accept the contract record.

- ClickHouse is ordered by `(service, time, uuid)` and has a token index on `message`.
- Elasticsearch keeps keyword indexes on the fields and an inverted index on `message`.
- Loki indexes only the labels `job`, `service`, `severity`, and `component`.
- VictoriaLogs uses `service`, `component`, and `log-severity` as stream fields.

Phase 2's ClickHouse `async_insert`
settings in [../deploy/vm/docker-compose.yml](../deploy/vm/docker-compose.yml)
belong to that phase and stay out of this comparison.

## Scenarios

Eight files sit next to the checked-in scenarios, one for each engine and
shape. Each file names that engine and its Compose endpoint. Both shapes last
3600 seconds. Ingest rates are process-wide events per second. Query rates
below are the contract unit, queries per minute.

| Case | File |
| --- | --- |
| ClickHouse steady | [phase3-clickhouse-steady.yaml](../log-generator/scenarios/phase3-clickhouse-steady.yaml) |
| ClickHouse spikes | [phase3-clickhouse-spikes.yaml](../log-generator/scenarios/phase3-clickhouse-spikes.yaml) |
| Elasticsearch steady | [phase3-elasticsearch-steady.yaml](../log-generator/scenarios/phase3-elasticsearch-steady.yaml) |
| Elasticsearch spikes | [phase3-elasticsearch-spikes.yaml](../log-generator/scenarios/phase3-elasticsearch-spikes.yaml) |
| Loki steady | [phase3-loki-steady.yaml](../log-generator/scenarios/phase3-loki-steady.yaml) |
| Loki spikes | [phase3-loki-spikes.yaml](../log-generator/scenarios/phase3-loki-spikes.yaml) |
| VictoriaLogs steady | [phase3-victorialogs-steady.yaml](../log-generator/scenarios/phase3-victorialogs-steady.yaml) |
| VictoriaLogs spikes | [phase3-victorialogs-spikes.yaml](../log-generator/scenarios/phase3-victorialogs-spikes.yaml) |

A steady file is one `linear` segment. `rps_min_concurrent` and
`rps_max_concurrent` are 20. `query_rate` is 1200, which is 20 queries per
second. Offered ingest is 72000 events. Offered queries are 72000.

A spikes file holds two spikes with `type: spike`. The base rate is 20
events/s and each spike holds 200 events/s, ten times the base. `query_rate`
is 1200 on every segment, which is 20 queries per second.

| Window (seconds) | Type | Ingest |
| --- | --- | ---: |
| 0 → 900 | linear | 20 events/s |
| 900 → 1500 | spike | 200 events/s |
| 1500 → 2400 | linear | 20 events/s |
| 2400 → 3000 | spike | 200 events/s |
| 3000 → 3600 | linear | 20 events/s |

`error_rate` is 0.01. The matrix runs the file whose name is
`phase3-<engine>-<shape>.yaml`, so the storage entry in that file is the
engine under test.

## Preload

Each official hour starts after that engine has accepted the same 1 million
rows. The live hour is further traffic on top of those rows.

`syscon-bench write --rows 1000000` builds `data/preload-1m.jsonl` once, from
the field pool and the message corpus, with seed 1. The runner copies that
file to `VM01`, `VM02`, and `VM03`. Before the hour it loads the copy on the
storage VM into the engine and refuses the hour when the stored count is
short. Steady and spikes for one engine each start from that file again,
because the previous case's volume is deleted first.

Both shapes query at 20 queries per second. Steady ingest is 20 events/s.
The spike hour keeps that 20 event/s base and raises the two spike windows to
200 events/s.

The spike hour is the larger case: 1,000,000 preloaded rows plus 288,000 live
events (2,400 seconds at 20 events/s and 1,200 seconds at 200 events/s). A
stored event is about 561 bytes of JSON (corpus messages average 93 bytes, and
the contract fields make up the rest), so those 1,288,000 rows are about
0.67 GiB raw. The steady hour adds 72,000 events and stays under that.
Queries are not stored rows.

Before the first preload, the Mac script reads free disk on the VM and exits
when the free space is below 8 GiB (8589934592 bytes). That is about 12 times
the raw spike case, which leaves room for an index, merge scratch, and the
engine image.

## Eight runs

The VM has 8 GB of RAM. `VM01` is the generator. `VM02` holds ClickHouse and
Loki. `VM03` holds Elasticsearch and VictoriaLogs. Before each case the
runner stops the databases on both storage VMs and deletes their data,
including a Phase 2 ClickHouse started from
[../deploy/vm/docker-compose.yml](../deploy/vm/docker-compose.yml). Only the
engine for that case is started. Ports 8001, 3000, and 9090 stay on
`VM01`. The Phase 2 generator stack is stopped first, because it already
publishes those three ports.

For each of ClickHouse, Elasticsearch, Loki, and VictoriaLogs:

1. Confirm SSH, Docker, and free disk on the VM. Remove the other engines'
   containers and data volumes first.
2. Start only that Compose profile, plus Prometheus, Grafana, cAdvisor, and
   the metrics receiver. Wait until the engine healthcheck passes.
3. Preload 1 million rows.
4. Smoke the same scenario for 180 seconds. Ingest rate, query latency, CPU,
   and RAM must be non-empty. A failed smoke stops the matrix.
5. Run the official 3600 seconds. This pass keeps `duration` at 3600.
6. Snapshot Prometheus and the Grafana panels for that case. Stop the engine
   and delete its data, and the other engines' data, before the next case.

## Metrics and report

The board in
[../deploy/docker/grafana/dashboards/syscon-bench.json](../deploy/docker/grafana/dashboards/syscon-bench.json)
already shows ingest rate, query p99, CPU, RAM, and disk used. Phase 3 adds:

- query average and p95 from `bench_query_duration_seconds` (the histogram
  already buckets up to 60 seconds)
- memory split from cAdvisor, working set and cache, with no second agent
- per-engine disk from `du -sb` of the data directory, not the VM filesystem
- ClickHouse active parts from the existing `:9363` scrape, on ClickHouse
  runs only

After each official hour the Mac runner appends a section to
[phase3-report.md](phase3-report.md): engine, scenario, preload row count,
and the series above. Graphs are Grafana panel exports or Prometheus range
queries, one timeline per case. That report is the phase exit. The phase
does not tune the engines.

## Mac orchestrator

[../deploy/phase3/run-matrix.sh](../deploy/phase3/run-matrix.sh) runs on the
laptop. It SSHs to the VM and runs the steps above. It starts one engine.
A flag selects smoke-only, or one engine, so a single case can be repeated
without the other seven.

SSH to `VM01`,
`VM02`, and `VM03` already works. Each storage VM has 8 GB of RAM, so the other
databases are stopped and their data is deleted before each case.

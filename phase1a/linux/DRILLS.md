# Phase 1A failure drills

Three drills, run against **A2m**, **A4** and **B1**. They are the part of Phase 1A that
actually decides the Kafka question, so they are not optional and they are not a follow-up.

At 1.47 GiB Kafka cannot win on throughput. A matrix of clean runs would therefore conclude
"Kafka is pure overhead" — a wrong answer arrived at honestly, because Kafka is bought for
decoupling and replay and neither appears in a run where nothing breaks. D1 is where A4 and B1
are actually compared; D2 is where A3w0 is expected to be disqualified.

All drills run **throttled at 50,000 events/s**, so there is a steady state to interrupt and
the outage window is a meaningful fraction of the run.

---

## The one measurement that matters: acked-but-lost

"Lost data" means data the *agent was told had been accepted* and which is not in the table.
Data the agent failed to send and retried is not lost, it is just slow.

Vector's own counter is the source of truth for what was acked:

```bash
# On the driver, while Vector is still running:
curl -s localhost:9598/metrics \
  | awk '/^vector_component_sent_events_total.*component_id="clickhouse"/ {print $2}'
```

```sql
-- On node-1, after the drill:
SELECT count() FROM sysbench.logs_hdfs WHERE Component != 'sysbench.Canary';
```

```
acked_but_lost = vector_sent_events_total - clickhouse_rows
```

Read the Vector counter **before** stopping Vector, and read it for the `clickhouse` sink
specifically — the total across all sinks includes the parse-failure and Prometheus sinks.

For B1 the acked counter is the `kafka` sink instead, which is exactly the point: Kafka acks
on behalf of ClickHouse, so B1's "acked" means "durable in Kafka", not "queryable". Record
both the Kafka sink count and the ClickHouse row count, and treat the gap as replay lag rather
than loss until the consumer has caught up.

---

## D1 — ClickHouse stopped for 5 minutes mid-ingest

A graceful outage: a restart, an upgrade, a config reload. The common case, and the one a
broker is usually bought for.

```bash
phase1a/linux/driver/replay.sh a4 1 throttled &     # or a2m, or b1
sleep 60                                       # let it reach steady state

ssh node-1 'systemctl stop clickhouse-server'
DOWN_AT=$(date +%s)
sleep 300
ssh node-1 'systemctl start clickhouse-server'
UP_AT=$(date +%s)
```

While it is down, sample the agent every 10 seconds:

```bash
while :; do
  curl -s localhost:9598/metrics | grep -E \
    'vector_buffer_events|vector_component_discarded_events_total|vector_component_errors_total'
  sleep 10
done
```

Record:

| Field | Meaning |
| --- | --- |
| `buffer_peak_events` | how deep the agent's buffer got |
| `discarded_events` | **must be 0.** Anything else is data loss during a planned restart |
| `catchup_seconds` | from ClickHouse accepting again to the row count reaching 11,175,629 |
| `rows_actual` | still exactly 11,175,629, or the arm failed the drill |
| `visible_latency_p99_ms` | recomputed across the whole run, including the outage |

Expected shape, and the reason the drill exists:

- **A2m** has a 500-event memory buffer and `when_full: block`, so it stops reading stdin and
  survives without loss — but in production stdin is a container's log stream that does not
  block, so this result must be read as "A2m loses nothing *here* because the driver can be
  backpressured". Say that explicitly in the write-up; it is the single easiest way for this
  phase to produce a misleading recommendation.
- **A4** absorbs the outage on disk and should show a large buffer, zero discards, and a
  catch-up burst.
- **B1** should not notice: Vector keeps producing to Kafka, and ClickHouse's consumer resumes
  from its committed offset.

---

## D2 — `kill -9` ClickHouse mid-ingest

An ungraceful loss: OOM kill, power, kernel panic. No flush, no shutdown hook.

```bash
phase1a/linux/driver/replay.sh a2m 1 throttled &
sleep 90

# Read the acked counter FIRST. After the kill, Vector's retry behaviour will move it.
ACKED_BEFORE=$(curl -s localhost:9598/metrics \
  | awk '/^vector_component_sent_events_total.*component_id="clickhouse"/ {print $2}')

ssh node-1 'pkill -9 -f clickhouse-server'
sleep 10
ssh node-1 'systemctl start clickhouse-server'
```

Let the run finish, then compute `acked_but_lost` as above.

Run this against **A2m, A4, A3w1 and A3w0**. A3w0 is the reason the drill exists:

- **A3w1** (`wait_for_async_insert = 1`) acks after the server-side buffer is flushed, so an
  acked row should be a persisted row and `acked_but_lost` should be 0.
- **A3w0** (`wait_for_async_insert = 0`) acks as soon as the row is in the in-memory buffer.
  Everything buffered at the moment of the kill was acked and is gone, so `acked_but_lost`
  should be clearly non-zero.

That result is the empirical settlement of [revise-syscon.txt](../../revise-syscon.txt) section
1.7, which argues ClickHouse's visibility delay is "a pipeline choice, not an engine limit".
It is — and D2 is where the price of the cheaper choice gets a number.

**Any arm with `acked_but_lost > 0` fails the gate in section 9 of
[phase1a-clickhouse-ingest.md](../../phase1a-clickhouse-ingest.md) and is out regardless of
throughput.** This mirrors the phase-1 stopping condition in
[benchmark-plan.md](../../benchmark-plan.md) section 6.

---

## D3 — Kafka replay after a crash (B1 only)

Whether Kafka's replay guarantee costs anything at the other end.

```bash
phase1a/linux/driver/replay.sh b1 1 throttled &
sleep 90
ssh node-1 'pkill -9 -f clickhouse-server'
sleep 10
ssh node-1 'systemctl start clickhouse-server'
```

Wait for the consumer to drain the topic, then:

```sql
SELECT count() - 11175629 AS delta
FROM sysbench.logs_hdfs
WHERE Component != 'sysbench.Canary';

SELECT assignments.topic, num_messages_read, exceptions.text[-1] AS last_exception
FROM system.kafka_consumers;
```

`delta > 0` is duplicates, `delta < 0` is loss. **Duplicates are the expected result**: the
`Kafka` table engine is at-least-once, so a crash between consuming a block and committing its
offset replays that block.

Duplicates are counted against the known total rather than by content uniqueness, because
`HDFS.log` may legitimately contain identical lines and deduplicating by content would hide
real duplication behind real repetition.

Record `delta`, and record what removing it would cost, because that cost belongs to Kafka's
column in the decision and not to a footnote:

- `ReplacingMergeTree` needs a unique key this schema does not have, plus `FINAL` on reads or
  tolerance of pre-merge duplicates.
- ClickHouse's own insert deduplication works on identical blocks, which a replayed Kafka
  block often is — worth testing, but it is a window-bounded guarantee, not an absolute one.

If `delta == 0` on all three repetitions, say so plainly and note that the drill did not
happen to interrupt an uncommitted block. That is a weaker claim than "no duplicates occur"
and should not be written up as the stronger one.

---

## Results

Drills append to `results/phase1a-drills.ndjson`, with the fields
[replay.sh](driver/replay.sh) leaves null plus the drill-specific ones:

```json
{
  "phase": "1a-drill",
  "drill": "D2",
  "arm": "a3w0",
  "acked_before_kill": 4500000,
  "acked_but_lost": 18432,
  "rows_actual": 11157197,
  "duplicates": 0,
  "buffer_peak_events": null,
  "discarded_events": 0,
  "catchup_seconds": null,
  "gate_passed": false,
  "notes": "wait_for_async_insert=0; acked rows in the server buffer were unrecoverable"
}
```

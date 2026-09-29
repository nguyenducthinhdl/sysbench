# Vector configs, one per arm

| File | Arm | Batching lives in |
| --- | --- | --- |
| [a1-nobatch.yaml](a1-nobatch.yaml) | A1 | nowhere - one INSERT per event |
| [a2s-batch-1mb.yaml](a2s-batch-1mb.yaml) | A2s | agent, 1 MB / 1s |
| [a2m-batch-10mb.yaml](a2m-batch-10mb.yaml) | A2m | agent, 10 MB / 5s |
| [a2l-batch-100mb.yaml](a2l-batch-100mb.yaml) | A2l | agent, 100 MB / 30s |
| [a3-async.yaml](a3-async.yaml) | A3w1, A3w0 | server, via `async_insert` |
| [a4-diskbuffer.yaml](a4-diskbuffer.yaml) | A4 | agent, plus a 2 GiB disk buffer |
| [b1-kafka-producer.yaml](b1-kafka-producer.yaml) | B1 | broker, consumed by ClickHouse's Kafka engine |
| [b2-kafka-consumer.yaml](b2-kafka-consumer.yaml) | B2 | broker, consumed by a second Vector |

## Why these files repeat themselves

The source, the VRL transform and the failure/metrics sinks are duplicated in every file
rather than factored into a shared include. That is deliberate.

Fairness rule 9 in [benchmark-plan.md](../../../benchmark-plan.md) requires every non-default
setting to be recorded with the result. A flat file can be copied verbatim into the result
record's `non_default_config` and read six months later without reconstructing which includes
were active. A three-way config merge cannot.

The cost is that a change to the VRL transform has to be applied eight times. If that happens,
verify afterwards that all eight are still identical:

```bash
for f in phase1a/linux/vector/a*.yaml phase1a/linux/vector/b1-*.yaml; do
  sed -n '/^    source: |/,/^$/p' "$f" | shasum -a 256
done | sort -u   # must print exactly one hash
```

## What is held constant across every arm

- `timezone: UTC` globally, so `parse_timestamp` does not silently use the rig's local
  timezone and shift the partition boundaries.
- `when_full: block` on every buffer. The count gate requires exactly 11,175,629 replayed
  rows, so backpressure must reach stdin instead of being absorbed by dropping events.
- `request.concurrency: 4`, pinned rather than adaptive, so the arms differ in batching and
  nothing else.
- `skip_unknown_fields: false`, so a schema mismatch is an error rather than a silent column
  drop.
- Parse failures routed to a file sink. All 11,175,629 lines were verified against the regex,
  so anything landing there means the input changed, and it must not be discovered as a
  missing-row count at the end of the run.

## Running one

The configs read from stdin; rate limiting is the driver's job
([../driver/replay.sh](../driver/replay.sh)), not Vector's, because Vector's `throttle`
transform drops events over its threshold and would break the count gate.

```bash
phase1a/linux/driver/replay.sh a2m 1 throttled
```

-- Phase 1A post-run collection. One JSON object per run, merged into the result record by
-- driver/replay.sh.
--
-- Run as:
--   clickhouse-client --host node-1 \
--     --param_run_start='2026-09-29 12:00:00' \
--     --param_clock_offset_ms=0 \
--     --queries-file phase1a/collect.sql
--
-- Every subquery is bounded by run_start rather than relying on truncated system tables, so
-- an unstable run can still be investigated afterwards (see clickhouse/reset.sql).

SELECT
    -- The count gate. Canary rows excluded by predicate, not by estimate.
    (SELECT count() FROM sysbench.logs_hdfs WHERE Component != 'sysbench.Canary')
        AS rows_actual,
    (SELECT count() FROM sysbench.logs_hdfs WHERE Component = 'sysbench.Canary')
        AS canary_rows,

    -- Ingest-to-visible latency, in milliseconds, from the canary rows. Body is
    -- 'zzqcanary <emit_epoch_ms>'; IngestedAt is server-side now64(3) at insert time. The
    -- clock offset is subtracted because the two timestamps come from different machines.
    (SELECT round(quantile(0.50)(lat)) FROM (
        SELECT toUnixTimestamp64Milli(IngestedAt)
               - toInt64OrNull(splitByChar(' ', Body)[2])
               - {clock_offset_ms:Int64} AS lat
        FROM sysbench.logs_hdfs
        WHERE Component = 'sysbench.Canary' AND lat IS NOT NULL
    )) AS visible_latency_p50_ms,
    (SELECT round(quantile(0.99)(lat)) FROM (
        SELECT toUnixTimestamp64Milli(IngestedAt)
               - toInt64OrNull(splitByChar(' ', Body)[2])
               - {clock_offset_ms:Int64} AS lat
        FROM sysbench.logs_hdfs
        WHERE Component = 'sysbench.Canary' AND lat IS NOT NULL
    )) AS visible_latency_p99_ms,

    -- Merge pressure. parts_created is the headline number for the batching comparison:
    -- A1 should produce orders of magnitude more parts than A2l for the same input.
    (SELECT countIf(event_type = 'NewPart') FROM system.part_log
        WHERE database = 'sysbench' AND table = 'logs_hdfs'
          AND event_time >= {run_start:DateTime}) AS parts_created,
    (SELECT countIf(event_type = 'MergeParts') FROM system.part_log
        WHERE database = 'sysbench' AND table = 'logs_hdfs'
          AND event_time >= {run_start:DateTime}) AS merges,
    (SELECT round(sumIf(duration_ms, event_type = 'MergeParts') / 1000, 2) FROM system.part_log
        WHERE database = 'sysbench' AND table = 'logs_hdfs'
          AND event_time >= {run_start:DateTime}) AS merge_seconds,

    -- Storage, after merges settle. Active parts only.
    (SELECT sum(bytes_on_disk) FROM system.parts
        WHERE database = 'sysbench' AND table = 'logs_hdfs' AND active) AS on_disk_bytes,
    (SELECT count() FROM system.parts
        WHERE database = 'sysbench' AND table = 'logs_hdfs' AND active) AS active_parts,
    (SELECT uniqExact(partition) FROM system.parts
        WHERE database = 'sysbench' AND table = 'logs_hdfs' AND active) AS partitions,

    -- A1's expected failure mode. system.errors is cumulative since server start, and the
    -- server is restarted between every run, so this is already per-run.
    (SELECT sum(value) FROM system.errors WHERE name = 'TOO_MANY_PARTS') AS too_many_parts_errors,

    -- A3 only. Zero rows here on an A3 run means async_insert was never active and the
    -- result is void - see clickhouse/async-insert.sql.
    (SELECT count() FROM system.asynchronous_insert_log
        WHERE database = 'sysbench' AND table = 'logs_hdfs'
          AND event_time >= {run_start:DateTime}) AS async_insert_flushes,

    -- B1 only. A non-empty exception here is why a "slow" Kafka arm might actually be a
    -- stalled one. 26.2 has no last_exception column; the 10 most recent errors are
    -- exceptions.text, newest at [-1].
    (SELECT sum(num_messages_read) FROM system.kafka_consumers) AS kafka_messages_read,
    (SELECT any(exceptions.text[-1]) FROM system.kafka_consumers WHERE length(exceptions.text) > 0)
        AS kafka_last_exception

FORMAT JSONEachRow;

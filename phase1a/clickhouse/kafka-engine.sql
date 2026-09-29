-- Arm B1: Vector -> Kafka -> ClickHouse Kafka table engine -> materialized view -> MergeTree.
-- See ../../phase1a-clickhouse-ingest.md section 4.
--
-- Run ddl.sql first: the MV writes into sysbench.logs_hdfs, the same table every direct arm
-- writes into. Same table and same schema is deliberate, so on-disk bytes and merge counts
-- are comparable against A2 and A4 rather than being a different measurement.

-- The streaming source. No text index and no partitioning: this is a consumer, not storage.
-- It also has no IngestedAt, because DEFAULT now64(3) is applied by the target table when
-- the MV inserts, which is the moment we actually want to timestamp.
CREATE TABLE IF NOT EXISTS sysbench.logs_hdfs_kafka
(
    Timestamp   DateTime('UTC'),
    Pid         UInt32,
    Level       LowCardinality(String),
    Component   LowCardinality(String),
    Body        String
)
ENGINE = Kafka
SETTINGS
    -- Substituted by the driver: infra:9092 on the rig, kafka:9092 under Docker.
    kafka_broker_list = '__KAFKA_BROKER__',
    kafka_topic_list = 'phase1a-hdfs',
    kafka_group_name = 'clickhouse-phase1a',
    kafka_format = 'JSONEachRow',
    -- Record all four of these with the result: they are B1's batching knobs and they are
    -- the direct counterpart of Vector's batch.* settings in the A2 arms.
    kafka_num_consumers = 4,
    kafka_max_block_size = 1048576,
    kafka_poll_max_batch_size = 65536,
    kafka_flush_interval_ms = 1000,
    -- 'stream' rather than the default 'default' so a malformed message lands in the
    -- virtual _error column instead of killing the consumer. A silent consumer stall would
    -- look exactly like slow ingest.
    kafka_handle_error_mode = 'stream';

CREATE MATERIALIZED VIEW IF NOT EXISTS sysbench.logs_hdfs_mv
TO sysbench.logs_hdfs
AS
SELECT
    Timestamp,
    Pid,
    Level,
    Component,
    Body
FROM sysbench.logs_hdfs_kafka
WHERE length(_error) = 0;

-- Parse failures are excluded above rather than dropped silently. Check the count before
-- trusting any B1 row count:
--
--   SELECT count() FROM sysbench.logs_hdfs_kafka WHERE length(_error) > 0;
--
-- Note this is a streaming read and will consume from the topic. For the drill, read the
-- error count from system.kafka_consumers and the Vector-side counters instead.

-- D3 duplicate accounting. The Kafka engine is at-least-once, so a crash mid-batch can
-- replay already-inserted rows. Duplicates are counted against the known total, not by
-- content uniqueness, because HDFS.log may legitimately contain identical lines:
--
--   SELECT count() - 11175629 AS delta
--   FROM sysbench.logs_hdfs
--   WHERE Component != 'sysbench.Canary';
--
-- delta > 0 means duplicates, delta < 0 means loss. Both are findings; only loss is a gate
-- failure under section 9 of the phase doc.

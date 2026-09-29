-- Phase 1A target table. See ../../phase1a-clickhouse-ingest.md section 5.
--
-- Derived from the ClickHouse DDL in corpus-spec.md section 10, reduced to the fields
-- HDFS_v1 actually carries. Same shape on purpose: LowCardinality identity columns, a
-- text index on the body, explicit day partitioning, identity-then-time sort order.
--
-- Requires ClickHouse >= 26.2 for GA text indexes.

CREATE DATABASE IF NOT EXISTS sysbench;

-- T1: single copy, plain MergeTree. The whole arm matrix runs here.
CREATE TABLE IF NOT EXISTS sysbench.logs_hdfs
(
    Timestamp   DateTime('UTC'),
    IngestedAt  DateTime64(3, 'UTC') DEFAULT now64(3),
    Pid         UInt32,
    Level       LowCardinality(String),
    Component   LowCardinality(String),
    Body        String,

    INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Component, Timestamp);

-- Timestamp is DateTime('UTC') rather than a bare DateTime so partition boundaries do not
-- depend on the server timezone.
--
-- IngestedAt is the one column with no equivalent in corpus-spec.md. It exists so the
-- server-side half of ingest-to-visible latency can be computed without trusting the
-- driver's clock. It must stay DEFAULT-populated: Vector never sends it.
--
-- Nine distinct Components makes this a weak sort key. Real (Cluster, Namespace, App) has
-- far more structure, so Phase 1A understates compression slightly. Recorded, not fixed.


-- T2 confirmation pass: run only for the winning arm, on node-1 and node-2, with Keeper.
-- Uncomment and substitute {shard} / {replica} from the macros in the server config.
--
-- CREATE TABLE IF NOT EXISTS sysbench.logs_hdfs_repl
-- (
--     Timestamp   DateTime('UTC'),
--     IngestedAt  DateTime64(3, 'UTC') DEFAULT now64(3),
--     Pid         UInt32,
--     Level       LowCardinality(String),
--     Component   LowCardinality(String),
--     Body        String,
--
--     INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64
-- )
-- ENGINE = ReplicatedMergeTree('/clickhouse/tables/{shard}/sysbench/logs_hdfs', '{replica}')
-- PARTITION BY toDate(Timestamp)
-- ORDER BY (Component, Timestamp);

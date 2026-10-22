-- Live table for envoy-log-generator. Separate from logs_hdfs so a Phase 1A replay
-- reset does not wipe generator traffic, and the HDFS schema is not stretched to fit
-- JSON access logs.
--
-- Applied by phase1a/macos/run-live.sh.

CREATE DATABASE IF NOT EXISTS sysbench;

CREATE TABLE IF NOT EXISTS sysbench.logs_live
(
    Timestamp   DateTime64(6, 'UTC'),
    IngestedAt  DateTime64(3, 'UTC') DEFAULT now64(3),
    Source      LowCardinality(String),
    HostName    LowCardinality(String),
    HostIp      String DEFAULT '',
    Level       LowCardinality(String),
    Component   LowCardinality(String),
    Method      LowCardinality(String) DEFAULT '',
    Path        String DEFAULT '',
    Status      UInt16 DEFAULT 0,
    DurationUs  UInt64 DEFAULT 0,
    RequestId   String DEFAULT '',
    Body        String,

    INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Source, Component, Timestamp);

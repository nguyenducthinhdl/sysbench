CREATE DATABASE IF NOT EXISTS syscon;

CREATE TABLE IF NOT EXISTS syscon.logs (
    message String,
    uuid String,
    `instance-id` String,
    `ip-address` String,
    `host-name` LowCardinality(String),
    `log-severity` LowCardinality(String),
    `status-code` UInt16,
    component LowCardinality(String),
    `trace-id` String,
    version LowCardinality(String),
    service LowCardinality(String),
    time DateTime64(3, 'UTC'),
    `jira-id` String,
    `group-id` LowCardinality(String),
    `external-01` LowCardinality(String),
    `external-02` LowCardinality(String),
    `external-03` LowCardinality(String),
    `binary-field` String,
    INDEX idx_message message TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64
) ENGINE = MergeTree
PARTITION BY toDate(time)
ORDER BY (service, time, uuid);

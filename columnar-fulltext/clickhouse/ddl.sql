-- Four arms from columnar-fulltext-spec section 5. Same columns and sort on every arm.
-- Bloom on TraceId only on ch_col. Text indexes are not combined on one table.
-- Requires ClickHouse >= 26.2 so the text index is GA.

CREATE DATABASE IF NOT EXISTS sysbench;

CREATE TABLE sysbench.ch_col
(
    Timestamp    DateTime64(6, 'UTC'),
    Cluster      LowCardinality(String),
    Namespace    LowCardinality(String),
    App          LowCardinality(String),
    Pod          String,
    Container    LowCardinality(String),
    HostName     LowCardinality(String),
    Level        LowCardinality(String),
    TraceId      String,
    SpanId       String,
    HttpMethod   LowCardinality(String),
    HttpPath     LowCardinality(String),
    HttpStatus   UInt16,
    DurationMs   UInt32,
    Attributes   Map(LowCardinality(String), String),
    Status       LowCardinality(String),
    Body         String,

    INDEX idx_trace TraceId TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Cluster, Namespace, App, Timestamp);

CREATE TABLE sysbench.ch_ft
(
    Timestamp    DateTime64(6, 'UTC'),
    Cluster      LowCardinality(String),
    Namespace    LowCardinality(String),
    App          LowCardinality(String),
    Pod          String,
    Container    LowCardinality(String),
    HostName     LowCardinality(String),
    Level        LowCardinality(String),
    TraceId      String,
    SpanId       String,
    HttpMethod   LowCardinality(String),
    HttpPath     LowCardinality(String),
    HttpStatus   UInt16,
    DurationMs   UInt32,
    Attributes   Map(LowCardinality(String), String),
    Status       LowCardinality(String),
    Body         String,

    INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Cluster, Namespace, App, Timestamp);

CREATE TABLE sysbench.ch_ng
(
    Timestamp    DateTime64(6, 'UTC'),
    Cluster      LowCardinality(String),
    Namespace    LowCardinality(String),
    App          LowCardinality(String),
    Pod          String,
    Container    LowCardinality(String),
    HostName     LowCardinality(String),
    Level        LowCardinality(String),
    TraceId      String,
    SpanId       String,
    HttpMethod   LowCardinality(String),
    HttpPath     LowCardinality(String),
    HttpStatus   UInt16,
    DurationMs   UInt32,
    Attributes   Map(LowCardinality(String), String),
    Status       LowCardinality(String),
    Body         String,

    INDEX idx_ng Body TYPE text(tokenizer = ngrams(3)) GRANULARITY 64
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Cluster, Namespace, App, Timestamp);

CREATE TABLE sysbench.ch_scan
(
    Timestamp    DateTime64(6, 'UTC'),
    Cluster      LowCardinality(String),
    Namespace    LowCardinality(String),
    App          LowCardinality(String),
    Pod          String,
    Container    LowCardinality(String),
    HostName     LowCardinality(String),
    Level        LowCardinality(String),
    TraceId      String,
    SpanId       String,
    HttpMethod   LowCardinality(String),
    HttpPath     LowCardinality(String),
    HttpStatus   UInt16,
    DurationMs   UInt32,
    Attributes   Map(LowCardinality(String), String),
    Status       LowCardinality(String),
    Body         String
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (Cluster, Namespace, App, Timestamp);

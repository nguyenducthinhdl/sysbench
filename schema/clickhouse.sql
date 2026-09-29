-- HDFS service logs. HostName is the instance IP (empty when the line
-- does not identify one). Sort matches Elasticsearch logsdb: host, then time.
-- Day partitions make retention a partition drop.
CREATE TABLE IF NOT EXISTS logs
(
    Timestamp     DateTime64(6, 'UTC'),
    Cluster       LowCardinality(String),
    Namespace     LowCardinality(String),
    App           LowCardinality(String),
    Pod           LowCardinality(String),
    Container     LowCardinality(String),
    HostName      LowCardinality(String),
    HostIp        Nullable(IPv4),
    Level         LowCardinality(String),
    Pid           UInt32,
    Component     LowCardinality(String),
    BlockId       String,
    Attributes    Map(LowCardinality(String), String),
    Body          String,

    INDEX idx_body  Body    TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64,
    INDEX idx_block BlockId TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (HostName, Timestamp);

-- JSONL field -> column
-- timestamp      Timestamp
-- cluster        Cluster
-- namespace      Namespace
-- app            App
-- pod            Pod
-- container      Container
-- host.name      HostName
-- host.ip        HostIp
-- level          Level
-- pid            Pid
-- component      Component
-- block_id       BlockId
-- attributes     Attributes
-- body           Body

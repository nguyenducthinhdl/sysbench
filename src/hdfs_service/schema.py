"""Production schema for the mocked HDFS service.

Same intent as corpus-spec.md section 10: typed columns for stable fields,
attributes for the leftover values, a text index on the body, and host-first
sort. ``host.name`` and ``HostName`` are the instance IP.
"""

from __future__ import annotations

import json

CLICKHOUSE_DDL = """\
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
"""

_EVENT_SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$id": "https://sysbench.local/schemas/hdfs-service-event.json",
    "title": "HDFS service log event",
    "description": (
        "One HDFS log line mocked as a production service event. "
        "host.name is the instance IP and is the logsdb sort key. "
        "It is an empty string when the line does not identify an instance. "
        "host.ip is present only then, and equals host.name."
    ),
    "type": "object",
    "additionalProperties": False,
    "required": [
        "timestamp",
        "cluster",
        "namespace",
        "app",
        "pod",
        "container",
        "host",
        "level",
        "pid",
        "component",
        "block_id",
        "attributes",
        "body",
    ],
    "properties": {
        "timestamp": {
            "type": "string",
            "format": "date-time",
            "description": "UTC, microsecond precision. HDFS Date+Time has second resolution.",
        },
        "cluster": {"type": "string"},
        "namespace": {"type": "string"},
        "app": {"type": "string", "enum": ["namenode", "datanode"]},
        "pod": {"type": "string"},
        "container": {"type": "string"},
        "host": {
            "type": "object",
            "additionalProperties": False,
            "required": ["name"],
            "properties": {
                "name": {
                    "type": "string",
                    "description": "Instance IP. Empty when the line does not identify an instance.",
                },
                "ip": {
                    "type": "string",
                    "format": "ipv4",
                    "description": "Same value as name, typed as an address.",
                },
            },
        },
        "level": {
            "type": "string",
            "enum": ["info", "warn", "error", "debug", "fatal"],
        },
        "pid": {"type": "integer", "minimum": 0},
        "component": {"type": "string"},
        "block_id": {"type": "string"},
        "attributes": {
            "type": "object",
            "additionalProperties": {"type": "string"},
            "properties": {
                "size": {"type": "string"},
                "peer_ip": {"type": "string"},
                "instance_port": {"type": "string"},
            },
        },
        "body": {"type": "string"},
    },
}

_ELASTICSEARCH = {
    "index_template": {
        "name": "logs-hdfs",
        "index_patterns": ["logs-hdfs-*"],
        "data_stream": {},
        "priority": 500,
        "template": {
            "settings": {
                "index.mode": "logsdb",
                "index.sort.field": ["host.name", "@timestamp"],
                "index.sort.order": ["asc", "desc"],
            },
            "mappings": {
                "properties": {
                    "@timestamp": {"type": "date_nanos"},
                    "timestamp": {"type": "date_nanos"},
                    "cluster": {"type": "keyword"},
                    "namespace": {"type": "keyword"},
                    "app": {"type": "keyword"},
                    "pod": {"type": "keyword"},
                    "container": {"type": "keyword"},
                    "host": {
                        "properties": {
                            "name": {"type": "keyword"},
                            "ip": {"type": "ip"},
                        }
                    },
                    "level": {"type": "keyword"},
                    "pid": {"type": "integer"},
                    "component": {"type": "keyword"},
                    "block_id": {"type": "keyword"},
                    "attributes": {"type": "flattened"},
                    "body": {"type": "match_only_text"},
                }
            },
        },
    },
    "ingest_pipeline": {
        "name": "logs-hdfs",
        "description": "Copy the canonical timestamp onto @timestamp for the logs data stream.",
        "processors": [
            {"set": {"field": "@timestamp", "copy_from": "timestamp"}},
        ],
    },
}

_LOKI = {
    "description": (
        "Baseline arm. Stream labels are the service identity only. "
        "The instance IP stays in the JSON line as host.name, not a label."
    ),
    "stream_labels": ["cluster", "namespace", "app", "pod"],
    "structured_metadata": [],
    "line": "full JSON event",
}

_VICTORIALOGS = {
    "_time": "timestamp",
    "_msg": "body",
    "stream_fields": ["cluster", "namespace", "app", "pod"],
    "fields": [
        "host.name",
        "host.ip",
        "level",
        "pid",
        "component",
        "block_id",
        "attributes",
    ],
}


def clickhouse_ddl() -> str:
    return CLICKHOUSE_DDL


def event_schema() -> dict:
    return _EVENT_SCHEMA


def elasticsearch_template() -> dict:
    return _ELASTICSEARCH


def loki_mapping() -> dict:
    return _LOKI


def victorialogs_mapping() -> dict:
    return _VICTORIALOGS


def dumps(document: dict) -> str:
    return json.dumps(document, indent=2) + "\n"

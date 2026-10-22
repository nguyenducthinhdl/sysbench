#!/usr/bin/env python3
"""Five Elasticsearch index templates, one arm each.

The analyzer is the Elasticsearch expression of ClickHouse splitByNonAlpha on
26.2: runs of ASCII letters and digits, case preserved, underscore is a break.
"""

from __future__ import annotations

import json
from pathlib import Path

ARMS = {
    "es-col": "logs-sysbench-col",
    "es-ft": "logs-sysbench-ft",
    "es-phrase": "logs-sysbench-phrase",
    "es-ng": "logs-sysbench-ng",
    "es-scan": "logs-sysbench-scan",
}

# match_only_text on Elasticsearch 9.2 rejects a field-level analyzer and uses the
# index default. Naming that default sysbench_split keeps es-ft case-sensitive
# and splitting on underscore, which is what ClickHouse splitByNonAlpha does.
ANALYSIS = {
    "analyzer": {
        "default": {
            "type": "custom",
            "tokenizer": "sysbench_split",
        },
        "sysbench_token": {
            "type": "custom",
            "tokenizer": "sysbench_split",
        },
    },
    "tokenizer": {
        "sysbench_split": {
            "type": "pattern",
            "pattern": "[^0-9A-Za-z]+",
        }
    },
}

INDEX_SETTINGS = {
    "index.mode": "logsdb_columnar",
    "index.number_of_shards": 1,
    "index.number_of_replicas": 0,
    "index.refresh_interval": "1s",
    "index.sort.field": ["host.name", "@timestamp"],
    "index.sort.order": ["asc", "desc"],
    "index.requests.cache.enable": False,
    "index.queries.cache.enabled": False,
}

COMMON_PROPERTIES = {
    "@timestamp": {"type": "date"},
    "cluster": {"type": "keyword"},
    "namespace": {"type": "keyword"},
    "app": {"type": "keyword"},
    "pod": {"type": "keyword"},
    "container": {"type": "keyword"},
    "host": {"properties": {"name": {"type": "keyword"}}},
    "level": {"type": "keyword"},
    "trace_id": {"type": "keyword"},
    "span_id": {"type": "keyword"},
    "http": {
        "properties": {
            "method": {"type": "keyword"},
            "path": {"type": "keyword"},
            "status": {"type": "integer"},
        }
    },
    "duration_ms": {"type": "integer"},
    "attributes": {"type": "flattened"},
    "status": {"type": "keyword"},
}

BODIES = {
    # Columnar mode rejects doc_values: false. index: false keeps body out of
    # the inverted index; the value still lives in doc values.
    "es-col": {
        "type": "keyword",
        "index": False,
        "ignore_above": 100000,
    },
    "es-ft": {
        "type": "match_only_text",
    },
    "es-phrase": {
        "type": "text",
        "analyzer": "sysbench_token",
        "index_options": "positions",
    },
    "es-ng": {
        "type": "wildcard",
        "ignore_above": 65536,
    },
    # index:false so there is no inverted entry. doc_values keeps the raw
    # string for a script scan. match_only_text and text cannot serve that
    # script on 9.2: query scripts do not see _source.
    "es-scan": {
        "type": "keyword",
        "index": False,
        "doc_values": True,
        "ignore_above": 100000,
    },
}


def template(arm: str) -> dict:
    stream = ARMS[arm]
    properties = dict(COMMON_PROPERTIES)
    properties["body"] = BODIES[arm]
    return {
        "index_patterns": [stream, f".ds-{stream}-*"],
        "data_stream": {},
        "priority": 500,
        "template": {
            "settings": {
                **INDEX_SETTINGS,
                "analysis": ANALYSIS,
            },
            "mappings": {"properties": properties},
        },
    }


def all_templates() -> dict[str, dict]:
    return {arm: template(arm) for arm in ARMS}


def main() -> None:
    out = Path(__file__).resolve().parent / "templates"
    out.mkdir(exist_ok=True)
    for arm, body in all_templates().items():
        (out / f"{arm}.json").write_text(json.dumps(body, indent=2) + "\n")


if __name__ == "__main__":
    main()

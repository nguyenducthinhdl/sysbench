#!/usr/bin/env python3
"""Query text for the columnar / full-text gate.

ClickHouse 26.2.19 rejects hasToken on zzq_s5 because underscore is a
splitByNonAlpha separator, and hasPhrase does not exist until 26.4. The text
below is the matrix query with those two substitutions, which is what the gate
freezes once the counts match.
"""

from __future__ import annotations

import json
from datetime import datetime, timedelta

START = datetime(2026, 9, 1)
END = START + timedelta(days=7)
INC2_ONSET = START + timedelta(days=2)
INC2_END = INC2_ONSET + timedelta(hours=48)

WINDOWS = {
    "7d": (START, END),
    "24h": (START + timedelta(days=3), START + timedelta(days=4)),
    "1h": (START + timedelta(days=3, hours=16), START + timedelta(days=3, hours=17)),
    "48h": (INC2_ONSET, INC2_END),
}


def ch_time(ts: datetime) -> str:
    return (
        f"{ts.year:04d}-{ts.month:02d}-{ts.day:02d} "
        f"{ts.hour:02d}:{ts.minute:02d}:{ts.second:02d}.{ts.microsecond:06d}"
    )


def es_time(ts: datetime) -> str:
    return (
        f"{ts.year:04d}-{ts.month:02d}-{ts.day:02d}T"
        f"{ts.hour:02d}:{ts.minute:02d}:{ts.second:02d}.{ts.microsecond:06d}Z"
    )


def ch_f1(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND Cluster = 'dc1'
  AND hasAllTokens(Body, 'zzq_s5')"""


def ch_f2(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasAllTokens(Body, 'zzq_pa zzq_pb')"""


def ch_f3(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND Namespace = 'payments' AND App = 'checkout-api'
  AND hasAllTokens(Body, 'zzq_s5')"""


def ch_f4(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasAllTokens(Body, 'zzq_s5')
  AND NOT hasToken(Body, 'healthcheck')"""


def ch_f5(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND position(Body, 'connection refused') > 0"""


def ch_f6(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND Body LIKE '%abc%'"""


def ch_f6_token(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasToken(Body, 'abc')"""


def ch_c1(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND Cluster = 'dc1'"""


def ch_c2(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT count()
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND Level = 'error' AND App = 'checkout-api'"""


def ch_c3(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT toStartOfMinute(Timestamp) AS minute, App, count() AS errors
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}' AND Level = 'error'
GROUP BY minute, App
ORDER BY minute, App"""


def ch_c4(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT Pod, count() AS c
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}' AND Level = 'error'
GROUP BY Pod
ORDER BY c DESC
LIMIT 10"""


def ch_c5(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT HttpPath, quantile(0.95)(DurationMs) AS p95
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
GROUP BY HttpPath
ORDER BY HttpPath"""


def ch_c6(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT toStartOfHour(Timestamp) AS h,
       count() AS total,
       countIf(Status = 'rejected') AS rejected
FROM sysbench.{table}
WHERE App = 'payment-api'
  AND Timestamp >= '{t0}' AND Timestamp < '{t1}'
GROUP BY h
ORDER BY h"""


def ch_k1(table: str, trace: str = "$TRACE", t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT Timestamp, App, Body
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND TraceId = '{trace}'
ORDER BY Timestamp
LIMIT 100"""


def ch_m1(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT App, count() AS c
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasAllTokens(Body, 'zzq_s5')
GROUP BY App
ORDER BY App"""


def ch_m2(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT quantile(0.95)(DurationMs) AS p95
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasAllTokens(Body, 'zzq_s5')"""


def ch_m3(table: str, t0: str = "$T0", t1: str = "$T1") -> str:
    return f"""SELECT Pod, count() AS c
FROM sysbench.{table}
WHERE Timestamp >= '{t0}' AND Timestamp < '{t1}'
  AND hasAllTokens(Body, 'zzq_s5')
GROUP BY Pod
ORDER BY c DESC
LIMIT 10"""


def _filters(*clauses: dict) -> dict:
    return {
        "size": 0,
        "track_total_hits": True,
        "query": {"bool": {"filter": list(clauses)}},
    }


def _range(t0: str, t1: str) -> dict:
    return {"range": {"@timestamp": {"gte": t0, "lt": t1}}}


def _match(text: str) -> dict:
    return {"match": {"body": {"query": text, "operator": "and"}}}


def es_f1(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), {"term": {"cluster": "dc1"}}, _match("zzq_s5"))


def es_f2(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), _match("zzq_pa zzq_pb"))


def es_f3(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(
        _range(t0, t1),
        {"term": {"namespace": "payments"}},
        {"term": {"app": "checkout-api"}},
        _match("zzq_s5"),
    )


def es_f4(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), _match("zzq_s5"))
    body["query"]["bool"]["must_not"] = [_match("healthcheck")]
    return body


def es_f5(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), {"match_phrase": {"body": "connection refused"}})


def es_f6(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), {"wildcard": {"body": {"value": "*abc*"}}})


def es_f6_token(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), _match("abc"))


SCAN_BODY = (
    "if (doc['body'].size() == 0) return false; "
    "String b = doc['body'].value; "
)
SCAN_F1 = SCAN_BODY + "return b.indexOf('zzq_s5') >= 0;"
SCAN_F2 = SCAN_BODY + "return b.indexOf('zzq_pa') >= 0 && b.indexOf('zzq_pb') >= 0;"
SCAN_F6 = SCAN_BODY + "return b.indexOf('abc') >= 0;"


def _script(source: str, t0: str, t1: str, *extra: dict) -> dict:
    return _filters(
        _range(t0, t1),
        *extra,
        {"script": {"script": {"lang": "painless", "source": source}}},
    )


def es_scan_f1(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _script(SCAN_F1, t0, t1, {"term": {"cluster": "dc1"}})


def es_scan_f2(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _script(SCAN_F2, t0, t1)


def es_scan_f6(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _script(SCAN_F6, t0, t1)


def es_c1(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(_range(t0, t1), {"term": {"cluster": "dc1"}})


def es_c2(t0: str = "$T0", t1: str = "$T1") -> dict:
    return _filters(
        _range(t0, t1),
        {"term": {"level": "error"}},
        {"term": {"app": "checkout-api"}},
    )


def es_c3(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = es_c2(t0, t1)
    body["query"]["bool"]["filter"] = [
        _range(t0, t1),
        {"term": {"level": "error"}},
    ]
    body["aggs"] = {
        "per_minute": {
            "date_histogram": {"field": "@timestamp", "fixed_interval": "1m", "time_zone": "UTC"},
            "aggs": {"by_app": {"terms": {"field": "app", "size": 100}}},
        }
    }
    return body


def es_c4(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), {"term": {"level": "error"}})
    body["aggs"] = {"top_pods": {"terms": {"field": "pod", "size": 10, "order": {"_count": "desc"}}}}
    return body


def es_c5(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1))
    body["aggs"] = {
        "by_path": {
            "terms": {"field": "http.path", "size": 1000},
            "aggs": {"p95": {"percentiles": {"field": "duration_ms", "percents": [95]}}},
        }
    }
    return body


def es_c6(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), {"term": {"app": "payment-api"}})
    body["aggs"] = {
        "per_hour": {
            "date_histogram": {
                "field": "@timestamp",
                "fixed_interval": "1h",
                "time_zone": "UTC",
                "min_doc_count": 0,
            },
            "aggs": {"rejected": {"filter": {"term": {"status": "rejected"}}}},
        }
    }
    return body


def es_k1(trace: str = "$TRACE", t0: str = "$T0", t1: str = "$T1") -> dict:
    return {
        "size": 100,
        "track_total_hits": True,
        "sort": [{"@timestamp": "asc"}],
        "query": {
            "bool": {
                "filter": [
                    _range(t0, t1),
                    {"term": {"trace_id": trace}},
                ]
            }
        },
    }


def es_m1(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), _match("zzq_s5"))
    body["aggs"] = {"by_app": {"terms": {"field": "app", "size": 20}}}
    return body


def es_m2(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), _match("zzq_s5"))
    body["aggs"] = {"p95": {"percentiles": {"field": "duration_ms", "percents": [95]}}}
    return body


def es_m3(t0: str = "$T0", t1: str = "$T1") -> dict:
    body = _filters(_range(t0, t1), _match("zzq_s5"))
    body["aggs"] = {"top_pods": {"terms": {"field": "pod", "size": 10, "order": {"_count": "desc"}}}}
    return body


def dump(body: dict) -> str:
    return json.dumps(body, indent=2)


# Sections frozen into queries/columnar-fulltext.md. Each entry is
# (heading, semantic, [(arm, language, text), ...]).
FROZEN = [
    ("C1", "columnar", "count events with cluster = dc1", [
        ("ch-col", "sql", ch_c1("ch_col")),
        ("es-col", "json", dump(es_c1())),
    ]),
    ("C2", "columnar", "count level = error and app = checkout-api", [
        ("ch-col", "sql", ch_c2("ch_col")),
        ("es-col", "json", dump(es_c2())),
    ]),
    ("C3", "columnar", "count of level = error grouped by minute and app", [
        ("ch-col", "sql", ch_c3("ch_col")),
        ("es-col", "json", dump(es_c3())),
    ]),
    ("C4", "columnar", "top 10 pods by error count", [
        ("ch-col", "sql", ch_c4("ch_col")),
        ("es-col", "json", dump(es_c4())),
    ]),
    ("C5", "columnar", "p95 of duration_ms by http.path", [
        ("ch-col", "sql", ch_c5("ch_col")),
        ("es-col", "json", dump(es_c5())),
    ]),
    ("C6", "columnar", "hourly rejection ratio for payment-api", [
        ("ch-col", "sql", ch_c6("ch_col")),
        ("es-col", "json", dump(es_c6())),
    ]),
    ("K1", "keyword", "events for one trace_id, ordered by time, limit 100", [
        ("ch-col", "sql", ch_k1("ch_col")),
        ("es-col", "json", dump(es_k1())),
    ]),
    ("F1", "TOKEN", "planted token zzq_s5 at 1 in 10^5, cluster = dc1", [
        ("ch-ft", "sql", ch_f1("ch_ft")),
        ("ch-scan", "sql", ch_f1("ch_scan")),
        ("es-ft", "json", dump(es_f1())),
        ("es-scan", "json", dump(es_scan_f1())),
    ]),
    ("F2", "TOKEN", "both zzq_pa and zzq_pb, any order", [
        ("ch-ft", "sql", ch_f2("ch_ft")),
        ("ch-scan", "sql", ch_f2("ch_scan")),
        ("es-ft", "json", dump(es_f2())),
        ("es-scan", "json", dump(es_scan_f2())),
    ]),
    ("F3", "TOKEN", "zzq_s5 narrowed to namespace = payments and app = checkout-api", [
        ("ch-ft", "sql", ch_f3("ch_ft")),
        ("es-ft", "json", dump(es_f3())),
    ]),
    ("F4", "TOKEN", "zzq_s5 present and healthcheck absent", [
        ("ch-ft", "sql", ch_f4("ch_ft")),
        ("es-ft", "json", dump(es_f4())),
    ]),
    ("F5", "phrase", "the phrase connection refused. es-ft is verify-from-source", [
        ("ch-ft", "sql", ch_f5("ch_ft")),
        ("es-ft", "json", dump(es_f5())),
        ("es-phrase", "json", dump(es_f5())),
    ]),
    ("F6", "SUBSTRING", "the decoy abc inside a larger token", [
        ("ch-ng", "sql", ch_f6("ch_ng")),
        ("ch-scan", "sql", ch_f6("ch_scan")),
        ("es-ng", "json", dump(es_f6())),
        ("es-scan", "json", dump(es_scan_f6())),
    ]),
    ("F6-token", "TOKEN", "token abc, expected to miss the decoy", [
        ("ch-ft", "sql", ch_f6_token("ch_ft")),
        ("es-ft", "json", dump(es_f6_token())),
    ]),
    ("M1", "TOKEN", "zzq_s5 then count grouped by app", [
        ("ch-ft", "sql", ch_m1("ch_ft")),
        ("es-ft", "json", dump(es_m1())),
    ]),
    ("M2", "TOKEN", "zzq_s5 then p95 of duration_ms", [
        ("ch-ft", "sql", ch_m2("ch_ft")),
        ("es-ft", "json", dump(es_m2())),
    ]),
    ("M3", "TOKEN", "zzq_s5 then top 10 pods by count", [
        ("ch-ft", "sql", ch_m3("ch_ft")),
        ("es-ft", "json", dump(es_m3())),
    ]),
]

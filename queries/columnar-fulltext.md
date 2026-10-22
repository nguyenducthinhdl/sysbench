# Columnar and full-text queries

Frozen by the 1 GB correctness gate. Later sizes use this text.
Do not edit a query without re-running the gate.

Corpus sha256 `67682d737621f8ddb75e2d35a47f04c61249aab6486e08e2231fd138de4d1734`, seed `20260901`, events `2016000`, bytes `1002338234`.
ClickHouse `26.2.19.43`. Elasticsearch `9.2.8`.
Host `macos`. This run does not fill spec §13.

Two substitutions from [matrix.md](matrix.md), required on the pinned ClickHouse 26.2 image:

- `hasToken(Body, 'zzq_s5')` is rejected: underscore is a `splitByNonAlpha` separator. `hasAllTokens(Body, 'zzq_s5')` is the token predicate, and Elasticsearch `match` with `operator: and` asks the same question.
- `hasPhrase` was added in ClickHouse 26.4. The phrase predicate on this image is `position(Body, 'connection refused') > 0`. `es-ft` still runs `match_phrase` and is `verify-from-source`.
- Elasticsearch 9.2 `match_only_text` rejects a field analyzer. `es-ft` uses that type and the index default analyzer, which is the same `splitByNonAlpha` split.
- `es-scan` stores `body` as an unindexed keyword and scans it from doc values. Query scripts on this version cannot see `_source`.

`$T0` and `$T1` are the window bounds. `$TRACE` is one trace id from the corpus.

## C1

count events with cluster = dc1. Semantic: `columnar`.

### `ch-col`

```sql
SELECT count()
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Cluster = 'dc1'
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "cluster": "dc1"
          }
        }
      ]
    }
  }
}
```

## C2

count level = error and app = checkout-api. Semantic: `columnar`.

### `ch-col`

```sql
SELECT count()
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Level = 'error' AND App = 'checkout-api'
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "level": "error"
          }
        },
        {
          "term": {
            "app": "checkout-api"
          }
        }
      ]
    }
  }
}
```

## C3

count of level = error grouped by minute and app. Semantic: `columnar`.

### `ch-col`

```sql
SELECT toStartOfMinute(Timestamp) AS minute, App, count() AS errors
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1' AND Level = 'error'
GROUP BY minute, App
ORDER BY minute, App
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "level": "error"
          }
        }
      ]
    }
  },
  "aggs": {
    "per_minute": {
      "date_histogram": {
        "field": "@timestamp",
        "fixed_interval": "1m",
        "time_zone": "UTC"
      },
      "aggs": {
        "by_app": {
          "terms": {
            "field": "app",
            "size": 100
          }
        }
      }
    }
  }
}
```

## C4

top 10 pods by error count. Semantic: `columnar`.

### `ch-col`

```sql
SELECT Pod, count() AS c
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1' AND Level = 'error'
GROUP BY Pod
ORDER BY c DESC
LIMIT 10
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "level": "error"
          }
        }
      ]
    }
  },
  "aggs": {
    "top_pods": {
      "terms": {
        "field": "pod",
        "size": 10,
        "order": {
          "_count": "desc"
        }
      }
    }
  }
}
```

## C5

p95 of duration_ms by http.path. Semantic: `columnar`.

### `ch-col`

```sql
SELECT HttpPath, quantile(0.95)(DurationMs) AS p95
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
GROUP BY HttpPath
ORDER BY HttpPath
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        }
      ]
    }
  },
  "aggs": {
    "by_path": {
      "terms": {
        "field": "http.path",
        "size": 1000
      },
      "aggs": {
        "p95": {
          "percentiles": {
            "field": "duration_ms",
            "percents": [
              95
            ]
          }
        }
      }
    }
  }
}
```

## C6

hourly rejection ratio for payment-api. Semantic: `columnar`.

### `ch-col`

```sql
SELECT toStartOfHour(Timestamp) AS h,
       count() AS total,
       countIf(Status = 'rejected') AS rejected
FROM sysbench.ch_col
WHERE App = 'payment-api'
  AND Timestamp >= '$T0' AND Timestamp < '$T1'
GROUP BY h
ORDER BY h
```

### `es-col`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "app": "payment-api"
          }
        }
      ]
    }
  },
  "aggs": {
    "per_hour": {
      "date_histogram": {
        "field": "@timestamp",
        "fixed_interval": "1h",
        "time_zone": "UTC",
        "min_doc_count": 0
      },
      "aggs": {
        "rejected": {
          "filter": {
            "term": {
              "status": "rejected"
            }
          }
        }
      }
    }
  }
}
```

## K1

events for one trace_id, ordered by time, limit 100. Semantic: `keyword`.

### `ch-col`

```sql
SELECT Timestamp, App, Body
FROM sysbench.ch_col
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND TraceId = '$TRACE'
ORDER BY Timestamp
LIMIT 100
```

### `es-col`

```json
{
  "size": 100,
  "track_total_hits": true,
  "sort": [
    {
      "@timestamp": "asc"
    }
  ],
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "trace_id": "$TRACE"
          }
        }
      ]
    }
  }
}
```

## F1

planted token zzq_s5 at 1 in 10^5, cluster = dc1. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Cluster = 'dc1'
  AND hasAllTokens(Body, 'zzq_s5')
```

### `ch-scan`

```sql
SELECT count()
FROM sysbench.ch_scan
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Cluster = 'dc1'
  AND hasAllTokens(Body, 'zzq_s5')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "cluster": "dc1"
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ]
    }
  }
}
```

### `es-scan`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "cluster": "dc1"
          }
        },
        {
          "script": {
            "script": {
              "lang": "painless",
              "source": "if (doc['body'].size() == 0) return false; String b = doc['body'].value; return b.indexOf('zzq_s5') >= 0;"
            }
          }
        }
      ]
    }
  }
}
```

## F2

both zzq_pa and zzq_pb, any order. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_pa zzq_pb')
```

### `ch-scan`

```sql
SELECT count()
FROM sysbench.ch_scan
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_pa zzq_pb')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_pa zzq_pb",
              "operator": "and"
            }
          }
        }
      ]
    }
  }
}
```

### `es-scan`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "script": {
            "script": {
              "lang": "painless",
              "source": "if (doc['body'].size() == 0) return false; String b = doc['body'].value; return b.indexOf('zzq_pa') >= 0 && b.indexOf('zzq_pb') >= 0;"
            }
          }
        }
      ]
    }
  }
}
```

## F3

zzq_s5 narrowed to namespace = payments and app = checkout-api. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Namespace = 'payments' AND App = 'checkout-api'
  AND hasAllTokens(Body, 'zzq_s5')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "term": {
            "namespace": "payments"
          }
        },
        {
          "term": {
            "app": "checkout-api"
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ]
    }
  }
}
```

## F4

zzq_s5 present and healthcheck absent. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_s5')
  AND NOT hasToken(Body, 'healthcheck')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ],
      "must_not": [
        {
          "match": {
            "body": {
              "query": "healthcheck",
              "operator": "and"
            }
          }
        }
      ]
    }
  }
}
```

## F5

the phrase connection refused. es-ft is verify-from-source. Semantic: `phrase`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND position(Body, 'connection refused') > 0
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match_phrase": {
            "body": "connection refused"
          }
        }
      ]
    }
  }
}
```

### `es-phrase`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match_phrase": {
            "body": "connection refused"
          }
        }
      ]
    }
  }
}
```

## F6

the decoy abc inside a larger token. Semantic: `SUBSTRING`.

### `ch-ng`

```sql
SELECT count()
FROM sysbench.ch_ng
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Body LIKE '%abc%'
```

### `ch-scan`

```sql
SELECT count()
FROM sysbench.ch_scan
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND Body LIKE '%abc%'
```

### `es-ng`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "wildcard": {
            "body": {
              "value": "*abc*"
            }
          }
        }
      ]
    }
  }
}
```

### `es-scan`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "script": {
            "script": {
              "lang": "painless",
              "source": "if (doc['body'].size() == 0) return false; String b = doc['body'].value; return b.indexOf('abc') >= 0;"
            }
          }
        }
      ]
    }
  }
}
```

## F6-token

token abc, expected to miss the decoy. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT count()
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasToken(Body, 'abc')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "abc",
              "operator": "and"
            }
          }
        }
      ]
    }
  }
}
```

## M1

zzq_s5 then count grouped by app. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT App, count() AS c
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_s5')
GROUP BY App
ORDER BY App
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ]
    }
  },
  "aggs": {
    "by_app": {
      "terms": {
        "field": "app",
        "size": 20
      }
    }
  }
}
```

## M2

zzq_s5 then p95 of duration_ms. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT quantile(0.95)(DurationMs) AS p95
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_s5')
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ]
    }
  },
  "aggs": {
    "p95": {
      "percentiles": {
        "field": "duration_ms",
        "percents": [
          95
        ]
      }
    }
  }
}
```

## M3

zzq_s5 then top 10 pods by count. Semantic: `TOKEN`.

### `ch-ft`

```sql
SELECT Pod, count() AS c
FROM sysbench.ch_ft
WHERE Timestamp >= '$T0' AND Timestamp < '$T1'
  AND hasAllTokens(Body, 'zzq_s5')
GROUP BY Pod
ORDER BY c DESC
LIMIT 10
```

### `es-ft`

```json
{
  "size": 0,
  "track_total_hits": true,
  "query": {
    "bool": {
      "filter": [
        {
          "range": {
            "@timestamp": {
              "gte": "$T0",
              "lt": "$T1"
            }
          }
        },
        {
          "match": {
            "body": {
              "query": "zzq_s5",
              "operator": "and"
            }
          }
        }
      ]
    }
  },
  "aggs": {
    "top_pods": {
      "terms": {
        "field": "pod",
        "size": 10,
        "order": {
          "_count": "desc"
        }
      }
    }
  }
}
```

## Gate counts at 1 GB

| Check | Expected | Arms |
| --- | --- | --- |
| rows:ch_col | 2016000 | clickhouse=2016000 |
| rows:ch_ft | 2016000 | clickhouse=2016000 |
| rows:ch_ng | 2016000 | clickhouse=2016000 |
| rows:ch_scan | 2016000 | clickhouse=2016000 |
| rows:es-col | 2016000 | elasticsearch=2016000 |
| rows:es-ft | 2016000 | elasticsearch=2016000 |
| rows:es-phrase | 2016000 | elasticsearch=2016000 |
| rows:es-ng | 2016000 | elasticsearch=2016000 |
| rows:es-scan | 2016000 | elasticsearch=2016000 |
| C1:7d | 1568000 | ch-col=1568000, es-col=1568000 |
| C2:7d | 30602 | ch-col=30602, es-col=30602 |
| F1:7d | 16 | ch-ft=16, ch-scan=16, es-ft=16, es-scan=16 |
| C1:24h | 223606 | ch-col=223606, es-col=223606 |
| C2:24h | 4327 | ch-col=4327, es-col=4327 |
| F1:24h | 0 | ch-ft=0, ch-scan=0, es-ft=0, es-scan=0 |
| C1:1h | 13196 | ch-col=13196, es-col=13196 |
| C2:1h | 86 | ch-col=86, es-col=86 |
| F1:1h | 0 | ch-ft=0, ch-scan=0, es-ft=0, es-scan=0 |
| F2:7d | 0 | ch-ft=0, ch-scan=0, es-ft=0, es-scan=0 |
| F4:7d | 10 | ch-ft=10, es-ft=10 |
| F6-token:7d | 0 | ch-ft=0, es-ft=0 |
| F5:7d | 8064 | ch-ft=8064, es-ft=8064, es-phrase=8064 |
| F6:7d | 20 | ch-ng=20, ch-scan=20, es-ng=20, es-scan=20 |
| F5:24h | 1150 | ch-ft=1150, es-ft=1150, es-phrase=1150 |
| F6:24h | 1 | ch-ng=1, ch-scan=1, es-ng=1, es-scan=1 |

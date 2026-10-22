# ClickHouse and Elasticsearch Columnar mode
Actually, Elastic Search has [columnar-storage mode](https://www.elastic.co/search-labs/blog/elasticsearch-columnar-storage) which is similar with ClickHouse's columnar storage principal. This benchmarking is just an experimental by measuring ClikHouse and Columnar Elasticsearch Search on the 1 GB logging datasize in term of the Storage and Full-text search feature.

Bencmarking: 1 GB prefix on ClickHouse 26.2.19.43 and Elasticsearch 9.5.5 in `logsdb_columnar`. The host is macOS Colima at 2 GB. [columnar-fulltext-spec.md](../columnar-fulltext-spec.md) §13 is for a 16-core / 64 GB cgroup, so these numbers stay out of that decision table.

## TL;DR

One corpus, loaded once into each layout. Storage is the bytes on disk after that load. Search is the same predicate on every arm that claims it, timed only after the counts match.

| | |
| --- | --- |
| Corpus | `data/corpus/1gb`, 2,016,000 events, 1,002,338,234 bytes |
| Engines | One process at a time. Merges idle. No `force_merge`. |
| Search window | 7 days, `2026-09-01` through `2026-09-08` |
| Repetitions | 3, after that engine restarted. Page cache left in place. |
| Warm | Median of repetitions 2 and 3 |
| Kept runs | Every arm of a query returned the same count |

Columnar Elasticsearch stores 1.02× to 1.53× the matching ClickHouse table. The n-gram arms are the closest, 1.02×. The scan arms are the widest, 1.53×.

Full text is available on both engines when the arm builds one (`ft`, `phrase`, `ng`). The `col` and `scan` arms answer the same predicates by reading the log line. Warm wall time for the token `zzq_s5` in `dc1` (16 events): `es-ft` 4 ms, `ch-ft` 7.5 ms, `ch-scan` 142.5 ms, `es-scan` 650.5 ms. The phrase `connection refused` (8,064 events) is 4 ms on `es-phrase`. ClickHouse 26.2 scans `Body` for that phrase, 70.5 ms. Substring `abc` (20 events) is 5 ms on `es-ng` and 18 ms on `ch-ng`. The token `abc` returns 0 on both token arms. Elasticsearch warm CPU-seconds were not measured. See Notes.

## Terms

An **arm** is one layout of the same events. `ch-col` and `es-col` are the `col` arm on each engine. The layouts are [clickhouse/ddl.sql](clickhouse/ddl.sql) and [elasticsearch/templates.py](elasticsearch/templates.py).

| Arm | ClickHouse | Elasticsearch |
| --- | --- | --- |
| col | Columns, plus a bloom filter on `TraceId`. No index on `Body`. | `body` is a keyword with `index: false`. The value is doc values. |
| ft | `idx_body`, a text skip index, tokenizer `splitByNonAlpha`. | `body` is `match_only_text`. |
| phrase | No separate table. The phrase query runs on `ch-ft`. | `body` is `text` with `index_options: positions`. |
| ng | `idx_ng`, a text skip index, tokenizer `ngrams(3)`. | `body` is a `wildcard` field. |
| scan | `Body` column, no skip index on it. | `body` is a keyword with `index: false` and doc values on. A script reads the value. |

`ft` and the ClickHouse tokenizer split on characters outside `0-9A-Za-z`. `zzq_s5` is the tokens `zzq` and `s5`. Case stays as written. `match_only_text` uses that same split and stores no word positions. `ng` matches the character sequence `abc`, including inside `traceabc`. `scan` runs the indexed arm's predicate by reading the raw log line.

| Column | Where it appears | What it is |
| --- | --- | --- |
| Inverted | Storage | Bytes of the search structure. ClickHouse: `system.data_skipping_indices`, `data_compressed_bytes` plus `marks_bytes`. That is the `TraceId` bloom on `ch-col`, `idx_body` on `ch-ft`, `idx_ng` on `ch-ng`, and 0 on `ch-scan`. Elasticsearch: `_disk_usage` `all_fields.inverted_index`. On `es-col` this is almost only `_id`. |
| All columns | ClickHouse storage | `system.parts_columns.column_bytes_on_disk` for every column, including `Body`. The skip index is outside this sum. |
| Body | Both | The log-line column alone. On ClickHouse it already sits inside All columns. On Elasticsearch the body table splits the field into inverted postings and doc values. |
| Doc values | Elasticsearch storage | The columnar store. Values of one field sit together and are read by document id. Keyword filters and the scan scripts use them. [Doc values](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/doc-values). |
| Wall ms, 3 reps | Search | How long each run waited, in order. Elasticsearch `took`. ClickHouse `query_duration_ms`. Repetition 1 follows the restart. |
| Warm wall ms | Search | Median of repetitions 2 and 3. With two samples, that median is their average. |
| Warm CPU-seconds | Search | CPU used on repetitions 2 and 3, user plus system, from ClickHouse `system.query_log`. This is CPU time. Wall ms is waiting time. The `es*` cells are empty. See Notes. |
| Full-text index | Search | Whether this query used the structure in the Inverted column. `yes`: ClickHouse `EXPLAIN indexes = 1` shows `Skip` `idx_body` or `idx_ng`, or the Elasticsearch profile leaf is a `TermQuery` or `PhraseQuery` on `body`. `no`: the query read the column or doc values. A phrase or token question can still be `no` when that arm has no index for it, or when ClickHouse 26.2 answers the phrase with `position()`. |

A ClickHouse text index is an inverted token dictionary used as a skip index: it drops granules, then the surviving granules are read as columns. [Text indexes](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/textindexes). [Data skipping indexes](https://clickhouse.com/docs/concepts/features/performance/skip-indexes/skipping-indexes).

`logsdb_columnar` stores fields as doc values. A `text` or `match_only_text` field stays indexed, which is the full-text path on columnar Elasticsearch. [Columnar index mode](https://www.elastic.co/docs/reference/elasticsearch/columnar). [`index.mode`](https://www.elastic.co/docs/reference/elasticsearch/index-settings/index-modules).

## Setup

| | |
| --- | --- |
| Corpus | `data/corpus/1gb`, sha256 `67682d737621f8ddb75e2d35a47f04c61249aab6486e08e2231fd138de4d1734` |
| Events | 2,016,000 |
| Raw JSONL | 1,002,338,234 bytes (956.2 MiB) |
| ClickHouse | 26.2.19.43, database `sysbench` |
| Elasticsearch | 9.5.5, `index.mode: logsdb_columnar`, one shard, zero replicas |
| Host | macOS, Colima at 2 GB |

`_source` is rebuilt from the columns, so `stored_fields` is 0 on every Elasticsearch arm. Points are 0. ClickHouse merges were idle: `ch_col`, `ch_ft`, and `ch_scan` have 27 parts, `ch_ng` has 34.

Bytes per raw gigabyte are `on_disk_bytes / (raw_jsonl_bytes / 10^9)`.

## Storage

### Total size

| Arm | ClickHouse bytes | ClickHouse per raw GB | Elasticsearch bytes | Elasticsearch per raw GB | ES / CH |
| --- | ---: | ---: | ---: | ---: | ---: |
| col | 141,630,835 | 141.3 MB | 201,012,637 | 200.5 MB | 1.42× |
| ft | 165,256,919 | 164.9 MB | 219,336,457 | 218.8 MB | 1.33× |
| phrase | — | — | 243,550,248 | 243.0 MB | — |
| ng | 247,277,238 | 246.7 MB | 253,352,163 | 252.8 MB | 1.02× |
| scan | 139,096,666 | 138.8 MB | 213,081,473 | 212.6 MB | 1.53× |

`es-phrase` is 1.47× `ch_ft`, the table that serves the ClickHouse phrase query.

The five Elasticsearch indexes together are 1,130,332,978 bytes, 1.13× the raw JSONL. The four ClickHouse tables together are 693,261,658 bytes, 0.69× the raw JSONL. Each sum counts the same events once per arm.

### Spec §9 buckets

ClickHouse columnar bytes include `Body`. Stored source on ClickHouse is that `Body` column again, so it is a subset of All columns. Elasticsearch stored source is `stored_fields`, which is 0. `_disk_usage` was taken with `run_expensive_tasks=true` on each data stream.

#### ClickHouse

| Arm | Inverted | What the index is | All columns | Body column | Remainder |
| --- | ---: | --- | ---: | ---: | ---: |
| ch-col | 2,534,169 | bloom on `TraceId` | 116,265,979 | 15,347,851 | 22,830,687 |
| ch-ft | 26,160,253 | `splitByNonAlpha` text index | 116,265,979 | 15,347,851 | 22,830,687 |
| ch-ng | 106,458,860 | `ngrams(3)` text index | 74,666,442 | 9,854,014 | 66,151,936 |
| ch-scan | 0 | no skip index | 116,265,979 | 15,347,851 | 22,830,687 |

The remainder is primary-key files and part metadata. On the three tables with 27 parts it is the same 22,830,687 bytes. `ch_ng` has more parts, and its column files are smaller than the other tables. The n-gram index is why that table is the largest ClickHouse arm.

Per raw gigabyte:

| Arm | Inverted | All columns | Body |
| --- | ---: | ---: | ---: |
| ch-col | 2.5 MB | 116.0 MB | 15.3 MB |
| ch-ft | 26.1 MB | 116.0 MB | 15.3 MB |
| ch-ng | 106.2 MB | 74.5 MB | 9.8 MB |
| ch-scan | 0 | 116.0 MB | 15.3 MB |

`TraceId` is the largest column on `ch_col`, `ch_ft`, and `ch_scan`: 54,624,841 bytes. `SpanId` is next, at 27,353,379 bytes. `Body` is third.

#### Elasticsearch

| Arm | Inverted | Doc values | Points | Stored fields | Store size |
| --- | ---: | ---: | ---: | ---: | ---: |
| es-col | 6,539,051 | 194,411,818 | 0 | 0 | 201,012,637 |
| es-ft | 24,060,167 | 195,066,144 | 0 | 0 | 219,336,457 |
| es-phrase | 41,886,423 | 201,420,501 | 0 | 0 | 243,550,248 |
| es-ng | 52,739,844 | 200,524,966 | 0 | 0 | 253,352,163 |
| es-scan | 5,747,926 | 206,892,156 | 0 | 0 | 213,081,473 |

Per raw gigabyte:

| Arm | Inverted | Doc values | Store |
| --- | ---: | ---: | ---: |
| es-col | 6.5 MB | 194.0 MB | 200.5 MB |
| es-ft | 24.0 MB | 194.6 MB | 218.8 MB |
| es-phrase | 41.8 MB | 201.0 MB | 243.0 MB |
| es-ng | 52.6 MB | 200.1 MB | 252.8 MB |
| es-scan | 5.7 MB | 206.4 MB | 212.6 MB |

Field totals sit a few hundred kilobytes under `store_size`. That gap is segment metadata. On `es-col` the only inverted field is `_id` (6,539,051 bytes). Keyword fields such as `trace_id` and `span_id` are doc values.

### Body

| Arm | Body inverted | Body doc values |
| --- | ---: | ---: |
| es-col | 0 | 9,369,882 |
| es-ft | 17,526,369 | 9,400,552 |
| es-phrase | 35,396,305 | 9,552,038 |
| es-ng | 46,209,559 | 9,396,516 |
| es-scan | 0 | 10,535,157 |

`es-col` and `es-scan` keep `body` as doc values, 9.4 MB and 10.5 MB. The same column is 15.3 MB on `ch_col` and `ch_scan`.

`es-ft` body postings are 17.5 MB, with a 9.4 MB doc-values copy beside them. ClickHouse `idx_body` is 26.2 MB and the `Body` column is not stored a second time.

`es-phrase` body postings are 35.4 MB, 2.02× `es-ft`, plus the doc-values copy. `es-ng` body postings are 46.2 MB plus 9.4 MB of doc values. ClickHouse `idx_ng` is 106.5 MB.

### What the sizes say

The scan gap, 1.53×, is the doc-values copy of every typed field on Elasticsearch against tighter ClickHouse compression of those columns. The document is the columns: there is no separate stored-source bill.

The token index is the extra on top of that. Body postings are 17.5 MB on `es-ft` and 26.2 MB on `ch_ft`. Positions take the Elasticsearch body postings to 35.4 MB. N-gram postings are 46.2 MB on `es-ng` and 106.5 MB on `ch_ng`.

## Search

Raw rows, including each `EXPLAIN` and profile, are in [results/columnar-fulltext-search.json](../results/columnar-fulltext-search.json). The 24-hour F1 window on this prefix has no `dc1` hits for `zzq_s5`, so the timed token search is the 7-day window. Query text is frozen in [driver/queries.py](driver/queries.py). ClickHouse uses `hasAllTokens(Body, 'zzq_s5')` because `hasToken` rejects the underscore. Elasticsearch `match` with `operator: and` splits that needle into the same two tokens.

| Query | Meaning | Count | Arm | Wall ms, 3 reps | Warm wall ms | Warm CPU-seconds | Full-text index |
| --- | --- | ---: | --- | --- | ---: | ---: | --- |
| C1 | columnar, `cluster = dc1` | 1,568,000 | ch-col | 48, 19, 12 | 15.5 | 0.016 | no, primary key |
| C1 | columnar, `cluster = dc1` | 1,568,000 | es-col | 59, 49, 21 | 35 | — | no, doc-values scan |
| F1 | token `zzq_s5`, `cluster = dc1` | 16 | ch-ft | 30, 8, 7 | 7.5 | 0.006 | yes, `idx_body` 17/209 granules |
| F1 | token `zzq_s5`, `cluster = dc1` | 16 | es-ft | 6, 4, 4 | 4 | — | yes, `TermQuery` `body:zzq` and `body:s5` |
| F1 | same token, scan arm | 16 | ch-scan | 379, 135, 150 | 142.5 | 0.133 | no |
| F1 | same token, scan arm | 16 | es-scan | 906, 664, 637 | 650.5 | — | no, script over doc values |
| F5 | phrase `connection refused` | 8,064 | ch-ft | 122, 61, 80 | 70.5 | 0.070 | no, `position()` scans `Body` |
| F5 | phrase `connection refused` | 8,064 | es-ft | 249, 136, 108 | 122 | — | terms only; `match_only_text` has no positions |
| F5 | phrase `connection refused` | 8,064 | es-phrase | 14, 4, 4 | 4 | — | yes, `PhraseQuery` |
| F6 | substring `abc` | 20 | ch-ng | 44, 20, 16 | 18 | 0.014 | yes, `idx_ng` 18/279 granules |
| F6 | substring `abc` | 20 | es-ng | 23, 6, 4 | 5 | — | doc-values automaton, not a term postings query |
| F6 | substring `abc` | 20 | ch-scan | 109, 55, 43 | 49 | 0.028 | no |
| F6 | substring `abc` | 20 | es-scan | 664, 578, 566 | 572 | — | no, script over doc values |
| F6-token | token `abc` | 0 | ch-ft | 38, 7, 4 | 5.5 | 0.002 | yes, `idx_body` pruned to 0/258 granules |
| F6-token | token `abc` | 0 | es-ft | 3, 1, 1 | 1 | — | yes, `TermQuery` `body:abc` |

## Is full text available?

Yes on both engines, on the arms that build a text index. `col` and `scan` have no full-text path on `body`. They still return the matching count by reading the column.

On F1, `ch-ft` warm wall time is 7.5 ms and `ch-scan` is 142.5 ms. `es-ft` is 4 ms and `es-scan` is 650.5 ms. On F6, `ch-ng` is 18 ms and `ch-scan` is 49 ms. `es-ng` is 5 ms and `es-scan` is 572 ms.

Phrase is the gap on ClickHouse 26.2.19. `hasPhrase` is absent, so F5 is `position(Body, 'connection refused') > 0` and `EXPLAIN` shows no `Skip`. `es-phrase` uses a `PhraseQuery` (4 ms). `es-ft` has the two terms and no positions (122 ms). [Match-only text](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/match-only-text).

F6-token returns 0 on `ch-ft` and `es-ft`. The decoy sits inside `traceabc` and `xxabcyy`. F6 returns 20. Token search and substring search are different questions, and both are available.

### Evidence

Spec §10 requires a ClickHouse `Skip` node that names that arm's text index, and an Elasticsearch text leaf of `TermQuery`, `PhraseQuery`, or a boolean of those on `es-ft` and `es-phrase`. Each `proof` value is in [results/columnar-fulltext-search.json](../results/columnar-fulltext-search.json).

| Claim | Proof in the result file | Document |
| --- | --- | --- |
| `ch-ft` token search uses a full-text index | F1 `ch-ft`: `Skip` / `idx_body`, 17 of 209 granules | [ClickHouse text indexes](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/textindexes) |
| `ch-ng` substring search uses a full-text index | F6 `ch-ng`: `Skip` / `idx_ng`, 18 of 279 granules | [ClickHouse data skipping indexes](https://clickhouse.com/docs/concepts/features/performance/skip-indexes/skipping-indexes) |
| `ch-col` and `ch-scan` have no body full-text index | C1 `ch-col` and F1 `ch-scan`: no `Skip` node | [clickhouse/ddl.sql](clickhouse/ddl.sql) |
| Phrase on ClickHouse 26.2 does not use that index | F5 `ch-ft`: no `Skip` node | [driver/queries.py](driver/queries.py) |
| Columnar mode indexes a text field | F1 `es-ft`: `TermQuery` `body:zzq` and `body:s5` | [Columnar index mode](https://www.elastic.co/docs/reference/elasticsearch/columnar), [`logsdb_columnar`](https://www.elastic.co/docs/reference/elasticsearch/index-settings/index-modules) |
| `match_only_text` stores no positions | F5 `es-ft`: two `TermQuery` leaves | [Match-only text](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/match-only-text) |
| `es-phrase` uses positions | F5 `es-phrase`: `PhraseQuery` `body:"connection refused"` | [Text field type](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/text), [elasticsearch/templates.py](elasticsearch/templates.py) |
| `es-ng` serves `*abc*` from the wildcard field | F6 `es-ng`: `BinaryDvConfirmedAutomatonQuery` | [Wildcard field type](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/keyword#wildcard-field-type) |
| `es-col` and `es-scan` read doc values | F1 `es-scan`: `ScriptQuery` on `doc['body']`. C1 `es-col`: `ScanningBinaryDocValuesTermQuery` on `cluster` | [Doc values](https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/doc-values), [elasticsearch/templates.py](elasticsearch/templates.py) |

## Notes

Warm CPU-seconds is empty on every `es*` row because this run recorded Elasticsearch waiting time and left CPU time unmeasured.

ClickHouse writes user and system CPU for each query into `system.query_log`. [driver/search_bench.py](driver/search_bench.py) reads `ProfileEvents['UserTimeMicroseconds']` and `ProfileEvents['SystemTimeMicroseconds']` and stores the sum as `cpu_seconds`. The Elasticsearch search response has `took`. That field is how long the request waited inside Elasticsearch. The bench also stores the client wall clock. Both are waiting time.

The CPU-seconds in spec §12 for Elasticsearch come from the container cgroup during the query. This host has no such account, and the bench does not sample one. Profile output is shard waiting time as well, and profile was off on the timed repetitions. An empty cell means the measurement was not taken.

# Phase 3 — 60-minute summary

Four engines, one official hour per case (steady + spikes). ClickHouse and Elasticsearch ran on VM02 with a 5M-row preload. Loki and VictoriaLogs ran on VM03 with a 1M-row preload. Aggregation is `sum(length(message))` (or the engine equivalent). Disk is `du` of the engine data directory after the hour. Sources: [phase3-full-60mins.md](phase3-full-60mins.md), [phase3-full-60mins-vm03.md](phase3-full-60mins-vm03.md), [phase3-full-60mins-async-insert.md](phase3-full-60mins-async-insert.md).

Preload sizes differ (5M vs 1M), so CH/ES vs Loki/VL is directional, not a fair row-for-row bake-off.

## Headline

| Engine | Host | Preload | Steady query avg | Spikes query p99 | Spikes disk (measured) | Spikes disk @5M† |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| ClickHouse | VM02 | 5M | 42.2 ms | 94.9 ms | 2.00 GB | 2.00 GB |
| ClickHouse + async_insert | VM02 | 5M | — | 92.8 ms | 2.02 GB | 2.02 GB |
| Elasticsearch | VM02 | 5M | 12.8 ms | 99.6 ms | 1.54 GB | 1.54 GB |
| Loki | VM03 | 1M | 20.6 ms | 94.5 ms | 0.47 GB | ~2.34 GB |
| VictoriaLogs | VM03 | 1M | 12.8 ms | 99.6 ms | 0.23 GB | ~1.15 GB |

†Loki/VL measured at 1M; **×5** linear scale to match CH/ES preload. Not a measured 5M run.

## Query p99 by class (ms)

### Steady

| Engine | full-text | exact | filter | aggregation |
| --- | ---: | ---: | ---: | ---: |
| ClickHouse | 37.9 | 99.9 | 24.5 | 24.7 |
| Elasticsearch | 92.4 | 75.3 | 7.0 | 42.3 |
| Loki | 35.8 | 99.5 | 24.1 | 24.6 |
| VictoriaLogs | 92.1 | 75.5 | 7.6 | 44.0 |

### Spikes

| Engine | full-text | exact | filter | aggregation |
| --- | ---: | ---: | ---: | ---: |
| ClickHouse | 41.4 | 123.4 | 24.6 | 37.4 |
| ClickHouse + async_insert | 33.8 | 99.1 | 23.6 | 25.4 |
| Elasticsearch | 160.5 | 99.6 | 19.8 | 108.5 |
| Loki | 41.0 | 113.1 | 24.6 | 37.6 |
| VictoriaLogs | 161.3 | 99.6 | 19.6 | 102.7 |

## Disk usage (1M → 5M scale)

CH/ES ran at 5M. Loki/VL ran at 1M. The **@5M** column is measured disk ×5 for Loki/VL so the four engines can be compared at the same row count. Linear scale is a first-order estimate only (fixed overhead, indexes, and compaction may not be linear).

### Spikes (after hour)

| Engine | Preload | Measured | @5M (Loki/VL ×5) |
| --- | ---: | ---: | ---: |
| ClickHouse | 5M | 2.00 GB | 2.00 GB |
| ClickHouse + async_insert | 5M | 2.02 GB | 2.02 GB |
| Elasticsearch | 5M | 1.54 GB | 1.54 GB |
| Loki | 1M | 0.47 GB | **~2.34 GB** |
| VictoriaLogs | 1M | 0.23 GB | **~1.15 GB** |

Exact bytes: Loki `468475343 × 5 = 2342376715`; VL `230164405 × 5 = 1150822025`.

### Steady (after hour)

| Engine | Preload | Measured | @5M (Loki/VL ×5) |
| --- | ---: | ---: | ---: |
| ClickHouse | 5M | 1.70 GB | 1.70 GB |
| Elasticsearch | 5M | 1.48 GB | 1.48 GB |
| Loki | 1M | 0.37 GB | **~1.87 GB** |
| VictoriaLogs | 1M | 0.18 GB | **~0.91 GB** |

At equal 5M scale, **VictoriaLogs stays smallest (~1.15 GB spikes)**; Elasticsearch (~1.54 GB) is next; ClickHouse (~2.00 GB) and scaled Loki (~2.34 GB) are the largest.

## Resources (spikes hour peak)

| Engine | CPU (cores) | Peak used mem | Disk (measured) | Disk @5M† |
| --- | ---: | ---: | ---: | ---: |
| ClickHouse | 0.67 | 1.32 GB | 2.00 GB | 2.00 GB |
| ClickHouse + async_insert | 1.02 | 1.48 GB | 2.02 GB | 2.02 GB |
| Elasticsearch | 0.84 | 0.66 GB | 1.54 GB | 1.54 GB |
| Loki | 3.61 | 9.38 GB | 0.47 GB | ~2.34 GB |
| VictoriaLogs | 0.68 | ~0.23 GB* | 0.23 GB | ~1.15 GB |

\*VictoriaLogs spikes Peak Used Mem was `no series` in the hour report. Estimate scales the 5-minute smoke ([phase3-smoke-full-5mins.md](phase3-smoke-full-5mins.md)) by VL’s own smoke→hour growth:

| | Smoke 5 min | Hour 60 min | smoke→hour |
| --- | ---: | ---: | ---: |
| VL steady | 164 MB | 188 MB | **×1.15** |
| VL spikes | 197 MB | **~227 MB (est.)** | ×1.15 (applied) |

Same two paths agree: `197 MB × (188/164)` and `188 MB × (197/164)` both give **~227 MB (~0.23 GB)**. For context, Loki grew more over the hour (steady ×1.34, spikes ×1.49); CH/ES spikes stayed ~flat vs their 5‑minute peaks (×0.95 / ×1.00).

Steady hours for CH and ES also had `no series` for CPU/mem. From the same 5-minute smoke: CH steady ~1.34 GB / 0.90 cores; ES steady ~0.66 GB / 0.46 cores. Loki steady peak mem was 8.36 GB / 3.63 cores. VictoriaLogs steady (measured hour) was 0.19 GB / 1.65 cores.

## Brief read

**Elasticsearch** is the fastest overall query average on the 5M set (steady 12.8 ms). Filters are cheap (~2–3 ms avg). Full-text and aggregation p99 rise hard under spikes (160 ms / 109 ms). Disk after spikes is about 1.54 GB.

**ClickHouse** pays more on exact match (p99 ~100–123 ms) because high-cardinality lookups still scan more than ES’s inverted index. Aggregation (`sum(length(message))`) is where CH is competitive or ahead of ES on average. Disk grows the most (~2.0 GB after spikes on 5M) from parts plus system logs. Turning on `async_insert=1` for the spikes re-run did **not** shrink end disk (2.02 GB vs 2.00 GB); query averages improved (15.5 ms vs 28.9 ms) with slightly higher CPU and mem.

**Loki** measures small on disk at 1M (~0.47 GB spikes) but **~2.34 GB if scaled ×5 to 5M**, in the same band as ClickHouse. It still spends the most CPU and resident memory (peaks near 9 GB). Query p99 sits in the same band as CH for most classes; exact is the heavy one.

**VictoriaLogs** is the smallest on disk even after 1M→5M scale (**~1.15 GB** spikes vs ES 1.54 GB / CH 2.00 GB) and matches ES-like filter latency. Full-text and aggregation p99 under spikes look like ES’s (≈160 / 103 ms). Steady query average matches ES (~12.8 ms) on the smaller preload.

## Caveats

- CH/ES preload 5M; Loki/VL preload 1M. Disk **@5M** for Loki/VL is measured ×5, not a real 5M run.
- Disk includes engine data-dir contents measured after the hour; CH growth includes MergeTree parts and system tables.
- VictoriaLogs spikes hour-average ingest in the report (216/s) looks like a peak mis-label; use the per-minute timeline for shape.
- CH async_insert After Preload disk in that file looks inconsistent with Disk Storage − Disk added; trust end Disk Storage for that run.

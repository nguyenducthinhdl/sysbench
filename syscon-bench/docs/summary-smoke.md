# Smoke summary

180-second smokes on 23 Nov 2026. Each case preloaded the same `data/preload-1m.jsonl` (1,000,000 rows), then ran 180 seconds. Steady ingest is 20 events/s and 20 queries/s. Spikes keep 20 queries/s and move ingest between 20 and 200 events/s. Each spike window is 30 seconds, so a 1-minute rate cannot reach 200 events/s.

All four engines ran on VM02, one at a time. Elasticsearch and VictoriaLogs are assigned to VM03, and VM03 has no Docker. The runner stopped the other databases and deleted their volumes before each case. It did not wait 5 minutes. Prometheus and Grafana stayed on VM01.

Grafana is `http://192.168.100.20:3000`. The times below are the windows to select. UTC is what the generator logged. ICT is UTC+7.

## Timeline

| Engine | Case | Preload stored | Smoke | UTC | ICT |
| --- | --- | --- | --- | --- | --- |
| ClickHouse | steady | before the smoke | ingest visible | 10:49:40–10:52:40 | 17:49:40–17:52:40 |
| ClickHouse | spikes | before the smoke | finished 10:56:53 | 10:54:40–10:57:40 | 17:54:40–17:57:40 |
| Elasticsearch | steady | 1,000,000 | finished 11:13:14 | 11:11:00–11:13:14 | 18:11:00–18:13:14 |
| Elasticsearch | spikes | 1,000,000 | finished 11:18:58 | 11:17:00–11:18:58 | 18:17:00–18:18:58 |
| Loki | steady | 13:50:52 | 13:50:54–13:54:35 | 13:50:54–13:54:35 | 20:50:54–20:54:35 |
| Loki | spikes | 1,000,000 | ended 14:01:26 | 13:57:30–14:01:26 | 20:57:30–21:01:26 |
| VictoriaLogs | steady | 14:06:13 | finished 14:09:16 | 14:06:14–14:09:16 | 21:06:14–21:09:16 |
| VictoriaLogs | spikes | 14:10:04 | finished 14:13:06 | 14:10:04–14:13:06 | 21:10:04–21:13:06 |

## Result

| Engine | Steady accepted | Spike accepted | Query failures | Steady query avg | Spike query p99 |
| --- | ---: | ---: | --- | --- | --- |
| ClickHouse | 3,600 | 14,400 | 0 | 8.2–8.8 ms | 34 ms |
| Elasticsearch | 3,600 | 14,400 | 0 | 2.5–3.7 ms | 19 ms |
| VictoriaLogs | 3,600 | 14,400 | 0 | 94–167 ms | 2.4 s |
| Loki | ingest at ~22/s | 1-minute peak 120/s | errors rose to 21.8/s | the few that returned took 20–29 s | client deadline 60 s |

ClickHouse spike events accepted were 14,400 with 0 failures, and the steady smoke passed the same 3,600 / 3,600 offer. Elasticsearch and VictoriaLogs logged `accepted` equal to `scheduled` on both shapes, with 3,600 queries and 0 failures.

Loki stored the 1,000,000 rows and ingested at the target rate. Searches did not keep up. Each full-text and exact search counts 24 hours across about 16,000 streams, which includes the whole preload. Resident memory peaked at 6.5 GB.

VictoriaLogs answered every search. Exact and full-text counts cover the preload from 2020-01-01 through now, so those queries read the million rows. The process peaked at 2.9 cores and stayed under 500 MB. Stored data size was about 164 MB. Retention was set to 100 years so the file's 2026-01-01 timestamps were kept. The stock 7-day retention drops them.

Charts for each engine:

- [ClickHouse](/Users/leona/.cursor/projects/Users-leona-Documents-Work-sysbench/canvases/clickhouse-smoke-report.canvas.tsx)
- [Elasticsearch](/Users/leona/.cursor/projects/Users-leona-Documents-Work-sysbench/canvases/elasticsearch-smoke-report.canvas.tsx)
- [Loki](/Users/leona/.cursor/projects/Users-leona-Documents-Work-sysbench/canvases/loki-smoke-report.canvas.tsx)
- [VictoriaLogs](/Users/leona/.cursor/projects/Users-leona-Documents-Work-sysbench/canvases/victorialogs-smoke-report.canvas.tsx)
- [Four-engine summary](/Users/leona/.cursor/projects/Users-leona-Documents-Work-sysbench/canvases/summary-smoke.canvas.tsx)

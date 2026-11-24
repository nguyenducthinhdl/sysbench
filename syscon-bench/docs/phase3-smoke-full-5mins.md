# Phase 3 report

Each section is one official hour. The table is the ingest rate across that hour.

## clickhouse / phase3-clickhouse-steady

- Preload rows: 5000000
- Window: 2026-11-24T04:41:40Z to 2026-11-24T04:46:43Z
- ingest (events/s): 18.42415714285714
- query avg (ms): 13.126
- query avg full-text (ms): 9.558
- query avg exact (ms): 29.500
- query avg filter (ms): 6.591
- query avg aggregation (ms): 6.681
- query p95 (ms): 46.327
- query p99 (ms): 86.881
- query p99 full-text (ms): 24.501
- query p99 exact (ms): 96.891
- query p99 filter (ms): 18.911
- query p99 aggregation (ms): 20.245
- Peak Used Mem (bytes): 1335619584
- CPU Usage (cores): 0.895
- After Preload Disk size (bytes): 673851861
- Disk Storage (bytes): 2322376779
- Disk added (bytes): 2320529678

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T04:41:40Z | 0.00 |
| 2026-11-24T04:42:40Z | 21.82 |
| 2026-11-24T04:43:40Z | 21.82 |
| 2026-11-24T04:44:40Z | 21.82 |
| 2026-11-24T04:45:40Z | 21.82 |
| 2026-11-24T04:46:40Z | 12.45 |


## clickhouse / phase3-clickhouse-spikes

- Preload rows: 5000000
- Window: 2026-11-24T04:54:09Z to 2026-11-24T04:59:13Z
- ingest (events/s): 80.00520187134502
- query avg (ms): 14.255
- query avg full-text (ms): 10.896
- query avg exact (ms): 31.085
- query avg filter (ms): 7.007
- query avg aggregation (ms): 8.284
- query p95 (ms): 47.490
- query p99 (ms): 88.261
- query p99 full-text (ms): 29.903
- query p99 exact (ms): 97.598
- query p99 filter (ms): 22.660
- query p99 aggregation (ms): 24.350
- Peak Used Mem (bytes): 1385639936
- CPU Usage (cores): 0.985
- After Preload Disk size (bytes): 673851861
- Disk Storage (bytes): 2221565654
- Disk added (bytes): 2220402042

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T04:54:09Z | 0.00 |
| 2026-11-24T04:55:09Z | 21.82 |
| 2026-11-24T04:56:09Z | 185.45 |
| 2026-11-24T04:57:09Z | 21.82 |
| 2026-11-24T04:58:09Z | 185.45 |
| 2026-11-24T04:59:09Z | 17.93 |


## elasticsearch / phase3-elasticsearch-steady

- Preload rows: 5000000
- Window: 2026-11-24T05:09:37Z to 2026-11-24T05:14:40Z
- ingest (events/s): 18.444921428571426
- query avg (ms): 4.767
- query avg full-text (ms): 2.170
- query avg exact (ms): 2.286
- query avg filter (ms): 2.742
- query avg aggregation (ms): 12.192
- query p95 (ms): 21.566
- query p99 (ms): 24.393
- query p99 full-text (ms): 4.985
- query p99 exact (ms): 4.993
- query p99 filter (ms): 7.686
- query p99 aggregation (ms): 24.907
- Peak Used Mem (bytes): 655664648
- CPU Usage (cores): 0.458
- After Preload Disk size (bytes): 1444783591
- Disk Storage (bytes): 1448234576
- Disk added (bytes): 1448082359

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:09:37Z | 0.00 |
| 2026-11-24T05:10:37Z | 21.82 |
| 2026-11-24T05:11:37Z | 21.82 |
| 2026-11-24T05:12:37Z | 21.82 |
| 2026-11-24T05:13:37Z | 21.82 |
| 2026-11-24T05:14:37Z | 17.68 |


## elasticsearch / phase3-elasticsearch-spikes

- Preload rows: 5000000
- Window: 2026-11-24T05:24:09Z to 2026-11-24T05:29:12Z
- ingest (events/s): 79.31959285714287
- query avg (ms): 11.329
- query avg full-text (ms): 2.394
- query avg exact (ms): 2.505
- query avg filter (ms): 2.946
- query avg aggregation (ms): 38.446
- query p95 (ms): 72.314
- query p99 (ms): 94.628
- query p99 full-text (ms): 8.486
- query p99 exact (ms): 8.905
- query p99 filter (ms): 8.881
- query p99 aggregation (ms): 98.850
- Peak Used Mem (bytes): 661759200
- CPU Usage (cores): 0.713
- After Preload Disk size (bytes): 1444783591
- Disk Storage (bytes): 1453063112
- Disk added (bytes): 1452911015

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:24:09Z | 0.00 |
| 2026-11-24T05:25:09Z | 21.82 |
| 2026-11-24T05:26:09Z | 185.45 |
| 2026-11-24T05:27:09Z | 21.82 |
| 2026-11-24T05:28:09Z | 185.45 |
| 2026-11-24T05:29:09Z | 12.49 |


## loki / phase3-loki-steady

- Preload rows: 1000000
- Window: 2026-11-24T05:23:13Z to 2026-11-24T05:29:16Z
- ingest (events/s): 16.338983050847457
- query avg (ms): 21212.165
- query avg full-text (ms): 19570.451
- query avg exact (ms): 21074.523
- query avg filter (ms): 21460.424
- query avg aggregation (ms): 22982.051
- query p95 (ms): 29000.000
- query p99 (ms): 29800.000
- query p99 full-text (ms): 29800.000
- query p99 exact (ms): 29800.000
- query p99 filter (ms): 29800.000
- query p99 aggregation (ms): 29800.000
- Peak Used Mem (bytes): 6224060416
- CPU Usage (cores): 3.676
- Disk Storage (bytes): 493225621
- Disk added (bytes): 493168245

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:23:13Z | 0.00 |
| 2026-11-24T05:24:13Z | 21.82 |
| 2026-11-24T05:25:13Z | 21.82 |
| 2026-11-24T05:26:13Z | 21.82 |
| 2026-11-24T05:27:13Z | 21.82 |
| 2026-11-24T05:28:13Z | 10.91 |
| 2026-11-24T05:29:13Z | 5.97 |


## loki / phase3-loki-spikes

- Preload rows: 1000000
- Window: 2026-11-24T05:31:03Z to 2026-11-24T05:37:07Z
- ingest (events/s): 76.27118644067797
- query avg (ms): 21700.599
- query avg full-text (ms): 21501.211
- query avg exact (ms): 21169.121
- query avg filter (ms): 23413.428
- query avg aggregation (ms): 21067.399
- query p95 (ms): 29000.000
- query p99 (ms): 29800.000
- query p99 full-text (ms): 29800.000
- query p99 exact (ms): 29800.000
- query p99 filter (ms): 29800.000
- query p99 aggregation (ms): 29800.000
- Peak Used Mem (bytes): 6315638784
- CPU Usage (cores): 3.661
- Disk Storage (bytes): 503155265
- Disk added (bytes): 503097889

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:32:03Z | 21.82 |
| 2026-11-24T05:33:03Z | 185.45 |
| 2026-11-24T05:34:03Z | 21.82 |
| 2026-11-24T05:35:03Z | 185.45 |
| 2026-11-24T05:36:03Z | 10.91 |
| 2026-11-24T05:37:03Z | 0.00 |


## victorialogs / phase3-victorialogs-steady

- Preload rows: 1000000
- Window: 2026-11-24T05:53:54Z to 2026-11-24T05:58:57Z
- ingest (events/s): 19.375881169590645
- query avg (ms): 16.654
- query avg full-text (ms): 35.038
- query avg exact (ms): 29.137
- query avg filter (ms): 2.064
- query avg aggregation (ms): 0.747
- query p95 (ms): 48.082
- query p99 (ms): 75.610
- query p99 full-text (ms): 117.156
- query p99 exact (ms): 58.937
- query p99 filter (ms): 4.991
- query p99 aggregation (ms): 4.961
- Peak Used Mem (bytes): 163848192
- CPU Usage (cores): 1.301
- Disk Storage (bytes): 157193874
- Disk added (bytes): 157185682

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:53:54Z | 0.00 |
| 2026-11-24T05:54:54Z | 21.82 |
| 2026-11-24T05:55:54Z | 21.82 |
| 2026-11-24T05:56:54Z | 21.82 |
| 2026-11-24T05:57:54Z | 21.82 |
| 2026-11-24T05:58:54Z | 17.47 |


## victorialogs / phase3-victorialogs-spikes

- Preload rows: 1000000
- Window: 2026-11-24T05:59:45Z to 2026-11-24T06:04:52Z
- ingest (events/s): 23.98870056497175
- query avg (ms): 17.437
- query avg full-text (ms): 17.834
- query avg exact (ms): 35.841
- query avg filter (ms): 7.793
- query avg aggregation (ms): 7.827
- query p95 (ms): 66.714
- query p99 (ms): 95.280
- query p99 full-text (ms): 77.738
- query p99 exact (ms): 103.616
- query p99 filter (ms): 24.278
- query p99 aggregation (ms): 24.734
- Peak Used Mem (bytes): 197197824
- CPU Usage (cores): 1.433
- Disk Storage (bytes): 167563304
- Disk added (bytes): 167555112

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T05:59:45Z | 16.67 |
| 2026-11-24T06:00:45Z | 36.89 |
| 2026-11-24T06:01:45Z | 17.00 |
| 2026-11-24T06:02:45Z | 21.82 |
| 2026-11-24T06:03:45Z | 21.82 |
| 2026-11-24T06:04:45Z | 16.50 |


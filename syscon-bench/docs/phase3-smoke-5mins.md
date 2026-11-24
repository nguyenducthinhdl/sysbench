# Phase 3 report

Each section is one official hour. The table is the ingest rate across that hour.

## clickhouse / phase3-clickhouse-steady

- Preload rows: 1000000
- Window: 2026-11-24T01:10:22Z to 2026-11-24T01:15:26Z
- ingest (events/s): 19.399430175438596
- query avg (ms): 7.959
- query avg full-text (ms): 7.681
- query avg exact (ms): 11.167
- query avg filter (ms): 6.556
- query avg aggregation (ms): 6.448
- query p95 (ms): 19.796
- query p99 (ms): 24.033
- query p99 full-text (ms): 21.437
- query p99 exact (ms): 24.808
- query p99 filter (ms): 14.871
- query p99 aggregation (ms): 9.985
- Peak Used Mem (bytes): 1140965376
- CPU Usage (cores): 0.409
- Disk Storage (bytes): 991008556
- Disk added (bytes): 987856555

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T01:10:22Z | 9.03 |
| 2026-11-24T01:15:22Z | 19.41 |


## clickhouse / phase3-clickhouse-spikes

- Preload rows: 1000000
- Window: 2026-11-24T01:17:22Z to 2026-11-24T01:22:25Z
- ingest (events/s): 80.02580070175439
- query avg (ms): 8.369
- query avg full-text (ms): 8.004
- query avg exact (ms): 11.773
- query avg filter (ms): 6.795
- query avg aggregation (ms): 6.888
- query p95 (ms): 20.822
- query p99 (ms): 24.521
- query p99 full-text (ms): 22.977
- query p99 exact (ms): 37.000
- query p99 filter (ms): 19.934
- query p99 aggregation (ms): 19.865
- Peak Used Mem (bytes): 1243127808
- CPU Usage (cores): 0.444
- Disk Storage (bytes): 935296266
- Disk added (bytes): 934034072

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T01:17:22Z | 11.40 |
| 2026-11-24T01:22:22Z | 80.24 |


## elasticsearch / phase3-elasticsearch-steady

- Preload rows: 1000000
- Window: 2026-11-24T01:25:07Z to 2026-11-24T01:30:10Z
- ingest (events/s): 18.40575974025974
- query avg (ms): 2.916
- query avg full-text (ms): 2.358
- query avg exact (ms): 2.228
- query avg filter (ms): 2.824
- query avg aggregation (ms): 4.107
- query p95 (ms): 5.243
- query p99 (ms): 9.372
- query p99 full-text (ms): 5.840
- query p99 exact (ms): 5.100
- query p99 filter (ms): 8.112
- query p99 aggregation (ms): 9.948
- Peak Used Mem (bytes): 677670800
- CPU Usage (cores): 0.501
- Disk Storage (bytes): 292051261
- Disk added (bytes): 291899384

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T01:25:07Z | 39.54 |
| 2026-11-24T01:30:07Z | 17.40 |


## elasticsearch / phase3-elasticsearch-spikes

- Preload rows: 1000000
- Window: 2026-11-24T01:32:36Z to 2026-11-24T01:37:40Z
- ingest (events/s): 79.47475714285714
- query avg (ms): 3.233
- query avg full-text (ms): 2.464
- query avg exact (ms): 2.356
- query avg filter (ms): 3.072
- query avg aggregation (ms): 4.976
- query p95 (ms): 7.997
- query p99 (ms): 9.846
- query p99 full-text (ms): 5.707
- query p99 exact (ms): 6.731
- query p99 filter (ms): 9.130
- query p99 aggregation (ms): 18.744
- Peak Used Mem (bytes): 609630840
- CPU Usage (cores): 0.792
- Disk Storage (bytes): 297227621
- Disk added (bytes): 297086599

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T01:32:36Z | 8.34 |
| 2026-11-24T01:37:36Z | 79.66 |


## loki / phase3-loki-steady

- Preload rows: 1000000
- Window: 2026-11-24T01:39:30Z to 2026-11-24T01:45:34Z
- ingest (events/s): 16.338983050847457
- query avg (ms): 24309.025
- query avg full-text (ms): 23612.582
- query avg exact (ms): 23536.072
- query avg filter (ms): 22343.078
- query avg aggregation (ms): 28332.125
- query p95 (ms): 29000.000
- query p99 (ms): 29800.000
- query p99 full-text (ms): 29800.000
- query p99 exact (ms): 29800.000
- query p99 filter (ms): 29800.000
- query p99 aggregation (ms): 29800.000
- Peak Used Mem (bytes): 6872399872
- CPU Usage (cores): 3.669
- Disk Storage (bytes): 501286353
- Disk added (bytes): 501228977

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T01:39:30Z | 41.76 |
| 2026-11-24T01:44:30Z | 18.31 |


## clickhouse / phase3-clickhouse-spikes

- Preload rows: 1000000
- Window: 2026-11-24T02:10:42Z to 2026-11-24T02:15:45Z
- ingest (events/s): 80.53203555555555
- query avg (ms): 8.413
- query avg full-text (ms): 7.850
- query avg exact (ms): 11.914
- query avg filter (ms): 6.866
- query avg aggregation (ms): 7.027
- query p95 (ms): 20.928
- query p99 (ms): 24.513
- query p99 full-text (ms): 23.113
- query p99 exact (ms): 34.446
- query p99 filter (ms): 20.863
- query p99 aggregation (ms): 21.705
- Peak Used Mem (bytes): 1220898816
- CPU Usage (cores): 0.449
- Disk Storage (bytes): 942067575
- Disk added (bytes): 938914632

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T02:10:42Z | 0.00 |
| 2026-11-24T02:15:42Z | 80.03 |


## elasticsearch / phase3-elasticsearch-spikes

- Preload rows: 1000000
- Window: 2026-11-24T02:18:32Z to 2026-11-24T02:23:35Z
- ingest (events/s): 79.71711428571429
- query avg (ms): 3.005
- query avg full-text (ms): 2.250
- query avg exact (ms): 2.217
- query avg filter (ms): 2.901
- query avg aggregation (ms): 4.600
- query p95 (ms): 6.830
- query p99 (ms): 9.526
- query p99 full-text (ms): 4.987
- query p99 exact (ms): 4.994
- query p99 filter (ms): 7.891
- query p99 aggregation (ms): 9.952
- Peak Used Mem (bytes): 681214984
- CPU Usage (cores): 0.847
- Disk Storage (bytes): 305614189
- Disk added (bytes): 305473353

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T02:18:32Z | 37.82 |
| 2026-11-24T02:23:32Z | 79.66 |


## clickhouse / phase3-clickhouse-spikes

- Preload rows: 5000000
- Window: 2026-11-24T03:01:53Z to 2026-11-24T03:06:58Z
- ingest (events/s): 79.4343642857143
- query avg (ms): 13.695
- query avg full-text (ms): 10.630
- query avg exact (ms): 30.308
- query avg filter (ms): 7.103
- query avg aggregation (ms): 7.070
- query p95 (ms): 47.998
- query p99 (ms): 88.841
- query p99 full-text (ms): 25.446
- query p99 exact (ms): 97.584
- query p99 filter (ms): 22.744
- query p99 aggregation (ms): 21.845
- Peak Used Mem (bytes): 1399586816
- CPU Usage (cores): 0.920
- Disk Storage (bytes): 2251232597
- Disk added (bytes): 2250079468

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T03:06:53Z | 79.39 |


## elasticsearch / phase3-elasticsearch-spikes

- Preload rows: 5000000
- Window: 2026-11-24T03:21:27Z to 2026-11-24T03:26:30Z
- ingest (events/s): 79.41765000000001
- query avg (ms): 3.036
- query avg full-text (ms): 2.490
- query avg exact (ms): 2.529
- query avg filter (ms): 3.049
- query avg aggregation (ms): 4.122
- query p95 (ms): 4.906
- query p99 (ms): 8.830
- query p99 full-text (ms): 6.135
- query p99 exact (ms): 4.997
- query p99 filter (ms): 7.197
- query p99 aggregation (ms): 9.883
- Peak Used Mem (bytes): 608756352
- CPU Usage (cores): 0.443
- Disk Storage (bytes): 1453253241
- Disk added (bytes): 1453101343

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-24T03:21:27Z | 0.00 |
| 2026-11-24T03:26:27Z | 79.66 |


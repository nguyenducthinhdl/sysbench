## HDFS_v1

`HDFS.log` is the input for [Phase 1A](../phase1a-clickhouse-ingest.md), the ClickHouse
ingest-architecture shakedown. Verified locally rather than trusted from the LogHub table:

| Property | Value |
| --- | --- |
| Lines | 11,175,629 |
| Bytes | 1,577,982,906 (1.47 GiB) |
| SHA-256 | `0783096174d7832c618337f9609e06e04abd86ddd7089b3c12b407e63bfebc52` |
| First event | `2008-11-09 20:35:18` |
| Last event | `2008-11-11 11:16:28` (3 calendar dates) |
| Distinct components | 9 |
| Distinct pids | 27,799 |
| Levels | INFO 10,812,836 / WARN 362,793 — no ERROR lines |

`HDFS.log` and the large preprocessed files are not in git (over 2 MB). After
`git clone`, run [`../scripts/init-data.sh`](../scripts/init-data.sh) from the repo
root, or download the zip yourself:

| | URL |
| --- | --- |
| Direct download | https://zenodo.org/records/8196385/files/HDFS_v1.zip?download=1 |
| Record (checksums, other LogHub sets) | https://zenodo.org/records/8196385 |
| LogHub table | https://github.com/logpai/loghub |

Unpack next to this README. `HDFS.log` SHA-256 must be
`0783096174d7832c618337f9609e06e04abd86ddd7089b3c12b407e63bfebc52`.

Every line matches `^[0-9]{6} [0-9]{6} [0-9]+ [A-Z]+ [^:]+: ` with zero exceptions, which is
what lets the Phase 1A parse transform use `!`-form VRL functions and treat any parse failure
as a changed input rather than an expected edge case.

Two things to know before reusing this file elsewhere:

- **No ERROR lines**, so it cannot carry an error-rate alerting scenario (C4).
- **Second-granularity timestamps, dated 2008**, so it cannot measure ingest-to-visible
  latency on its own. Phase 1A injects a wall-clock canary for that.

It is not a substitute for the synthetic corpus in [corpus-spec.md](../corpus-spec.md): there
are no planted needles with asserted counts, no ground-truth incidents, and almost no stream
cardinality. It is real, messy and available now, which is exactly what a pipeline decision
needs and a detection-quality measurement does not.

### Upstream description

HDFS (http://hadoop.apache.org/hdfs) is the Hadoop Distributed File System designed to run on commodity hardware. Due to the popularity of HDFS, it has been widely studied in the literature. 

This log set is generated in a private cloud environment using benchmark workloads, and manually labeled through handcrafted rules to identify the anomalies. The logs are sliced into traces according to block ids. Then each trace associated with a specific block id is assigned a groundtruth label: normal/anomaly. 

We have preprocessed the dataset for easy use in research, including:
+ HDFS.log_templates.csv
+ anomaly_label.csv
+ Event_traces.csv
+ Event_occurrence_matrix.csv
+ HDFS.npz

### Citation
If you use the HDFS_v1 dataset from loghub in your research, please cite the following papers.
+ Wei Xu, Ling Huang, Armando Fox, David Patterson, Michael Jordan. [Detecting Large-Scale System Problems by Mining Console Logs](https://people.eecs.berkeley.edu/~jordan/papers/xu-etal-sosp09.pdf), in Proc. of the 22nd ACM Symposium on Operating Systems Principles (SOSP), 2009.
+ Jieming Zhu, Shilin He, Pinjia He, Jinyang Liu, Michael R. Lyu. [Loghub: A Large Collection of System Log Datasets for AI-driven Log Analytics](https://arxiv.org/abs/2008.06448). IEEE International Symposium on Software Reliability Engineering (ISSRE), 2023.

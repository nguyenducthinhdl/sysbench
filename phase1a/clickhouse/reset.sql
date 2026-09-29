-- Cold-start reset between repetitions (fairness rule 3 in ../../benchmark-plan.md).
--
-- Full sequence, run from the driver node between every run:
--   clickhouse-client --queries-file phase1a/clickhouse/reset.sql
--   clickhouse-client --queries-file phase1a/clickhouse/ddl.sql
--   systemctl restart clickhouse-server        # on node-1
--   sync && echo 3 > /proc/sys/vm/drop_caches  # on node-1
--
-- DROP SYNC so the next run does not start while the previous table's parts are still
-- being removed in the background, which would otherwise show up as phantom merge CPU.

DROP TABLE IF EXISTS sysbench.logs_hdfs SYNC;
DROP TABLE IF EXISTS sysbench.logs_hdfs_mv SYNC;
DROP TABLE IF EXISTS sysbench.logs_hdfs_kafka SYNC;

-- system.part_log, system.asynchronous_insert_log and system.query_log are NOT truncated
-- here. Every collection query in collect.sql is bounded by the run's start time instead,
-- so the logs stay available for post-hoc investigation of an unstable run.

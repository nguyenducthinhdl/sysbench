-- Arm A3 - server-side batching via async_insert.
--
-- Vector's clickhouse sink does not expose arbitrary ClickHouse settings, so async_insert is
-- attached to the user Vector authenticates as rather than to the insert statement. This is
-- also how it would be done in production: the pipeline owner sets the agent's batching, the
-- database owner sets the server's, and neither has to edit the other's config.
--
-- The two A3 sub-arms differ ONLY in these settings. The Vector config (a3-async.yaml) is
-- byte-identical between them, which is why the arm is recorded by user rather than by file.

-- A3w1: durable ack. The insert is acked after the server-side buffer is flushed, so an
-- acked row is a persisted row.
CREATE USER IF NOT EXISTS phase1a_async_w1 IDENTIFIED WITH no_password;
GRANT INSERT, SELECT ON sysbench.* TO phase1a_async_w1;
ALTER USER phase1a_async_w1 SETTINGS
    async_insert = 1,
    wait_for_async_insert = 1,
    async_insert_max_data_size = 10000000,
    async_insert_busy_timeout_ms = 1000;

-- A3w0: fire and forget. The insert is acked as soon as it lands in the server-side buffer,
-- before the part exists on disk. This is the sub-arm expected to lose acked data under the
-- D2 kill -9 drill, and it is the empirical settlement of revise-syscon.txt section 1.7 -
-- which says ClickHouse visibility delay is "a pipeline choice, not an engine limit".
CREATE USER IF NOT EXISTS phase1a_async_w0 IDENTIFIED WITH no_password;
GRANT INSERT, SELECT ON sysbench.* TO phase1a_async_w0;
ALTER USER phase1a_async_w0 SETTINGS
    async_insert = 1,
    wait_for_async_insert = 0,
    async_insert_max_data_size = 10000000,
    async_insert_busy_timeout_ms = 1000;

-- Confirm the settings actually applied before trusting the run. A typo here would silently
-- produce an ordinary synchronous-insert arm wearing A3's label:
--
--   SELECT name, value FROM system.settings WHERE name LIKE '%async_insert%';
--   -- run as the arm's user, not as default
--
-- And afterwards, confirm the path was actually async:
--
--   SELECT count(), sum(rows), max(flush_time) FROM system.asynchronous_insert_log
--   WHERE event_time > {run_start};
--
-- Zero rows there means the arm did not use async_insert and the result is void.

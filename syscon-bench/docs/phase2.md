# Phase 2: Linux VM, Tailscale, Vector

ClickHouse runs on a Linux VM. The log generator runs as a container on another
host that is already on the tailnet. Vector is a sidecar of that generator.
This phase does not add a Kubernetes Deployment or a CRD, and it does not
change [contract.md](contract.md).

The contract still decides the scenario file, the pool of 1000 identities,
the event fields, the message mix, the query classes, and the client metrics
push. See contract sections 2, 4, 5, 6, 7, and 8. Phase 2 only changes where
the bytes go.

## Order

1. On the VM, install ClickHouse and leave `syscon.logs` empty.
2. On the generator host, build the image and the Vector sidecar. Do not start scenario 01.
3. Write the 2GiB file and ingest it. This is the last step before the formal run.
4. Start scenario 01 only after the preload checks pass.

## VM

SSH to the VM. From `syscon-bench/deploy/vm`:

```bash
CH_USER=admin CH_PASSWORD=admin ./install-clickhouse.sh
```

`CH_USER` and `CH_PASSWORD` are the scenario file's basic-auth pair. The
checked-in scenarios use `admin` / `admin` for storage and for the dashboard.
The script requires Docker and a Tailscale IPv4 address. It does not install
Tailscale and it does not take an auth key.

The script starts `clickhouse/clickhouse-server:26.2`, creates `syscon.logs`,
and sets durable async insert on that user:

- `async_insert = 1`
- `wait_for_async_insert = 1`
- `async_insert_max_data_size = 10000000`
- `async_insert_busy_timeout_ms = 1000`

An acked insert is on disk. `wait_for_async_insert = 0` is not used. The last
line of output is `http://<tailscale-ip>:8123`. Prometheus metrics listen on
port 9363. The table is empty when the script finishes.

## Generator host

Both hosts are already on the tailnet. From `syscon-bench/deploy/generator`,
point the scripts at the URL printed on the VM:

```bash
export STORAGE_ENDPOINT=http://100.x.y.z:8123
export CH_USER=admin
export CH_PASSWORD=admin
```

`deploy/generator/docker-compose.yml` builds the same image as the Phase 1
bench. `up` starts Vector only when a script sets `VECTOR_CONFIG`. It does not
start `syscon-bench run` and it does not write the preload file. The `writer`
and `bench` services are profiles.

`--via vector` posts newline-delimited contract JSON to `http://127.0.0.1:8686`.
The bench container shares Vector's network namespace, so that address is the
sidecar. Queries use the ClickHouse adapter on `STORAGE_ENDPOINT`. The storage
name stays `clickhouse`, which is the Prometheus label. The scenario file is
not edited. Its dashboard entry stays `http://localhost:8001`; the bench
entrypoint forwards that port to the dashboard container. The run POSTs the
five contract series there. Prometheus scrapes `GET /metrics` on that receiver,
Vector on `:9598`, and ClickHouse on the Tailscale host `:9363`.

Grafana is http://localhost:3000. It uses the Phase 1 board, including the
Vector and ClickHouse inserted-row panels. `bench_ingest_*` during the live
run means Vector accepted the batch. ClickHouse inserted rows are the stored
count.

## Preload, then the formal run

Scenario 01 offers 360000 events. That is not 2GiB. The preload is a separate
file, and it is the last action before scenario 01.

```bash
./preload.sh
./run.sh
```

`preload.sh` checks that the VM user has durable async insert, writes
`preload.jsonl` until the file reaches 2147483648 bytes, and has Vector read
that file into `syscon.logs`. The events use the contract identity pool and
the contract message mix. Message length stays inside the scenario `min_size`
and `max_size`. This path does not start the scenario clock and does not push
the client series.

The script exits, and does not start the benchmark, unless both of these hold:

- `SELECT count() FROM syscon.logs` grew by the number of lines written
- `system.asynchronous_insert_log` gained a row for `syscon.logs`

An empty async-insert log means the insert was not async.

`run.sh` refuses to start unless that stamp is present and ClickHouse still
reports at least that many rows. It then runs the checked-in scenario 01 for
the full 3600 seconds. It does not shorten `duration`. Scenario 02 stays a
manual run: set `SCENARIO=test-scenario-02.yaml` only after you mean to spend
that hour, and only after the same preload check.

Filter queries use the live half-open window of 60 seconds. The preload file
is the store under test for full-text and exact match. A hit count of zero is
logged and does not fail the run. A query tick before Vector has accepted an
event does not increment `bench_query_errors_total`.

## Results

No formal run has been recorded from this workspace. `run.sh` appends a dated
block below after scenario 01 finishes on the generator host. That block is
the preload size, the row count, the async-insert log rows, and the live
client, Vector, and ClickHouse series.

<!-- results -->

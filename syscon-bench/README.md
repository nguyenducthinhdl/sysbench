# Benchmark for syscon

Build a Benchmark component for the sysconn to compare the logging storage (Clickhouse, Elastic Search, Liki, Victoria) by the same Logging Partern (RPS, max events size, ...)

## Features

1. Provide 2 separeted components: Client(log generators, query client) and the logging storage interface.
2. Dashboard to view the benchmarking results:
    - Storage Metrics: CPU, RAM, RPS, Error
    - Client Metrics: Log size, Log RPS, Query Latency/Error
3. Integration to any Infrastructure type: K8s, VM, Docker

## Logging Data
Get from survey: https://docs.google.com/spreadsheets/d/1eNMPQrv7KRI6Jv5Mj-81oru8oPW0Ui8bMenp0V67te8/edit?resourcekey=&gid=445018203#gid=445018203

1. Log Schema
    - About 20 fields to mock a logs from Application/backend (60%), Web server, reverse proxy, load balancer, API gateway (20%), (Container, Kubernetes, OS 10%) and Database/cache (10%)
    - Mandatory fields:
        - message
        - uuid
        - instance-id
        - ip-address
        - host-name
        - log-severity
        - status-code
        - component
        - trace-id
        - version
        - service
        - time
2. Logging Pattern
- Number of mocking pod/instances to generate is about 1000
- The message will be a Structured JSON (60%), Access Log format (10%), Plain text and natural error message (10%), Stack traces / exception dumps (5%), Audit/security events (5%)

3. Querry Pattern for Search
- Full-text search: 30%
- Exact-Match: Filtering by service, time, severity, status, or other fields: 30%
- Filtering by service, time, severity, status, or other fields: 40%

## Scenario clock

`duration` and each segment `time_stamp` are seconds from the start of the run.
The generator walks one chain:

`0` → `time_stamp` of the first segment → … → `time_stamp` of the last segment → `duration`

The last `time_stamp` equals `duration`. The first segment covers the seconds from `0` up to its `time_stamp`. Each following segment covers the seconds from the previous `time_stamp` up to its own `time_stamp`.

## Metrics

1. Storage Metrics
    - Ingestion Metrics: 
        - Ceiling Throughput: Maximum stored lines or events per second
        - Resource Utilization on Ingestion: CPU and memory consumption scaling relative to the target throughput (e.g., measuring CPU percentage and JVM heap or non-page-cache memory used on the way to the ceiling).
        - Write Amplification (Bytes Written to Disk): Actual bytes written to disk per stored log line, which matters heavily for cloud block storage where write throughput is capped
    - Storage Efficiency Metrics (Space Path)
        - Disk Space per Million Lines: Total physical disk volume consumed per 1,000,000 log records
        - Compression Ratio: Raw log payload size versus compressed on-disk footprint (e.g., benchmark comparisons showing 500GB of raw logs compressed down to ~142GB for Loki, ~208GB for SigNoz, and ~388GB for Elasticsearch depending on indexing granularity)
        - Index Overhead: The proportion of storage dedicated to inverted text indexes, columnar metadata, or label indexes versus raw log payloads
2. Query Metrics
    - p99 Latency: The 99th-percentile response time for standard observability queries (such as fetching newest error lines, aggregating errors per app, searching high-cardinality terms, or computing lines-per-minute) measured from request dispatch to reading the final byte
    - Query Failure / Timeout Rate: The frequency at which complex or high-cardinality aggregations drop, fail, or time out under load
    - Aggregate Query Speed: Comparative execution duration for heavy analytical scans (e.g., columnar engines like ClickHouse or SigNoz completing aggregate queries in sub-second times compared to several seconds in legacy Lucene/Elasticsearch stacks)

## Phases

- Phase 1: Log generators and Query Client with scenario
    - Log generator application
    - Query Client
    - Send directly via endpoint
    - testing by docker
- Phase 2: Integrate with Infra
    - Infra is K8s and logs would be sent to the log agent like Vector
    - building k8s crd or deployment

## TechStack
1. Log generator / Query Client: Rust or Golang. We hope to regroup to 1 application
2. Dashboard: Prometheus and Graphana
3. Logging Agent: Vector

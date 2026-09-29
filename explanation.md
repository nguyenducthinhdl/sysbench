# Why `syscon.txt` was revised

Companion to [revise-syscon.txt](revise-syscon.txt). Every claim in the 20 Sept note is
listed below with a verdict, the evidence, and what it changes about the benchmark. Line
numbers refer to [syscon.txt](syscon.txt).

Verdicts used:

| Verdict | Meaning |
| --- | --- |
| CONFIRMED | Claim is correct as written. |
| PARTLY | Directionally right, but the reasoning or the scope is wrong. Restated in the revision. |
| WRONG | Factually incorrect. Replaced in the revision. |
| OBSOLETE | Was true recently, is not true for the versions we would deploy in 2026. |
| OPEN | Cannot be settled by reading docs. Turned into a measured item or an owner-tagged question. |

Versions used for verification: Elasticsearch 9.x, ClickHouse 26.x, Grafana Loki 3.5
(with the announced 4.0 changes noted), VictoriaLogs cluster (current docs), Apache Kafka 4.x.

---

## 1. Full-text search section

### 1.1 "Loki (StateLess because of S3 as final storage)" (line 10)

**PARTLY.** The read path is close to stateless, but the write path is not. Ingesters
accumulate chunks in memory and only flush to object storage on `chunk_idle_period` /
`max_chunk_age` / size thresholds. Until that flush, the only other copy is the
write-ahead log on the ingester's local disk and the in-memory replicas
(`replication_factor`, default 3). An ingester terminated without WAL replay or
`flush_on_shutdown` loses its unflushed window.

Calling Loki stateless leads to two bad decisions: treating ingesters as freely
restartable, and skipping a durability test because "S3 has it anyway". Both are wrong.

**Impact:** Phase 3 must `kill -9` a Loki ingester mid-ingest and count acked-but-missing
lines. Loki gets no durability free pass relative to the others.

### 1.2 "Loki: Scan all. Good if you know which part to search" (lines 11-13)

**CONFIRMED as the baseline**, and it is the right mental model for our workload. Worth
recording *why*, because the note leaves the impression that Loki's bloom filters might
rescue free-text search. They will not:

- Blooms were introduced in 3.0 as experimental and remain experimental in self-managed
  Loki and GEL; in Grafana Cloud they are a limited preview for tenants ingesting
  **more than 75TB/month**. ([bloom filter docs](https://grafana.com/docs/loki/latest/operations/bloom-filters/))
- Loki 3.3 made a breaking pivot: blooms **index structured metadata, not free text**
  (`#14061`, block schema V3, Bloom Compactor replaced by Bloom Planner + Bloom Builder).
  ([3.3 release notes](https://grafana.com/docs/loki/latest/release-notes/v3-3/))
- Acceleration only applies to label filter expressions on structured metadata placed
  **before** any parser stage. `{cluster="prod"} | detected_level="error" | logfmt` is
  accelerated; the same filter after `| json` is not.
  ([query acceleration docs](https://grafana.com/docs/loki/latest/query/query_acceleration/))

So for a query like `{app="x"} |= "abc" |= "def"` over free-form log lines, Loki
decompresses and greps every chunk matching the stream selector. That is the number we
should publish for Loki.

**Impact:** Loki's headline arm runs without blooms. A second, clearly labelled arm moves
the needle terms into structured metadata and enables blooms, to quantify what we would
gain by changing the ingestion pipeline. Do not blend the two.

### 1.3 "Tag indexing: Depend on ingestion pipelines for indexing like K8s tags" (line 9)

**CONFIRMED**, and it is the most important operational truth in the whole note. For Loki
and VictoriaLogs, what is cheap to query is decided by the shipper configuration, before
any data is stored. For Elasticsearch it is decided by the index template and for
ClickHouse by the DDL. In all four cases the schema decision is upstream of the query, and
a wrong choice is expensive to undo.

**Impact:** The corpus ships with one fixed label/field mapping used identically by all
four engines ([corpus-spec.md](corpus-spec.md)), otherwise we would be benchmarking four
different schema decisions rather than four engines.

### 1.4 "CH - structure vs unstructure log?" (line 15)

**OBSOLETE.** This was a real blocker two years ago. It is now answered:

- **Unstructured / semi-structured:** the `JSON` type is production-ready from ClickHouse
  **25.3**. It splits JSON documents into per-path subcolumns rather than storing an
  opaque blob. ([JSON type docs](https://clickhouse.com/docs/reference/data-types/newjson),
  [25.3 release](https://clickhouse.com/blog/clickhouse-release-25-03)) The practical
  limit is path explosion, governed by `max_dynamic_paths` (default 1024, keep under
  ~10,000); beyond the threshold extra paths land in a shared overflow file that is still
  queryable but slower.
- **Full text:** text (inverted) indexes are **GA in ClickHouse 26.2**, with no special
  setting required. They were beta in 25.12 behind `enable_full_text_index=1`.
  ([text index docs](https://clickhouse.com/docs/reference/engines/table-engines/mergetree-family/textindexes))
  Tokenizers: `splitByNonAlpha`, `splitByString`, `ngrams`, `sparseGrams`, `asciiCJK`,
  `icu`, `array`. Unordered multi-term AND is exactly `hasAllTokens(col, ['a','b'])`.

Note that ClickStack (ClickHouse's own observability stack) still defaults to a
`Map(LowCardinality(String), String)` attribute schema and treats JSON-typed schemas as
beta, because observability attributes are numerous and sparse.
([map vs json](https://clickhouse.com/docs/use-cases/observability/clickstack/ingesting-data/schema/map-vs-json))
We follow that advice: typed columns for stable fields, `Map` for attributes, `JSON` only
where the structure is genuinely unpredictable.

**Impact:** Two consequences. ClickHouse enters the bake-off as a genuine full-text
contender rather than an analytics-only outsider, and we must pin ClickHouse >= 26.2 —
benchmarking 25.x would measure a beta feature and understate it badly. Also, because the
index is silently skipped when whole tokens cannot be extracted, every ClickHouse query in
the suite needs an `EXPLAIN indexes=1` proof (see 1.5).

### 1.5 Unstated assumption: "unordered search (a b = b a)" is one comparable operation (line 5)

**WRONG, and this is the single biggest methodological risk in the exercise.** The four
engines do not implement the same operation:

| Engine | Idiomatic expression | Semantics |
| --- | --- | --- |
| Loki | `{...} \|= "abc" \|= "def"` | **substring**, case-sensitive |
| Elasticsearch | `match` with `operator: and` | **token**, after the analyzer |
| ClickHouse | `hasAllTokens(body, ['abc','def'])` | **token**, per index tokenizer |
| VictoriaLogs | `abc AND def` | **word/token** |

Searching `abc` finds the line `traceabc=1` under substring matching and does not find it
under token matching. So the four engines can legitimately return different row counts for
"the same" query, and whoever returns fewer rows looks faster. Tokenizer choice matters
too: `ngrams` and `splitByNonAlpha` have different recall for the same needle, and in
ClickHouse the index is skipped outright if the query's tokenizer does not match the
index's.

**Impact:** Phase 0 is a correctness gate. For every query we pin one semantic, express it
per engine, and assert identical result counts on a 5GB slice before any timing is
recorded. Where an engine cannot express the pinned semantic with index support, that is
reported as a capability gap, not as a latency number.
See [queries/matrix.md](queries/matrix.md).

### 1.6 "Victoria: Weak Data persistence (replica, Sharding)? Could be lost data. Weak Availability" (lines 17-20)

**PARTLY — right conclusion, wrong mechanism.** Data is *persisted* properly:
`vlstorage` writes to local disk under `-storageDataPath`, compresses, merges parts in the
background and enforces retention. What is missing is **replication**:

- `vlinsert` shards incoming logs evenly across `vlstorage` nodes and **does not replicate
  them**. This is deliberate, for linear scalability and to allow query pushdown to
  storage nodes. ([cluster docs](https://docs.victoriametrics.com/victorialogs/cluster/))
- Consequently, "if a `vlstorage` node goes down, the data on that node is not available
  for query". Maintainer confirmation that in-cluster replication is not supported and is
  not planned: [VictoriaLogs#1281](https://github.com/VictoriaMetrics/VictoriaLogs/issues/1281).
- Writes survive a node loss (`vlinsert` redistributes to healthy nodes), so the failure
  mode is a **read availability hole**, not silent loss of newly ingested data.
- The supported HA pattern is 2+ **independent** clusters with `vlagent` fanning out via
  repeated `-remoteWrite.url`, each URL having its own on-disk buffer
  (`-remoteWrite.tmpDataPath`, capped by `-remoteWrite.maxDiskUsagePerURL`).
  ([vlagent docs](https://docs.victoriametrics.com/victorialogs/vlagent/))

So "could be lost data" overstates it and "weak availability" understates the cost: HA
means running the whole thing twice.

**Impact:** This is a pricing fact, not a footnote. If VictoriaLogs wins on efficiency by
30% but needs 2x hardware for HA, it loses. Criterion 3 therefore reports every engine at
two durability tiers, and VictoriaLogs' tier-2 configuration is two clusters.

### 1.7 "Elastic search (expect best)" (line 6) and "Search within 1 day -> Elastic (Expensive)" (line 75)

**OPEN on speed, OBSOLETE on cost.** Elasticsearch being strongest at ad-hoc full-text is
a reasonable prior and the benchmark will confirm or deny it. The cost half needs
re-measuring, because the storage story changed:

- `logsdb` index mode is GA since Elastic Stack 9.0 and is applied **by default** to new
  `logs-*-*` data streams (clusters upgraded from 8.x only get it if they had no matching
  streams before).
  ([logs data streams](https://www.elastic.co/docs/manage-data/data-store/data-streams/logs-data-stream))
- It bundles host-first index sorting, synthetic `_source`, ZSTD and TSDB numeric codecs.
  Elastic's own figures: ~60% reduction in the conservative case, up to 76-77% in nightly
  benchmarks with synthetic `_source` (161.9GB to 37.5GB), at a 10-20% indexing cost.
  ([storage savings](https://www.elastic.co/observability-labs/blog/elasticsearch-logsdb-index-mode-storage-savings),
  [storage evolution](https://www.elastic.co/observability-labs/blog/elasticsearch-logsdb-storage-evolution))
- Sort order carries most of the benefit: `host.name` first then `@timestamp` gives up to
  ~40%, timestamp-first gives under 10%.

**Impact:** ES must be benchmarked with `logsdb` and host-first sort, otherwise we publish
a straw man and the "Elastic is expensive, use Loki beyond 1 day" heuristic inherits an
error of 2-4x in storage. The retention crossover point is a **result**, not an input.

---

## 2. Storage engine section

### 2.1 "S3 is expectly slow? vs Internal Disk, be limited by network throughput" (lines 35-39)

**OPEN — both sides of the argument are testable and neither was tested.** DungTrung's
concern and TungDam's rebuttal ("downloaded from multiple clusters/nodes and aggregated,
not too much impact") are both plausible. On-prem MinIO or Ceph over 25GbE can deliver
multiple GB/s aggregate, and Loki fans chunk fetches out across queriers, so the binding
constraint in practice is usually per-query fan-out concurrency and object-store request
rate/TTFB rather than raw link bandwidth. TungDam's "no need to read all 10GB" is also
correct in principle — the stream selector and time range prune chunks before download —
but *how much* is pruned is exactly what varies between engines.

**Impact:** Do not resolve this by debate. Phase 3 runs Loki twice, once against MinIO and
once against a local `filesystem` store, with everything else fixed, and records bytes
fetched, object-store request count and p99 TTFB per query. The delta is the answer.

### 2.2 "CH: sharding: by default is day" (lines 80-81)

**WRONG, and it conflates two different mechanisms.** In ClickHouse, *sharding* is
horizontal distribution across nodes (Distributed tables / cluster topology) and has
nothing to do with time. *Partitioning* is `PARTITION BY` within one table, and MergeTree
has **no default partitioning at all** — omit the clause and you get a single partition.
The day-granularity idea comes from the OTel/ClickStack log schema convention,
`PARTITION BY toDate(Timestamp)`, which exists so retention can be enforced by dropping
whole partitions instead of running row-level TTL mutations.

**Impact:** Our DDL states `PARTITION BY toDate(...)` explicitly, and retention cost
(drop-partition vs TTL merge) becomes a measured line item rather than an assumption.

### 2.3 "VM Storage: No communication between clusters (can not get data from another clusters). Double write" (lines 40-42)

**CONFIRMED.** This is the direct consequence of 1.6: with no cross-cluster read path,
HA requires writing the same data to both clusters and load-balancing reads in front
(`vmauth` or any HTTP LB). ThangPham's summary is accurate.

**Impact:** Captured as the tier-2 topology for VictoriaLogs; the double write is also a
double ingest CPU cost that criterion 3 must charge to VictoriaLogs, not hide.

### 2.4 "S3: dynamical tier (low frequency, hot frequency), cold storage" (line 32)

**OPEN / out of scope for round 1.** Tiering is a real lever but it multiplies the test
matrix. Every engine here has some version of it (ES ILM hot/warm/cold, ClickHouse
storage policies with `move_factor` and volume tiers, Loki via object-store lifecycle
rules, VictoriaLogs via retention and disk-usage caps).

**Impact:** Round 1 uses a single hot tier for all four so the numbers stay comparable.
Tiering is scoped as a round-2 question, once we know which engine we are tuning.

---

## 3. Log layer design section

### 3.1 "File Store --> Loki --> Elastic?" (line 46)

**PARTLY — a real pattern, stated as a question.** Cheap bulk tier plus a selective
indexed tier is sound, and it is what most large shops end up with. What the note omits is
that it needs a routing decision and it roughly doubles write cost for anything that lands
in both. The two shapes are not equivalent: **dual-write** (send everything to both, index
a subset) is simple and expensive; **promotion** (bulk only, copy into the indexed tier on
demand or by rule) is cheaper and adds latency plus a moving part.

**Impact:** Turned into an explicit decision with a quantitative trigger: what percentage
of log volume needs indexed search within 24h? Under roughly 10-20%, a tiered design wins;
above that, a single well-tuned engine is simpler and cheaper. That percentage is a
question for the log owners, not for the rig.

### 3.2 "Loki: Kafka for decoupling read, write and balance workload ... 10x write throughput ... 3 clusters ... dedup before saving on S3 ... read within 3 hours -> local else S3" (lines 52-58)

Four claims, four different verdicts:

1. **Kafka in front of Loki — CONFIRMED but EXPERIMENTAL.** It exists:
   `ingester.kafka_ingestion.enabled`, a partition ring, and a top-level `kafka_config`
   block explicitly marked `category:"experimental"`.
   ([ingester.go](https://github.com/grafana/loki/blob/main/pkg/ingester/ingester.go),
   [config reference](https://pkg.go.dev/github.com/grafana/loki/v3/pkg/loki)) It is tied
   to the new `dataobj` V2 path, where Kafka records are buffered, sorted and flushed as
   columnar "THOR" objects to object storage, with TSDB indexes built separately by a
   background consumer. ([dataobj format](https://deepwiki.com/grafana/loki/6.3-dataobj-storage-format))
   Loki 3.4 was still adding the Kafka *development environment*.
2. **"10x write throughput" — UNVERIFIED.** No documented general claim. It should not be
   repeated as fact in a document that other teams will read as a decision record.
3. **Deduplication across 3 clusters before writing to S3 — WRONG as a Loki feature.**
   Loki has no cross-cluster dedupe (nothing equivalent to Thanos/Mimir HA-replica
   dedupe). Mirroring to three clusters yields three copies, and "accept duplicate" is
   then a query-time correctness problem, not a solved one.
4. **"Read within 3 hours -> local, else S3" — CONFIRMED,** and it maps to a real knob:
   queriers consult ingesters for recent data within `query_ingesters_within` (default
   3h) and object storage beyond it. Good instinct, now with a name.

**Impact:** The baseline Loki arm is stable 3.5 with TSDB / schema v13 on object storage.
Kafka + dataobj is an optional, clearly labelled experimental arm — useful to look at, not
something to base a production decision on this quarter. The cross-cluster dedupe idea
needs a design spike before it appears in any architecture diagram.

### 3.3 "Design of Loki is too complicated" (line 58)

**CONFIRMED as an operational concern, and it is about to get sharper.** Announced for
Loki 4.0 (announcements dated 2026-08-24, targeted at self-managed v4.0.0):

- **Simple Scalable Deployment is removed.** Loki 4.0 will not run in SSD mode; you must
  move to distributed microservices or HA monolithic.
  ([migration guide](https://grafana.com/docs/loki/latest/setup/migrate/ssd-to-distributed/))
- **Thanos object storage clients become the default** (`-use-thanos-objstore=true`),
  aligning Loki with Mimir. ([announcement](https://grafana.com/whats-next/2026-08-24-thanos-object-storage-now-default-for-loki/))
- **Legacy multi-store backends removed**: Cassandra, DynamoDB, BoltDB for indexes,
  BigTable, gRPC store. Loki 4.0 fails to start if they are referenced.
  ([announcement](https://grafana.com/whats-next/2026-08-24-loki-multi-store-backends-removed/))

**Impact:** Choosing SSD mode for the benchmark would put us on a removed topology.
Benchmark distributed mode, and score component count and configuration surface explicitly
under criterion 5 so that "too complicated" becomes a number we can weigh against
throughput instead of a matter of taste.

### 3.4 "Sometime Kafka has no Zero copy ... Because of TLS and mTLS ... Can solve by sharing session key in Nginx case" (lines 63-66)

**First half CONFIRMED, proposed fix WRONG for Kafka.**

Confirmed: Kafka's `sendfile` path is only used by `PlaintextTransportLayer`.
`SslTransportLayer.transferFrom()` reads into a buffer, calls `sslEngine.wrap()`, then
writes to the socket — `FileChannel.transferTo()` never happens. Kafka's own docs now say
"in-kernel `SSL_sendfile` is currently not supported by Kafka ... `sendfile` is not used
when SSL is enabled". ([KAFKA-13799](https://issues.apache.org/jira/browse/KAFKA-13799))
Page cache still helps; the zero-copy socket path does not.

Wrong: the nginx trick is kTLS, and it does not carry over. There is no broker switch for
kTLS, NIC TLS offload or `TLS_TX_ZEROCOPY_RO` on the file-backed Fetch path, and the JVM
`SSLEngine` path has no kTLS integration. Stock Kafka enables none of it.
([detailed analysis](https://josedavidbaena.com/blog/zero-copy/02-tls-kafka-hidden-tradeoff))

**Impact:** Treat broker TLS CPU as a fixed cost to measure once (brokers with TLS on vs
off, CPU per MB/s) and then budget, not as something to engineer away. If the overhead is
unacceptable, the levers are terminating mTLS at a sidecar or accepting plaintext inside a
trusted L2 domain — a policy decision, not a tuning one. And since the previous benchmark
was affected by this, the number belongs in the report so nobody rediscovers it.

---

## 4. Aggregation section

### 4.1 "Scenario 1 (Recording Rules): Alert 100 error/minutes. Loki: grep ... Need to process, not easy to do. Elastic: Good, supported" (lines 70-73)

**WRONG about Loki.** The Loki ruler evaluates LogQL recording and alerting rules
natively; the rule is a one-liner:

```logql
sum(rate({app="payments"} |= "error" [1m])) > 100
```

All four engines support this, by four different mechanisms with very different costs:

| Engine | Mechanism | Cost shape |
| --- | --- | --- |
| Loki | ruler, LogQL `rate()` over a line filter | rescans the window on every evaluation |
| Elasticsearch | Kibana alerting rule / transform / ES\|QL | index-backed, cheap per evaluation |
| VictoriaLogs | LogsQL `stats` + vmalert | scan-based, usually fast |
| ClickHouse | incremental materialized view + external scheduler | cheapest steady-state, needs a scheduler |

ClickHouse deserves the callout: an incremental MV updates aggregates at insert time, so
the alert query reads a tiny pre-aggregated table. Nothing else here does continuous
aggregation that cheaply.

**Impact:** Criterion 4 measures rule evaluation CPU and detection delay under continuous
load rather than filling in a supported/not-supported table. "Not easy to do" was steering
us away from a configuration one-liner.

### 4.2 "We can search real-time on ES ... CH writing is good by batching --> delay time for searching" (lines 101-104)

**PARTLY, and the conclusion is backwards.**

- Elasticsearch is **near** real time, not real time. A document becomes searchable at the
  next refresh, `index.refresh_interval` default 1s. Only GET-by-id is realtime. Under
  `logsdb` with data streams, refresh behaviour and ILM rollover are worth checking rather
  than assuming.
- A **synchronous** ClickHouse `INSERT` writes a part and makes it atomically visible as
  soon as the insert is acked — in principle *lower* visibility latency than ES's refresh
  cycle. The delay people observe comes from client-side batching (ClickHouse wants large
  batches, so clients buffer) or from `async_insert` without `wait_for_async_insert=1`.
  That is a pipeline choice, not an engine limitation.

The underlying mechanics in the note are right — MergeTree writes parts straight to disk,
Lucene buffers in memory with a translog — but the inference drawn from them is not.

**Impact:** Stop arguing from architecture. Add **ingest-to-visible latency** (p50/p99) as
a first-class measured metric, probed identically on all four with a timestamped canary
record, at whatever batching configuration we would actually run in production.

### 4.3 "Search within 1 day -> Elastic (Expensive); Search > 1 day -> Loki" (lines 75-76)

**PARTLY.** A sensible heuristic with no measurement behind it, and one of its inputs
(ES storage cost) is stale — see 1.7.

**Impact:** The crossover point becomes an output of phase 2, expressed as cost per GB per
day of retention against p95 latency per query class, and it goes in the handbook as a
table rather than folklore.

---

## 5. Conclusions section

### 5.1 "Benchmark is not too much important vs supported feature and build notebook recommendation" (line 92)

**PARTLY — agree on ordering, disagree on de-prioritising.** Features first is correct:
a feature gate is cheap and eliminates options before we spend rig time. But the decisions
that hurt us later are all quantitative — hardware sizing, retention tiers, the HA
hardware multiplier, whether Kafka is needed in front. You cannot write a credible
recommendation notebook without numbers underneath it.

**Impact:** Phase 0 is a 2-day feature gate on a 5GB slice; the full 500GB benchmark runs
only on the survivors. Same priority order as the meeting, without giving up the evidence.

### 5.2 "Why push log to CH? ... Do we need to ignore CH for logwar?" (lines 97-99)

**Question resolved — do not ignore it.** Per 1.4, ClickHouse now has GA text indexes and
a GA JSON type, which removes the technical objection. ThangPham's argument ("if we have
got existing CH, leverage CH for logging") is the strongest one in the section, because it
is the only one that also lowers operational cost: one fewer system to learn, staff and
patch. The business case in the note — analysts cannot predict which field they will need
— is precisely the case where a columnar engine with a JSON type beats a schema-on-write
indexer.

**Impact:** ClickHouse is a full participant in all phases. Flagged as a question for the
note: is there an existing production ClickHouse cluster we could genuinely reuse? The
answer materially changes its TCO score.

### 5.3 "Write throughput - do we need to care? Back pressure" (lines 85-87)

**CONFIRMED that we need to care**, for two reasons the note does not give. Write
throughput sets the hardware floor (and therefore the bill), and back-pressure behaviour
at saturation is an incident-severity question: an engine that sheds load predictably is
operationally very different from one that stalls the shippers or OOMs.

**Impact:** Criterion 3 measures throughput **per core at equal durability** and records
what happens past the knee (429s, shipper lag, OOM, silent drops). Without the durability
normalisation, an engine with no replication wins this criterion for free — the trap 1.6
sets up.

---

## 6. AI topics section

### 6.1 "On-Prem: Can not limit by network" (line 117)

**WRONG as stated, right underneath.** On-prem network controls exist and work:
Kubernetes NetworkPolicy, Cilium identity-based policy, SPIFFE/SPIRE workload identity
with mTLS, service-mesh L7 authorization. TungDam's Envoy proposal (line 114) is the right
shape and ThinhNguyen's resource tagging (line 120) is the right self-serve mechanism.

The actual problem is different and more fundamental: **network identity is not user
identity.** An agent acting on behalf of an engineer needs a delegated, per-request
identity so its authorization can be computed as "what this human may do, intersected with
what this task needs". No IP allowlist, node-level grant or L4 policy can express that.
TungDam's "1 node contains many Agents, we need just open for node" (line 119) is
convenient and is exactly the wrong granularity — it makes every agent on that node
equally privileged.

**Impact:** The revision reframes the section around workload identity plus delegated
short-lived credentials, with the L7 proxy as the enforcement point and a policy engine
(OPA/Cedar) as the decision point, rather than around network segmentation.

### 6.2 "500 engineers vs 1000 Agents ==> security model scaling" (line 116)

**CONFIRMED as the core problem.** The scaling answer is not more policies, it is a
different unit of identity: treat each agent instance as a non-human identity with an
owner, a scope, a TTL and an approval path. Then 1000 agents are not 1000 policy objects,
they are 500 humans' entitlements projected through short-lived, narrowed tokens. Three
rules make it tractable: an agent is never broader than its owner; credentials live
minutes, not months; every action is attributable to (agent, owner, task).

### 6.3 "Grafana have to create service account token, how to manage these tokens?" (lines 124-125)

**CONFIRMED as a real problem; the implied answer is the wrong one.** Minting long-lived
Grafana service-account tokens per agent across 100 clusters creates a secret-sprawl
problem with no revocation story. The workable pattern is a token broker: the agent
presents its workload identity, the broker validates the delegation chain and mints a
short-lived, scoped Grafana SA token on demand — or Grafana sits behind the same
authenticating proxy as everything else and never issues agent tokens at all. "Y"'s
instinct to lock down from the resource side (line 128) is right; it just needs to be
resource-side *authorization*, not client-side lockdown.

### 6.4 "LLM can do the outlier detection vs hard rule" (lines 144-147)

**OPEN, and it is testable.** TungDam's heuristic — if you can detect the outlier well,
you understand it well enough to write the rule — is a good razor and true for most
recurring alerts. It is not universally true: it fails where the rule is expressible but
not maintainable (thousands of services with drifting baselines), and where the signal is
novel log *shapes* rather than threshold crossings. DungTrung's point about lower false
alarms is plausible and unproven; ThangPham's night-shift triage example is the strongest
concrete use case because it targets the expensive human step, not the detection step.

**Impact:** Framed as: deterministic rules page, LLMs triage, summarize, correlate and
propose, humans approve actions. And make it measurable — replay one month of historical
alerts and compare precision/recall against the existing rule set. That benchmark can
reuse the log corpus, so it is nearly free once the rig exists.

### 6.5 Unstated dependency: agents will be our most expensive query client

Not in the note at all, and it connects the two halves of the meeting. A thousand agents
issuing exploratory log queries is a load pattern no human population produces: wide time
ranges, unselective filters, retries. Whichever engine we pick must support **per-tenant
query quotas and limits**, or the first enthusiastic agent rollout becomes an outage.

**Impact:** Added as a hard requirement under criterion 5 (Loki per-tenant limits,
Elasticsearch search throttling and circuit breakers, ClickHouse quotas and settings
profiles, VictoriaLogs concurrency and resource limits), and as an explicit reason the
logging decision should precede the agent platform work rather than run parallel to it.

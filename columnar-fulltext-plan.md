# Columnar and full-text plan

How to execute [columnar-fulltext-spec.md](columnar-fulltext-spec.md). The spec defines the
arms, the queries, the proofs, and the decision thresholds. This plan orders the work.
Where the two disagree, the spec wins.

This is a single-machine study. It does not use the three-node rig and it does not fill
the four-engine handbook. It produces the six headline cells in spec §13.

---

## 1. Preconditions

- Spec accepted as frozen. A query that is not in spec §7 waits for a spec change.
- Docker or a local install can run ClickHouse >= 26.2 and Elasticsearch 9.x, one at a time,
  inside a cgroup of 16 cores and 64 GB.
- The corpus generator from [corpus-spec.md §11](corpus-spec.md) can emit the five nested
  sizes in spec §6: 1, 2, 5, 10, and 20 GB. `tools/gen-corpus/` is not in the tree yet.
  Building that generator is step 1. Generating 500 GB is not on this plan.
- Phase 1A's chosen ClickHouse batch size, if it exists, is the family V batch. Until it
  exists, family V uses the provisional batch in spec §7.5 and keeps the label.

---

## 2. Steps

### Step 1 — Generator

Implement the corpus-spec generator far enough to emit one 20 GB generation and the nested
prefixes at 1, 2, 5, 10, and 20 GB. Same seed. Needle counts asserted at each cut, including
the hit-floor table in spec §6.

Exit: one `manifest.json` per size, with per-shard hashes and asserted needle counts, and a
non-zero exit if an assertion fails. The 1 GB hash is reproducible by hashing the first
1 GB of the 20 GB byte stream.

### Step 2 — Layouts

Write the arm DDL and the Elasticsearch index templates from spec §5. Include the pinned
analyzer. Record every non-default setting in the file that the size loop copies onto each
result record.

Before any load, run the ten-line tokenizer fixture from spec §11.1 against an empty
table and an empty index. Token lists must match. A mismatch here is a mapping bug, and
loading data on top of it wastes the 1 GB load.

### Step 3 — Correctness gate at 1 GB

Load the 1 GB prefix into `ch-col`, `ch-ft`, `ch-ng`, `ch-scan`, `es-col`, `es-ft`,
`es-phrase`, `es-ng`, and `es-scan`, each from empty.

Run spec §11.2 through §11.5. Store the counts. On a mismatch, stop. Do not start timing
and do not retune the analyzer to force a match without writing the semantic change back
into the spec and into [queries/matrix.md](queries/matrix.md).

Freeze the final query text per arm into `queries/columnar-fulltext.md` once the counts
match, so later sizes cannot drift.

### Step 4 — Size loop

For each size in order, 1 GB then 2, 5, 10, and 20:

1. Drop the previous size. Load this prefix into empty tables and indexes.
2. Repeat the count checks. A mismatch stops that size.
3. Wait until merges settle. Write the storage record for every arm (`size_gb` set).
4. Drop the page cache. Confirm with spec §10 that F1 on `ch-ft` and `es-ft` sets
   `index_used: true` at 1 in 10^5. A failure here is an index bug, not a slow result.
5. Time families C, K, F, and M at concurrency 1. Cold is repetition 1 after a restart,
   sync, and a dropped page cache. Warm is repetitions 2 and 3.
6. Write one `results/columnar-fulltext.ndjson` record per query per repetition, including
   `size_gb` and the proof excerpt.
7. Run the natural-token check for this size.

Reclassify any run that fails spec §10 to `scan` before computing ratios. Cells under the
hit floor are written as `unmeasured`.

The 1 GB timing happens in this loop, after step 3 has frozen the query text. Step 3 does
not itself produce headline numbers.

### Step 5 — Window and concurrency

The window sweep (spec §8.2) is part of the size loop, because F1 and C2 are already in
those families. After the concurrency-1 curve is inside the repetition tolerance, run
concurrency 10 on F1 (1 in 10^5, 24h) and on C5 at 1 GB and at 20 GB only.

### Step 6 — Visibility

Only after the size loop is stored. Reload the 20 GB prefix from empty, at the declared
batch size, one engine at a time, and run family V during that ingest. One hundred
canaries. Write p50 and p99. This pass is separate from the quiet-index timing load.

Doing this last keeps a visibility surprise from becoming a reason to change the query
arms already measured.

### Step 7 — Headline note

Write `results/columnar-fulltext.md` with:

- the six cells from spec §13 at each of 1, 2, 5, 10, and 20 GB,
- the threshold row each size lands in, and the curve when those rows differ,
- the list of runs reclassified to `scan` or marked `unmeasured`,
- the storage split per baseline arm per size,
- one paragraph each for Q-FT, Q-COL, and Q-CROSS.

Leave [notebook/logging-decision-handbook.md](notebook/logging-decision-handbook.md)
unchanged. Those cells wait for the four-engine phases.

---

## 3. Schedule

| Step | Effort | Depends on |
| --- | --- | --- |
| 1 Generator, five nested sizes | 1–2 days | corpus-spec |
| 2 Layouts and tokenizer fixture | 0.5 day | spec §5 |
| 3 Correctness gate at 1 GB | 0.5 day | steps 1–2 |
| 4–5 Size loop, windows, concurrency | 3 days | step 3 |
| 6 Visibility during the 20 GB load | 0.5 day | step 4 |
| 7 Headline note | 0.5 day | steps 4–6 |

About a week on one machine. Five loads from empty, not one slice grown until it exceeds RAM.

---

## 4. What ends the study early

- A correctness-gate mismatch at any size. Fix the mapping or amend the spec. Do not publish a ratio from that size.
- Both FT and COL land inside 2× at every size. Write the headline note and stop. Spec §13 already says operability decides.
- A needle the manifest puts under 20 hits. Mark that cell `unmeasured` and continue. It is not a failed run.

---

## 5. Relationship to the logwar phases

| Logwar phase | This plan |
| --- | --- |
| Phase 0 feature gate | Step 3 is the ClickHouse and Elasticsearch half of that gate for the overlapping query ids, at 1 GB. Counts frozen here are the counts phase 0 should reproduce at the same size. |
| Phase 1A ingest | Supplies the ClickHouse batch size for family V. This plan does not reopen the Kafka question. |
| Phase 1 ingest knee | Not run. STORE is on-disk bytes per raw GB after merges, at each of the five sizes. |
| Phase 2 query | The five-size curve answers the columnar-versus-inverted question inside 20 GB. Phase 2 still has to run Loki and VictoriaLogs, concurrency 50, and the incident session. |
| Handbook | Step 7 stays in `results/`. The handbook is filled when the logwar phases catch up. |

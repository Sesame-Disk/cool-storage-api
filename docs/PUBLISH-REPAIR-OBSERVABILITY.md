# Publish-Repair Observability

This runbook covers the Prometheus metrics of the published-block-reference
repair worker. The block-publication funnels that take part in the W2
repair protocol (v2 CreateFile / UploadFile, OnlyOffice, batch copy/move
destination, SeafHTTP uploads, Sync commit publish — the funnels
inventoried in PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md §8.2) queue a
durable repair row per fs_object that carries staged block-reference work.
Initial W2 publication attempts queue the relevant repair rows before HEAD
and clear them when the protocol has authority to settle that repair
identity. Some request-local losses deliberately leave shared rows durable.
Not every HEAD publication queues a row: the content-resurrection paths
(`RevertFile`, `RevertDirectory`, `RestoreTrashItem`, `RevertDirents`)
publish HEAD with no `pub:` staging and no repair row — the known PC-0 gap
`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`, PRE-X1 / PRE-GC, out of
scope here — and metadata-only HEAD mutations stage no blocks at all.

Rows from the **initial** W2 publication attempt are queued before HEAD.
Rows can also be queued **after** HEAD: Sync's idempotent post-publication
reconciliation (`repairPublishedSyncCommitBlockDelta`, run by a client
retry once `targetHead` is already canonical) stages the delta and queues
durable repair rows again; if that reconciliation fails, the worker
processes rows created after HEAD.

The worker is the durable **processor** of repair rows that remain: the
invariant is that a row was queued (before or after HEAD, as above) and
the request **did not successfully clear it** — whether because it lacked
the authority to (an ambiguous outcome), because it died, or because a
clear it was entitled to perform simply failed. How a row initially
remained is **not** an exhaustive taxonomy; examples, of which only the
first has a dedicated ingress/handoff counter here:

- **post-HEAD reconciliation did not complete** in the request — HEAD is
  published, permanent `fs:` promotion and/or attempt-pin cleanup failed —
  and the funnel handed the row over
  (`publish_repair_post_head_reconciliation_failures_total`, plus the
  one-shot immediate repair it schedules);
- **HEAD outcome ambiguous / uncertain** — the CAS may or may not have
  applied; the request returns an error and leaves the attempt pin and the
  row intact for the worker (v2-like funnels) or for the worker or an
  idempotent client retry (Sync);
- **process death** anywhere between queueing the row and clearing it;
- **request-local clear failure** — the durable row's DELETE failed after a
  *successful* reconciliation (the request still returns success and only
  logs a warning), after a proven conflict loser's cleanup, or on a
  pre-HEAD queue / rollback / abort path;
- other request / crash cases not listed here.

Separately from how a row *entered*, every later **visit outcome** is
counted by `publish_repair_visits_total{ok|retained|failed}`: a row the
worker retains (UNKNOWN, or a conclusive non-reachability the protocol has
no durable negative authority to act on) is not a provenance route — it is
a visit outcome, visible there and in `pending_rows`, and it can be the
outcome of the immediate repair as much as of a sweep visit. A row with no
positive reachability can be retained indefinitely and, on successful
unresolved visits, repeatedly re-pinned (see the known issue
`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` for a dead/unreachable
publication whose repair row survived request-local cleanup; it is
revisited under retry/backoff, not on every sweep; ordinary
post-success clear failure is REACHABLE and retryable). The worker's job
for any row is the same: classify reachability of the commit and settle
(REACHABLE → `fs:`) or retain and renew. Why this worker exists, how the
normal case works and why its
expected cadence is not a proven bound are recorded in
[PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md)
§8. These metrics supply repair-health/backlog/operations measurements. The
fail-closed GC health gate discussed by those historical records (§8.8 G/H,
R31 design proof D12) was a W2-11 proposal. [E1-11](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md)
supersedes that proposal for covered current-version pre-D safety: a matching
non-expiring repair vetoes a new D through the direct EACH_QUORUM destructive
proof, independently of worker visit/renewal cadence or pub: TTL. No new health
gate is required for that contract. W2-13 discovery/completion/scale remains
OPEN; this does not cover unadopted funnels or post-COMMITTED revocation.
**These metrics report; they gate nothing.** No protocol, schema or scheduling
behavior changes with them.

## Scrape scope

- SesameFS exposes `/metrics` only on the internal listener path; scrape
  from the node itself, from a private network, or through
  `docker compose exec sesamefs ...` as described in [DEPLOY.md](./DEPLOY.md).
- **Every node runs its own sweep** (`StartPublishedBlockReferenceRepairer`
  is started by every process; there is no leader). Every series below is
  per node and process-local: counters restart at 0 with the process, the
  gauges are re-seeded by that node's next sweep.
- **A node's backlog gauges are its observation, not a global or atomic
  witness.** A sweep is a sequential pass over 32 buckets at the node's own
  consistency: rows can appear and disappear while it runs, and another
  node — in another datacenter especially — may list rows this node does
  not see, or fewer. Nodes and DCs may disagree. `max()` across nodes is a
  **dashboard aggregation only**: `max(publish_repair_pending_rows) = 0`
  does not prove there is no pending repair anywhere. These gauges must never
  supply destructive absence authority. The runtime obtains its pre-D proof
  directly from Cassandra references and pending repairs at EACH_QUORUM,
  with unknown/error fail-closed; it does not use sweep gauges as that proof.

## What the sweep is

Startup sweep, then one sweep every minute. A sweep lists the 32 buckets of
`published_block_reference_repairs` sequentially and, per row, either reaps
progress-only residue, skips the row (process-local retry hint in the
future; row younger than 30 s; advisory lease in the future) or runs a
repair visit: hydrate → bounded reachability classifier → REACHABLE promote
`fs:` and settle / otherwise renew the durable 35-day pin and retain.
Requests whose post-HEAD reconciliation fails also schedule one immediate
background visit (~50 ms later, one attempt, process-local) — and that
immediate visit can itself end `retained`; rows that remained for any
other reason (ambiguous HEAD, process death, a failed clear, …) get no
immediate visit and wait for the sweep.

## Metrics

### `publish_repair_pending_rows`

Gauge. Repair rows (rows the sweep could act on; progress-only residue
excluded) **observed pending while the last complete sweep traversed the
buckets** — one that listed every bucket. A row is counted when listed,
before its visit, so a row the same sweep then settled and deleted is still
counted: the value can conservatively overstate the backlog remaining at
completion until the next sweep (no second scan is made to avoid extra
Cassandra I/O). A sweep with a bucket-listing error leaves it unchanged, so
it never under-reports because part of the backlog was unreadable.

A pending row is not a real block_reference: the row has no TTL, its pub: pins
do (35 d). The matching durable row nevertheless vetoes a new D in the covered
pre-D proof. This gauge reports observed repair backlog, not per-block TTL or
authoritative global presence/absence.

### `publish_repair_oldest_pending_age_seconds`

Gauge. Age, at the instant the last complete sweep **finished**, of the
oldest row that sweep observed pending while traversing (completion time −
`created_at`, clamped at 0 for a row queued while the sweep ran; a row the
sweep itself then settled still counts); 0 when there is none. Queue-time
age of the row, not remaining pin TTL. An old pending row warrants
investigation. `visits_total` and `renewal_failures_total` help
characterize the worker's behavior, but neither proves whether that row's
repair-owned pins are currently live (a `failed` visit may have renewed
them; a `retained` one may have skipped the renewal because the row
vanished; a renewal error may have partially or ambiguously applied).

### `publish_repair_last_sweep_started_timestamp_seconds`

Gauge. Unix timestamp of the last sweep start on this node, complete or
not; 0 means never.

### `publish_repair_last_complete_sweep_timestamp_seconds`

Gauge. Unix timestamp at which the last sweep on this node that listed
every bucket **finished** (completion, not start: a 40-minute sweep that
has just completed reads as fresh); 0 means never. Per-row repair failures
do not withhold it (the backlog was observed); a bucket that could not be
listed does.

It is a **completion / activity heartbeat, not a per-bucket freshness
watermark**: the sweep is sequential, so at the instant it completes the
observation of bucket 0 is as old as the whole sweep took — `time() −
last_complete = 0` can coexist with a 40-minute-old view of the first
buckets. It proves "a complete pass finished recently", not "every bucket
was observed recently". Operational freshness analysis must also account for
sweep span (`publish_repair_sweep_duration_seconds`); this series alone is
not a per-bucket freshness watermark or destructive authority.

This is the heartbeat an operator alert reads. With a 1-minute cadence:

```text
expr: time() - publish_repair_last_complete_sweep_timestamp_seconds > 600
for: 5m
```

reads as "this node has not fully observed the repair backlog for ten
minutes". It fires on a stuck or very slow sweep and on a Cassandra that
cannot list a bucket; it does not fire on rows that fail to settle. It does
not, by itself, cover the scrape target disappearing (the series vanishes
rather than growing stale): pair it with the general `up == 0` /
target-absent alerting, as for every other per-process series.

### `publish_repair_sweep_duration_seconds_{bucket,sum,count}`

Histogram of sweep wall-clock durations, buckets from 100 ms to 1 h. The
sweep is sequential and every visited row may run the bounded classifier
(30 s) plus per-block fan-outs, so a sweep longer than its own 1-minute
cadence is normal under backlog — and is exactly the evidence that the
cadence is not a bound. Watch p95 against the cadence, not against an
absolute number.

### `publish_repair_sweep_rows_total{outcome=...}`

Counter of what the sweep did with each listed row:

- `visited`: ran a repair visit (see `publish_repair_visits_total` for how
  it ended);
- `skipped_retry_hint`: process-local retry hint still in the future
  (5 min → 6 h after a failed visit; lost on restart);
- `skipped_young`: row younger than the 30 s staleness cutoff;
- `skipped_lease`: advisory `lease_expires_at` still in the future (5 min
  after queueing; never authority, scheduling only);
- `residue_reaped` / `residue_reap_not_applied` / `residue_reap_failed`:
  progress-only residue rows (no staged blocks; never pending). `reaped`
  only when the conditional reap applied; `not_applied` when the CAS lost
  to a concurrent requeue or reaper and nothing was removed by this sweep;
  `failed` on error.

`skipped_*` rows are still pending and still counted in
`publish_repair_pending_rows`.

### `publish_repair_visits_total{outcome=...}`

Counter of repair visits by how they ended, from the sweep and from the
immediate scheduler alike:

- `ok`: no error — the row was settled (REACHABLE, `fs:` installed, pin
  removed, row deleted) or found gone and the visit was a no-op;
- `retained`: the settlement selected the retain/retry outcome — UNKNOWN,
  DEFINITELY_NOT_REACHABLE without durable cleanup authority (a
  *conclusive* classification the worker is not allowed to act on), or an
  unsupported outcome — and **no renewal failure was observed**. It proves
  neither why the row was retained, nor that the pin was renewed (the
  renewal helper skips the renewal when the row vanished between its own
  checks, and the visit still ends "retained"), nor that the row still
  exists. A steady rate of `retained`
  for the same backlog means rows that never resolve;
- `failed`: the visit encountered an operational error — classifier,
  hydrate, settlement or renewal. Such a visit **may nevertheless have
  renewed the pin and kept the row** (a classifier read timeout after a
  successful renewal is `failed`). **This is the counter to watch**, but
  neither value is proof about liveness: do not read `retained` as
  "renewal succeeded" nor `failed` as "liveness lost".

### `publish_repair_renewal_failures_total`

Counter of durable 35-day pin renewals that returned an error. The renewal
is a sequential per-block fan-out that stops at its first error, so one
increment may be a partial renewal (some blocks refreshed, the rest not),
and the failing write itself may have applied ambiguously (record §8.5).
A rising rate therefore means **renewal success cannot be shown** —
liveness maintenance is unhealthy or uncertain — not that any block has
lost all its owners: the prior pins may well be alive. Every increment is
also a `failed` visit.

### `publish_repair_post_head_reconciliation_failures_total{funnel=...}`

Counter of post-HEAD reconciliation-failure / repair-handoff **events**
associated with an **already-published commit**: one increment per funnel
invocation that did not complete its request-local reconciliation
(permanent `fs:` promotion and attempt-pin cleanup) and handed the work to
the repair path — whether that invocation published HEAD itself (the
initial post-HEAD failure) or is a later idempotent reconciliation retry of
the same commit — independent of how many files or fs_objects the
invocation carried. It is incremented at the
funnel sites (`schedulePendingPublishedFileRepairs`,
`finalizeSeafHTTPPublishedBlockReferences`,
`scheduleSyncCommitBlockReferenceRepairs`), not in the scheduler, so
Sync's one-call-per-fs_object scheduling does not multiply it. It is
**not once per distinct publication**: a retry of the same commit (Sync's
idempotent retry runs `repairPublishedSyncCommitBlockDelta` and, on
failure, schedules again) increments again — there is no durable
deduplication for a metric. The label is the funnel's own label:
`CreateFile`, `UploadFile`, `OnlyOffice`, `BatchOperationDestination`, the
SeafHTTP operation names (`commitUploadedFileMultiBlock`, …) and the Sync
operation names. Sync's finalize can fail after `fs:` was installed (e.g.
on attempt-pin cleanup), so this is "reconciliation did not complete", not
strictly "`fs:` missing".

This family is **not seeded**: the funnel labels are defined by the call
sites across three packages, so a funnel's series is absent until its
first event rather than 0 (`rate()` of an absent series is "no data").
Every other `publish_repair_*` family is seeded at registration.

In a healthy deployment this counter is close to flat; `HEAD → fs:` is
attempted inside the request with up to 8 promotion attempts.

### `publish_repair_immediate_repairs_total{outcome=ok|failed|deduplicated}`

Counter of the scheduling of the one-shot background visit those requests
run: `deduplicated` when a repair for the same key was already scheduled
(the call is dropped), otherwise the run's outcome. This is scheduling
volume — Sync schedules one key per fs_object of a commit — not
publications. A `failed` immediate repair leaves the row to the durable
sweep (first durable visit ≈ 5 min after queueing).

## Suggested dashboard panels

### Backlog and age (dashboard aggregation; per-node observations, see scope)

```promql
max(publish_repair_pending_rows)
max(publish_repair_oldest_pending_age_seconds)
```

### Sweep health per node

```promql
time() - publish_repair_last_complete_sweep_timestamp_seconds
histogram_quantile(0.95, sum by (instance, le) (rate(publish_repair_sweep_duration_seconds_bucket[15m])))
```

(`instance` — or the deployment's equivalent target label — must stay in
the `sum by`; dropping it merges every node into one histogram.)

### Why rows are pending

```promql
sum by (outcome) (rate(publish_repair_visits_total[15m]))
rate(publish_repair_renewal_failures_total[15m])
```

### Post-HEAD reconciliation failures handed to the repair path

```promql
sum by (funnel) (rate(publish_repair_post_head_reconciliation_failures_total[1h]))
sum by (outcome) (rate(publish_repair_immediate_repairs_total[1h]))
```

This panel is scoped to post-HEAD reconciliation activity and is **not
exhaustive backlog provenance**: it covers the only way into the repair
path that has a dedicated ingress/handoff counter (see the intro). Rows
that remained for any other reason — ambiguous HEAD outcome, process
death, a failed request-local clear after success or after a loser's
cleanup, other cases — appear in `publish_repair_pending_rows` and in the
visit-outcome series (`publish_repair_visits_total`) without any
corresponding event here, so `pending_rows` rising while these rates stay
flat is a valid — and informative — combination.

## Abandoned repair retention (PRE-GC measurement)

Phase 1 of the measurement plan in
[`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`](./KNOWN_ISSUES.md#issue-publish-repair-dead-row-retention-01-a-deadunreachable-publications-repair-row-surviving-request-local-cleanup-can-be-retained-and-repeatedly-re-pinned-indefinitely)
(P2 PRE-GC since 2026-10-12) uses only the series above. It adds no code and
no gate.

```promql
# Per node: keep the target label (`instance` or the deployment's equivalent)
max by (instance) (publish_repair_pending_rows)
max by (instance) (publish_repair_oldest_pending_age_seconds)

# Freshness of those gauges, per node
time() - publish_repair_last_complete_sweep_timestamp_seconds
histogram_quantile(0.95, sum by (instance, le) (rate(publish_repair_sweep_duration_seconds_bucket[1h])))

# Aggregate visit outcomes; does not identify rows
sum by (instance, outcome) (increase(publish_repair_visits_total[6h]))
```

`max()` without `by` is a cross-node dashboard aggregation (see Scrape
scope). It is not a per-node view.

How to read it:

- **Freshness first.** The backlog gauges change only when a sweep lists every
  bucket. After a listing error they keep the previous value. Trust a node's
  backlog only when its last complete sweep is recent, and account for the
  sweep span.
- **Revisits.** Healthy rows settle within minutes. There is no guaranteed
  revisit interval: the 6 h retry cap bounds only a process-local backoff,
  and the sweep is sequential.
- **The 24 h threshold.** An oldest pending age above 24 h on a fresh node is
  an operational investigation threshold, not a measured or guaranteed
  bound.
- **Outcomes.** `retained` is an inconclusive visit that returned no error.
  `failed` is a visit that returned an error. That covers walk problems (the
  1024-node chunk limit, an ancestry cycle, a malformed or unreadable parent)
  as well as operational causes such as an unavailable DC. Neither outcome
  rules out retention, and neither series names the rows.
- **Inspect each row read-only** from the repair table:
  - commit row present;
  - HEAD equal to the commit's parent;
  - walk cursor (`reachability_cursor_commit_id`);
  - `staged_block_ids`.

  Classify the row as one of the issue's shapes, or as an operational cause.

These series do not say why a row was retained. They give per-node
observations and count rows, not the bytes those rows pin. A retain reason
and a pinned-bytes estimate are Phase 2: a separate code change, made only if
Phase 1 shows real retention.

## What is deliberately not here

- **Remaining TTL of the repair-owned pins** ("oldest successful
  repair-owned refresh"). Cassandra exposes it per block as
  `TTL(created_at)` of the `block_references` row, but reading it costs one
  query per block per pending row per sweep, and as an *observation* it is
  not a certified last-successful-refresh witness (ambiguous writes,
  cross-DC; record §8.5). Whether a gate reads it on demand, treats the
  unknown as unsafe, or needs no exact freshness at all is the gate
  design's decision (design proof D6), not a metric to add blindly.
- **Any gate.** Destructive GC keeps its existing gates (`Enabled`, leader
  lease, grace, topology); it does not read these series.

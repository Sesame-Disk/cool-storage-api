# E1-13 / W2-13 — pending repair safety during discovery delay

Frozen base: main@fc50e168db91212cdb6d74b78f3f7db8756c6310 (#268 merged).

## Contract and plan

Measure one Office/CreateFile plaintext single-block durable repair not yet visited
by the repair sweep. Target and backlog repairs must all come from productive
writers interrupted before HEAD, followed by real independent competitor HEADs.
No fabricated repair, HEAD, P, classifier, references, D or promotion.

1. Five named legs: control-no-backlog, backlog-before-target,
   target-not-yet-visited-gc, fresh-process-backlog, eventual-target-visit.
2. Observe actual bucket coordinates; choose naturally earlier buckets for real
   backlog. created_at/lease controls change eligibility, never ordering. No
   substituted listing, sorting inside the production sweep or relocated rows.
3. Integration-only exact-session/library observer before a productive visit;
   pause earlier work while target has not entered repair execution. Distinguish
   bucket listing, visit entry and native classification.
4. Shorten only existing real temporary references and await Cassandra expiry;
   check zero global refs, non-expiring target repair and exact HEAD/tree/P1/K1.
5. During backlog pause run real owned-candidate GC; observe EQ repair guard,
   released claim, retained candidate, no D/root, exact P1/K1 intact.
6. Resume productive sweep; verify observed backlog order then native UNKNOWN,
   exact repair-owned pub renewal, no fs promotion, unchanged target HEAD/tree/P.
   Fresh process restart must rediscover Cassandra rows without local hints.
7. Omit only GC pending-repair lookup in a disposable test container. Same
   unvisited-target zero-ref schedule must reach exact productive COMMITTED;
   finish owned TERMINAL and independently verify all fixture cleanup.
8. Independent metadata/bytes/expiry tracker+projection cleanup for every fixture,
   completeness/unavailable/filter negatives, race repeats, vet, existing gates,
   standard Docker go-all-test, final source/docs audit and quota checks.
9. Commit/push and create reviewable PR. No merge or GC activation.

## Disposition limits

Only measured Office delayed-discovery current-version pre-D safety can become
CLOSED-EVIDENCE. W2-13 overall remains OPEN P2 discovery/convergence/scale. Finite
backlog and eventual visit are not arbitrary-delay proof, fairness, SLA or bounded
completion. GC's direct EQ scan also has backlog/availability cost; failures must
remain fail-closed. W2-12/14, concurrent cleanup, other funnels, Phase5/6, E1/X1,
PRE-GC/A1 and activation remain OPEN. No worker redesign, new index/schema,
parallelism, health gate, bucket-count/TTL or standard dev GC configuration change.

## Validation

Accepted corrected-source results are recorded below. Initial and rejected runs are historical; no result is inferred from the plan.

## Harness and initial evidence

The first productive matrix passes all five legs: isolated child 28.68s, parent
34.773s including TestMain cleanup. Final strengthened-source checks are pending.
A diagnostic command initially inherited unrelated mandatory gates from Compose;
that filtered command correctly failed completeness and is not accepted evidence.
Dedicated commands explicitly unset unrelated gates; standard go-all-test retains
all mandatory gates.

Every target and backlog repair comes from real Office/CreateFile execution paused
by the existing interruption seam after exact-P authority, before HEAD. This is
an in-process abrupt-interruption fixture, not an OS writer crash claim. A separate
productive blockless competitor advances HEAD, yielding native UNKNOWN later.
Each fixture owns its org/library; the worker backlog spans orgs. This measures
sequential repair discovery delay, not same-org GC scan scalability or large load.

Rows keep their productive bucket and PK. The harness observes natural hash
placement, creates up to 16 owned fixtures until three earlier-bucket repairs
exist, and chooses a target in the later bucket. Sorting selects fixtures only;
production listing/order is untouched. Selected rows retain future leases; equal-bucket extras are deferred beyond the
controlled scheduling observation and are still independently cleaned. Age/lease changes
are explicit eligibility controls, never an ordering or latency guarantee.

The before-visit observer is scoped to exact DB session/library and runs after
scheduling filters, before repair execution; normal builds compile it as a no-op.
The child pauses at its first backlog entry. At that point the controlled sweep has not entered
target repair execution. Independent reads require no durable target
anchor/cursor/exhaustion and zero refs, detecting persisted progress or renewal
from another visitor; this is not a fleet-wide in-flight-read quiescence claim. Child accounting separately
requires each selected row's actual native UNKNOWN classifier result. Resume must
visit every selected backlog before target and renew its exact repair-owned pub:
without fs promotion, HEAD/root/P changes, repair removal, D or recovery root.

The fresh-process leg SIGKILLs this paused sweep and verifies the OS signal. A new
process must rediscover the same first backlog from Cassandra and eventually
visit target; no local scheduling map is seeded, cleared or copied. This does not
characterize sustained duplicate workers or a populated local-backoff restart.

Shared E1-11 GC/teardown helpers retain their E1-11 log labels; auxiliary-expiry
cleanup retains E1-12 labels. Cleanup covers every created fixture, including
unselected extras, through exact metadata/repair/K1 and tracker/projection checks.
The omission must reach productive COMMITTED then own TERMINAL before teardown;
it proves guard necessity, not a post-D publication violation.

## Cleanup audit correction

A read of a completed E1-13 fixture found one pending_published_fs_objects owner
and its discovery projection after metadata/repair/K1/expiry teardown. Existing
shared fixture cleanup did not cover that separate surface. This introduced P2
TEST-INFRA is fixed within E1-13: enumerate actual owned-library fs coordinates
before metadata teardown, read exact owners, use DeletePendingPublishedFSObjectOwner,
and independently require canonical and exact by-day projection absence afterward.
The omission script requires the affirmative owner cleanup marker and rejects
owner read/delete/identity/verification errors. No publication protocol change.

Prior completed E1-13 fixtures are repaired only at coordinates from their own
successful teardown logs, after verifying library/block metadata absent and exact
owner org identity. 89 owned fixtures were checked and 89 owners plus their exact
projections removed and verified. No active or foreign fixture is purged; quota
policy and limits are unchanged. This does not repair unrelated historical tests.

## Pre-owner-cleanup verification (historical source)

- Strengthened target-not-visited assertions: 15/15 race legs across three runs
  PASS, 129.501s; ordinary/integration vets, completeness and inventory PASS.
- Three E1-13 negative controls PASS. Guard omission reached productive exact
  COMMITTED while target unvisited, completed own TERMINAL, metadata/K1/expiry
  teardown PASS (11.708s); it predates independent pending-owner cleanup.
- Four W2, three E1-11 and two G5 negative controls PASS. A shell command used an
  incorrect W2 script filename after successful own gates/mutation; the existing
  gates were subsequently run with the correct filename and exit 0.
- Earlier Go file SHA-256:
  95096721dc25f5995bac67b55d2fe156aafe4b51b4fc3cbe0e9311b1252d168e.
- First standard Docker go-all-test exit 0: integration 668.529s, API 20/20
  and OIDC 25/25. This is historical, before pending-owner correction.
  At that historical snapshot, corrected-source checks were still pending; accepted results follow below.

## Safety disposition

The measured Office single-block pre-D subset has positive guard evidence during
controlled backlog delay. The target repair exists before claim/proof and cannot
expire; GC directly scans its org prefix at EQ, independent of worker discovery.
Native UNKNOWN settlement retains it and does not promote fs or publish HEAD.
Removing only that destructive guard permits exact D. This supports only the
measured pre-D subset; W2-13 overall stays OPEN P2 discovery/convergence/scale.

Neither finite timing nor selected bucket order proves arbitrary delays, SLA,
fairness, sustained load, same-org GC scan scalability, outages, concurrent cleanup,
progress Paxos races, post-COMMITTED revocation or other funnels. The direct GC
scan also has backlog and availability cost; read errors remain fail-closed under
the existing protocol. No scheduler/TTL/index/health-gate or dev GC change.

## Backlog selector audit correction

The first selector checked an incorrect sorted index: some schedules obtained
only two strictly earlier-bucket repairs despite the three-row minimum in the
plan. The measured delay was real, but those runs do not certify that minimum.
Selection now requires the third-lowest bucket to precede target and independently
asserts at least four actual eligible coordinates (three backlog + target).
This changes fixture selection only, never the productive scan or row placement.

The intermediate owner-cleanup source passed 15/15 race legs in 149.424s; it is
historical for the final backlog-size contract. Final source must repeat the
matrix, negative controls, omission and standard regression with both corrections.

Frozen-ledger coverage: the original E1-13 attack row requests a reachable repair
behind backlog and post-D continuation. This measured target is unpublished and
natively UNKNOWN; only its pre-D guard causality is covered. That broader
reachable/post-D schedule stays unexecuted by this PR. Existing E1-11 reachable
settlement evidence is separate and is not relabeled as a backlog leg.

## Isolation correction after rejected repeat

A complete detached repeat on 6284e8445 exited 1 (134.672s): two legs correctly
rejected non-null target visitor progress before the controlled sweep could
establish its pause. The shared e19 repair daemon can independently scan aged,
lease-expired rows each minute. This is schedule contamination, not a reproduced
D or publication RED; that run is not final accepted evidence. No progress/refs
were erased to hide it, and owned teardown still completed.

Keep selected real repairs leased until wall-clock +1h; age only created_at.
Unselected extras are leased to +3h. The dedicated sweep observes eligibility
at +2h using an integration-only entry into the same productive sweep body.
The normal worker passes its original UTC now, preserving behavior and queries.
No process-global clock is overridden; native classifier, progress, renewal,
settlement, GC and physical recovery use their original clocks and code. This is
an explicit scheduling control, not a latency/clock-health or lease-authority proof.
The shared daemon remains enabled and correctly skips future-leased target rows.
Normal release callbacks and Cassandra/SILO authority remain unchanged.

The source extraction is only to share that existing body with the test's scoped
eligibility observation; it is not a repair scheduler fix. Final race/gates/full
regression must use this corrected source. All prior runs are historical.

## Corrected-source accepted checks (63747ec1a)

- Final dedicated pipeline exit 0, persisted in the Docker container.
- Race matrix: 15/15 named legs across three fresh isolated children PASS,
  160.321s including parent/child teardown; ordinary/integration vets, completeness
  and mandatory-gate inventory PASS. Each backlog has at least three real earlier
  repairs; target progress remains absent at the controlled pause.
- Three own filtered/unavailable/filtered-child negative controls PASS.
- Guard omission PASS (16.053s): unvisited target, zero real refs, exact productive
  COMMITTED, own TERMINAL recovery and independent metadata/K1/expiry/pending-owner
  canonical+projection teardown for every fixture.
- Four W2 negative controls PASS again. Existing E1-11 three and G5 two negatives
  passed earlier; full standard regression exercises their productive gates.
- E1-13 Go file SHA-256:
  35f9bd87417d61c6b4fe065082c07b16b56e27068a040c623a0cc6ea9273e5d2.
- All six changed Go files in the final standard Docker runner match host bytes.
  Default worker scheduling still passes its original UTC clock to the same body.
- Final standard Docker go-all-test retry exited 0: integration 705.954s, API
  20/20 suites and OIDC 25/25 tests. All 13 standard daemon-dependent controls
  PASS; existing optional topology/Phase6 skips are not claimed as execution.
- All 17 E1-13 fixture owner-teardown checks passed in that standard run.
  Both backends report CLEAN and quota_usage 0, with hard quota policy and
  storage_quota 2000000000 unchanged. Earlier full-suite exit 0 is historical.
- Final source/docs audit found no unresolved introduced P0/P1/P2 within this
  measured scope. W2-13 overall, the frozen reachable/post-D row and X1 remain OPEN.

## Interrupted standard run

The standard runner started at 19:59 UTC on 2026-10-07 was interrupted at
20:16:01 UTC with Docker exit 255. The diagnostic container also exited 255 at
the same restart, while backend services restarted and recovered. Docker logs
for the interrupted container were unavailable afterward; the partial saved log
is not accepted as full-suite evidence. The unchanged final image was restarted
with the default go-all-test service command at 20:23 UTC; its result must be
recorded separately. No timeout, gate, GC configuration or test assertion was
relaxed for this retry.
## Bounded three-DC validation plan (2026-10-15)

Base `main@485d9b87640a66324e405ea54facdeb5d72bb5ed` (#297 merged), branch
`codex/e113-three-dc-evidence`. Measure only the existing five E1-13 legs;
the target's GC call already inherits #296's e111GC topology correction.

1. Freeze current source/image. Reuse only the private owned #296/#297
   Cassandra 5.0.9/SILO fixture after verifying zero roots/orphans/repairs,
   complete NetworkTopologyStrategy RF1 declaration for dc-na/dc-eu/dc-asia,
   all-node readiness and GC-OFF API. Record retained auxiliary storage probes
   separately; do not claim the whole bucket is empty.
2. Run the unchanged required five-leg matrix. Check naturally observed earlier
   backlog buckets, target unvisited/zero-ref state, direct EQ repair guard,
   real sweep SIGKILL/restart and native UNKNOWN/exact pub renewal. Current
   fixtures hold HEAD at the interrupted attempt's parent; no competitor moves
   it to SUPERSEDED. Do not relabel the fixture's panic as a writer OS crash.
3. Repeat with race twice and native sessions through dc-eu, sequentially.
   Execute original filtered/unavailable/child-filtered negatives and the
   guard-only omission mutation in a disposable source copy. Require the real
   COMMITTED certificate, target-not-visited marker, all three existing owned
   cleanup checks and restored source identity.
4. Preserve and diagnose any failure before a bounded correction. If current
   code passes, publish only evidence/tracking, with no invented code fix.
5. Audit source/topology, commands/exits, cleanup, links and claims before PR.
   Finite backlog is not a fairness/latency guarantee. Other E1 cases, the P2
   umbrella, E1-01/X1/PRE-GC/A1 and ordinary GC-OFF disposition remain separate.

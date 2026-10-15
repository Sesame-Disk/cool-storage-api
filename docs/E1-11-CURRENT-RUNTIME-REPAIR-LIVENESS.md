# E1-11 / W2-11: current-runtime repair liveness

## Frozen plan

Base: main@4f7c29923fb6022c4b7e48996b50918011781679 (#264 merged).
Branch: codex/e1-11-current-runtime-repair-liveness.

Evidence first. One real Office CreateFile, one canonical SHA256 block and
exact P1/K1, plaintext, isolated org/repo. No repair row, HEAD, P or D is
manufactured by CQL. Real TTL expiry uses the existing reference API with a
short test TTL. Explicit candidate injection exercises worker safety, not
candidate discovery (W2-14). Existing e19 keyspace/GC-OFF backend isolates the
schedule; the standard Compose daemon configuration remains unchanged.

Required legs:
- normal repair control;
- expiry during a real classifier HEAD read;
- expiry after a real REACHABLE classifier, before promotion;
- clean UNKNOWN retained repair from a real interrupted writer that lost HEAD;
- native wire loss during classification of an applied HEAD;
- native wire loss in the destructive pending-repair scan.

The interrupted not-yet-published UNKNOWN control is explicitly distinct from
an applied HEAD with an unavailable classifier. Neither injected outcomes nor
process-wide classifier replacement is evidence.

Capture exact HEAD/tree/fs_id, canonical P/K, original pub/up TTL, repair
identity/staged IDs, all refs, worker candidate/claim/lifecycle/recovery roots
and bytes. REACHABLE must acknowledge fs: before deleting repair; retry must
be no-op. UNKNOWN/errors retain repair and must not authorize a new D.

Mutation: remove only the destructive pending-repair lookup in a disposable
container source copy. Run the identical post-classifier reachable schedule;
require a real COMMITTED D as the failure reason, not a build/selector/early
assertion failure. Restore by discarding the container. Test cleanup must work
on both GREEN and RED, including exact K1 and owned repair rows.

Add an independent evidence gate with missing-leg, filtered-selector and
unavailable-backend negative controls. Repeat under race. Run W2/G4/G5 gates
and standard Docker go-all-test, then audit source, scope, cleanup and claims.

No runtime fix unless the unmutated schedule produces a RED. No TTL policy,
schema, scheduler, renewal ordering, health gate, upload cost or GC activation
changes. W2-12/13/14, concurrent cleanup, other funnels, retention/Phase5/6,
PC-D1B.5 and W2-10 remain OPEN. Any W2-11 disposition applies only to the
covered current-version non-expiring repair / destructive pre-D contract;
a late repair cannot revoke an already COMMITTED D. Results pending.

## Implementation and evidence

Implementation commit: af2e5a131 (frozen plan: 3dc849fbb).

The actual Office CreateFile handler queues the repair; an observation stop
models interruption after applied HEAD, before request-local promotion. The
visitor runs the real hydrate/classifier/renew/promote/settle implementation.
During-classifier evidence stops after an acknowledged SERIAL HEAD read and
before its caller continues. Post-classifier evidence uses a session/library
keyed integration-only barrier; the ordinary build has a no-op callback.
There is no replacement of the classifier, repair loader or GC liveness result.

Cassandra actually expires every observed original up:/pub: after the existing
reference API shortens its test TTL to 2 seconds. The fixture verifies that
repair created_at has no TTL. At the zero-reference boundary the actual worker
claims the original placement, performs the EACH_QUORUM proof and releases the
claim without D; its exact candidate survives and bytes/HEAD/tree/P1 remain.

| Leg | Classification and result |
| --- | --- |
| normal | Applied HEAD, real REACHABLE visit, fs: acknowledged before repair delete; settled retry makes no fs:/repair writes |
| during-classify | TTL expires while classifier HEAD read is observed; productive GC blocked before D, then normal settlement |
| reachable-post-classify | TTL expires after real REACHABLE result and before promotion; productive GC blocked, exact fs: settlement and no-op retry |
| unknown-retained | Actual interrupted pre-HEAD writer plus competing empty-file HEAD yields clean UNKNOWN; pending repair remains, exact per-row pub: is renewed, no fs: appears |
| classifier-wire-error | Applied HEAD, native loss of SERIAL HEAD SELECT returns driver error/UNKNOWN; pending guard blocks D during expiry, visit reports failed and renews; independent recovery then settles |
| guard-wire-error | Native loss of destructive pending-repair SELECT yields no authority; worker releases claim, preserves candidate/P1/K1; the real REACHABLE visitor settles afterward |

Initial six-leg matrix PASS. Final source with explicit no-TTL, write-free retry
and teardown assertions: race five repetitions, **30/30 PASS**, 140.593s. Vets
PASS in ordinary and integration builds. The independent filtered, unavailable
backend and child-filtered gates all reject false success. Existing W2 closure
negative controls PASS (filtered, unavailable, missing legacy, filtered 3-DC).

The omission mutation removes only blockHasPendingPublicationGlobal from the
pre-D proof in a disposable container. The identical reachable-post-classify
schedule then produces canonical retirement, an exact published lifecycle,
COMMITTED orphan and its recovery root for original P1. This certificate is
the RED assertion; build/selector failures are not accepted. Productive recovery
completes its owned D to TERMINAL before the failed fixture is torn down.
Mutation script PASS, 42.813s, with affirmative teardown verification and no
orphan/root/K1 residue. An initial harness teardown gap was found by that check;
it was corrected, its one owned debug residue removed, and the final mutation
was rerun. That initial run is not the accepted cleanup evidence.

Every successful/failed fixture deletes only its isolated org/repo metadata,
repair rows, expiry records, exact K1 and GC test state using the existing
teardown helpers. Independent checks verify blocks, refs, lifecycles, orphans,
fs_objects, commits, library index, repairs and K1 are absent before marking
its evidence leg observed. Tests do not allocate libraries in shared user quota.
The standard runner's shared-backend cleanup/quota audit is recorded below.

### Final regression and audit

Standard Docker go-all-test: **PASS, exit 0**. Integration 956.708s; API 20/20 suites; OIDC 25/25 tests. All 13 standard daemon-dependent controls PASS, with no GC-disabled skips. Before the run both main and e19
backends are CLEAN with hard quota policy unchanged. All four reconstructed
backends use binary SHA256 fff3fe531bbde5e9f192d495f212cf42b8d4b2e65be0c0e97c3b94f713aae6f3.
Main dev GC=true, isolated e19 GC=false; no daemon setting was changed.
The accepted rerun's Go sources match 73d083425 (E1-11 test SHA256
2b99977706e55bd79f29020212d9bc687f7cb82b2148bb4ff492ca16e3dd1a10).

### Contract disposition

W2-11: CLOSED-EVIDENCE for the covered current-version pre-D safety contract.
This is safety from a durable repair acquired by an adopted writer, not a
promise of gap-free TTL references or bounded repair completion. The new
single-node RF=1 matrix does not independently certify multi-DC availability;
the existing #239/#241 global acquisition/read-domain and three-DC evidence
remain prerequisites of the covered shared mechanism.

A clean UNKNOWN from an interrupted writer that never won HEAD is explicitly
not a claim about a reachable applied HEAD. The separate native classifier
failure leg supplies applied-HEAD/UNKNOWN-error coverage without inventing
history loss. Clean UNKNOWN retains repair; REACHABLE installs real refs before
removing it; a destructive read error supplies no authority. Negative scans
and concurrent promotion/late acquisition retain the separate #252 evidence.

W2-12..14, candidate discovery, worker scale/completion bounds, cleanup races,
per-repair Paxos isolation, unadopted funnels, W2-10 overall, E1/X1, PRE-GC/A1
and Phase5/6 are not closed. A repair first acquired after COMMITTED D cannot
revoke that D. No stable-pin redesign or health gate was implemented; #220,
#222 and #224 remain rejected historical designs. Production GC remains OFF.

Reproduction uses the existing Docker test image/network/env file. The full
standard command is `docker compose --profile test run --rm --no-deps go-all-test`.
Focused evidence requires Docker, CASSANDRA_KEYSPACE=sesamefs_e19, all three
SESAMEFS_URL variables targeting the isolated backend, SESAMEFS_E111_CHILD=1,
and SESAMEFS_REQUIRE_E111_REPAIR_LIVENESS_EVIDENCE=1; run
`go test -race -tags integration -v -count=5 -timeout=6m -run '^TestCurrentRuntimeRepairLiveness|^TestEveryEvidenceGateIsWiredIntoTestMain$' ./internal/integration`.
The two scripts are run inside a disposable Docker test container:
`bash scripts/e111-repair-liveness-mutation.sh` and
`bash scripts/e111-repair-liveness-gates.sh`.

### Standard-suite harness correction

The first full standard run exposed a pre-existing G5 test schedule race:
TestG5CassandraMinIOOldLifeWithoutDayScheduling expected its restarted worker
to recover at least one old-life root, but the live dev daemon recovered that
root first (manual result 0/nil; daemon recovery at 03:11:41 UTC). E1-11 passed
inside that same run. This first run is not accepted as GREEN regression.

Only this manual G5 continuation test now delegates to a child in the existing
GC-OFF e19 keyspace when the standard suite supplies SESAMEFS_G5_ISOLATED_URL.
Every original G5 assertion, including n>=1 and exact P1/P2/K1/K2/reference
checks, is preserved. Its own child evidence gate rejects missing/unavailable
execution before the parent may mark G5 coexistence observed. No production GC
or shared daemon setting is changed, and the other daemon-dependent tests stay
in their original environment. Targeted race/gate verification and a fresh
full go-all-test rerun are required after this harness correction.

A second failure in the first run (Sync continuity/direct, library deleted)
was caused by running a separate gate process against the shared backend while
the full suite was active. TestMain globally cleans ephemeral libraries before
and after each process. Subsequent focused gates/races and the accepted full
regression must therefore run sequentially; no Sync runtime fix is inferred
from that contaminated schedule.

The standard integration budget is raised from 15m to 18m for the added
mandatory E1-11 matrix plus isolated G5 child startup/teardown. Individual
visitor waits, native query deadlines and child timeouts remain bounded and
unchanged. This increases suite headroom; it does not relax any safety assertion
or disable any daemon-dependent test.

Post-harness-correction focused verification: isolated E1-11 **18/18 PASS**
under race (three parent repetitions), isolated G5 old-life **3/3 PASS** with
all original assertions preserved, gate inventory PASS; combined 280.932s.
Five independent negative controls PASS, including G5 filtered/unavailable.
Final ordinary/integration vets PASS. The first full run also exhausted its
old 15m budget (900.547s); its failures/timeout are recorded, not accepted as
GREEN. The accepted regression below must use the new harness sequentially.

### Accepted final snapshot and cleanup

Tested Go/harness commit: 73d083425; subsequent changes are documentation only.
The final omission script PASS (26.231s): exact COMMITTED certificate, productive
owned recovery to TERMINAL, and affirmative metadata/repair/K1 cleanup. The
six-leg matrix also PASS inside the accepted standard regression. G4/G5 and
mandatory W2 closure controls PASS; optional dedicated 3-DC/soak suites are
not claimed as rerun by this standard single-node regression.

After the accepted full run both main and e19 report CLEAN: no owned test
libraries, groups, active test orgs or deleted test orgs awaiting GC. Storage
quota_usage is 0 on both, before and after; storage quota 2,000,000,000 and hard
policy unchanged. Traffic limits remain unchanged; real upload/download traffic
accounting increases normally (main combined 537121747 -> 608457123 bytes;
e19 21262 -> 22749). Traffic counters were not reset.

Final scoped source/claim audit found no unresolved introduced P0/P1/P2.
The observed test-infrastructure races were corrected without changing the
production protocol, GC daemon configuration or any existing safety assertion.
The standard budget/harness adjustment is explicit above. No runtime RED was
observed without the guard omission; no new fence/health gate was justified.

### Cross-audit correction — 2026-10-07

Audit of PR HEAD 1656de975 confirmed one introduced P2 documentation defect:
the current scoped CLOSED-EVIDENCE status coexisted with unqualified OPEN/P1
statements in the issue body and historical #220/#222/#224 records. These are
now explicitly historical at their pinned parents. The old destructive-gap
diagnosis is distinguished from the current pending-repair veto; discovery,
last-pub: candidate creation, known-loser and cleanup residuals stay in their
separate W2-12/13/14 and registered follow-up contracts.

Only Markdown changes in this correction. Runtime, test sources, scripts and
Compose remain identical to the accepted tested snapshot. The recorded Docker
go-all-test result is retained, not presented as a new run. Documentation
consistency, linked evidence, diff scope and whitespace were rechecked; no new
runtime fix or broader closure is justified by the report. The G5 daemon/manual
race predates this PR and the existing scoped harness isolation remains.

### Scope extraction — PR #267

Reaudit at c9a4eb23b found no new E1-11 safety defect. Its remaining objection
concerns bundling a pre-existing G5 harness fix. That fix, its TestMain/Compose
wiring and independent negative controls now belong to prerequisite
[PR #267](https://github.com/Sesame-Disk/sesamefs/pull/267), based directly on
main@4f7c2992. PR #266 targets that prerequisite branch until it is merged;
merge #267 first, then retarget #266 to main. Neither PR was merged here.
The dependency was integrated without rewriting published #266 history.

The effective combined Go sources and Compose are identical to c9a4eb23b;
only gate-script ownership and documentation differ. E1-11's gate script now
contains its three own controls; the two G5 controls live in the prerequisite's
scripts/g5-coexistence-gates.sh. Existing standard integration budget 18m stays
in E1-11, where its additional mandatory matrix requires it. Prior accepted
combined go-all-test evidence remains applicable to the identical compiled
source/Compose; it is not represented as a new run on either split PR.

Post-extraction verification at e098028e9: Docker E1-11 6/6 under race PASS
(49.586s), gate inventory/completeness PASS, all three independent E1-11
negative controls PASS. Both backends CLEAN; storage quota_usage 0, hard policy
and 2,000,000,000-byte limit unchanged. Independent prerequisite G5 validation
is 9/9 under race plus its two negative controls and both vets (PR #267).
Git confirms no effective Go/Compose difference from c9a4eb23b and no G5
file/wiring changes in #266's diff against its prerequisite. The full standard
go-all-test result remains the earlier accepted identical-code run; no full
rerun is claimed here. Scope objection is resolved by separation, not by a
production fix. Merge order: #267, retarget #266 to main, then review/merge #266.

### Merge readiness after #267 — 2026-10-07

The user merged prerequisite #267 as main@308d77df68afc2090d1a090528642950b028f4e3.
PR #266 is now aligned with that main and targets main directly; the temporary
stacked-base/merge-order instructions above are historical. Its diff contains
no G5 isolation files, G5 control script, G5 documentation or new G5 wiring.
No published history was rewritten and no PR was merged by this readiness step.

Git verifies effective internal Go sources and Compose still exactly match the
accepted c9a4eb23b snapshot; alignment introduced no code change. Prior full
Docker go-all-test PASS and the post-extraction race/negative-control results
remain the tested evidence, not a newly claimed test run. Final diff/scope,
current versus historical W2-11 status, residual claims and whitespace were
rechecked. No unresolved introduced P0/P1/P2 was found. W2-11 closure remains
covered pre-D only; W2-12..14/E1/X1/activation remain OPEN, production GC OFF.

### Live discovery/observability reconciliation — audit at a1df08de1

Confirmed one P2 live-contract inconsistency: W2-13 discovery text and the
observability runbook/metrics comments still tied covered pre-D safety to
renewal cadence and a future sweep-health gate. They now distinguish the
historical W2-11 proposal from direct non-expiring repair authority. W2-13
remains OPEN for discovery/convergence/scale/completion bounds; no scheduler,
health gate or operational completion guarantee was implemented. W2-12/14,
cleanup and other PRE-GC contracts remain separate and OPEN.

Changes are Markdown and comments inside metrics.go. Docker validation parses
both versions and compares their executable Go token/literal streams excluding
comments: identical fingerprint de6acc7851001b5815b850af6e5f5f2dcc48f454db220f4f517c5b5dca7a0cab.
Names, labels, Help strings, metric constructors and all executable statements
are unchanged; all other internal Go, test sources, scripts and Compose remain
identical to the audited a1df08de1 snapshot. Documentation checks and diff --check
PASS. Raw metrics.go text differs only in comments; earlier raw-Go identity
statements describe their recorded snapshots, not this textual correction.
The recorded full go-all-test and race results are prior runs, not new executions
claimed here. Updated scoped audit has no unresolved introduced P0/P1/P2.

## Bounded three-DC diagnosis plan (2026-10-15)

Base `main@550a2257bcdfe6902bad64d9541714367ff96f1e` (#295 merged), branch
`codex/e111-three-dc-diagnosis`. Scope: the existing six E1-11 legs and their
recorded three-DC fixture-progress failure; no new safety invariant or gate closure.

1. Freeze unmodified test/runtime sources. Use a fresh private Docker project:
   Cassandra 5.0.9, NetworkTopologyStrategy RF1 in dc-na/dc-eu/dc-asia, SILO and
   a current-source GC-OFF backend. Verify all three nodes, actual/declared map,
   readiness, owned candidate and session used by the productive worker.
2. Run the existing required E1-11 matrix unchanged. Preserve failures and
   teardown evidence. Trace shareProjectionDBForTest and the observed native
   worker session separately; do not assume the withdrawn #293 diagnosis.
3. Only after reproduction, isolate one cause. A test-session configuration
   correction must preserve the real topology gate, native wire faults,
   LOCAL_QUORUM/SERIAL/EACH_QUORUM queries and existing proof/cleanup assertions.
   Do not refactor independent helpers or silently certify other E1 residuals.
4. Require the identical matrix GREEN on the same three-DC fixture, appropriate
   race repetitions, the original fail-closed gate controls and relevant one-DC
   compatibility evidence. A runtime defect would require its own bounded fix.
5. Audit source/image identity, positive versus expected-negative exits, missing
   or skipped evidence, and owned cleanup before opening the focused PR. Report
   all unrun suites and retain E1-01/X1/PRE-GC/A1 OPEN; ordinary GC stays OFF.

Hypothesis to test: E1-11 constructs its observed worker via w2EvidenceSession,
whose DatabaseConfig declares only LocalDC, omitting the configured replication
map. This is not openIntegrationProjectionDB and is not yet causal evidence.

## Three-DC reproduction and scoped correction (2026-10-15)

The original matrix **FAILS** on frozen main@550a2257b: normal PASS, five GC
legs FAIL, process exit 1. Every failed leg reports `worker did not visit owned
real candidate`; the productive worker logs `destructive topology gate rejected
the G2 handoff`, with actual dc-asia:1,dc-eu:1,dc-na:1 versus declared dc-na:1.
All six fixtures verify owned metadata/repairs/K1 cleanup. This is a reproduced
fixture-progress failure, not an X1 physical-safety violation.

**Cause.** The fixture's shareProjectionDBForTest already loads the full map.
E1-11 separately creates its observed worker DB with w2EvidenceSession, whose
minimal DatabaseConfig omits replication. The real gate therefore correctly
refuses before the candidate visit. This is distinct from the unsupported
openIntegrationProjectionDB attribution withdrawn in #293.

**Correction.** In e111GC only, wrap the same observed/faulted native session
with the fixture's validated config.Load().Database before constructing its
CassandraStore. No query, observer, proxy, timeout, consistency, assertion or
production gate is replaced. The fixture already requires config.Load before
this call, so this creates no new application/auth dependency for independent
runners. The shared helpers and other E1 consumers are unchanged. Implementation
`9d02e73d80daff4862a16f8e8bee0457bf3a01c3` adds nine test-only lines.

### Frozen execution identity and topology

- Baseline image `sesamefs-e111-3dc-red`:
  `sha256:899e2be85b638e7d6c525a243db7189943e8f74061f71126ff8bec5a99c77296`,
  plan snapshot a84bc04cc; all test/runtime source identical to the base.
- Corrected image `sesamefs-e111-3dc-green`:
  `sha256:ab4c54ea7741998e6c24749470edee1260c6f97731407eacef632f8705938da7`.
  LF-normalized e111_repair_liveness_test.go hashes:
  baseline `cd4d542a3c96f67709dfa581f744cbe93337991c023f4b571491731232804b8f`,
  corrected `f90b15131d8ebd54ae937a546f0fa87a2b96601d988bc6e69c68f2e43abc746f`.
  Unchanged w2_native_wire_test.go:
  `d22a369ca0b7150463589480823ca84805b7c535345edaa8fe4fcf58e556a842`.
- Fresh private project sesamefs-e111-3dc, no host ports; Cassandra 5.0.9,
  NetworkTopologyStrategy RF1 in dc-na/dc-eu/dc-asia, all three UN/healthy.
  The dedicated sesamefs_e19 keyspace's actual system_schema map matches the
  explicit CASSANDRA_REPLICATION_DCS declaration. SERIAL and EACH_QUORUM remain real.
- SILO RELEASE.2026-09-16T00-00-00Z image
  `sha256:635197cb9f36d01bee221d34d1c7d7960f6a95c48b0b6c01d99cd13bdae51a46`;
  private owned buckets/objects, no shared stack. Backend built from base source,
  binary SHA256 `f95399fbbcf07e5f853c04067839247222280b1bf02511e0142bfb95177cc6ab`.
  Backend GC OFF; the matrix verifies all supplied API URLs are GC-OFF.
  API/seed backend uses dc-na; test native/verification sessions use dc-na,
  then dc-eu in the separate EU run. This is healthy multi-DC topology evidence,
  not a divergent-replica, outage or every-coordinator proof.
- One-DC compatibility reuses private sesamefs-e101h Cassandra/SILO volumes,
  datacenter1 RF1, with a backend built from the same current source and GC OFF.
  Only the E1-11 gate is required for its directed selector.

### Results and failure accounting

Ignored logs and explicit exits in tmp/:

| Log | Result |
|---|---|
| e111-3dc-red.log | Original matrix 1/6 PASS, 5 FAIL; six verified teardowns, exit 1. No SKIPs. |
| e111-3dc-green.log | Same six legs PASS, six verified teardowns, exit 0; gate inventory and no-global-fanout guards PASS. |
| e111-3dc-race.log | Corrected matrix -race -count=2, 12/12 legs and 12 teardowns PASS, exit 0. |
| e111-3dc-eu.log | Full six-leg matrix through dc-eu PASS, six teardowns, exit 0. |
| e111-3dc-negatives.log | Original filtered/unavailable/child-filtered gate controls PASS. Original omission mutation produces exact COMMITTED D(P1), expected test exit 1 and verified disposal. Deliberately declaring only dc-na:1 still triggers the real topology rejection and expected test exit 1, with cleanup. Wrapper exit 0. |
| e111-one-dc.log | Six legs/cleanups PASS, but package exit 1 because Compose inherited unrelated mandatory gates. This run is not an overall PASS. |
| e111-one-dc-scoped.log | Corrected launch unsets unrelated SESAMEFS_REQUIRE_* and requires E1-11 alone: 6/6 legs/teardowns PASS, package/runner exit 0. |
| e111-unit-vet.log | Complete short Go tree, ordinary vet and integration vet PASS, wrapper exit 0. |

The early fixture-start attempts failed before tests (OnlyOffice configuration
required a secret; keyspace absent). OnlyOffice was disabled for this fixture and
the explicit three-DC keyspace was created before migration; no product code
changed to fix startup. Native classifier/guard losses remain actual driver
errors and are positively checked by E1-11; no unavailable-storage SKIP counted.

After all three-DC runs, independent EACH_QUORUM counts of recovery roots,
orphans and publication repairs are zero. The mutation restores its source in
the disposable runner; publication_liveness.go matches the immutable green
image byte-for-byte. No mutation enters the PR. TestMain's pre-existing partial
cleanup-DB declaration still emits a topology warning; it is not the corrected
worker session and no general harness repair is claimed.

### Reproduction and final scope

With an isolated GC-OFF API, SILO, migrated test keyspace and explicit complete
Cassandra topology configured as above (see [testing](./TESTING.md)):

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E111_REPAIR_LIVENESS_EVIDENCE=1
unset SESAMEFS_E111_ISOLATED_URL
go test -tags integration -v -count=1 -timeout 8m -run '^(TestCurrentRuntimeRepairLiveness|TestCurrentRuntimeRepairLivenessCompleteness)$' ./internal/integration
```

Run inside the built Docker image. Each supplied SESAMEFS_URL must point to the
isolated backend and CASSANDRA_KEYSPACE to its migrated keyspace; set all hosts,
local DC and replication map explicitly. Repeat with -race -count=2; the EU run
changes only CASSANDRA_HOSTS/LOCAL_DC. Gate/mutation scripts are the existing
scripts/e111-repair-liveness-gates.sh and scripts/e111-repair-liveness-mutation.sh,
run sequentially in a disposable container with their required E19 hostname.

Not run: full standard integration/API/OIDC, standalone Sync/frontend or the
other six historical E1 three-DC cases. No runtime/schema/API/default/dependency
change; no complete three-DC suite, E1-01/X1/PRE-GC/A1 closure or activation claim.
The remaining six fixture-progress cases retain their open diagnosis/evidence
obligations. PREPARED-without-projection remains pending as recorded by #295.
Final scoped audit: no introduced blocker identified. All existing six leg
assertions, native fault paths, required gates, observers and cleanup controls
remain unchanged. Production code, the shared helper, scripts, Compose and
configuration defaults have no diff. The only Go change is the nine-line local
E1-11 adapter correction; all remaining edits are the frozen plan/evidence and
tracking summaries. Relative links (245), new evidence anchor, source/image
hashes and git diff --check PASS. The original configuration failure and
mis-scoped one-DC exit remain disclosed; no skipped leg is credited as evidence.
Backward compatible: yes, supported runtime contracts/configurations are unchanged.

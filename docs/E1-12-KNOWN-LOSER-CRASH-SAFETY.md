# E1-12 / W2-12 — definitive loser crash safety

Frozen base: main@2a6eb32fd1a5a6a0e71ddd18ddc0a2fedfaad0eb (#266 merged).

Current update (2026-10-15): the [three-DC validation below](#three-dc-validation-on-current-main-2026-10-15)
uses current main after #296. Earlier UNKNOWN/retained-loser statements describe
their frozen source; current crash-restart derives SUPERSEDED since E1-15E and
settles only the repair's own row/pub:. This update changes no gate disposition.

## Contract

Characterize one Office/CreateFile, plaintext single-block attempt on Cassandra/SILO.
Obtain actual HEAD CAS applied=false, stop before request-local cleanup, SIGKILL
its Linux writer process, and restart the productive repair sweep in a new process.
At natural up:/pub: TTL expiry, measure exact P1/K1, HEAD/tree/commit/fs, repair,
refs, candidate, released claim, lifecycle and recovery root. No fabricated repair,
CAS result, physical life, HEAD or D. TTL/repair scheduling control is explicit.

## Sequence and acceptance

1. Normal publication and definitive-loser normal-cleanup controls.
2. Writer pauses before HEAD; an independent empty-file publication wins HEAD.
3. Resume writer to actual applied=false; integration-only observation pauses
   immediately before cleanup. Parent verifies exact attempt, then OS SIGKILL.
4. Confirm durable repair and artifacts survived while competitor HEAD stays exact.
5. Shorten existing real temporary references through the reference API and await
   actual Cassandra expiry. Confirm zero real references and non-expiring repair.
6. Run productive owned-candidate GC before repair can renew pub:. Require direct
   EACH_QUORUM repair guard, no D/root, claim released, candidate/P1/K1 intact.
7. A new process runs the actual bucket sweep, not a direct known-row visit.
   Require native classifier UNKNOWN, retained repair, no fs promotion and
   unchanged competitor HEAD/tree/P1. Repeat recovery to establish stable retention.
8. Guard-only omission mutation must reach exact COMMITTED, finish owned TERMINAL
   recovery and independently verify cleanup. This proves guard necessity, not a
   complete post-D X1 violation.
9. Completeness/unavailable/filtered-child negatives, race repetitions, vet,
   existing W2/G4/G5 gates and standard Docker go-all-test, sequentially.
10. Audit final source/docs/diff and cleanup/quota, commit/push and create PR.

## Disposition and exclusions

GREEN closes only the measured Office definitive-loser crash pre-D safety subset.
W2-12 overall stays OPEN: no durable loser witness or eventual cleanup authority
is supplied. W2-13/14, concurrent cleanup/Paxos, other funnels, Phase5/6, X1,
PRE-GC/A1 and activation stay OPEN. No speculative runtime protocol fix. If actual
runtime produces RED, preserve evidence and fix only the reproduced funnel.
Standard development GC configuration stays unchanged; use existing e19 isolation.
Tests must own and clean all fixtures; quota policy/limits must not be relaxed.

## Evidence

Initial Docker matrix: six legs PASS; productive isolated child 20.48s, parent
24.729s including TestMain teardown. This is initial evidence, not final gates.
At this initial snapshot race repetitions, omission, negative controls and full go-all-test were still pending. Final verification below supersedes that status.

The clean-loser control lets normal cleanup run and then permits the handler's
ordinary retry to succeed; it checks that the first attempt's repair/pub are gone.
Crash controls kill the real writer after the observed definitive conflict and
before cleanup. Recovery runs the actual bucket sweep in a new test process.
Only created_at/lease_expires_at of the existing real repair are aged/reset for
scheduling; no classification or destructive authority is manufactured. Before
GC, up:/pub: are shortened through AddBlockReference and naturally expire; before
that expiry the test rejects any unexpected permanent reference. These are harness
controls, not a production TTL policy or a time-to-discovery guarantee.

Shared E1-11 helpers execute the exact GC probe and independent owned teardown;
their log labels remain E1-11. Their omission reaches exact COMMITTED and completes
productive TERMINAL recovery before reporting RED. E1-12 does not label that
omission alone as a complete X1 post-D publication counterexample.
## Harness audit corrections

The first omission run failed at an eager guard assertion before productive GC;
that failure is rejected as COMMITTED evidence. The harness now verifies the
actual zero-ref boundary independently, then measures the guard through the real
worker. The corrected omission reached exact COMMITTED, completed productive
TERMINAL recovery and verified absence of owned metadata/repair/K1 (7.561s).
Production guard source is restored by the script trap; no host mutation occurs.

The cleanup control now explicitly observes the original pub:<commitID> before
CAS, and checks its absence after normal loser cleanup. Repair-owned renewal is
separately identified as pub:<repo:commit:fsID>. The normal retry must settle;
fresh-process sweeps must renew that exact repair-owned pub. Final verification
is rerun after these assertion corrections, not inferred from earlier PASS.
## Positive exclusion proof and current disposition

For this actual failed Office attempt the competitor HEAD/root stays exact and
does not publish the losing file. The writer has terminated, so its request-local
cleanup cannot race our recovery. `UpdateLibraryHead` returns the definitive
conflict on a successful MapScanCAS response with applied=false; the observation
is reached only after that conflict, never by substituting the CAS result.

The non-expiring row contains this exact canonical block and persists after all
real refs expire. `BlockPublicationLivenessGlobal` reads real refs globally and
then directly scans the pending repair domain at EACH_QUORUM. A matching row is
REPAIR_GUARD_ONLY, which releases the held claim and preserves the candidate;
the productive probe observes that exact path. Omission of only this scan reaches
exact COMMITTED with matching P/class/key/claim/orphan/root: the guard is necessary.

A new repair process runs the native resumable classifier through the productive
bucket sweep. Exhausting the current HEAD ancestry remains UNKNOWN because there
is no durable negative witness. UNKNOWN renews its own pub: and retains the row;
settlement does not promote fs: or write HEAD. Two new processes demonstrate both
initial discovery and stable post-progress retention. Lease resets are scheduling
controls, not a discovery SLA or additional cleanup authority.

Disposition: the measured Office definitive-loser crash pre-D safety subset is
CLOSED-EVIDENCE. W2-12 overall remains OPEN for durable loser classification,
cleanup/convergence and retention debt; this PR intentionally supplies no witness.
The omission is guard necessity evidence, not a complete post-D X1 violation.
No production protocol/query/TTL/health-gate change; only the observational Office
hook, compiled as a no-op outside integration. Other funnels, concurrent cleanup,
W2-13/14, Phase5/6, E1/X1, PRE-GC/A1 and activation stay OPEN.

## Pre-expiry-cleanup verification (historical snapshot 6cdca06e1)

- Docker race: 18/18 measured legs across three independent child runs PASS,
  89.498s including parent/child teardown. Completeness and gate inventory PASS.
- Ordinary and integration vets PASS on the audited source.
- Three E1-12 filtered/unavailable/filtered-child negative controls PASS.
- Guard omission: exact productive COMMITTED, own TERMINAL recovery and
  independently verified cleanup PASS (7.413s); source restored afterward.
- Four existing W2 closure negatives PASS. Existing E1-11 three negatives and G5
  two negatives PASS in the preceding sequential validation.
- E1-12 file SHA-256 in the full-test image matches the audited working tree:
  9f92c052da8466596d2289c53db13def3efbffb4c4be02021ffa34d14f73d6a5.
- Standard Docker go-all-test PASS on that snapshot: integration 593.530s, API 20/20 suites and OIDC 25/25. This precedes the auxiliary-expiry cleanup fix and is not the final corrected-source run.

Every counted leg records evidence after the independent owned teardown. That
check verifies blocks/refs/lifecycle/orphans, commits/fs/index/repair and exact K1
absence; normal fixtures use their own org and do not consume shared user quota.
No concurrent integration job or stale cleanup is run during go-all-test.
Source audit: all five changed Go files in the running full-suite container match
the audited host bytes. The dedicated race/gate container differs only in gofmt
formatting/trailing newline of the two barrier files; their canonical gofmt
output matches the host exactly. No executable source difference is hidden by the
image reuse. Shared standard development GC configuration is unchanged by diff.
## Final cleanup audit correction

A direct post-teardown Cassandra read found one gc_provisional_block_refs row
for a completed crash fixture, with its non-expiring by-day projection. The child
writer cannot populate its parent's request-local uploadRefs, so the existing
fixture helper omitted this auxiliary cleanup. This introduced P2 TEST-INFRA is
corrected locally: teardown enumerates only the owned org/block tracker partition,
uses DeleteProvisionalBlockReferenceExpiry for every exact recorded coordinate,
and an independent later check requires both canonical and exact by-day rows absent.
The omission script additionally requires that affirmative expiry-cleanup marker
and rejects its failures. Production protocol and shared cleanup remain unchanged.

The preceding 593.530s full-suite PASS did not check these auxiliary rows, so it
is historical rather than sufficient final evidence. Final race/gates/mutation
and full Docker go-all-test are repeated on the corrected source. Prior local runs'
owned residues are also removed through the productive helper, only for org/repo
coordinates logged by E1-12 completed teardown and with blocks/library metadata
independently absent. 36 canonical trackers and matching projections were removed;
81 finished fixture coordinates were checked. No active fixture or foreign row is
purged, and quota policies/limits are not changed.
## Corrected-source final verification (d35f40b03)

- Expanded-cleanup race matrix: 18/18 PASS, 79.870s; ordinary/integration vets,
  completeness and mandatory-gate inventory PASS.
- Three E1-12 negative controls and four W2 closure negatives PASS again.
- Guard omission PASS (9.147s): exact COMMITTED, own productive TERMINAL recovery,
  metadata/repair/K1 cleanup and both provisional tracker/projection checks.
- Final standard Docker go-all-test completed PASS on corrected source:
  integration 610.866s, API 20/20 suites, OIDC 25/25 tests. All 13 standard
  daemon-dependent controls PASS without GC-disabled skips. Existing Phase6 and
  optional topology/probe skips retain their original prerequisites; the separate
  soak-tag suite is not claimed. G4/G5 gates PASS within the standard run.
- Every final E1-12 leg confirms metadata/K1 and expiry tracker/projection cleanup.
  Post-suite cleanup checks report CLEAN on standard and isolated e19 backends;
  both organization quota_usage values are 0 with hard policy and 2,000,000,000
  limits unchanged. No unresolved introduced P0/P1/P2 found in the audited scope.
  The earlier full-suite PASS remains explicitly historical.
- Corrected E1-12 Go file SHA-256:
  e6c35778ed1f957d5812bfa1102a693a815eb53f5813919f49a54cebf457c3bc.

No production runtime behavior changed between these verification snapshots;
the new correction extends test teardown and its independent validation only.
GC schedule controls: the existing store helper ensures/enqueues the real owned
canonical candidate with an aged eligibility timestamp; the productive worker has
zero test grace and dequeue discovery is narrowed to that candidate. Claims,
EACH_QUORUM proof, lifecycle/orphan/root publication and physical recovery remain
productive. This does not demonstrate automatic candidate creation at pub: expiry
(W2-14), discovery latency, production grace policy or background scheduling.

## Bounded three-DC validation plan (2026-10-15)

Base `main@a5adc93e3a49185fc4ec5369c26ffa17354f6be7` (#296 merged), branch
`codex/e112-three-dc-evidence`. Scope: the existing six E1-12 legs, with the
inherited e111GC topology correction already present. No new fix is presumed.

1. Freeze current source/image and verify the private Cassandra 5.0.9/SILO
   fixture, actual and declared NetworkTopologyStrategy RF1 map across
   dc-na/dc-eu/dc-asia, all-node readiness and GC-OFF API. Reuse only the owned
   #296 fixture after independent zero-root/orphan/repair checks; retain volumes.
2. Require the existing E1-12 six-leg gate on unmodified current code. Inspect
   real applied=false, verified writer SIGKILL, TTL expiry, exact worker guard,
   separate-process sweep and current SUPERSEDED outcome where applicable.
   Retain metadata/K1 and expiry-tracker/projection cleanup assertions.
3. Run race repetitions and a separate EU-coordinator matrix sequentially.
   Run the original filtered/unavailable/child-filtered gate negatives and
   pending-repair omission mutation only in a disposable container copy; verify
   its exact COMMITTED certificate, owned cleanup and source restoration.
4. If current code fails, preserve the concrete failure and isolate its cause
   before any bounded correction. If it passes, publish evidence/tracking only;
   do not add tests, a generic harness refactor or speculative product changes.
5. Audit commands/exits, skipped evidence, source/topology identity, cleanup and
   links before the focused PR. Report unrun suites. Other E1 cases, E1-01/X1,
   PRE-GC/A1 and GC activation dispositions remain separate and OPEN.

## Three-DC validation on current main (2026-10-15)

Current code passes the existing six E1-12 legs without modification. The two
GC legs already call e111GC and inherit #296's complete topology declaration.
This validates current fixture progress; it is not a newly reproduced runtime
bug, a separate E1-12 fix or proof of the exact cause of every historical run.

### Frozen source, fixture and command

Base `main@a5adc93e3a49185fc4ec5369c26ffa17354f6be7`; image source snapshot
`11233a54d` adds only the frozen plan. Image `sesamefs-e112-3dc-current`:
`sha256:731c7584873acee078b8c6d3543e731b7217114d3590c08f3fe9a40dc1353613`.
LF-normalized Go hashes:

| Source | SHA256 |
|---|---|
| e112_known_loser_test.go | 5b045290199b7d023e3ce5d6e1112aeb96acfe0292470e3baf84f16ede70d397 |
| e111_repair_liveness_test.go | f90b15131d8ebd54ae937a546f0fa87a2b96601d988bc6e69c68f2e43abc746f |
| w2_native_wire_test.go | d22a369ca0b7150463589480823ca84805b7c535345edaa8fe4fcf58e556a842 |

The current-source backend binary SHA256 is
`f95399fbbcf07e5f853c04067839247222280b1bf02511e0142bfb95177cc6ab`.
All non-Markdown files match the base; no test, helper, script or runtime change.

Private owned project sesamefs-e111-3dc reuses #296's retained volumes, with no
host ports. Before this run, independent EACH_QUORUM counts of roots, orphans
and repairs were zero. Cassandra 5.0.9 has three healthy UN nodes; actual and
declared NetworkTopologyStrategy maps are dc-na:1,dc-eu:1,dc-asia:1 on the dedicated
sesamefs_e19 keyspace. SILO RELEASE.2026-09-16T00-00-00Z image remains
`sha256:635197cb9f36d01bee221d34d1c7d7960f6a95c48b0b6c01d99cd13bdae51a46`.
The new runner/backend use current source and GC OFF; all supplied API URLs
resolve to this one isolated backend. The matrix checks its GC-OFF condition.

The API backend coordinates through dc-na. The initial test process, writer,
worker and recovery use dc-na; the separate EU run moves the native test
sessions to dc-eu while keeping that API backend. There is no divergence or
partition injection, no separate API replica per DC and no every-coordinator claim.
Existing TestMain cleanup-DB topology warnings remain outside this measurement.

With this isolated, migrated Docker fixture and complete hosts/local-DC/map
configuration (see [testing](./TESTING.md)), run inside the built runner:

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E112_KNOWN_LOSER_EVIDENCE=1
unset SESAMEFS_E112_ISOLATED_URL
go test -tags integration -v -count=1 -timeout 8m -run '^(TestKnownLoserCrashSafety|TestKnownLoserCrashSafetyCompleteness)$' ./internal/integration
```

The actual initial command also includes TestEveryEvidenceGateIsWiredIntoTestMain
and TestNoGlobalGCFanoutInIntegrationSuite. Race repeats use -race -count=2;
EU changes CASSANDRA_HOSTS/LOCAL_DC to the EU node/DC. Evidence selectors do not
relax the original required E1-12 gate or source assertions.
### Results, cleanup and qualifications

All logs are ignored local tmp/ artifacts; each named execution has its own
explicit .exit file. Source and assertions are unchanged throughout.

| Log | Result |
|---|---|
| e112-3dc-current.log | Six legs PASS, four verified writer SIGKILLs, one new-process SUPERSEDED sweep; six metadata/K1 and six expiry-tracker/projection cleanup checks; gate/fanout guards PASS; exit 0. |
| e112-3dc-race.log | -race -count=2: 12/12 legs PASS, eight verified SIGKILLs, two new-process sweeps, 12 metadata/K1 and 12 expiry cleanups; exit 0. |
| e112-3dc-eu.log | Six legs PASS through dc-eu, four SIGKILLs, one new-process sweep and six of each cleanup check; exit 0. |
| e112-3dc-negatives.log | Existing filtered, unavailable and child-filtered gate controls PASS. Existing crash-gc omission mutation produces exact COMMITTED D(P1), expected nonzero test result and verified metadata/K1 plus expiry cleanup. Wrapper exit 0. |

No positive leg failed or skipped. The omission's COMMITTED certificate exists
only in the deliberately mutated disposable test copy; it is not a defect
reproduced on current main. The mutation restores publication_liveness.go;
its raw SHA256 matches the immutable runner image after execution. Original
scripts/e112-known-loser-gates.sh and scripts/e112-known-loser-mutation.sh run
sequentially with their E19 hostname resolving to the isolated backend.

The successful crash-gc/restart legs observe real zero references, an
EACH_QUORUM pending-repair scan, released claim and retained candidate/P1/K1
without D/root. Four crash legs verify OS SIGKILL after actual applied=false
before request-local cleanup. Crash-restart's independent bucket sweep derives
SUPERSEDED from the competitor's canonical HEAD chain, removes only the repair
and its own pub:, preserves both commits/roots and original P1/K1, and installs
no fs: for the losing attempt. The older UNKNOWN retention paragraphs above
remain their original frozen-version evidence, not this current result.

Independent final EACH_QUORUM counts of roots, orphans and repairs are zero.
Every E1-12 owned fixture verifies metadata/K1 and its exact expiry coordinates.
This is not an empty-bucket claim: final object listing also finds auxiliary
12 verified 41-byte discoverStorageClass probes under the shared test organization, from
this run and #296. Their discover.txt contents identify the probe origin; the
helper deletes its library by API but does not directly remove its physical
object with background GC OFF. These probes are outside the isolated E1-12
P1/K1 assertions and remain in the retained private volumes. No auxiliary
cleanup change or independent retention gate disposition is proposed here.

Not run: full Go/vet, full standard integration/API/OIDC, standalone Sync/frontend,
a new one-DC suite, the other E1 three-DC cases, or a divergent-replica/outage
schedule. No prior full-suite/race result above is relabelled as a new run.
This PR adds only documentation: current E1-12 no longer reproduces its recorded
fixture-progress failure in the measured healthy topology after #296. E1-3,
E1-7, E1-09, E1-10e and E1-13 remain pending. The umbrella P2, E1-01/X1 and
PRE-GC/A1 stay OPEN; ordinary/production GC remains OFF. The internal
PREPARED-without-projection qualification from #295 is unchanged.
Final scoped audit: no introduced blocker identified. All non-Markdown files
match main@a5adc93e3; original gates, mutations, source assertions and cleanup
helpers are unchanged. Source/image hashes, 249 relative file links, the new
section anchor and git diff --check PASS. Successful exit summaries require all
named legs and both kinds of owned cleanup; no SKIP is credited as evidence.
Twelve retained auxiliary probes were individually checked by content and are
explicitly excluded from any empty-bucket claim. No runtime fix, new test or
general three-DC suite result is inferred. Backward compatible: yes, docs only.

# CURRENT_WORK.md history (archived 2026-10-12)

**Why archived:** `CURRENT_WORK.md` had grown to 1,973 lines. Only its top (the active and
recent E1 slices) and the gates table were still maintained. Everything below was moved here
with evidence text preserved and relative links repaired so it can still be consulted. Several statements here are no longer true: for example,
the "Frozen Components" list (most of those files have changed since), "GC ✅ DONE" in the
Strategic Roadmap (destructive GC is still gated by X1 and PRE-GC/A1) and the port 8082 in
Quick Commands (it is 8080 by default).

**Source:** `CURRENT_WORK.md` at commit `5c3163871`, lines 528–1335 and 1356–1973.
**Current status lives in:** `CURRENT_WORK.md`, `docs/OPEN-WORK-INDEX.md`,
`docs/KNOWN_ISSUES.md`, `docs/X1-CRITICAL-PATH.md`, `docs/CHANGELOG.md`.

---

**Merged PR #234, `fix/library-hard-delete-lease-global-serial` (rebased onto `main@cd591709c`, 2026-09-30; originally 2026-09-26):**
Every hard-delete lease LWT (library, user and org) now pins global `SERIAL`
for acquire (including stale takeover), renew, and conditional release,
independent of a session default of `LOCAL_SERIAL`. The post-rebase audit found
that global SERIAL contention between DCs can return `CAS_WRITE_UNKNOWN` or a
CAS write timeout for an acquire (6/41 concurrent races on the 3-DC fixture);
the caller returned without releasing and a later Paxos round could still
commit the proposal, stranding the lease until stale takeover (~90 min). Each
acquire entry point now makes a best-effort own-token release on that outcome
and still returns the error; a failed release is reported as such, and the
remaining single-owner liveness residual is recorded in the known issue. The
lease token, TTL, stale takeover, renewal and release semantics are otherwise
unchanged; no caller flow changed. Residual:
renew/release can also see an unknown outcome under contention. Renew fails
closed before the final mutation; the deferred release then only attempts to
drop the lease. API restore/permanent delete log a failed release, while the GC
cascades record it inside `hardDeleteLease` after their last `Check()` (and the
child-item guard discards it), so it is not surfaced. Either way the lease may
remain until stale takeover or TTL. A bounded retry is a liveness follow-up
documented in the known issue. The cross-audit also registered the pre-existing
`ISSUE-GC-USER-HARD-DELETE-RESTORE-SERIALIZATION-01` (P1, PRE-GC / A1; not
fixed here).

Evidence (all in Docker): `go test ./... -count=1`, `go vet ./...` and
`go test -race -short ./...` pass; seven directed mutations are RED for their own
reason; the isolated 3-DC harness passes with `LOCAL_SERIAL` sessions.
Final run after the rebase onto `main@cd591709c` (2026-09-30): 60/60 pinned
3-DC races, official harness 2/2 on fresh fixtures (one run settled an
ambiguous acquire); downgrading the pin to `LOCAL_SERIAL` gives two owners in
16/20 runs. The harnesses now scope their backend, fixture and runner names to
this checkout's compose project and never modify the host tree.
`ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01` is closed. The separate P1
non-fencing finding remains a FOLLOW-UP; this change does not make a broader GC
readiness claim. Per [docs/X1-CRITICAL-PATH.md](../X1-CRITICAL-PATH.md) §7 this
closes the first of the two current-runtime lease items (the global SERIAL
pin); the generation-fenced final batch (`ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01`)
remains open in both §6 and §7. `GC_ENABLED=false` remains mandatory.

### PR #239 crossed audit correction (2026-09-30)

Pre-D GC now distinguishes real references, repair-only protection and zero.
Repair-only releases the exact claim and preserves candidate/discovery/queue,
postponing without retry. After COMMITTED, only actual references retain the
existing contradiction policy; a late repair cannot veto D. Both worker
regressions are included in 17 mandatory continuity legs. G2/G3 retirement and
durable physical continuation are proved; the future physical executor remains
outside this PR. W2-0/W2-6a remain OPEN and GC_ENABLED=false.
See [crossed audit evidence](../W2-0-PUBLICATION-CONTINUITY.md).

## Merged PR #239: fix/w2-0-publish-liveness-through-head (base main@50c50903e, 2026-09-29)

#239 added a fail-closed GC guard using the existing non-expiring
`published_block_reference_repairs` rows. Acquisition explicitly writes at
LOCAL_QUORUM; destructive readers scan all 32 organization bucket prefixes at
EACH_QUORUM, consume all pages, and repeat the reference probe after a negative
repair scan. Settled GC claims use the existing EACH_QUORUM/global SERIAL domain.
F1 Office, F2 UploadFile and F3 validate exact P after repair acquisition; Sync
now retains its provenanced placements and validates them again after queueing.

W2-0 and W2-6a remain OPEN. The stage-to-repair gap still exists, but the final
check rejects HEAD if GC won there. After acquisition, the non-TTL repair guards
against expiry after validation. Mixed deployments with older destructive
readers do not have this protection. UNKNOWN/unreachable records retain the
existing R31 conservative behavior and can inhibit collection indefinitely.
The cold path adds up to 32 organization range reads plus a final ref probe.

This is a useful partial correction. UNKNOWN settlement is tested through the
existing classifier seam; process death is modeled by an abrupt panic, with
real durable rows and discovery-worker recovery. Neither is a wire-level HEAD
ambiguity or an OS process-kill experiment. Full closure still requires those
experiments and rollout evidence. Historical W2-1/2/6 claims are not upgraded.
See [plan, evidence and limits](../W2-0-PUBLICATION-CONTINUITY.md).

**Previous branch - W2-6a CreateFile Office-template exact placement (`fix/w2-6a-createfile-office-exact-p`, base `main@63aa84573`, 2026-09-29):**
Scoped follow-up after merged #237. `CreateFile` carries the actual materialized
SHA-256/class/key to the existing final exact-P validator after durable `pub:`
and immediately before HEAD. Real Cassandra/MinIO RED: all six GC races (two
for each Office extension) returned 201 and advanced HEAD. GREEN after the fix:
409, HEAD unchanged, `pub:` cleaned, D unrevoked or fully retired P still absent.
The nine Office legs also cover fresh/reused templates; empty CreateFile remains
blockless. Plan and evidence: [docs/W2-6A-CREATEFILE-EXACT-P.md](../W2-6A-CREATEFILE-EXACT-P.md).
Cross-audit correction: W2-6a remains `OPEN`. The fix rejects GC that wins before
exact-P validation; TTL-bound `pub:` (35d) does not prove continuity until HEAD.
W2-0 remains OPEN even for adopting callers; historical closure claims require
re-audit. R31, PC-D1B.5, G4 and PRE-GC remain unchanged.
`GC_ENABLED=false` remains mandatory in production. Directed Docker races use
a local override disabling GC on all three nodes; the full development suite
uses the documented primary-node worker/scanner exception for its scanner test.

**Merged PR #237 - W2-6 UploadFile exact placement (`fix/w2-6-uploadfile-exact-p`, base `main@163a8c60b`, 2026-09-29):**
First W2 exit row after the X1 reset. `UploadFile` now passes its materialized
exact placement to the shared finalizer, so `pub:` is staged and the placement
is re-validated immediately before HEAD (the W2-0 shared mechanism, already
used by CreateFileFromBlocks). Demonstrated RED first on real Cassandra (GC
committed / fully retired the placement while the own `up:` had lapsed → HEAD
published anyway), GREEN after the fix (409, HEAD unchanged, `pub:` dropped).
W2-6 `CLOSED-FIX` pre-HEAD. The PR #237 cross-audit found the existing
`CreateFile` Office-template funnel missing from the canonical W2 checklist;
it is now tracked as W2-6a and remains `OPEN` (the empty-file path has no
blocks). That is the next scoped W2 row, followed by SeafHTTP and OnlyOffice.
`GC_ENABLED=false` remains mandatory.

**Merged #236 — X1 critical-path reset (docs only, `docs/x1-reset-critical-path`, base `main@a5dea859a`, 2026-09-29):**
[docs/X1-CRITICAL-PATH.md](../X1-CRITICAL-PATH.md) is now the source of
record for the order of work toward X1. It restores the #201 roadmap
(W2 exit checklist → G4 → G5 → E1 → X1 CLOSED → PRE-GC list → A1) and
supersedes the "PC-D1B.5 → productive consumer → PC-2" ordering written in
the entries below, which are kept as history. PC-D1B.5 is reclassified
PRE-GC (the only destroyers that can falsify a witness, D1–D3, are GC-only);
the PublicationCoordinator and the PC-D1 line are frozen, and PC-2 is not an
X1 prerequisite. It also adds the finding classification rule (current code +
current runtime / current code + planned GC activation / code that does not
exist) and reclassifies both library hard-delete lease issues as
CURRENT-RUNTIME (the SERIAL-domain one config-dependent). X1 CLOSED does not
authorize GC: activation also needs the PRE-GC / A1 gate (every open
PRE-GC/PRE-ACTIVATION-tagged entry plus §6, including Phase 5 and the Phase 6
execute-time TOCTOU). W2 rows close only as CLOSED-EVIDENCE, CLOSED-FIX or
CLOSED-GATED. No runtime, schema, CQL, guard or mutation-suite change.
`GC_ENABLED=false` remains mandatory.

**Merged PR #233, PC-D1B.3 Mapping Authority (rebased onto `origin/main@cc57cd2dc`, 2026-09-25):**
This branch adds the write-once, byte-proven mapping authority and cold-path
promotion. Productive mapping resolution pins `LOCAL_QUORUM`; the upload
read-before-write check inherits the configured session consistency. The source
guard inventories both readers and each Query/Batch.Bind CQL callsite, fails
closed on Query/Bind method values, BatchEntry and direct Batch.Entries writes,
and rejects ambiguous CQL control flow. Dynamic-query table markers are tied to
the exact argument reaching the callsite. The hot-path inventory includes the
real upload pre-check; projection freeze has exactly one caller under
`blockMappingPromotionPorts`. The 2026-09-26 cross-audit closes all eight
reported findings with source-guard and evidence changes: promotion factory and
helper aliases, integration build-tag pinning, execution-site claim-table CQL,
non-DB receiver methods, method identity, driver timestamp setters, and
consistency setter overrides. Production Mapping Authority behavior is
unchanged.

Docker verification is complete: 67 directed source mutations plus real-
Cassandra T1 were expected RED (68/68); `go test ./... -count=1`, `go vet
./...`, and `go test -race -short -timeout 20m ./...` passed. The full
`go-integration-test` Compose profile passed, including the real-Cassandra
mapping certifier and upload-writer checks (`internal/integration`: 287.127s).
The isolated 3-DC mapping-authority harness also passed cross-DC promotion,
concurrent conflicts, outage/recovery, and global SERIAL readiness. The
recovery harness probes global SERIAL readiness after restoring the DCs before
checking certification.
The PC-D1B.4 lifecycle decision from current `main` remains intact; its runtime
and the separate GC resolver follow-up stay out of scope.
Future migrations that touch `block_id_mappings` require explicit Mapping
Authority review; migration hardening remains a follow-up outside this PR.
The paired SHA-1 compatibility projection issue remains a pre-existing
CURRENT-RUNTIME / FOLLOW-UP (`ISSUE-PCD1B-PAIRED-MAPPING-COMPATIBILITY-01`,
does not block #233). Claim/projection reconciliation remains a separate
PRE-CONSUMER follow-up (`ISSUE-PCD1B-MAPPING-CLAIM-PROJECTION-RECONCILIATION-01`,
does not block #233).
`GC_ENABLED=false` remains mandatory.

**Merged PC-D1B.4 certification-window lifecycle fence — cross-audit round 4, final Paxos race characterization (2026-09-24, `docs/pc-d1b4-certification-window-fence`, base `main@62a2c0e0`):**
Decision record: [docs/PC-D1B-CERTIFICATION-WINDOW-FENCE.md](../PC-D1B-CERTIFICATION-WINDOW-FENCE.md),
`ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01`. Only the destruction of
witness-covered state (HEAD commit, reachable fs_object, permanent `fs:`
reference) can falsify a witness; claims already forbid semantic replacement,
HEAD movement is already fenced, and soft-delete/restore/hard delete do not
change the certified dependency set. Selected fence (for PC-D1B.5): a
per-library `continuity_destruction_epoch timeuuid` +
`continuity_destruction_pending map<uuid, timeuuid>` (token -> owning
generation) + `continuity_destruction_superseded` on the canonical row, written
only by global-SERIAL LWTs. GC destroyers (D1-D3) take an intent per durable
QueueItem (fresh generation as epoch, `pending[t] = g`, witness cleared,
superseded high-water raised on takeover), issue every destructive write
`USING TIMESTAMP ts(g)`, and complete `IF pending[t] = g` after acknowledged
writes. When captured S is non-null, the certifier must reaffirm every covered
row on every attempt through a gateway-only `VerifiedReaffirmationCapability`
(full identity projection, `EACH_QUORUM`, fail closed), regardless of local
`WRITETIME`; UNKNOWN requires a global retry. The 3-DC harness proves both
that LOCAL_QUORUM loses the row outside its DC (CW-M23) and that a local-only
post-UNKNOWN state with a high timestamp must be globally reaffirmed on retry
(CW-M27; the model covers the partial/UNKNOWN transition). CW-M28 forbids
same-clock future tombstones. CW-M29 now makes cross-node time an explicit
premise: a fleet-wide lease bounds skew across every application/Cassandra
timestamp source, `safe_now = gc_local_now - Δ - 1us`, and unknown, stale or
regressed clock health blocks destructive writes while retaining P for retry.
Whole-row W covers every live regular cell, including display and reference
metadata. Real Cassandra characterizes partial-row survival and same-clock
future-tombstone poisoning (CW-M26/M28). Durable token derivation now freezes
the RFC URL namespace UUID, exact field encodings and block/commit/fs_object
CW-M30 vectors; enqueue persists effective `identity_at` and requeue preserves
it, with no producer stamping change. CW-M31 requires global `EACH_QUORUM` absence
proof for any E/P/S bypass; a local `CanonicalLibraryExists` miss never mints
authority. CW-M32 applies the clock-health lease to supported writers and
Cassandra timestamp coordinators, not only GC.
DLQ/expiry/operator paths abandon-by-takeover before an item leaves the queue;
the pending map has a backpressure cap.
Best-effort D4/D5 cleanups of commits proven never to be HEAD take a
`ProvenUncoveredCleanupCapability` and write no fence state. The certifier
captures the fence before its final revalidation, refuses a busy library and
predicates the captured epoch in the witness CAS. Witness stays `(H, V)`;
validity unchanged; productive witness authority is read only at global
SERIAL (frozen in the PC-D1 contract); no hot-path Paxos, no fan-out (all
covered state is library-scoped), no per-identity state.
Evidence: `internal/db/pcd1b4_certification_window_model_test.go` (exhaustive
interleavings: main and four weaker fences have counterexamples, the selected
fence has none, including the same-token stale-completion retry, paused
generations issuing late deletes, CW-M27 local-timestamp skip, CW-M28
same-clock future-tombstone poisoning, CW-M29 independent-clock skew, CW-M31
global-absence proof requirements, CW-M32 writer-side clock admission, and
CW-M33/CW-M34 in-flight authority models),
`internal/db/pcd1b4_inflight_authority_model_test.go` (stable-absence and
drain/recovery contracts),
`internal/db/pcd1b4_lifecycle_mutation_inventory_test.go` (source-derived
lifecycle statements and destroyer call sites), real-Cassandra
characterization `internal/integration/pcd1b4_certification_window_characterization_test.go`
(R1-R11; R3b/R4/R5/R10/R10b/R11b UNSAFE today; CW-M11/M26/M28 evidence) and
isolated 3-DC `scripts/pc-d1b4-certification-window-multidc-characterization.sh`
(R12, CW-M23/M27/M31, CW-M33 accepted-Paxos and post-barrier issuance races
with G21/G22 RED via
`internal/integration/pcd1b4multidc/stable_absence_paxos_race_test.go`, CW-M34
via `internal/integration/pcd1b4multidc/inflight_materialization_test.go`, and
G16 local-proof mutation);
`scripts/pc-d1b4-certification-window-guard-mutation-validation.sh` proves the
guards bite (G1-G15/G17-G20 plus C1 on real Cassandra); G16 is the isolated-3DC
global-to-local absence mutation; G22 removes the SERIAL-present proof veto in
the 3-DC runner. The destroyer inventory also
rejects aliases of destroyer primitives and raw `block_references` deletes.
Cross-audit rounds 2–4 close CW-M29's clock premise, CW-M31 cross-DC visibility,
CW-M33 stable absence, and CW-M34 in-flight writer lifetime. The isolated
Cassandra 5.0.9 image first proves settlement of a pre-existing accepted HEAD
proposal; G21 removes the barrier and turns RED. It also covers the issuance
window: SERIAL observes H0, then a new accepted CAS begins, hard delete and
EACH_QUORUM absence follow, and the stored SERIAL-present result vetoes proof;
G22 removing that veto turns RED. The test builder pins Cassandra binary
digest, source commit and SHA-256 for both patched sources; the stock Cassandra
and SesameFS runtime are unchanged. §14/§16 now account for SERIAL plus
EACH_QUORUM proof cost and CW-M34 lifetime-tracking/recovery costs.
CW-M34 separately confirms a healthy timestamped write can return success while
hidden under a later tombstone; G20 removes the selected writer drain and turns
RED. Both are decision/characterization contracts only. The decision is now
CLOSED / MERGEABLE; PC-D1B.5 implements runtime protection before GC activation
or a productive consumer. No productive runtime, schema, certifier, writer, GC,
mapping-authority, consumer or PC-2 change.
Registered PRE-CONSUMER `ISSUE-PCD1B-MAPPING-PROJECTION-STABILITY-01` (mutable
mapping rows vs Mapping Authority after #233). The runtime migration is the
next available number (`028` if #233 lands first).
Side findings registered: `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01` (was
`ISSUE-PCD1B4-WITNESS-GHOST-ROW-01`; now also covers the intent LWT),
`ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01`,
`ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01` (P1, CURRENT-RUNTIME / FOLLOW-UP;
also required PRE-GC; pre-existing, not a #232 blocker), and Phase 6 execute-time
TOCTOU under `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`.
`GC_ENABLED=false` keeps the GC worker disabled; it does not gate the synchronous
`PermanentDeleteRepo` API hard delete or protect the restore lifecycle race.

**Historical merged PR #230 — PC-D1B identity-authority primitive (2026-09-21, `feat/pcd1b-metadata-identity-authority-primitive`):**
PR #230 lands the commits/fs_objects identity-authority primitive authority-only,
as decided in PR #229. Migration 026 adds `identity_authority_claims` under the
full `((library_id, identity_kind, identity_id))` key, with no TTL.
`ClaimIdentityAuthority` uses `INSERT ... IF NOT EXISTS` pinned to
`LibraryHeadSerialConsistency` (global `SERIAL`); ambiguous LWT results remain
Unknown. V1 is length-delimited: commit digests bind all seven immutable stored
fields and fs_objects share one `fs_object` key with `dir`/`file` subtype in the
digest. UUID fields are canonicalized, TEXT fields remain byte-exact, and file
digests bind ordered logical SHA-1 and canonical SHA-256 lists. Evidence includes
unit/contract coverage, 26/26 directed mutations RED for their specific reason
(UUID/digest canonicality, semantic UPDATE fields, repo-wide claim immutability,
claim lifecycle, SERIAL, inventory and authority-only scope), real single-node
Cassandra, and the isolated 3-DC winner/delete-recreate legs. The source-derived
inventory currently accounts for all 24 observed commits/fs_objects writer and
deleter declarations; it is a foundation, not the final no-bypass fence. No
production caller is wired, the certifier gate (#228, M14-M15) is not in this
PR, mapping-authority representation and M18/M19 promotion remain separate
follow-up work, and `GC_ENABLED=false`.

**PC-D1B.2 metadata identity-authority wiring (2026-09-21, `codex/pcd1b-identity-authority-wiring`):**
The follow-up wiring is implemented on top of merged PR #230. Every semantic
`commits` / `fs_objects` producer now obtains a typed gateway capability before
materialization; library initializers retain their existing `LoggedBatch` by
adding immutable authorized projections to it. `PutCommit` deliberately uses the
complete V1 retry identity (parent, root, creator, description and server-owned
millisecond `created_at`), so same parent/root with a changed creator or
description is a conflict. A pre-read recovers a durable claim timestamp after a
claim/source crash; a CAS Conflict authorizes nothing until an exact second claim
is Idempotent. Paired canonical files preserve `block_ids=SHA-256` and
`seafile_block_ids_sha1=SHA-1`; a matching authoritative paired row is accepted
by Sync, while a SHA-1-only replacement conflicts. Existing source rows are
verified against the claim before an authorized token is returned; divergent
rows fail closed. Individual deletes verify authority and leave claims intact;
whole unpublished-library rollback uses its existing HEAD authority and deletes
only source partitions. The repo-wide fence confines semantic CQL to the gateway
and leaves only the exact display-only `{obj_name, full_path, mtime}` updates.
The measured gateway contract is recorded in the ADR: a new commit costs one
SERIAL claim read, one global-SERIAL claim, one source verification read and
one LoggedBatch; an exact commit retry has no LWT; a new fs_object has no claim
pre-read, one global-SERIAL claim, one source verification read and one
LoggedBatch; an exact fs_object retry repeats its exact claim LWT and source
materialization, while mixed-funnel SHA-1-only compatibility performs the
paired conflict plus an exact SHA-1-only re-claim before the same source check.
Docker evidence includes unit/contract tests, the mutation runner, single-node
gateway crash/delete/recreate legs, measured observer output and isolated 3-DC
LOCAL_SERIAL-session/global-SERIAL-authority races.
This remains a wiring PR: #228 M14/M15, mapping authority/M18-M19, certification-window fencing,
productive consumers, claim retirement, historical backfill and GC activation
remain out of scope; `GC_ENABLED=false`.

**PR #231 re-audit closure (2026-09-22):**
The consolidated re-audit findings are closed. Cassandra MapScan typed-nil lists are treated as NULL by the shared fs_objects identity readers, non-nil empty lists remain explicit, metadata-only placeholders can be completed, and the zero-block file shape is verified against its durable claim. Real-Cassandra integration coverage now includes SHA1-only exact RecvFS replay, placeholder completion, directory create/retry/delete with the claim retained, and gateway deletes for directory, SHA1-only file and zero-block file identities. The source inventory rejects projection access outside the gateway and the mutation runner verifies B22 projection access, B23 strings.Join CQL and B24 helper-returned CQL all turn RED, alongside the existing M16/M17 and B1-B21 mutations.

Final Docker evidence: the full go-integration-test profile passed (313.641s), go test ./... -short -cover passed, and scripts/pcd1b-identity-authority-mutation-validation.sh passed. The standard local stack does not supply isolated 3-DC host variables, so the 3-DC cases that require them are reported as skips by that run; this closure records the single-node full-profile result and does not add new 3-DC claims. No failing integration test remained to attribute to main. #228 M14-M15 and mapping-authority/M18-M19 work remain separate; GC_ENABLED=false.

**PR #228 certifier identity-authority closure (2026-09-22, branch rebased on M231 `67ec2f76b3729719ac369310aac430d4012174c5`):** the cold-path certifier now verifies the captured commit projection and every reachable fs_object against the durable global-SERIAL identity claims written by #230/#231. It reads strict presence-aware source projections, revalidates those projections and claims before witness settlement, and never authorizes/promotes metadata. Paired canonical SHA-256 dependencies come from the claim-bound list; an agreeing mapping is compatibility only, and a disagreement returns `identity_conflict`. SHA-1-only identities whose dependency needs an unauthoritative mapping return `identity_unproven` before physical or liveness work. Noncanonical authority-bound root and directory-entry fs_ids fail as `malformed_tree` without being remapped; canonical zero-block Cassandra files remain subject to exact durable identity claims. An authority-verified `EMPTY_SHA1` root or directory entry is an empty reachable subtree without an fs_objects row, matching the Sync/API semantics; the commit claim is still required. Evidence on the final HEAD (2026-09-23, Docker): short suite, race, vet, mutation runner 17/17, full Compose integration (280.112s), and the isolated 3-DC harness `scripts/pc-d1b1-certified-baseline-multidc-validation.sh` all GREEN; the 3-DC main leg now also runs the real-Cassandra edge tests, including EMPTY_SHA1. Missing/conflicting evidence fails closed and unavailable authority returns UNKNOWN; none writes a witness. M14a/b and M15a/b extend the mutation contract as 17 execution legs for 15 frozen contracts, and isolated 3-DC legs exercise fs_object A/B divergence, H→R1/R2 commit divergence, and unavailable SERIAL authority. Existing exact-P, physical-byte, permanent EACH_QUORUM liveness/GC checks, final HEAD fence, and ambiguous witness settlement remain. Mapping authority/M18-M19, lifecycle fencing, the productive consumer, PC-2, and GC activation remain separate; `GC_ENABLED=false`.

**PC-D1B metadata identity authority (2026-09-20, `docs/pc-d1b-metadata-identity-authority-decision`, PR #229):**
architecture decision only, no runtime, schema or certifier change. Baseline
certification may not witness a HEAD unless the commit-to-root mapping and
every reachable fs-object identity are backed by durable write-once provenance
claimed in the canonical global `SERIAL` domain - explicitly pinned, never
inherited from `database.serial_consistency`, which a supported deployment may
set to `LOCAL_SERIAL`. A file's canonical dependency comes from its
authority-bound SHA-256 list when it has one, and from an authoritative
`block_id_mappings` row when it carries logical SHA-1 ids only; a mapping
consulted for compatibility must agree but is not a second authority. A
certifier-local `EACH_QUORUM` read is rejected as authority. Claims survive
deletion of their source row, and re-creating a key writes under the existing
claim. The file digest binds both the logical SHA-1 list and the paired
canonical SHA-256 `block_ids`, because `fs_id` is SHA-1-derived and would
otherwise let two different physical dependencies share one claim. Mapping
authority is acquired by cold-path promotion, never by adding a per-block LWT
to the upload hot path, and a promotion is a per-identity cutover: it must
validate the value it claims against an independent trusted source and
neutralize pre-fence mutations that can still be delivered (pending hints
included), since convergence proves agreement rather than provenance.
Promotion is coverage, not a #228 prerequisite: until a mapping-authority
representation exists, the certifier detects mapping-dependent SHA-1-only
identities and returns `identity_unproven` without a witness. It is
greenfield forward work rather than history, because `storeSyncFSObject` leaves
`seafile_block_ids_sha1` unset and so a library created after launch can hold a
SHA-1-only identity that needs one. A covered
identity may not disappear between the final revalidation and witness
settlement without failing the CAS or invalidating the authority state it
checks; on current evidence only the dormant GC cascade can reach that, so the
fence is mandatory PRE-GC and pre-consumer rather than a merge gate for #228.
Minimum certifier correctness (fail closed on unproven identities) is
separated from coverage. Deployment is greenfield (`docs/DEPLOY.md`), so
historical cutover, pre-authority backfill and forensic reconstruction are
non-goals rather than roadmap stages, and the authority-aware release lands
before first production traffic. Accepted debt registered as
`ISSUE-PCD1B-AUTHORITY-CLAIM-RETIREMENT-01`. Deliberately left open: whether the
stored witness gains `R`/`D`/`A`, which needs composable-digest semantics, an
epoch-bump rule, and the cost of new `IF` predicates on the landed PC-D1A
primitives. Registered as `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`;
record:
[docs/PC-D1B-METADATA-IDENTITY-AUTHORITY.md](../PC-D1B-METADATA-IDENTITY-AUTHORITY.md).

**Publish-repair worker observability (2026-09-18, `feat/publish-repair-observability`):**
the metrics #223 §8.8 H asked for and the prerequisite of the fail-closed
GC health gate (§8.8 G / #224 D12): pending rows and oldest pending age
(complete sweeps only), sweep start / complete-sweep heartbeat, sweep
duration, per-row sweep outcomes, visit outcomes (ok / retained / failed),
renewal failures, post-HEAD reconciliation-failure events by funnel, immediate-repair
outcomes. Report only; no gate, no protocol or schema change. Runbook:
[docs/PUBLISH-REPAIR-OBSERVABILITY.md](../PUBLISH-REPAIR-OBSERVABILITY.md).
Not added on purpose: remaining pin TTL per block (cost + not a certified
witness; the gate design decides). Follow-up
`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` (P2, FOLLOW-UP / GENERAL):
a dead/unreachable publication whose repair row survived cleanup can be
retained and repeatedly re-pinned while successful unresolved visits
refresh the 35d `pub:` — not post-success clear failure, not
settle-on-commit-absence, and not a visit-every-sweep / refresh-every-
visit guarantee. Does not block X1 safety closure or destructive-GC
activation.

**R31 repair-liveness design proof — outcome B (2026-09-18, `docs/r31-repair-liveness-design-proof`, PR #224):**
the smallest candidate for "liveness maintenance separate from
classification" — refresh the stable repair-owned `pub:<repo:commit:fsID>`
in place as a sequential fan-out before the classifier, accepting ≤ 35 d
over-retention — is **rejected** without writing runtime. Multiple
admissible schedules make `main` safe and the candidate unsafe, in two
classes — the untouched suffix after a partial refresh (`main`'s later
renewal or promotion may reach it, V0's only after the pre-step; the
minimal witness, independent of ambiguous-write semantics) and the block
whose refresh failed with a known-unapplied outcome (no second renewal on
UNKNOWN, the block waits for the next visit while `main` renews it; `fs:`
delayed on REACHABLE; conditional, since an ambiguous error may have
applied) — each under UNKNOWN and REACHABLE; in all of them the pre-step consumes time
for blocks it fails to cover and can delay their next owner past the
instant `main` would have installed it. Continue-on-
error and promote-first do not repair it. Lessons: a failable in-visit
pre-step ahead of `main`'s handoff is unsafe unless failed/unreached
blocks have an independent owner or a proven temporal invariant covers the
added delay (V0 and #222 had neither); a long-TTL owner protects only
the blocks it reached; stationary latency is not an invariant; "main would
have failed too" is never an argument. What remains unstudied: a maintainer
that never delays the visit's handoff, with all of §8.8's obligations.
`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01` stays OPEN; X1, W2/R31,
GC unchanged. **Parked**: it blocks declaring X1 closed and activating
destructive GC on this guarantee, not ordinary development; next work is
repair-worker observability (prerequisite of a fail-closed GC health gate),
then the gone-check and Paxos-domain follow-ups — not another renewal
variant. Record: [docs/R31-REPAIR-LIVENESS-DESIGN-PROOF.md](../R31-REPAIR-LIVENESS-DESIGN-PROOF.md).

**Publish-repair liveness: PR #220 and PR #222 closed without merge (2026-09-18, `docs/r31-publish-repair-liveness-lessons`):**
`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01` remains **OPEN** (P1,
PRE-X1 / PRE-GC). #220 (durable 35d renewal before the classifier, then a
durable cleanup-witness protocol) was rejected because that protocol failed
to demonstrate unlimited retries with structurally bounded durable state —
it did not show that accepting a TTL-bounded, stable-identity over-retention
residual is invalid; #222 (transient write-only `:walk` pre-pass) was rejected because it
inserts work before `main`'s unbounded stable-owner handoff, and schedules
exist where `main` keeps a block continuously live while #222 opens a
zero-ref interval. Nothing from either PR is in `main`. The canonical
record — problem, both attempts, counterexamples, invariants
(main-liveness non-regression; bounded durable state under unlimited
retries), the stop rule and the mandatory design gate — is
[docs/PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](../PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md).
The record also freezes the operational model of `main` (§8): the 35d
`pub:` TTL is a crash/recovery backstop, not publication latency — every
content funnel attempts `stage pub: → queue repair → HEAD → request-local fs:
promotion attempt → clear repair on success` inside the request, the durable row has no TTL,
the sweep runs on every node at startup and every 1 min behind a 5 min
advisory lease with 5 min – 6 h process-local retry hints — and states that
this expected cadence is not a proven discovery/maintenance bound. It
records, as an UNPROVEN hypothesis, that liveness maintenance may be
separated from reachability classification (keep every pending repair
comfortably alive first, then classify), with the questions that must be
answered first: remaining-liveness knowledge, a derived margin, partial
renewal, crash, whether TTL-bounded over-retention can simply be accepted,
whether the destructive cross-DC gone-check is needed at all, prolonged
outage and a possible fail-closed GC health interlock. Next step is a
design/characterization PR against that gate, not another incremental
runtime fix. The separate
`ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01` was closed by the scoped
follow-up: a post-renewal local absence now returns gone without deleting the
stable repair-owned `pub:`; the existing TTL bounds the resulting
over-retention. `RENEWAL-AFTER-CLASSIFY-01`, X1, the remaining W2/R31
residuals, and GC activation remain open.

**Library HEAD global SERIAL domain (2026-09-14, `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01`):**
all current writers and guards that compete for canonical
`libraries.head_commit_id` authority now pin
`SerialConsistency(db.LibraryHeadSerialConsistency)` =
`gocql.Serial`, independent of `database.serial_consistency` /
`CASSANDRA_SERIAL_CONSISTENCY`. The four legacy productive LWTs remain
`FSHelper.UpdateLibraryHead`, `SyncHandler.updateLibraryHeadWithStats`,
`FSHelper.InitializeLibraryHeadIfUnset`, and `deleteUnpublishedLibraryRow`.
PC-D1A also adds two authority-only certified-frontier primitives with the
same explicit global SERIAL domain; no productive consumer has been migrated.
Other LWTs may still inherit the session default, including `LOCAL_SERIAL`.
PC-0 inventories competing HEAD mutations from `Query`/`Bind` CQL (not a
raw string-literal walk; package-level `var fn = func` included) and
chain-pins the domain; mutation legs M1–M28 go
RED if any seam is downgraded, the constant becomes `LOCAL_SERIAL`, a hidden
DELETE IF names `head_commit_id` after another predicate or uses
`sesamefs.libraries`, a HEAD DELETE is passed through unresolvable CQL
(`fmt.Sprintf`), a package-level FuncLit hides a DELETE IF, an already
allowlisted unresolved Query (`UpdateLibrary`) becomes a HEAD LWT, a SET
fragment or lock `fmt.Sprintf` format inside an allowlisted caller is not
source-resolvable, an `updates := []string{"head_commit_id = ?"}` initializer
preloads a HEAD column, `injectHead(&updates)` mutates the slice, the SET
loop ranges something other than `updates`, `poison(&query)` appends a HEAD
IF, `update` is assigned inside the SET loop, `Migrator.apply` reassigns
`stmt` via `strings.Join`, a lock helper format becomes
`DELETE FROM libraries ... IF EXISTS`, `poison(&stmt)` keeps a stale CQL
binding, a second `SerialConsistency(localSerial)` last-write-wins, a second
HEAD `Query.Exec()` inside an inventoried writer, a `range` rebind of a CQL
ident, an embedded `migrations/*.cql`
statement competes for HEAD, a migration writes `head_commit_id` /
whole-row-deletes `libraries` under `IF EXISTS` without naming the column
in IF, or a concat `UPDATE libraries SET` + `head_commit_id` Query is added
outside the inventoried CAS writers. Real 3-DC evidence
(`scripts/library-head-serial-domain-multidc-validation.sh`) is self-managed
(not `./scripts/test.sh api`) and runs with session
default `LOCAL_SERIAL` and requires exactly one winner for concurrent HEAD
advance and concurrent initial HEAD. This **satisfies** the PC-D1 global SERIAL
prerequisite. It does **not** implement the certified baseline frontier, migrate
a funnel, close W2/R31/X1, change GC, or start PC-2.

**R31-C1 published repair reachability convergence (2026-09-12, `fix/r31-publish-repair-reachability-convergence`):**
closes `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01`. Each ancestry
chunk walks at most 1024 sequential EACH_QUORUM parent reads under a
30-second context; UNKNOWN still retains. SERIAL HEAD is observed when
creating or replacing the durable anchor, not on every retry of that snapshot.
A visit that clean-walks to genesis may re-observe HEAD and walk a second
chunk in that same context. The per-visit bound is enforced by code
(`publishedCommitReachabilityMaxHeadObservations = 2`, spent by the
anchor-creating read and by each re-anchor attempt; a re-anchor CAS loser that
would need a third read returns UNKNOWN and resumes next visit) → at most two
SERIAL HEAD observations / 2048 parent reads. A resumed chunk seeds the
anchored HEAD into `visited`, so ancestry leading back to HEAD is a detected
cycle rather than a cursor rotating across chunks (cycles entirely below HEAD:
`ISSUE-PUBLISH-REPAIR-CROSS-CHUNK-CYCLE-01`). The
walk is now resumable: the first observation persists
`reachability_anchor_head_commit_id` + `reachability_cursor_commit_id` +
`reachability_anchor_exhausted` on the existing
`published_block_reference_repairs` row (migration 024), later retries
continue from the cursor (the next unread commit, including after timeout /
later parent-read error), and a later live HEAD does not restart in-flight
work. After a clean walk to genesis, that snapshot is persisted as exhausted
before the SERIAL HEAD re-read; a newer SERIAL HEAD may then replace it. A
missing repair row is a terminal no-op.
Root/cycle/error stay UNKNOWN with no durable negative witness. Cursor/anchor
writes are a tiny SERIAL LWT for monotonic progress only; INSERT/DELETE of the
repair row stay ordinary. Those LWTs share the 32 bucket partitions of the
discovery table (`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`). The
progress-only residue that race can leave (PK + `reachability_*` only) is now
reaped by the sweep by tombstoning only the reachability cells under a SERIAL
`IF created_at = null AND lease_expires_at = null` — never the row, because an
ordinary requeue INSERT outside Paxos with an older timestamp would be shadowed
by a row tombstone; cell tombstones cannot shadow anything the INSERT writes.
Not settlement. A row listed live that becomes residue before hydrate or a
mid-classify reload is treated as gone (loaded ordinary cells are
authoritative). While a repair is unresolved, the worker can write/refresh a
per-row `pub:<repo:commit:fsID>` for `staged_block_ids` (not v2's shared
`pub:<commitID>` and not Sync's random attempt). The shared worker
best-effort removes that identity before deleting the row. Ordinary Sync
success only clears repair rows — it does not walk blocks to DELETE those
refs. Concurrent renewal of the same row can still leave TTL-bounded `pub:`
(`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`). An unresolved visit can
write/refresh that per-row `pub:` after classification while the repair row
is still pending. That is not a gap-free handoff: if prior liveness expires
before that write, a zero-ref interval exists even if the later renewal
recreates `pub:` (`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`). If
discovery starts after expiry, the gap already existed
(`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`). The 6h retry hint is
process-local and is not a visit-interval bound. Owner-sweep still uses
the #213 FromStore classifier. No PublicationCoordinator, funnel, or GC
change.

**Library rollback ghost recovery (2026-09-11, `fix/library-rollback-ghost-projections`):**
closes `ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01`. New-library rollback
now writes `library_rollback_pending` before the HEAD LWT introduced by #214,
then runs idempotent derived cleanup and drops the marker. A Server-owned
reaper (`RecoverPendingLibraryRollbacks`) rediscovers crash state from
Cassandra and **re-enters `deleteUnpublishedLibraryRow`**; the marker does
not authorize cleanup. The reaper is bounded and fair (clustering cursor +
rotating start bucket). Independent of `GC_ENABLED`. Soft-delete / trash
cascade was not used (`InitializeLibraryHeadIfUnset` interaction). Group-library
creation resumability remains a separate issue.

**PC-1 (2026-09-11, `feat/pc1-publication-coordinator-skeleton`):** the
`PublicationCoordinator` skeleton and the common publication types now exist
in `internal/publication` (standard-library only, no I/O): `AttemptIdentity`
(attempt id and target commit kept separate — Sync mints a fresh UUID, v2
reuses the commit id), the tri-state `HeadOutcome` with `UNKNOWN != KNOWN_LOSER`
and fail-closed attempt cleanup codified (UNKNOWN always retains; APPLIED
cleanup requires an attempt id distinct from the canonical target), the
`SettlementDisposition` rule (`promote` / `cleanup-attempt` / `retain`), the
opaque `PublishableInput` / `DependencyEvidence` boundary with only the
candidate `WorkSetScopeNewlyLive` declared, `Phase` labels with no order, and
a zero-field `PublicationCoordinator` whose only method is the pure
`ValidateSettlement`. **Zero funnels migrated, zero runtime change, zero productive
importers, zero schema/CQL/CL/TTL/GC change.** No existing HEAD classifier
(`InitialHeadOutcome`, v2 sentinels, Sync errors) was converted: the §9 mapping
in [`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`](../PUBLICATION-PROTOCOL-CHARACTERIZATION.md)
shows one v2 shape whose classification would change, so the unification stays
a separate PR. `TestPC0PublicationCoordinatorTypeIsNotImplemented` was retired
and replaced by eight `TestPC1*` source contracts in
`internal/db/pc1_publication_coordinator_contract_test.go` (exactly one
declaration, zero productive importers, no funnel references, stateless and
storage-free package, inventoried type/method/package-function/call surfaces,
PC-0 inventory content pinned); the stateless guard rejects reassignment and
address-taking of allowed sentinel vars **inside `internal/publication`**
(the exported vars remain assignable by Go; PC-1 has zero productive
importers), the coordinator unit guard rejects invalid complete settlements,
and the WorkSetScope unit guard covers typed/inferred/untyped assignable
constants and pins `"newly-live"` exactly. The mutation suite grows to 30/30,
including fail-closed coverage for arbitrary package-level mutable state,
package-level orchestration entry points, type-surface capability smuggling,
all declared work-set scope forms, direct/indirect in-package sentinel
mutation, and incomplete coordinator validation.
**PC-D1 (2026-09-12, `docs/pc-d1-inherited-dependency-continuity`):** the
inherited-dependency continuity decision is closed. The single owner is the
certified baseline frontier: certify a concrete HEAD under contract version V,
persist a durable witness only while that HEAD remains current, and use
`WorkSetScopeNewlyLive` incrementally only with a valid witness. Baseline
certification is GC-aware per dependency: capture exact physical incarnation P,
establish non-expiring current-library liveness, then revalidate exact P plus GC
authority before the witness LWT. TTL pins bridge certification only and cannot
justify a witness; legacy deterministic locators must be rematerialized to
minted P before certification.
**PC-D1A (2026-09-19, PR #227):** the authority foundation is implemented:
migration 025 adds the canonical witness columns; `LibraryState` rejects
missing, stale, unsupported, and soft-deleted witnesses; baseline certification
is HEAD-fenced; and the atomic frontier advance requires
`(H,H,V) -> (H',H',V)` under the global SERIAL domain. It adds no certifier,
historical backfill, exact-P/GC handshake, productive consumer, funnel
migration, or GC activation.

**PC-D1B.1 (2026-09-19):** the cold-path one-library / one-HEAD certifier is
implemented. It walks complete reachable trees, resolves exact minted P,
writes permanent current-library liveness with EACH_QUORUM proof, revalidates
P and GC authority, and settles ambiguous witness CAS through an authoritative
serial read. It adds no productive consumer, backfill scheduler, lifecycle
serialization, funnel migration, PC-2, or GC activation.
Status after PC-1 / PC-D1 / HEAD SERIAL domain:

```text
PC-0: CLOSED / characterization complete (#211)
H1:   CLOSED (#214)
PC-1: CLOSED (2026-09-11)
PC-D1 inherited dependency decision (ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01): CLOSED (architecture decision); PC-D1A authority foundation, the fail-closed PC-D1B.1 certifier gate, and PC-D1B.3 Mapping Authority are implemented (#227/#228/#230/#231/#233 merged); the line is FROZEN by the 2026-09-29 X1 reset and re-evaluated in E1 (historical backfill is a greenfield non-goal)
ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01: CLOSED (2026-09-14) — global SERIAL prerequisite satisfied
PC-D1B.5: PRE-GC (not pre-consumer); WIP parked on feat/pc-d1b5-certification-window-fence-runtime
PC-2: NOT STARTED — not an X1 prerequisite
W1:   CLOSED
W2:   OPEN — exit checklist in docs/X1-CRITICAL-PATH.md §4
R31:  OPEN — residual rows W2-11..W2-14
G1/G2/G3: CLOSED
G4:   OPEN — starts only after the W2 exit checklist is closed
G5:   OPEN
X1:   OPEN — only E1 may close it
Next, in order (docs/X1-CRITICAL-PATH.md, supersedes the former
"PC-D1B.5 → productive consumer → PC-2" order):
1. W2 exit checklist rows, demonstrate-first (W2-0 cross-cutting up:→pub:
   continuity first).
2. G4 → G5 → E1 (X1 CLOSED).
3. PRE-GC list (Phase 5 / former H4, minimal PC-D1B.5 for D1–D3, ghost row,
   repair-liveness health gate, GC mapping resolver, hard-delete lease) → A1.
   The former "H5 before X1" note is subsumed by the W2 exit checklist.
In parallel (current runtime): hard-delete lease global SERIAL, then the
generation-fenced final batch.
```

**PC-0 (2026-09-09):** publication-protocol characterization on
`docs/pc-0-publication-protocol-characterization`. Inventory, observed
partial-order kernel, multi-DC matrix, and source contracts only. No
coordinator implementation, no production behavior change. The target
coordinator contract requires classified input to become publishable before
stage: `BORROWED` must acquire durable own `up:`, while rejected
`UNPROVENANCED`/`ERROR` must not enter `stage pub:`. Current Sync can stage
before its scope gate discovers those outcomes; that is a W2 gap, not an
observed universal ordering. Verdict in
[`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`](../PUBLICATION-PROTOCOL-CHARACTERIZATION.md):
`PROCEED WITH COORDINATOR`. W2/R31/X1 remain OPEN. `GC_ENABLED=false`.

**H1 (2026-09-11, `fix/h1-conditional-head-initializer`):** PC-0 merged
(#211). The H1 follow-up is implemented: `FSHelper.InitializeLibraryHeadIfUnset`
(`IF head_commit_id = null AND created_at != null`) is the only HEAD
initializer; `InitializeLibraryFS` and Sync `createInitialCommit` publish
through it (tri-state outcome; UNKNOWN never cleans up nor rolls a library
back; a demonstrated KNOWN_LOSER discards its attempt-unique commit row,
best effort, and so does a definitive rejection — missing or invalid row —
whose CAS demonstrably never published it), `GET /commit/HEAD` returns the Paxos-settled HEAD from a
blind DC only once its commit is locally servable. Evidence: unit +
default-stack integration + real 3-DC handler-level
(`scripts/h1-initial-head-multidc-validation.sh`).
`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` resolved; coordinator prerequisite
met. Second review round (same day) closed 4 more runtime gaps found by three
independent audits: the classifier now recognizes the real native-protocol-v4
CAS-timeout shape (`RequestErrWriteTimeout`/`RequestErrWriteFailure` with
`WriteType: "CAS"`, not just v5's `CAS_WRITE_UNKNOWN`); the three creation
handlers no longer answer a `503 Retry-After` that a retry could not honor
(it minted a new library) — they preserve the library and answer an honest
`500` with the preserved `repo_id`, and durable resumption is split out as
`ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01` (a `pending_group_library_creations`
marker was tried, audited as a separate idempotency subsystem, and parked on
`feat/group-library-creation-claims`); `SettleAdoptedInitialHead` attempts the
KNOWN_LOSER's best-effort commit discard before the adopted-HEAD visibility
check, not after; and a definitive (non-ambiguous) CAS rejection now discards
its own now-orphaned attempt-unique commit row. See
[`docs/KNOWN_ISSUES.md`](../KNOWN_ISSUES.md#issue-library-initial-head-concurrency-01-concurrent-initial-library-head-initialization-can-race)
for the full writeup. Registered
`ISSUE-LIBRARY-HEAD-ADOPTED-TREE-VISIBILITY-01` as a separate follow-up:
adopting a blind-DC HEAD only proves its `commits` row is locally servable,
not the tree behind it. Next: PC-1 skeleton (done 2026-09-11, see above); H4
(Phase 5) before any GC activation; H5 before X1.

**PC-0 deep audit (2026-09-10, ninth pass):** every claim re-verified in
Docker, including the #210/#213 3-DC evidence scripts (4/4, 6/6) and the
PC-0 gate. Verdict stands. Four characterization gaps closed, no runtime
change: (1) `head_commit_id` has six writers, two of them unconditional
`UPDATE` initializers outside the CAS domain — reproduced reverting an
LWT-published HEAD from a blind DC on the real 3-DC fixture; prioritized
separate follow-up, coordinator prerequisite
(`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`, PC-0 §3.4); (2)
revert/restore reclassified as content-resurrection publication paths
(`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`, §3.5); (3) GC Phase 5
cascade deletes fs_objects shared with HEAD —
`ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`, **P0 latent, PRE-GC**,
refutes "ordinary GC protects inherited dependencies" as-is; (4) §11/§12
precision (`ISSUE-PUBLISH-HEAD-TREE-STATS-COST-01`). Plus
`ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` (P1, PRE-X1). New source
contracts, a GC characterization test, a 3-DC reproduction script, and 9/9
mutation legs.

**Current baseline (2026-09-10):** `main` at `7b9102af9` contains merged #209/#210/#212/#213. #212 implements G3 canonical retirement on the GC side and is orthogonal to the publication funnels; no PC-0 funnel re-characterization is required.

**PC-0 audit pass (2026-09-09):** branch rebased onto `main` (now contains
merged #209/#210). Re-characterized §7/§8/§11/§13 against #210's merged
`BlockReferenceExistsEachQuorum` cross-DC fallback (the doc previously still
said "#210 not in this baseline" after the rebase had already landed it —
stale). Registered a new finding, `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`:
`PublishableInput` only covers dependencies newly live on a HEAD, not
dependencies inherited unchanged from the old HEAD whose continuity R3's own
`LogicalPositiveBlockDelta` note already flags as possibly unproven. Fixed
`TestPC0PublicationCoordinatorTypeIsNotImplemented` from a literal
`strings.Contains("type PublicationCoordinator struct")` scan of 3 fixed
directories to an AST walk over all of `internal/` matching any
`PublicationCoordinator` type declaration (struct, interface, alias, or
generic). Corrected the W2-status vocabulary line to R3's actual
`PROVEN_CONTINUOUS`/`CONDITIONAL`/`UNGUARDED`/`UNKNOWN` (was a shortened
`PROVEN`/`CONDITIONAL`/`UNKNOWN`). Re-scoped `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01`
from `CHARACTERIZATION-PR` (ambiguous with "introduced by this PR") to
`FOLLOW-UP / W2`.

The W2 post-HEAD slice also has a separate 3-DC reachability leg
(`scripts/w2-post-head-multidc-validation.sh`); it is independent of the
existing X2/P3 GC harness and proves that local blindness cannot authorize
cleanup of a publication made in another datacenter.

**PC-0 second audit pass (2026-09-09):** the integration matrix harness had
not been updated in step with the doc's own #210 re-characterization (M2/M3/M8
still said "#210 not in this baseline"); fixed to `PRIOR-EVIDENCE-NOT-RERUN`/
`MIXED`. F8/F9's CL/Cost rows, §12's cost table, and PUBL-7 did not carry
#210's LOCAL_QUORUM-miss-escalates-to-EACH_QUORUM cost/availability trade-off;
added an explicit cost-bucket breakdown and corrected PUBL-7's error-vs-miss
framing. Introduced `ExpectedP` as distinct from the final exact-P
revalidation: the candidate boundary previously read as if `BORROWED`'s
exact-P check ran in the adapter before staging (reopening the W1 TOCTOU);
clarified that adapters only capture `ExpectedP`, and the final check runs in
the coordinator's readiness step after stage and before HEAD, with its order
relative to repair kept funnel-specific.
Corrected `TestPC0PublicationCoordinatorTypeIsNotImplemented`'s comment, which
overclaimed detection of a `type X = PublicationCoordinator` alias (RHS name)
the AST walk does not resolve. Made the HEAD-caller inventory walk
`internal/api` recursively instead of listing two fixed directories. Fixed the
M3 mutation's CRLF-broken Perl pattern in
`scripts/pc0-publication-inventory-mutation-validation.sh` (the three original
mutation legs in that pass now run RED). Aligned the inherited-dependency sequencing with
`KNOWN_ISSUES.md`: resolve `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`
before PC-2, not "PC-1 or later."

**PC-0 fourth audit pass (2026-09-10):** confirmed and corrected the remaining
characterization drift: §3.1 now describes Sync provenance as the complete
LQ→EQ scope gate; §14 and this file distinguish the observed current kernel
from the target `PublishableInput` boundary; F3 includes its session-claim
LWT in the coordination cost; M7 includes Sync's provenanced exact-P path;
the inventory walks all of `internal/`; funnel mappings call their first
column characteristic seams rather than a universal `prepare` phase; and the
negative consistency pin has a diagnostic message. The current PC-0 mutation
script had four legs, and all four were expected to turn RED. The two existing
PC-0-only source contracts for tree classification and the nil stored-upload
fence are listed in the test plan. No runtime behavior changed.

**PC-0 fifth audit pass (2026-09-10):** reconciled the remaining audit findings
against source: final `ExpectedP` validation is documented after stage and
before HEAD, with repair order kept funnel-specific; F3's full successful
request now includes the optional committed-session slot-release LWT; and
`ensureCommitBlockOwnLiveness` is documented as a plain quorum upsert that
renews or recreates the same `up:` row, not a read hit/miss. The production
function inventory now fails closed on duplicate path/function keys instead of
silently replacing a method receiver. Today's Sync global-miss behavior remains
an explicit W2 gap, not a claim that Sync rejects unprovenanced input. The
opt-in PC-0 3-DC gate was then executed against
`docker-compose.cassandra-3dc.yaml` after migrating its RF-1 keyspace:
`dc-na`, `dc-eu`, and `dc-asia` all connected and M1–M8 were recorded (M9 added by the ninth pass);
the temporary runner and fixture volumes were removed afterward. No runtime
behavior changed.

**PC-0 sixth audit pass (2026-09-10):** rebased onto `main` at `d95eec8d6`
(the merged #213 baseline) and re-characterized the shared repair classifier:
HEAD observation is `SERIAL`, ancestry is at most 1024 sequential
`EACH_QUORUM` reads under one 30-second context, and inconclusive evidence is
`UNKNOWN`/retain. M3/M4 now include #213's one-DC-unavailable retention and
later-HEAD ancestor evidence without closing M6 or broader R31. Corrected the
M1 matrix wording so F3's session-claim LWT is not omitted, softened the PR
inventory claim to its documented lexical named-call scope, and changed the
inherited-dependency tracker wording from "not addressed" to "not resolved".
No runtime behavior changed.

**PC-0 seventh audit pass (2026-09-10):** confirmed the inventory gap for
package-level function-valued variables and extended
`pc0ParseProductionFuncs` to index `var = func` literals; the mutation
validation now has five RED legs, including that exact publisher shape.
Added an explicit durable-repair seam to every mapped block-bearing funnel and
a contract freezing `stage < durable repair < HEAD`; the empty-file/no-dependency
case is documented as the only no-row degeneration. Reworded the kernel across
the characterization, R3, and changelog documents so readiness is optional but
repair is not when physical block dependencies exist. Corrected the
post-#213 `onlyOfficeCommitReachable` statement and advanced the stale issue
registry/index dates. No runtime behavior changed.

**Last Updated**: 2026-09-11
**Session**: PC-1 PublicationCoordinator skeleton and common publication types, branch `feat/pc1-publication-coordinator-skeleton` (rebased onto `main` at `e1e0092fe`, containing #211, H1 #214, and #215 rollback recovery). Previous session: PC-0 multi-DC publication protocol characterization (PR #211), branch `docs/pc-0-publication-protocol-characterization`. This pass reconciles the latest audits against the current source; no end-to-end coordinator implementation or production behavior change.

Twelve independent audit rounds hardened this slice. Round 2: the fan-out calling the scope gate had no cancellation (`syncCommitProvenancedBlockIDs` now uses `errgroup.WithContext`, bounding a degraded datacenter's blast radius to at most `syncCommitBlockPlacementConcurrency` (20) block-level provenance checks admitted/in flight, instead of O(N)); the real 3-DC all-cross-DC-hit cost scenario was missing from the characterization. Round 3: the consistency guard pinned the named constant's *value* but not that `BlockReferenceExistsEachQuorum` actually *binds* it (`TestBlockReferenceExistsEachQuorumBindsTheNamedConsistencyConstant` + mutation M15 close that); the cost benchmark reused overlapping block slices across N, letting an earlier scenario's `EACH_QUORUM` query silently heal a shared block's local visibility before a later scenario reused it (fixed with disjoint per-N block sets and an explicit locally-blind precondition check before each timed measurement); cost-test cleanup was best-effort (`t.Logf`) instead of fail-closed; and the scope-gate-only cost numbers were being read as if they were the full end-to-end HEAD-readiness cost for a recovered block, which they are not (the rest of #206's pipeline -- `ProbeBlockReuse`, renewal, exact-P validation -- still runs for a recovered block, though all of it stays `LOCAL_QUORUM`/session-inherited, not newly WAN-bound). Round 4: `SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_XDC_EVIDENCE` was missing from `TestMain`'s evidence-gate chain; the round-3 cleanup fix only reached the cost fixtures, not the single-block correctness fixture (generalized into one three-surface, continue-on-failure `t.Errorf` helper shared by both); the round-3 causal claim about the 3.4-5.0s-to-1.67s change was backwards (a healed block skips the WAN hop, which should make the contaminated run faster, not slower -- the old number is retired, not explained); "real inter-datacenter distance" language was replaced with "real 3-DC Cassandra fixture" throughout; and the "confirmed by the project owner" trade-off claim, which had no corroborating GitHub record, was re-confirmed directly by the owner in a live session. Round 5: the disjoint-set cost number varies far more than one sample suggested -- five further runs of the real 3-DC script observed N=1000 between 1.67s and 8.1s on the same fixture and code, so no single number in this doc is "the" cost, only the query count (exactly N fallback `EACH_QUORUM` reads for N clean local misses, at most 20 concurrently in flight -- not `2N` counting the always-first `LOCAL_QUORUM` reads too) is exact; the bounded-fail-fast unit test accepted up to `2x` the documented concurrency bound before failing (tightened toward exactly `syncCommitBlockPlacementConcurrency`) and was renamed from `...StopsSchedulingAdditionalWork` to `...StopsAdditionalDBProbes` since goroutines are still created, they just skip the external call; `docs/TESTING.md`'s cost-fixture default (1111, not 1000) and `docs/OPEN-WORK-INDEX.md`'s remaining "real inter-DC distance" phrase were also stale. Round 6: the round-5 test tightening overshot -- asserting `got == 20` claimed a lower-bound runtime guarantee that does not exist (only "at most 20" is real; the test's own uniform-latency mock is why 20 was observed every time), loosened to `got > 20` failing; "exactly `⌈N/20⌉` waves...confirmed every run" overstated a continuously-refilled `errgroup.SetLimit` pool as discrete synchronized batches, reworded to "N reads, at most 20 concurrently in flight, wall-clock scales roughly with N/20"; and `docs/R3-LIVENESS-CONTINUITY.md` said the fallback's cost is "bounded by the local-miss rate for provenanced blocks," when it actually runs -- and costs the same -- for genuinely-unprovenanced (dedup) blocks too, since the scope gate cannot tell the two apart before running it. Round 7: leftover `⌈N/20⌉ waves`/"round trips" framing survived in three more places round 6 hadn't touched (this file, and two other paragraphs in `docs/KNOWN_ISSUES.md`), reworded to "N fallback reads, at most 20 concurrently in flight"; the dedup case was said to pay "essentially the same" cost as the measured all-cross-DC-hit numbers, when only its query count and availability dependency are actually identical -- its own wall-clock was never separately measured, softened accordingly; `sync_w2_putblock_xdc_provenance_cost_test.go` called the real 3-DC fixture itself "WAN-separated" (it's the same single-Docker-host fixture every other round's fix already stopped calling that) and called the single-DC characterization a "lower bound on latency" (too strong -- nothing here proves any production topology's latency floor), both corrected; and `syncBlockHasOwnLivenessProvenanceFn`'s doc comment said a global miss means a block "genuinely has no PutBlock provenance," contradicting the function's own top-line description ("currently has a *live*... reference") and the still-open `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01` (provenance past its 48h TTL is indistinguishable from never-existed) -- corrected to "no currently observable live provenance." Round 8: three more spots in `docs/KNOWN_ISSUES.md` carried the exact overclaims round 7 had already fixed elsewhere in the same file -- the Fix section's own "genuinely unprovenanced" line, the hotpath follow-up's "same cost applies" to dedup, and a dangling reference to a "15-20x multiplier estimate" whose actual number no longer existed anywhere in the document after an earlier round's rewrite -- all fixed or removed. Round 9: the scope-gate cost table was still called "the right *lower bound* for how long until HEAD can be attempted," overclaiming given the same table's own documented 1.67s-8.1s variance; reworded to "the measured scope-gate component of the total HEAD-readiness cost." "One concurrency wave" survived in six more live-doc/code spots (this file's own Round 2 clause, `docs/KNOWN_ISSUES.md` x4, `docs/R3-LIVENESS-CONTINUITY.md`, `docs/TESTING.md`, and the production comment in `internal/api/sync.go` itself) despite round 6 establishing that `errgroup.SetLimit` is a continuously-refilled pool, not discrete waves -- all reworded to "at most `syncCommitBlockPlacementConcurrency` (20) provenance DB probes in flight" (itself superseded by round 11's further correction to "block-level provenance checks admitted/in flight," since each admitted check may issue both a `LOCAL_QUORUM` and an `EACH_QUORUM` read, not one probe each). Left alone: the dated `docs/CHANGELOG.md` historical entry, two mentions of an unrelated feature's own "finalize wave" (chunked-upload permit, not this fan-out), and one spot in `docs/KNOWN_ISSUES.md`'s hotpath follow-up describing a genuinely different, actually-discrete-barrier mechanism (the four sequential pipeline stages) where "wave" is an accurate description. Round 10: a real scope leak, not wording -- `syncCommitProvenancedBlockIDs` (and the `syncBlockHasOwnLivenessProvenanceFn` it calls) was already shared, on `main`, by both the pre-HEAD gate (`ensureSyncCommitBlockPublicationReadiness`) and the post-HEAD best-effort renewal (`renewSyncCommitBlockOwnLivenessBestEffort`, used only by the already-reachable-commit repair path). Adding the EACH_QUORUM fallback to that shared function silently gave the post-HEAD path a new cross-DC availability dependency it never had and does not need. That path runs on `repairPublishedSyncCommitBlockDelta`'s idempotent retry path, which stages its own fresh `pub:` handshake before this call and does not assume the commit's permanent `block_references` already exist here (this repair exists precisely to heal a prior publish whose finalize did not complete, so they may not yet). The issue this PR closes is a gap in the pre-HEAD gate specifically; the post-HEAD renewal had no such gap on `main` (it was already `LOCAL_QUORUM`-only, correctly, unrelated to what this PR fixes), so the right move is to preserve its exact prior behavior -- a missed renewal there costs nothing but a delay until the next opportunity -- rather than fold it into the pre-HEAD fix's scope, unlike the pre-HEAD gate's one-shot, correctness-bearing decision before an irreversible HEAD CAS. Split into two explicit functions -- `syncCommitProvenancedBlockIDs` (pre-HEAD, `syncBlockHasOwnLivenessProvenanceFn`, can reach `EACH_QUORUM`) and the new `syncCommitProvenancedBlockIDsLocalOnly` (post-HEAD, the new `syncBlockHasOwnLivenessProvenanceLocalOnlyFn`, `LOCAL_QUORUM` only, matching `main`'s original behavior exactly) -- sharing only the small union/bounded-concurrency helper `syncCommitBlockIDUnion`, not a parameterized scope-gate call: an earlier attempt to parameterize instead of duplicate broke `TestR3SyncPutBlockReadinessDeclaredExceptionIsFrozen`'s static call-graph walk, which cannot resolve a function value passed as an argument back to its concrete callee. `TestRenewSyncCommitBlockOwnLivenessBestEffortNeverEscalatesToEachQuorum` freezes the split with a poison-pilled pre-HEAD seam. Also reworded the R3 guard's own violation message, which said "never SERIAL/EACH_QUORUM" without noting that check is scoped to the walked api-side wrapper code, not the separately-tracked `allowedDBCalls` allow-list that already permits `BlockReferenceExistsEachQuorum` by name. Round 11: the round-10 test only froze which package-level var `renewSyncCommitBlockOwnLivenessBestEffort` calls -- both vars were mocked, so `syncBlockHasOwnLivenessProvenanceLocalOnlyFn`'s own real body never ran; a future edit rewriting that body to call `BlockReferenceExistsEachQuorum` would have stayed green. Added `TestSyncBlockHasOwnLivenessProvenanceLocalOnlyFnBodyNeverReachesEachQuorum`, a source-contract test parsing the real body for exactly what it calls. Round 10's own justification for the split also claimed the post-HEAD path's commit is "already durably protected by its own permanent `block_references`" -- not necessarily true: `repairPublishedSyncCommitBlockDelta` exists precisely to heal a prior publish whose finalize did not complete, so those permanent refs may not exist yet when the best-effort renewal runs; the real justification is simpler -- this path was already `LOCAL_QUORUM`-only on `main`, unrelated to the gap this PR closes, so it is preserved as-is rather than expanded into scope. Corrected in `sync.go` and here. The bounded-fail-fast contract still said "at most 20 provenance DB probes," which reads as a cap on total database reads; the actual guarantee is at most 20 block-level provenance *checks* admitted/in flight after cancellation, each of which may itself issue both a `LOCAL_QUORUM` and an `EACH_QUORUM` read, and a check already past its own `ctx.Done()` gate is not cancelled mid-flight. Reworded in `sync.go`. Round 12: an external audit found round 11's own new test was itself still fragile -- it located `syncBlockHasOwnLivenessProvenanceLocalOnlyFn`'s body via a textual `"\n}\n"` boundary search and a substring blocklist on the literal word `EachQuorum`, which would not catch a future rewrite routing through a differently-named global helper (for example the existing `BlockHasReferencesGlobal`) or a body containing a nested block the boundary search misjudged. Replaced with an AST-based guard: it parses `sync.go` with `go/parser`, locates the real `*ast.FuncLit`, and walks every call in its body with `go/ast.Inspect` against a positive allow-list (only `h.db.BlockReferenceExistsLocalQuorum` and the pure referrer-key helper `syncBlockUploadReferrer` may be called) that fails closed on anything else, confirmed by temporarily swapping in `BlockHasReferencesGlobal` and observing the test fail before reverting. Also found round 11's "block-level provenance checks admitted/in flight" wording fix had only reached `sync.go` itself: `docs/KNOWN_ISSUES.md` (three spots), `docs/R3-LIVENESS-CONTINUITY.md`, `docs/TESTING.md`, and this file's own Round 2 and Round 9 clauses still read "(20) provenance DB probes in flight" as if it were a cap on total database reads -- all reworded to match, with the Round 9 clause explicitly marked superseded rather than silently rewritten. A `sync.go` comment claiming a clean `LOCAL_QUORUM` miss is itself "(logged, does not block the repair)" was also wrong -- only an error return is logged, by `repairPublishedSyncCommitBlockDelta`, never a clean miss -- corrected. In the PR body, the tenth-round section's residual "cross-DC replication converges in seconds, and the next renewal opportunity sees it locally" justification (no such guarantee exists or is needed) was replaced with the real one -- the post-HEAD repair's own fresh `pub:` handshake, not the best-effort `up:` renewal, is what protects the current reconciliation attempt -- and the third-round section's `1.67s (authoritative...)` causal claim, already contradicted by the fourth- and fifth-round sections below it, was explicitly marked superseded instead of left standing uncorrected in place.

**The remaining architectural question -- whether genuinely-unprovenanced (dedup-only) blocks correctly depend on every datacenter's availability now, since a local miss can't be told apart from genuine absence without the fallback itself -- was raised explicitly as a decision the project owner, not the implementing agent, needed to make.** Confirmed 2026-09-09: ship as scoped, accept the trade-off. `docs/KNOWN_ISSUES.md` has the full analysis of why no cheaper alternative exists within scope (a plain `QUORUM` fallback is unsafe with one replica per DC; strengthening the `up:` write itself would impose an unconditional cost on every `PutBlock`) and the explicit confirmation record.

Real 3-DC evidence: RED without the fallback, GREEN with it, one-DC-down fails closed, and the disjoint-set all-cross-DC-hit cost methodology at N=1/10/100/1000 (N=1000 observed 1.67s-8.1s across runs on this local fixture -- exactly N fallback `EACH_QUORUM` reads, at most 20 concurrently in flight, is the exact formula; wall-clock is not). 15/15 mutations (M1-M15) produce the expected RED. Does not touch expired provenance, GC, G1-G5, or X1; `GC_ENABLED=false`.

The preceding G1 (PR #207, merged), G2 (PR #209, merged; PREPARED-to-COMMITTED handoff writing a durable exact `(P,D)` recovery root before PREPARED, aborting or promoting PREPARED through the exact D authority, stopping `processBlock` at COMMITTED without physical deletion -- root reconciliation directly settles PREPARED rows including roots older than the bounded `_by_day` scheduling window, root `first_seen_at` write-once and reused on replay), and W2 post-HEAD publication continuity sessions remain historical context; their mechanisms are unchanged by this slice.

**Docker validation contract (W2)**: unit, contract, mutation, and nine-leg integration evidence runs are Docker-only. The existing X2/P3 multi-DC harness is a separate workflow and is unchanged by this branch. The canonical `go-integration-test` and `go-all-test` commands set `SESAMEFS_REQUIRE_W2_POST_HEAD_EVIDENCE=1` explicitly in their command, never permanently in the service environment; this prevents directed runs from inheriting unrelated evidence gates. The W2 gate fails if the real Cassandra/MinIO run does not execute shared-engine success, crash-after-applied-HEAD, ambiguous-applied, ambiguous-unknown-retain, lease-expiry, synchronous CAS-loser cleanup, reachable-ancestor, restart-replay, and the real pre-HEAD `CreateFileFromBlocks` repair race. `GC_ENABLED=false` remains required in every DC.

**Current W2 post-HEAD slice**: `pub:` staging and the durable repair row precede the HEAD CAS; the repair cold path reads the canonical org-scoped HEAD with SERIAL and immutable parents with EACH_QUORUM under #213's 1024-node/30-second bound. The shared repair classifier uses #213's bounded tri-state reachability classifier. OnlyOffice retains its separate legacy boolean ancestry traversal and remains outside that narrow closure. Positive reachability promotes `pub:` to `fs:`; every non-reachable, incomplete, or unavailable observation retains the repair row and does not actively remove its artifacts. The `pub:` reference still has its finite 35-day TTL; discoverable zero-ref transition remains the separate R31 follow-up. Definitive CAS losers use the request-local synchronous cleanup path. This slice does not claim W2/R31 closure for other publication funnels or broader lifecycle/GC contracts.

**W2 Sync PutBlock -> HEAD slice (2026-09-07, branch `fix/w2-sync-putblock-head-continuity`):** the branch now closes the scoped direct-HEAD safety path for currently observable PutBlock provenance. Canonical IDs are resolved positionally once and split per file; liveness and exact placement are renewed/validated with bounded concurrency before HEAD; auto-merge is contract-tested to complete readiness before queueing repair intent; a readiness failure creates no durable repair row, and once readiness succeeds and the row is queued, queue ambiguity, ambiguous-CAS, and divergent-CAS outcomes do not authorize clearing it -- only successful settlement does; auto-merge IDs include a fresh UUID attempt ID; and an opt-in post-CAS crash barrier preserves staged state for replay. Expired-provenance continuity and the remaining W2/R31 rows stay OPEN. The new integration file has five primary legs plus an explicit crash/restart/replay leg gated by `SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_CRASH_EVIDENCE=1`; no claim is made that the optional leg passed unless that gate is run. No G1/G2/GC protocol or schema changes; `GC_ENABLED=false`.

**📏 File Size Rule**: Keep this file under **500 lines** unless unavoidable. Move detailed content to:
- `docs/KNOWN_ISSUES.md` - Detailed bug tracking
- `docs/CHANGELOG.md` - Session history
- `docs/IMPLEMENTATION_STATUS.md` - Component status
- Other appropriate documentation files

---

## 🚀 NEW SESSION? START HERE

**PROJECT STATUS**: ~85-90% production ready (see `docs/IMPLEMENTATION_STATUS.md`)


---


**Then review**:
1. **"What's Next"** → Top priorities (work on #1 unless user specifies)
2. **"Frozen Components"** → What NOT to touch (breaks desktop clients)
3. **"Critical Context"** → Essential facts to remember

### Quick Context
1. **Sync Protocol**: Baseline-verified for the current desktop sync hardening scope. Do not treat it as frozen; compatibility-sensitive follow-up coverage still exists.
2. **Backend API**: ~98% complete by surface count, which is not the same as ready — see blocker #4 for the open authorization/resource findings. OIDC ✅, GC implementation present; destructive activation blocked by X1 and the PRE-GC / A1 gate (X2 closed 2026-08-14), Library Settings ✅, Monitoring ✅, Departments ✅, Admin Panel (groups/users) ✅, OIDC Group/Dept Sync ✅, Tag cascade ✅, Admin Link Management ✅, Upload Links ✅, Org Admin Panel ✅, Superadmin Departments ✅, Custom Share Permissions ✅
3. **Frontend UI**: ~85% complete (all modals migrated, About modal rebranded, File History UI ✅, History Download ✅, Snapshot View ✅, Restore from History ✅, Share Dialog all 8 tabs ✅, permission UI ~75% with granular flags, ~51 ModalPortal wrappers to clean up, folder icons ✅). Plans/permissions Phase 3 is in progress, not closed.
4. **Test flow**: Prefer Docker-first validation. `./scripts/test.sh sync` now runs the single-client sync suite plus the real active-active desktop harness; default behavior is fail-fast and `--keep-going` is opt-in.
5. **Current risk shape**: destructive GC must remain disabled fleet-wide. Of the two confirmed live-data safety blockers, X2 is closed (fix proven on a real three-DC cluster, regression mutation-verified) and X1 remains open — the *deletion-activation* gate rests on X1 and then on the PRE-GC / A1 gate ([docs/X1-CRITICAL-PATH.md](../X1-CRITICAL-PATH.md) §6). The upload-fence PR series addresses separate writer/GC races and does not close it. **Product go-live is a different gate** and is not held by X1: see the table above and blocker #4.
6. **X1 design state:** accepted architecture is frozen in [`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](../GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md) (D0, 2026-09-02). **Order of work (2026-09-29 reset):** [`docs/X1-CRITICAL-PATH.md`](../X1-CRITICAL-PATH.md) — W2 exit checklist → G4 → G5 → E1 → X1 CLOSED, then the PRE-GC list and A1; PC-2 and the PC-D1 line are not X1 prerequisites. That is a documentation freeze, not X1 closure and not GC activation. Historical option comparison remains in [`docs/GC-X1-CLOSURE-OPTIONS.md`](../GC-X1-CLOSURE-OPTIONS.md) and is **not** the active roadmap. #199/#200 are characterization evidence only; W1 now consumes the #200 BorrowedFS result in the writer path. `GC_ENABLED=false` is pinned explicitly in `docker-compose.prod.yml`. The current branch contains the W2/R31 post-HEAD repair slice, G1 exact `(P,D)` identity and durable recovery discovery, and G2 PREPARED-to-COMMITTED handoff; G4-G5 and X1 remain open; G3 canonical retirement is implemented by #212. This branch does not activate destructive GC.
7. **Current X1/P3 key:** P1 locator authority merged 2026-08-21 (PR #181), P0/R12 landed 2026-08-23 (PR #183), and P2/R9/R24 closed 2026-08-24 after its structural prerequisite (PR #184). This branch implements the P3 writer boundary: existing-incarnation PUTs revalidate exact `(storage_class, storage_key)` authority immediately before PUT, and metadata repair is non-creating and tuple-bound. Docker evidence for R10/R13/R17 is green, including a deliberate-mutation run in which reordering the fence reads, making the repair create-capable, re-tagging a permanent failure as retryable, or restoring SERIAL to the dedup path each turns a gate red. The cross-DC half of the consistency contract is now MEASURED on the real three-datacenter fixture (`scripts/x2-multidc-validation.sh --p3`): with dc-na down neither fence publication may complete, a fence published in dc-eu blocks a dc-na writer, and both weaker publication levels turn the fail-closed leg red — the `QUORUM` mutation being what the third datacenter exists for. Fence reads pin their own consistency so the argument does not depend on `database.consistency`, which accepts `ONE`. R13's P3 writer boundary is GREEN. Strict A+ sequential-life / keep-canonical-until-delete is SUPERSEDED as the X1 closure target by D0 physical-life handoff; it is not an OPEN residual of `StartBlockDeleteOrphan`. R18/R27 are explicitly open in CURRENT production because rejected `up:` references are retained and deferred-orphan rescheduling is not implemented; D0 marks the old postpone-and-reproject design `PENDING RE-EVALUATION`. Keep `GC_ENABLED=false` on every replica in every DC.

8. **Current X1/P4a key:** this branch binds the destructive claim to the exact physical incarnation and to a per-attempt owner. Migration `017` puts `storage_key` on `gc_block_candidates` and its `_by_day` projection; `EnsureBlockGCCandidate` captures it from the canonical row and refuses to write a candidate it cannot name a `P` for. The claim CAS is `IF storage_class = ? AND storage_key = ? AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null`, `claimID` is a fresh UUID per ATTEMPT (`blockDeleteClaimID` is deleted and its absence is gated), and release, stale takeover, finalize and candidate cleanup all condition on the exact tuple. `ClaimBlockDelete` returns a classified outcome, so a non-applied CAS is no longer read as completion. Evidence is green on real Cassandra under `SESAMEFS_REQUIRE_P4A_EVIDENCE=1` (four legs: exclusive ownership with exact takeover, the ABA case, retry semantics, and stale-claim release bound to the observed incarnation), with deliberate mutations red via `scripts/p4a-mutation-validation.sh` (see the count below). **P4a snapshot (historical):** R14a GREEN, R16 GREEN, R20 PARTIAL; at that merge R14b was still OPEN and the orphan path had not yet settled in the serial domain. **Current:** R14b GREEN (#194 / P4b-2). `StartBlockDeleteOrphan` is bound to exact stored `(P,D)` and is not an untouched residual keeping strict A+ non-overlap OPEN. That sequential-life target is SUPERSEDED by D0. X1 remains OPEN. Technical debt #21 and #22 are closed; #23 (GC no longer sweeps metadata-free stubs) and #24 (pre-017 candidate rows need a fresh zero-ref decision, not a backfill) are opened as follow-ups, #24 being a PRE-ACTIVATION requirement rather than a merge blocker. A second review pass found and fixed four defects in the first cut: the stale takeover re-read the row instead of CASing against the authority it observed (so a P1 worker could drop P2 fence); the same hole existed at the owner-agnostic pre-check call site, reachable with no clock skew; the orphan publication and the S3 delete took their locator from an ordinary post-claim re-read rather than from the claim; and ErrBlockCandidateTargetUnavailable was fatal at all three enqueue sites, which was self-poisoning on the fs_object path. The deliberate mutations are red — the script reports its own current total, and the evidence update below is authoritative for it; two of them earned their keep by exposing a non-compiling mutation and a store/mock mirror with neither copy protecting the other. One behavioural narrowing to be aware of: GC no longer deletes metadata-free stub rows, because it has no exact authority over a row with no locator — an unclaimed stub is not an upload fence, and the only producer of a `deleting` metadata-free stub was the old claim CAS. The writer path does claim metadata-free rows, under `gc_state='repairing_stub'`, and cleans up its own; those are not a delete fence and were never GC's to touch. A third review pass closed the last claim-side hole: `releaseBlockClaim` collapsed `BlockReleaseNotOwner` into a bare `nil`, so a late loser — an attempt whose claim was taken over while it worked — walked through the "re-referenced after claim" unwind and consumed the CURRENT owner's candidate. Nothing about the candidate changes in that race (same block, same `P`, same `candidate_at`), so the exact-`P` CAS cannot refuse it; the wrapper now returns the outcome and settlement requires `BlockReleaseReleased`. R16 is GREEN only with both entrances closed — `BlockClaimFreshOwner` at the claim and this one at the release. Landed with it: the post-claim stub branches (driven by an ordinary read the claim had already contradicted in the serial domain, and DLQ-bound with the fence up) are gone in favour of hand-back-and-postpone; `GCFailureCodeBlockAuthorityInvalid` was documented as postponing but was never in `shouldPostponeWithoutRetry`; and the grace postpone and an unnameable claim owner got their own codes. Keep `GC_ENABLED=false` on every replica in every DC.

**P4a evidence update (2026-08-27):** This supersedes every earlier P4a status wording in
this file and every earlier mutation count. The script output is authoritative.
`internal/integration/p4a_claim_authority_test.go` has four real-Cassandra legs: exact
ownership/takeover, physical ABA, retry under real CAS, and stale-claim release bound to
the observed incarnation. `scripts/p4a-mutation-validation.sh` prints its own total on a
clean run — cite that, not a number copied from prose.

**P4a claim visibility (2026-08-28):** After an uncertain LWT, SERIAL seeing our own
`claimID` is not `BlockClaimAcquired`. Production confirms the canonical `blocks` row at
`EACH_QUORUM` (exact P, `deleting`, claim id, `claimed_at`) before granting destructive
authority — the same split P4b-1 uses for orphan `SameTarget`. Direct `applied=true`
stays Acquired. That confirmation read requires effective `blocks.read_repair=BLOCKING`
(empty is not the default); source/schema and `SESAMEFS_REQUIRE_P4A_EVIDENCE=1` pin it.
Mutations `m_settled_own_claim_skips_each_quorum`,
`m_settled_claim_visibility_downgrades_to_local_quorum`,
`m_claim_loses_explicit_non_idempotent_pin`,
`m_claim_loses_zero_retry_policy`,
`m_claim_loses_non_speculative_policy` and
`m_blocks_disables_blocking_read_repair` hold those gates. The mock follows the same
exact classifier, including `claimed_at`, and the initial claim LWT explicitly disables
driver retries and speculative execution.

**Audit hardening files (2026-08-28):** `internal/gc/store_mock.go`,
`internal/gc/store_cassandra.go`, `internal/gc/p4a_claim_ownership_test.go`,
`internal/gc/p4a_claim_authority_guard_test.go`,
`scripts/p4a-mutation-validation.sh`, `docs/TESTING.md`, `docs/CHANGELOG.md` and
`CURRENT_WORK.md`. Docker validation passed the full 50-mutation P4a/R26 matrix and the
full short Go suite.

A fourth review pass closed ordinary post-claim `GetBlockInfo` errors and divergent
locators: each now releases the exact claim, preserves the candidate, and postpones
without consuming retry budget.

A **fifth** pass closed the other half of the same principle. Preserving the candidate
only preserves recovery while a work item can still carry it back, and five post-claim
unwinds — the global verify's non-availability branch, a non-canonical `storage_class`, an
empty/untrimmed `storage_key`, a failed `GetBlockStoreForOrg`, a rejected
`ValidatePhysicalLocator` — released the fence and then returned an ordinary error without
looking at whether the fence was still theirs. A late loser therefore spent the item's
retry budget, and at the cap `ItemBlock` reached a DLQ it never leaves, past a scanner day
cursor already advanced to `today-1`: candidate present, work item unreachable, foreign
fence standing. An item already near the cap needed ONE lost race. All five now return a
classified foreign-owner result on `BlockReleaseNotOwner` (`refuseRetryForForeignClaimOwner`
/ `releaseClaimThenFailWithRetry`), and `processOrg` leaves the stale queue row untouched,
while an attempt that still owns its claim spends retries exactly as before, so a permanent
item defect still reaches the DLQ where a human sees it. Gated by
`TestP4A_ForeignOwnerUnwindDoesNotSpendTheRetryBudget`,
`TestP4A_OwnedUnwindStillSpendsTheRetryBudget`,
`TestP4A_LateLoserDoesNotTouchAnAlreadyAdvancedQueueRow` and
`TestP4AForeignOwnerQueuePolicyPrecedesGenericLifecycle`, plus two new mutations.

The same pass found the mutation gate itself half-open: `m_enqueue_item_mints_block_candidate`
matched literal `\n\t\t` against a CRLF working tree, so it silently applied NOTHING and
aborted the run before the remaining mutations were reached. Fixed to whitespace runs, per
the rule the script's own header states.

P4a remains R14a GREEN / R16 GREEN / R20 claim-side PARTIAL. P4b-2 now carries that
claim authority through orphan publication and finalize: `CommitBlockDeleteOrphanHandoff`
is the irreversible commit on `blocks`, resume is `CommittedOwner` of the stored D, and
`SameAuthority` / `DifferentAuthority` replace same-P-only `SameTarget`. **Explicitly still
open:** X1 itself, R18/R27, P4c-orphan PK, and enabling GC. `GC_ENABLED=false` remains required.

**Queue lifecycle review update (2026-08-27):** The attempted generic LWT hardening of
`RequeueItem` is withdrawn from this branch. `CompleteItem`, `FailItem` and `RequeueItem`
must not be presented as one atomic lifecycle: the first two use ordinary batches and a
partial CAS on requeue would create a false guarantee. `RequeueItem` is restored to the
ordinary logged `DELETE(old)` + `INSERT(new)` path; its concurrent race is a documented
follow-up, not a P4a closure claim.

The mergeable late-loser rule is narrower. `blockClaimForeignOwnerError` is handled by
`processOrg` before generic logging, retry, postpone or DLQ handling. The stale worker calls
none of `CompleteItem`, `RequeueItem`, `FailItem` or retry mutation, and its queue identity,
candidate and retry count remain unchanged. The five P4a ownership/unwind fixes remain
active, including the owned path's normal retry/DLQ behavior. `GC_ENABLED=false` remains
required while the follow-up chooses one authority for `Requeue`/`Complete`/`Fail`, DLQ and
pending state.

**P4b-1 update (2026-08-27):** `StartBlockDeleteOrphan` now publishes the canonical row
with a write-once `EachQuorum + Serial` LWT and no driver retry/speculation. Its result is
classified as `Created`, `SameTarget`, `DifferentTarget`, `NotPublished`, `Ambiguous`,
`Invalid`, `ProjectionUnconfirmed` or `LifecycleAdvanced`; same-target resumes use the stored `first_seen_at`
and repair the identity-only discovery projection only while the row is still `pending_s3`.
The worker only releases/postpones a
confirmed conflict or absent settled row; uncertain, malformed, unconfirmed projection
or advanced-phase states retain claim, candidate and queue lifecycle. Publication-invalid has its own
failure code: the untouched check runs before the postpone check, so reusing
`block_authority_invalid` would have silently taken the candidate-authority error off the
postpone path P4a had just put it on. Unit coverage and ten deliberate
mutations are green/red as required, and real-Cassandra evidence is gated by
`SESAMEFS_REQUIRE_P4B_EVIDENCE=1`. Write-once also drops the stale-phase reset.
A same-P row already at `pending_mapping_cleanup` no longer authorizes finalize; recovery
may still clear that completed-phase row without a physical delete. For that stale-phase
ambiguity itself the trade stays leak-biased until R14b binds incarnation. `SameTarget`
confirms canonical `EACH_QUORUM` visibility before finalize and does not renew TTL
(R28: crash-retry still authorizes Finalize because `Created` already inserted the
fence; remaining TTL is not a lease). `NotPublished` requires SERIAL-confirmed absence.
With a datacenter down, P3 accepts `BlockClaimAmbiguous` (err may be nil after
SERIAL settlement of an unowned row; when SERIAL sees our claim but
`EACH_QUORUM` confirmation is unavailable, the outcome is Ambiguous with a non-nil
error) and orphan `NotPublished` or `Ambiguous`; never claim
`Acquired` or orphan `Created`/`SameAuthority`. P4b-2 then binds that publication to the
exact P4a claim authority. `GC_ENABLED=false` remains required.

**P4b-2 update (2026-08-28):** R14b is GREEN. Migration `019` adds `blocks.gc_orphan_handoff`
(null until commit, never written false) and `gc_s3_orphans.gc_claim_id` / `gc_claimed_at`.
Migration `020` adds `gc_block_delete_lifecycles` (PK `((org_id, block_id), claim_id)`,
phases `published`/`terminal`, never DELETE). Do not fold 019 or 020 into
`001_initial_schema.cql`. `CommitBlockDeleteOrphanHandoff` is the irreversible commit
(EACH_QUORUM + SERIAL, exact `(P,D)`). `AlreadyCommitted` and `CommittedOwner` require
canonical `EACH_QUORUM` visibility; an empty non-applied handoff CAS SERIAL-settles.
After commit: no release, takeover, Complete, Requeue, Fail, or retry++. `CommittedOwner`
revalidates locator/store/topology, then treats `BlockHasReferencesGlobal` as a
contradiction detector (error or refs>0 → `committed_pending`; R3 stays OPEN). Orphan
publication INSERTs the lifecycle tombstone first and SERIAL-re-reads it after the
orphan INSERT; terminal D cannot recreate an authorizing orphan. Finalize
`AlreadyFinalized` requires an exact published `(P,D)` certificate and does **not**
authorize S3 — only applied `Finalized` does. Missing/mismatch/garbage certificates
fail closed. `RecoverS3Orphans` `pending_s3` SERIAL-observes D before S3 and will not
DELETE against terminal (stale-orphan clear is allowed). Production `DeleteS3Orphan`
runs only after `published → terminal`. Never-delete lifecycle partitions grow by one
row per D (operational follow-up). Evidence: unit + `scripts/p4b-authority-mutation-validation.sh`;
real Cassandra `internal/integration/p4b_claim_orphan_authority_test.go` under
`SESAMEFS_REQUIRE_P4B_EVIDENCE=1`. 3-DC P3 fail-closed: with dc-na down,
`CommitBlockDeleteOrphanHandoff` from dc-eu is Ambiguous, not
Committed/AlreadyCommitted. P4b-1's write-once script stays
`scripts/p4b-mutation-validation.sh`. X1 stays OPEN (winner-`Finalized` Physical ABA
is not closed here). `GC_ENABLED=false`.

**G2 update (2026-09-08):** PR #209 implements the D0 PREPARED-to-COMMITTED
handoff after G1. Root recovery directly settles PREPARED rows and the root token
is write-once; this does not implement G3-G5 or enable destructive GC.

**D0 update (2026-09-02):** #199 (strict non-overlap characterization) and #200
(BorrowedFS HEAD characterization) are merged as evidence-only PRs. #199 already
reasoned with independent physical lives after #185; it characterized a
conservative keep-`blocks(P1)`-until-delete strategy, which D0 supersedes
because durable exact handoff keeps authority on P1 without leaving `blocks(P1)`
canonical through cleanup. G3 Finalize vacates `blocks(L)` while `orphan(Pold)`
still fences writers; productive `blocks=P2` + `orphan=P1` is G4. Source of
record: [`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](../GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).
**Still CURRENT/TRANSITIONAL:** orphan `(org,L)` writer fence, Finalize-before-S3,
recovery ref re-check, and the P4c-orphan PK. G1's exact `(P,D)` recovery identities have no TTL; G2's PREPARED/COMMITTED handoff is implemented on this branch but does not activate GC. R31 OPEN. H not
reclassified. R18/R27 pending re-evaluation. 020 stays. Activation remains A1
after E1. `GC_ENABLED=false`.

**D0 follow-up (2026-09-02):** G3 vacates `blocks` while writers stay fenced until
G4. `PHYSICAL_COMPLETE` is confirmed DELETE only (ambiguous stays COMMITTED).
PREPARED abort CAS-revokes exact D's commit capability before removing the
orphan; a SERIAL non-commit observation while D1 still owns must not delete
it. After Abort returns not-owner, classify exact D1 (promote, settle that
PREPARED, or fail closed) so Case A/B converge without TTL. G2 must not create
durable PREPARED until G1 supplies a restart-findable recovery root; G5 is
later scheduling hardening. W2's irreversible
frontier is `D committed`, not zero-proof. Minted lives are `K1 != K2`. D0
does not claim “no physical ABA”; H stays OPEN.

### Inter-session Update (2026-05-21)

- PR61 focus is desktop sync conflict hardening and closeout, not audit-log expansion.
- Sync HEAD promotion now has branch-parity coverage for both `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch`.
- The hardening baseline is now verified for same-tree idempotence, safe non-overlapping auto-merge, and fail-closed retryable `503` behavior for unsafe conflicts.
- Real active-active desktop proof now exists via two `seaf-cli` clients in Docker hitting separate backend nodes.
- `./scripts/test.sh sync` now chains the single-client sync suite and the active-active harness; it stops on the first failing suite by default, with `--keep-going` available when you need aggregate failure reporting.
- `./scripts/test.sh` now also prints failure excerpts for compose-backed and script-backed suites, so Docker/test-runner failures are immediately visible without digging through full logs.
- Sync cleanup was tightened to release/delete stale `sync-test-*` and `sync-aa-*` libraries so the suite no longer drifts into `Library limit reached` failures.
- A broader canonical quota-reservation prototype was audited and explicitly split out of this branch; the confirmed defects are documented in `docs/KNOWN_ISSUES.md`, and PR61 should keep only the test-runner improvement from that line of work.
- Remaining sync follow-up debt is narrower: deeper-tree active-active branches, quota rejection during auto-merge, and broader 3-node/org-level quota contention races.

### Step 2: Before Making ANY Code Changes
- ✅ Check `docs/IMPLEMENTATION_STATUS.md` - Is component 🔒 FROZEN?
- ✅ If FROZEN → DO NOT MODIFY without explicit user approval
- ✅ If ✅ COMPLETE → Modify with caution, verify tests pass
- ✅ If 🟡 PARTIAL / ❌ TODO → Safe to actively develop

### Step 3: At End of Session - Update Documentation
**📋 MANDATORY: Run [docs/SESSION_CHECKLIST.md](SESSION_CHECKLIST-2026-02-02.md)**

---

## Current Branch Summary ✅

**Date**: 2026-05-21
**Focus**: Desktop Sync Conflict Hardening + Docker-first Validation + PR61 Closeout

### Completed In This Branch Slice

- Hardened sync HEAD publish behavior to distinguish safe same-tree retries from unsafe divergent conflicts.
- Added integration proof for both handler entry points: `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch`.
- Added multi-instance convergence coverage for parent-promotion races during retry.
- Added a real-client active-active harness with safe auto-merge and unsafe `503` scenarios.
- Folded the active-active harness into `./scripts/test.sh sync` and made the wrapper fail-fast by default.
- Fixed `--keep-going`, active-active `--keep`, and sync test cleanup/capacity drift.
- Realigned the docs so status and testing guidance no longer contradict the code and current validation surface.

### Remaining Follow-up Debt

- Extend desktop sync coverage into deeper-tree active-active branches and quota rejection during auto-merge.
- Add broader multi-node quota-race coverage beyond the current per-user concurrent upload test.
- Keep `CURRENT_WORK.md` and related status docs synced whenever the branch focus shifts; this file had drifted badly enough to become misleading.

## Historical Session Summary ✅

**Date**: 2026-03-31
**Focus**: Frontend/Backend Split Audit + Nginx Production Hardening + Bug Fixes

### Completed This Session (Session 59)

#### Nginx frontend container — 6 production bugs fixed ✅
All were silent failures that would only surface under production load:
- `client_max_body_size 100G` at server block (was missing → nginx default 1MB blocked large uploads)
- Proxy timeouts: `proxy_read_timeout 3600s`, `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s` at server block
- `proxy_buffering off; proxy_request_buffering off` on transfer routes (`/d/`, `/u/d/`, `/lib/`, `/repo/`, `/seafhttp/`)
- `proxy_http_version 1.1` + `proxy_set_header Connection ""` on all proxy locations (HTTP/1.1 keepalive)
- `sendfile on; tcp_nopush on; tcp_nodelay on` at server block
- `gzip_vary on; gzip_comp_level 6`

#### Nginx production reverse proxy — improvements ✅
- Upstream keepalive: `keepalive 32/16/8` on all 3 upstream blocks
- Separate rate limit zone for file transfers (`transfer` zone 20r/s vs `api` zone 100r/s)
- Content-Security-Policy header added
- All `add_header` directives now use `always`
- `client_max_body_size 100G` (was 20G)
- Frontend location: `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s`

#### Bundle hash coupling fix ✅
- `internal/api/v2/sharelink_view.go` — `fetchBundleManifest()` fetches `asset-manifest.json` from
  frontend container at startup. 3-level fallback: HTTP → filesystem scan → hardcoded.
  `FRONTEND_URL` env var added to both docker-compose files.

#### Logout fixes ✅
- `internal/api/server.go` — `handleLogout` now calls `SessionManager.InvalidateSession(token)`
  before clearing cookie and redirecting (server-side session was never invalidated before)
- `frontend/src/components/common/logout.js` + `account.js` — clear `sesamefs_auth_token` and
  `custom_permissions_*` from localStorage on click (was left behind after backend redirect)

**Files changed**: `frontend/nginx.conf`, `nginx/nginx.conf.template`, `internal/api/v2/sharelink_view.go`, `internal/api/server.go`, `frontend/src/components/common/logout.js`, `frontend/src/components/common/account.js`, `docker-compose.yaml`, `docker-compose.prod.yml`, `docs/V1-PRODUCTION-ROADMAP.md`, `docs/CHANGELOG.md`, `CURRENT_WORK.md`

### Previous Session (Session 55) — Org Admin Panel + Superadmin Parity

**Date**: 2026-03-05

#### Org Admin Panel — Full Implementation ✅

Implemented complete org admin panel in `internal/api/v2/org_admin.go` with 50+ endpoints covering:

- **Users**: CRUD, password reset, owned/shared repos, search, import, invite (12 endpoints)
- **Groups**: CRUD, members, group libraries, search (13 endpoints)
- **Repositories**: List, delete, transfer, browse dirents (4 endpoints)
- **Trash Libraries**: List, clean, delete single, restore (4 endpoints)
- **Departments & Address Book**: List departments, full address book group CRUD with ancestors (6 endpoints)
- **Group Owned Libraries**: Create + soft-delete (2 endpoints)
- **Share Links**: List + delete with org ownership verification (2 endpoints)
- **Upload Links**: List + delete with org ownership verification (2 endpoints)
- **Devices**: Empty responses — no device table (3 endpoints)

**Performance fixes applied:**
- `resolveUsersMap()` — batch user resolution replacing N+1 queries
- No ALLOW FILTERING — `ListOrgGroupLibraries` iterates org libs + checks shares by partition key
- `sort.Slice` — replaced O(n²) bubble sort in `ListOrgRepos`
- Group quotas stored in `organizations.settings['group_quota_{groupID}']`

#### Superadmin Parity — Departments/Address Book/Group-Owned Libs ✅

Added 9 new endpoints to superadmin panel in `internal/api/v2/admin_extra.go`:
- `AdminListOrgDepartments`, `AdminListAddressBookGroups`, `AdminAddAddressBookGroup`
- `AdminGetAddressBookGroup` (with ancestors), `AdminUpdateAddressBookGroup`, `AdminDeleteAddressBookGroup`
- `AdminAddGroupOwnedLibrary`, `AdminDeleteGroupOwnedLibrary`
- `AdminUpdateGroupMemberRole`

Routes registered in `internal/api/v2/admin.go`.

#### Documentation Updated ✅

- `docs/ADMIN-FEATURES.md` — Added §4 (Superadmin departments), §5 (Org Admin Panel full docs), §6 (Parity table)
- `docs/ENDPOINT-REGISTRY.md` — Registered all 50+ org admin + 9 superadmin endpoints
- `docs/IMPLEMENTATION_STATUS.md` — Updated admin panel rows, added org admin entry, updated metrics
- `CURRENT_WORK.md` — This update

**Files changed**: `org_admin.go`, `admin.go`, `admin_extra.go`, `departments.go`, `ADMIN-FEATURES.md`, `ENDPOINT-REGISTRY.md`, `IMPLEMENTATION_STATUS.md`, `CURRENT_WORK.md`

### Previous Session (Session 54) — Upload File Replace/Autorename Fix

**Problem**: `replace=0` in upload was not triggering auto-rename (`file (1).ext`), default was overwriting.
**Fix**: Updated upload handler to check `replace` param correctly.

### Previous Sessions (53 and earlier — see docs/CHANGELOG.md)

- **Session 53**: Admin trash libraries 405 fix + cleanup handler + orphan data documentation
- **Session 52**: Retrocompat fix — pre-index users, admin `/sys/users/` multi-org fix
- **Session 45**: Superadmin script (`make-superadmin.sh`) + CreateOrganization seafile-js compat
- **Session 44**: Desktop client file browser fixes (oid header, upload/download protocol, trailing slash)
- **Session 33-34**: Admin share link + upload link management (13 endpoints) + verification
- **Session 32**: Bug fix sprint (5 bugs) + tag management enhancement
- **Session 30**: Snapshot view, revert with conflict handling
- **Sessions 22-29**: Admin panel, OIDC sync, File History UI, GC metrics, search, trash
- **Sessions 12-21**: GC, Monitoring, Departments, modal migration, move/copy fixes
- **Sessions 1-11**: Core API, tags, permissions, OIDC, library settings, OnlyOffice

---

## What's Next (Priority Order) 🎯

### 🔴 PRIORITY 1: PR61 Desktop Sync Hardening Closeout

**Status**: 🟡 Baseline verification is complete; the remaining work is merge hygiene and narrower follow-up coverage, not core sync correctness.
**Details**: `internal/api/sync.go`, `internal/integration/library_projection_regression_test.go`, `internal/integration/multi_instance_mutations_test.go`, `scripts/test-sync-active-active.sh`, `scripts/test.sh`

**Use this branch for:**

1. Running Docker-first closeout validation before merge (`./scripts/test.sh sync` first).
2. Extending the active-active sync matrix into deeper-tree conflicts or quota-rejection branches.
3. Keeping branch-status docs aligned with the validated behavior actually present in code.

**Do not let this branch drift into:**

1. Audit-log expansion.
2. Broad frontend cleanup unrelated to desktop sync.
3. New role/ownership behavior changes without a separate branch discussion.
4. Canonical quota reservation/resync experiments; keep that work in a dedicated follow-up branch.

### ✅ ~~PRIORITY 1: Admin Library Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 12 endpoints implemented in `internal/api/v2/admin.go`
**Details**: [docs/ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 1

All admin library endpoints implemented: list, search, get, create, delete, transfer, browse dirents, history settings, shared items, trash libraries. Frontend `seafile-api.js` methods already wired.

### ✅ ~~PRIORITY 2: Admin Share Link & Upload Link Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 13 endpoints across 5 files
**Details**: [docs/ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 2

Admin share link list/delete fixed; upload links full feature (DB tables, user CRUD, admin list/delete); per-user link endpoints; frontend API methods added.

### 🔴 PRIORITY 3: Audit Logs & Activity Logs — PRIORITIZE NEXT

**Status**: 🟡 Partial foundations exist: `audit_log` for deletion events and `users.last_login_at` for latest successful login, but no historical login/file activity dataset or APIs
**Details**: [docs/ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 3

**Two related systems need implementation:**

1. **Audit Logs** (admin-facing): Login logs, file access logs, file update logs, permission audit logs. Needed for compliance and admin visibility. Frontend pages exist at `frontend/src/pages/sys-admin/logs-page/` and `frontend/src/pages/org-admin/org-logs-*.js`.

2. **Activity Feed** (user-facing): The `/api/v2.1/activities/` endpoint currently returns stub `{"events": []}`. The dashboard activities feed and file activity panels depend on this. Frontend components exist (`frontend/src/pages/dashboard/activity-item.js`, `frontend/src/models/activity.js`).

**What exists today:**
- `internal/middleware/audit.go` — 13 action types defined, `AuditEvent` struct, console-only logging, 8 unit tests
- `audit_log` table — persists deletion/compliance events for GC, groups, departments
- `users.last_login_at` — real latest-login timestamp updated on successful auth and exposed in admin/org-admin user responses
- Frontend UI components for both admin logs and user activity feed

**Next logical slice:**
- Implement `login_logs` first as the bridge between point-in-time `last_login_at` and real historical audit/reporting
- Reuse the successful-auth hooks already touching `users.last_login_at`
- Expose login history to the existing admin login-log pages before expanding to file/activity logs

**Explicit pending gap:**
- `/sys/statistics/file/` and `/org/statistics-admin/file/` still cannot be made real without `file_update_logs` and `file_access_logs`
- That dependency is separate from `login_logs`; login audit work does not unblock file-operation charts by itself
- There is also a confirmed scope bug in org-admin statistics: traffic-based org-admin metrics can resolve to platform-wide aggregates when the org-admin shell is mounted with platform-org context

**Remaining full backlog:**
- 5 dedicated Cassandra tables (login_logs, file_access_logs, file_update_logs, permission_audit_logs, activities) with 90-day TTL
- New `internal/api/v2/audit.go` handler file (~5 endpoints)
- Async DB write integration (buffered channel pattern) across ~15 existing handlers
- Wire up frontend pages to real API endpoints

### ~~🟡 PRIORITY 4: File History UI Wiring~~ — ✅ COMPLETE (Session 23)

Detail sidebar now has Info | History tabs for files. Full-page history also works. Integration tests: 17 assertions passing.

### 📋 PRIORITY 4: Test Coverage Improvement

**Status**: Go integration test framework built (Session 24), coverage gaps identified

**Current unit test coverage** (from `go test -cover`):
| Package | Coverage | Lines | Priority |
|---------|----------|-------|----------|
| `internal/crypto` | 90.8% | ~600 | ✅ ABOVE THRESHOLD (was 69.6%) |
| `internal/api/v2` | 20.5% | 14,136 | HIGH — biggest codebase, most untested |
| `internal/api` | 19.1% | 4,769 | HIGH — sync protocol edge cases |
| `internal/db` | 0% | 1,139 | MEDIUM — all DB access only via integration |
| `internal/middleware` | 42.1% | 752 | MEDIUM — permission logic |
| `internal/storage` | 46.4% | 1,561 | MEDIUM — S3/block edge cases |
| `internal/templates` | 0% | 327 | LOW — email rendering |
| `internal/logging` | 0% | 66 | LOW — instrumentation |
| `internal/metrics` | 0% | 111 | LOW — instrumentation |

**Next steps** (in priority order):
1. **Add more Go integration tests** — share links, admin endpoints, groups, batch ops (parallels existing bash tests)
2. **DB interface mock** — define `Store` interface for `internal/db`, implement mock, unlock unit tests for all handlers
3. **API v2 handler unit tests** — error paths, validation edge cases in `files.go` (3,564 lines), `admin.go` (1,462 lines)
4. **Concurrent access tests** — race detector integration tests for simultaneous uploads/downloads
5. **testcontainers-go** — real Cassandra in CI for `internal/db` unit tests

**Frontend Testing Strategy** (7 test files currently, need expansion):
- Current: `utils.test.js`, `dirent.test.js`, `modal-pattern.test.js`, `seafile-api-tags.test.js`, `seafile-api-oidc.test.js`, `permission-checks.test.js`, `dirent-list-item.test.js`
- **Metrics to track**: Component coverage (% of components with tests), critical path coverage (login→upload→share flow), API mock coverage
- **Priority areas**: Dialog components (conflict dialogs, restore dialogs), API integration layer, permission-based UI visibility
- **Tools**: Jest + React Testing Library (already configured), consider adding Cypress for E2E

### 📋 PRIORITY 5: Frontend Cleanup (Lower)

- **ModalPortal Wrapper Cleanup** — ~51 parent components have unnecessary `<ModalPortal>` wrappers (harmless, cosmetic)
- **Frontend Permission UI** — ~60% complete, readonly/guest users still see some buttons they can't use

---

## Strategic Roadmap

### Phase 1: Production Blockers 🔴 — ALL COMPLETE ✅

| Item | Status | Notes |
|------|--------|-------|
| **OIDC Authentication** | ✅ DONE | Phase 1 complete |
| **Garbage Collection** | ✅ DONE | Queue worker + scanner + admin API |
| **Health Checks/Monitoring** | ✅ DONE | `/health`, `/ready`, `/metrics`, slog logging |

### Phase 2: Core Feature Completion

| Item | Status | Notes |
|------|--------|-------|
| **Admin Panel (Groups/Users)** | ✅ DONE | Option A (OIDC-managed). 16 endpoints + OIDC sync. 29 tests. |
| **Admin Library Management** | ✅ DONE | 12 endpoints in admin.go. See [ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 1 |
| **Admin Link Management** | ✅ DONE | Share + upload links. 13 endpoints. See [ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 2 |
| **Superadmin Departments/Address Book** | ✅ DONE | 9 endpoints. See [ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 4 |
| **Org Admin Panel** | ✅ DONE | 50+ endpoints. Full parity with superadmin. See [ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 5 |
| **Org Delete (3-state lifecycle)** | ✅ DONE (backend) | active → deactivated → deleted (30-day grace → cascade). Frontend TODO: ISSUE-FRONTEND-ORG-DELETE-01 |
| **Audit Logs** | ❌ TODO | 5 tables, ~5 endpoints, ~15 handler integrations. See [ADMIN-FEATURES.md](../ADMIN-FEATURES.md) § 3 |
| **File History UI** | ✅ DONE | Detail sidebar History tab + full-page view. 17 integration tests. |
| **GC TTL Enforcement** | ✅ DONE | Scanner Phase 5 (version_ttl_days) + Phase 6 (auto_delete_days) + share link deletion |
| **Frontend Modal Migration** | ✅ 122/122 | All done; ~51 ModalPortal wrappers to clean up |
| **Library Settings Backend** | ✅ DONE | History, API tokens, auto-delete, transfer |
| **Department Management** | ✅ DONE | Admin CRUD + hierarchy, 29 integration tests |
| **Frontend Permission UI** | 🟡 ~60% | Hide/disable based on role |

### Phase 3: Already Complete ✅

| Item | Status | Completed |
|------|--------|-----------|
| Sync Protocol | ✅ 🔒 FROZEN | 2026-01-16 |
| File Operations Backend | ✅ COMPLETE | 2026-01-27 |
| Batch Move/Copy | ✅ COMPLETE | 2026-01-27 |
| Sharing System | ✅ COMPLETE | 2026-01-22 |
| Groups Management | ✅ COMPLETE | 2026-01-22 |
| Department Management | ✅ COMPLETE | 2026-01-31 |
| Admin Panel (Groups/Users) | ✅ COMPLETE | 2026-02-02 |
| OIDC Group/Dept Sync | ✅ COMPLETE | 2026-02-02 |
| File Tags | ✅ COMPLETE | 2026-02-12 (cascade+rename) |
| Permission Middleware | ✅ COMPLETE | 2026-01-27 |
| OnlyOffice Integration | ✅ 🔒 FROZEN | 2026-01-29 |
| Search | ✅ COMPLETE | 2026-01-22 |

### Phase 4: Future Features (Lower Priority)

| Item | Priority | Notes |
|------|----------|-------|
| Thumbnails | LOW | Visual polish |
| File Comments | LOW | Collaboration feature |
| Watch/Unwatch | LOW | Needs notification system |
| Multi-region Replication | LOW | Future scaling |

---

## Frozen/Stable Components 🔒

**Freeze procedure**: See [docs/RELEASE-CRITERIA.md](RELEASE-CRITERIA-2026-03-17.md) for the formal stability rules and Component Test Map. Components need ≥ 80% Go coverage, ≥ 90% integration endpoint coverage, zero open bugs, and 3 clean sessions in 🟢 RELEASE-CANDIDATE before reaching 🔒 FROZEN.

### ⚠️ CRITICAL: Sync Code FROZEN (2026-01-19)
**User directive**: DO NOT MODIFY sync code without explicit approval

### Code Files - Sync Protocol 🔒
- `internal/api/sync.go` (lines 949-952, 125-130, 1405-1492) - Protocol formats
- `internal/api/v2/encryption.go` - Password endpoints

### Code Files - Crypto 🔒 (Frozen 2026-02-04)
- `internal/crypto/crypto.go` - PBKDF2, Argon2id, AES-256-CBC (90.8% unit test coverage, 39 tests)

### Code Files - Monitoring/Health 🔒 (Updated 2026-02-04)
- `internal/health/health.go` - Liveness and readiness probes 🔒
- `internal/metrics/metrics.go` - Prometheus metric definitions (GC metrics expanded Session 28)
- `internal/metrics/middleware.go` - Request metrics middleware 🔒
- `internal/logging/logging.go` - Structured logging setup 🔒

### Code Files - OnlyOffice 🔒 (Frozen 2026-01-29)
- `internal/api/v2/fileview.go` - File view auth wrapper + OnlyOffice editor HTML (json.Marshal config). Note: History download handler added (Session 25) — OnlyOffice code paths unchanged.
- `internal/api/v2/onlyoffice.go` - OnlyOffice API endpoint + JWT signing + editor callback

### Code Files - Web Downloads (Updated 2026-02-16)
- `internal/api/seafhttp.go` - `streamFileFromBlocks()` (primary download path — prefetch pipeline, 4MB buffers)
- `internal/api/seafhttp.go` - `HandleDownload()` (token validation, 4MB streaming buffer)
- `internal/api/seafhttp.go` - `addFileToZip()` (ZIP Store method, batch block resolve, 4MB buffers)
- `internal/api/seafhttp.go` - `resolveBlockIDs()` (batch Cassandra IN queries, 100/batch)
- `internal/api/v2/fileview.go` - `ServeRawFile()` / `DownloadHistoricFile()` (batch resolve + 4MB buffers)
- `internal/api/v2/sharelink_view.go` - Share link raw file streaming (batch resolve + 4MB buffers)
- `internal/storage/s3.go` - Custom HTTP transport (64 conn/host, 128KB read buffers)
- ⚠️ `getFileFromBlocks()` is DEPRECATED — kept only for upload metadata path

### Frontend Components 🔒 (Frozen 2026-01-23)
- `frontend/src/pages/my-libs/` - Library list view
- `frontend/src/pages/starred/` - Starred files & libraries
- `frontend/src/components/dirent-list-view/` - File download functionality

### Protocol Behaviors 🔒
- fs-id-list: JSON array (NOT newline-separated)
- Commit objects: OMIT `no_local_history` field
- `encrypted` field: integer in download-info, string in commits
- `is_corrupted` field: integer 0 (NOT boolean)
- `/seafhttp/` auth: `Seafile-Repo-Token` header (NOT `Authorization`)

---

## Critical Context for Next Session 📝

### 🎯 Project Goal
**Mission**: Build complete Seafile replacement ready for production
**Target Users**: Global cloud storage, especially needing China access
**Timeline**: ASAP but thorough - "want it soon, do it right"

### 📊 Current State (Updated 2026-03-05)
- **Sync Protocol**: 100% working, desktop clients fully compatible 🔒 FROZEN
- **Backend API**: ~98% implemented — OIDC ✅, GC implementation present; destructive activation blocked by X1 and the PRE-GC / A1 gate (X2 closed 2026-08-14), Library Settings ✅, OnlyOffice ✅, Tags cascade ✅, Org Admin Panel ✅, Superadmin Departments ✅
- **Frontend UI**: ~83% functional (all modals migrated, folder icons ✅, ~51 ModalPortal wrappers to clean up)
- **Production Ready**: blocked for destructive GC until X1 closes and then the PRE-GC / A1 gate ([docs/X1-CRITICAL-PATH.md](../X1-CRITICAL-PATH.md) §6) closes (X2 closed 2026-08-14); keep `GC_ENABLED=false` on every replica/DC
- **Admin Panels**: Both superadmin and org admin at feature parity
- **Active Bugs**: tracked canonically in `docs/KNOWN_ISSUES.md`; GC activation needs X1 and then the PRE-GC / A1 gate (X2 closed)

### Critical Facts to Remember

**Permissions System** (UPDATED 2026-01-27):
- Backend: ✅ 100% COMPLETE - All endpoints check permissions
- Frontend: 🟡 ~30% - "New Library" button done, many features remain
- API returns: `can_add_repo`, `can_share_repo`, `can_add_group`, etc.
- Check `window.app.pageOptions.canAddRepo` in render methods

**User Roles**:
- `admin` → Full access, `is_staff: true`
- `user` → Can create libraries, share, upload
- `readonly` → View only, no write operations
- `guest` → Most restricted, view only

**Test Users** (password: `password` for all):
- `admin@sesamefs.local` (token: `dev-token-admin`)
- `user@sesamefs.local` (token: `dev-token-user`)
- `readonly@sesamefs.local` (token: `dev-token-readonly`)
- `guest@sesamefs.local` (token: `dev-token-guest`)

---

## Documentation Map 📚

### Session Continuity (Read First Every Session)
- **[CURRENT_WORK.md](../../CURRENT_WORK.md)** - This file - Session state, priorities
- **[docs/KNOWN_ISSUES.md](../KNOWN_ISSUES.md)** - Detailed bug tracking
- **[docs/CHANGELOG.md](../CHANGELOG.md)** - Session history
- **[docs/IMPLEMENTATION_STATUS.md](../IMPLEMENTATION_STATUS.md)** - Component stability matrix

### Protocol & Sync (🔒 Reference Implementation)
- **[docs/SEAFILE-SYNC-PROTOCOL-RFC.md](../SEAFILE-SYNC-PROTOCOL-RFC.md)** - Formal RFC with test vectors 🔒
- **[docs/ENCRYPTION.md](../ENCRYPTION.md)** - Encrypted libraries, PBKDF2, Argon2id

### Implementation Guides
- **[docs/API-REFERENCE.md](../API-REFERENCE.md)** - API endpoints, implementation status
- **[docs/ENDPOINT-REGISTRY.md](../ENDPOINT-REGISTRY.md)** - ⚠️ CHECK BEFORE ADDING ENDPOINTS
- **[docs/FRONTEND.md](../FRONTEND.md)** - React frontend patterns, modal fixes
- **[CLAUDE.md](../../AGENTS.md)** - Complete project context for AI assistant

---

## Quick Commands

```bash
# Run server
docker compose up -d sesamefs frontend

# Rebuild after changes
docker compose build --no-cache sesamefs frontend && docker compose up -d

# Test API with different users
curl -H "Authorization: Token dev-token-admin" http://localhost:8082/api2/account/info/
curl -H "Authorization: Token dev-token-readonly" http://localhost:8082/api2/account/info/

# Run tests (ALWAYS use test.sh)
./scripts/test.sh api              # Bash integration tests (335+ assertions)
./scripts/test.sh go               # Go unit tests
./scripts/test.sh go-integration   # Go integration tests (requires backend)
./scripts/test.sh all              # Everything
./scripts/test.sh api --quick      # Skip slow tests
```

---

## End of Session Checklist

**📋 See [docs/SESSION_CHECKLIST.md](SESSION_CHECKLIST-2026-02-02.md) for complete checklist**

Quick reminders:
- [x] Update `CURRENT_WORK.md` (what was done, next priorities)
- [x] Update `docs/KNOWN_ISSUES.md` (bugs fixed/discovered)
- [x] Update `docs/CHANGELOG.md` (add session entry)
- [x] Keep `CURRENT_WORK.md` under 500 lines

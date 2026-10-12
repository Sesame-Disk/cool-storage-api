# Open Work Index

## Abandoned repair retention moved to PRE-GC (2026-10-12)

[ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01](./KNOWN_ISSUES.md#issue-publish-repair-dead-row-retention-01-a-deadunreachable-publications-repair-row-surviving-request-local-cleanup-can-be-retained-and-repeatedly-re-pinned-indefinitely) is
now P2 PRE-GC (was FOLLOW-UP / GENERAL), with a measure-first plan. Phase 1
uses the existing metrics ([runbook](./PUBLISH-REPAIR-OBSERVABILITY.md#abandoned-repair-retention-pre-gc-measurement)).
Any code comes later and only if the data shows real retention. Not X1.

## E1-16 post-D contract alignment (2026-10-10)

The X1 invariant is physical (G4 contract 5): a logical reference of L after
COMMITTED D(P1) is not a P1 dependency. W2-1..9 and W2-12..14 are CLOSED for
X1, and W2-10 stays OPEN. E1-15A/B and the R31 convergence residuals move from
PRE-X1 to PRE-GC with severity unchanged, so they still block A1. Before X1
CLOSED: W2-10 (E1-17), E1-01, the three-DC residuals (suite re-measured 2026-10-10), the D0 §25
reclassification list and the identity claim-table PRE-X1 audit debt.
See [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md). Older dated sections below
are snapshots; X1 §4 is current. The three-DC re-measurement registered
[ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01](./KNOWN_ISSUES.md#issue-seafhttp-upload-finalize-lease-error-500-01)
as a P2 CURRENT-RUNTIME availability follow-up (intermittent 500 on a
concurrent SeafHTTP finalize; cause unlogged).

## PR #258 resource follow-up (2026-10-05)

[ISSUE-FILEFROMBLOCKS-WORKER-FANOUT-01](./KNOWN_ISSUES.md#issue-filefromblocks-worker-fanout-01)
is P2 GENERAL / OPEN, pre-existing: verifyManifestBlocks and
ensureCommitBlockOwnLiveness still create workers before semaphore admission.
Separate from the capture/final-validator bounds in #258.

## PR #258 source-removal identity follow-up (2026-10-05)

[ISSUE-CROSSREPO-MOVE-SOURCE-IDENTITY-RACE-01](./KNOWN_ISSUES.md#issue-crossrepo-move-source-identity-race-01)
is P1 GENERAL / OPEN, pre-existing: after copying FS-A, move can reanchor
source cleanup to a replacement FS-B and remove it by name without checking
its fs_id. Separate from E1-09 physical-life safety and existing move partial
success; not fixed or closed by #258.

## Greenfield first-production reconciliation (2026-09-30)

W2-0 is **CLOSED-EVIDENCE for the covered current-version mechanism** under
the [first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The #239 durable repair / final exact-P ordering and #241 native wire loss,
real SIGKILL, independent recovery and three-DC evidence are preserved.
Both incompatible mixed-version combinations still reproduce D(P)+HEAD;
ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 tracks them as P1 FOLLOW-UP / GENERAL,
outside v1 first activation. No compatibility gate is inferred or added.
W2-1, W2-2 and W2-6a remain OPEN through the separately tracked R31 residuals.
Other W2 rows are unchanged. W2 completion -> G4 -> G5 -> E1 -> X1 CLOSED ->
PRE-GC -> A1 -> GC ON remains the #201/#236 path; GC stays disabled.

The dated #238/#239 entries and characterization tables below are historical
evidence snapshots. Their old W2-0 OPEN / pending-evidence statements are
superseded by this reconciliation and the current X1 checklist; they do not
define today's closure state.

### PR #239 crossed audit correction (2026-09-30)

Pre-D GC now distinguishes real references, repair-only protection and zero.
Repair-only releases the exact claim and preserves candidate/discovery/queue,
postponing without retry. After COMMITTED, only actual references retain the
existing contradiction policy; a late repair cannot veto D. Both worker
regressions are included in 17 mandatory continuity legs. G2/G3 retirement and
durable physical continuation are proved; the future physical executor remains
outside this PR. W2-0/W2-6a remain OPEN and GC_ENABLED=false.
See [crossed audit evidence](./W2-0-PUBLICATION-CONTINUITY.md).

## W2-0 follow-up — durable repair gate (2026-09-29)

Covered F1/F2/F3 and Sync-provenanced writers now acquire a non-expiring repair
before final exact-P. GC reads every organization repair page at EACH_QUORUM
and repeats the reference probe after a negative scan; acquisition pins LQ.
Sync preserves its captured placement through queueing. UNKNOWN/unreachable
rows retain existing R31 behavior, so stranded repairs can inhibit collection.
W2-0/W2-6a remain OPEN: network ambiguity, OS process-kill and rollout evidence
are pending. Older GC readers must be upgraded; cold-path cost is up to 32
organization range reads plus a ref probe, proportional to pending rows.
See [plan, evidence, individual re-audit and limits](./W2-0-PUBLICATION-CONTINUITY.md).

**Last updated:** 2026-10-10 (E1-16 post-D contract alignment; see the top section). Previous: 2026-09-30 (greenfield W2-0 scope reconciliation; #240 was later closed without merge)
**X1 critical-path reset (2026-09-29):** [X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md) is the source of record for the order of work toward X1: W2 exit checklist → G4 → G5 → E1 → X1 CLOSED → PRE-GC list → A1. It supersedes the "PC-D1B.5 → productive consumer → PC-2" ordering in the entries below. PC-D1B.5 is PRE-GC only; the PublicationCoordinator and PC-D1 line are frozen and PC-2 is not an X1 prerequisite. Both library hard-delete lease issues were classified CURRENT-RUNTIME (the SERIAL-domain one config-dependent); PR #234 closes the SERIAL-domain one, the non-fencing one stays open. The A1 gate is every PRE-GC/PRE-ACTIVATION-tagged entry plus the §6 table. Findings are classified by the §3 rule (current runtime / current code under planned GC activation / nonexistent code = not a finding).
**PC-D1B.4 status (2026-09-24):** **DECISION CLOSED / MERGEABLE.** CW-M33's accepted-Paxos pause/barrier race, post-barrier SERIAL-present issuance race, and G21/G22 mutations are green/red as required on the isolated Cassandra 5.0.9 test image. CW-M31 remains closed for cross-DC visibility, with stable absence now requiring both SERIAL to observe absence and the EACH_QUORUM recheck. CW-M34 freezes the in-flight writer lifetime property and has model plus real 3-DC success-but-hidden characterization; runtime permits/recovery remain PC-D1B.5. CW-M27..CW-M34 decision findings are closed; PC-D1B.5 runtime remains required before GC activation (reclassified PRE-GC only by the 2026-09-29 reset; no longer a productive-consumer prerequisite). §14/§16 now include the SERIAL+EACH_QUORUM proof cost and distinguish normal local writer lifetime accounting from exceptional drain/recovery I/O. No runtime, schema, consumer, mapping-authority or GC change landed here.
**PR #231 closure status (2026-09-22):** the final Docker full integration profile and the short Go suite pass. Real-Cassandra evidence covers nullable fs_objects replay, placeholder completion, directory retry and delete, SHA1-only delete, zero-block delete, and durable claim retention. The mutation runner confirms the projection-access and dynamic-CQL bypass cases B22-B24 turn RED. The local profile skips tests requiring separate 3-DC host variables; see the PR #231 closure record in CURRENT_WORK.md. No #228, mapping-authority, or GC work was added to this PR.
**Scope (narrowed 2026-07-25):** production blockers, recent readiness /
upload-fence audit follow-ups, and leftovers from consolidating the parallel
pending-work trackers. **This is not the entire product backlog.** Roadmap /
product / UX items that are not defects live in
[V1-PRODUCTION-ROADMAP.md](./V1-PRODUCTION-ROADMAP.md),
[PLANS-AND-PERMISSIONS.md](./PLANS-AND-PERMISSIONS.md), and
[TECHNICAL-DEBT.md](./TECHNICAL-DEBT.md). The migration table at the bottom
records where every deleted-tracker item went.

## How this repo tracks work

Three layers, and each has exactly one job. Most of the contradictions this index
was created to fix came from a finding living in two layers at once with only one
of them updated.

| Layer | File(s) | Owns | Does **not** own |
|---|---|---|---|
| **Registry of record** | [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) | The `ISSUE-*` id, **current** status, severity, fix direction | The deep reasoning behind a finding |
| **Audit / analysis docs** | [PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md), [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), `SECURITY-ASSESSMENT-*`, `UPLOAD-*` | Evidence, severity rationale, why alternatives were rejected, verification performed | Live status — audits are dated snapshots (`Status as of YYYY-MM-DD`) that link to `ISSUE-*` |
| **This index** | `OPEN-WORK-INDEX.md` | The one-screen list for **this scope** and the cross-references / migration table | Full product backlog; any detail beyond one line |

**Rules that keep it honest:**

1. A finding gets **one** `ISSUE-*` id, even when two audits found it
   independently. When that happens, say so in the issue (see
   `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`, which umbrellas B4 and X10 with
   closable subcontracts).
2. **Status changes go in `KNOWN_ISSUES.md` only.** An audit document is a
   snapshot of a date; do not retro-edit its verdict — add a dated note. Registry
   Open/Closed tables and the PR-plan status table are **historical / PR-progress**
   views, not competing live trackers.
3. Cite code by **symbol name**, not `file.go:1234`. Line numbers rot.
4. A finding must not appear in multiple status tables with independently
   maintained or contradictory states. An executive summary may **reference** the
   canonical row (e.g. "also listed under Production Blockers"); do not maintain
   two competing status columns for the same id.
5. Do not claim this index lists "everything open in the repo". It lists the
   scope in the header. Use the migration table for deleted-tracker provenance.
6. **Effective config = `configs/*.yaml` + the environment. Never conclude
   anything from the YAML alone.** `Config.Load()` parses YAML, then
   `applyEnvOverrides()` replaces selected fields from the environment, then
   `Validate()` checks the result — `Validate()` does **not** apply overrides.
   Prod runs `config.prod.yaml` *with* a prod `.env` derived from
   `.env.prod.example`. The YAML may hold inert placeholders (e.g.
   `replication_dcs: {datacenter1: 1}`) while the env template ships the real
   multi-DC posture (`CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`).
   Before calling a config value stale, read **both** the YAML and
   `.env.prod.example`, and confirm the symbol path in `applyEnvOverrides()`.
   The inverse also matters: some fields (`chunked_staging_max_bytes`,
   `server.max_upload_mb`) have **no** env override, so for those the YAML
   really is the whole story — see `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`.

## Current G1 PR Audit

PR #207 implements the G1 exact `(P,D)` orphan identity and durable recovery-root
surface. Its audit evidence is recorded in the PR validation results. The
implementation uses the durable block-delete lifecycle CAS as the write-once
`first_seen_at` token, keeps root settlement projection-first, bounds root pagination,
normalizes recovery scheduling
to UTC days, and keeps root errors separate from `_by_day` cursor errors.

The PR remains subject to the separate X1 destructive-GC activation gate and must
not be treated as permission to set `GC_ENABLED=true`.

---

## Production blockers — must close before go-live

**Single-node go-live still has open HIGH findings, and they are independent of
X1.** Corrected 2026-08-22: this summary previously read "no single-node go-live
blockers remain", which contradicted the HIGH rows in this document's own
High/Medium table a few lines below. Do not restore that claim.

Three gates, kept separate on purpose:

- **Enabling destructive GC** — blocked by X1
  (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`) and then by the PRE-GC / A1 gate ([X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md) §6);
  X1 closure alone does not authorize activation. X2 closed 2026-08-14.
  See the GC section below.
- **Single-node go-live** — blocked by the resource-amplification finding
  below (`ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`) and the API-key mutation-scope
  bypass (`ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01`).
  `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01` was fixed 2026-10-05 (see
  Recently closed). Nothing about GC gates
  these, and closing X1 does not close them. `ISSUE-ZIP-STREAM-LATEFAIL-01` is
  Medium per the registry, not a go-live blocker.
- **Multi-instance operation** — additionally blocked by the two node-local
  state issues in the table below.

What genuinely closed: NF-1 2026-07-25; B4 2026-08-04; the object-storage
posture issue and the sync public-link token auth gap 2026-08-07;
`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01` 2026-08-22. See
[PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md)
(dated snapshot) and
[PROD-READINESS-VERIFICATION-20260822.md](./PROD-READINESS-VERIFICATION-20260822.md)
(baseline re-verification at `a1570b186`, with the selected findings last verified
at committed snapshot `05197691c`; later corrections are outside that snapshot's
provenance boundary).

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | HIGH | Chunked-upload state is node-local; non-sticky routing silently loses files | Readiness B1 — **multi-instance only** |
| `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | HIGH | Desktop-SSO pending token is in-memory per process | Readiness B5 — **multi-instance only** |

The single-node HIGH rows are **not duplicated here**; their canonical rows live
in the High/Medium table below, per rule 4.

### Recently closed

Kept briefly so a reader who arrives with the old blocker list can see it moved
rather than vanished. Drop rows once they stop being recent; status of record
stays in [KNOWN_ISSUES.md](./KNOWN_ISSUES.md).

- `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01` — **Fixed 2026-10-05** (HIGH,
  single-node). `recv-fs` now bounds the decompressed side: 16 MiB per object (the
  default Cassandra CQL request limit; an inflation bound, not a persistence
  guarantee) and 64 MiB per request (representative client batches; a highly
  repetitive batch under the client's 1 MiB compressed threshold can still
  exceed it). Over either
  cap is a 413, never a truncated object. One ~1 MiB request inflating to 256 MiB
  cost 1950.6 MiB of allocation before and 102.9 MiB after. The aggregate across
  concurrent requests remains `ISSUE-SYNC-METADATA-CONCURRENCY-01`.
- `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` — **Fixed 2026-08-12** (readiness SEC-3 /
  NF-3). The `sesamefs_auth` cookie is `httpOnly=true` on every OIDC login and
  logout writer, funneled through one `setAuthCookie` helper per package. Verified
  first that nothing in this repository reads it from JS. `Secure` is untouched —
  that is `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01`, still open.
- `ISSUE-SYNC-UNBOUNDED-BODIES-01` — **Fixed 2026-08-12** (registry X9). All four
  remaining sync handlers read through `readLimitedRequestBody`; `pack-fs` and
  `check-fs` also gained the id-count cap that stops a body under the byte cap from
  expanding ~17x. It bounds one request body at a time and nothing more — and for
  `RecvFS`, only the **compressed** body at that. The three residuals are all above:
  the aggregate term is `ISSUE-SYNC-METADATA-CONCURRENCY-01`,
  the inflate is `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`, and the work an
  accepted id list triggers is `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`.
- `ISSUE-BATCH-MOVE-FALSE-SUCCESS-01` — **Fixed 2026-08-12.** Legacy same-repo batch
  move via `POST /file/move` returned `{"success":true,"moved":N}` without touching
  the FS tree; it now returns 501 pointing at `sync-batch-move-item/`. Still
  unimplemented, no longer lying about it. The UI never used this path.
- `ISSUE-SHARELINK-PASSWORD-BYPASS-01` — **Fixed 2026-07-25** (readiness NF-1 /
  SH-6). Gate runs before the inline read and the OnlyOffice token mint; the
  bundle builder drops protected content; the OnlyOffice helper fails closed.
  Live integration covers both halves: both public endpoints for inline
  content, and a `.docx` fixture for the OnlyOffice credential (a `.md` fixture
  cannot reach that branch). Both mutation-verified against the cluster.
- `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` — **Closed 2026-08-04** (B4 umbrella;
  A1/A2, B, C and D0-D6 complete). Download admission is enabled in every
  shipped configuration and its capacities are validated against the measured
  2 GiB process-local budget. Byte-rate shaping remains a separate finding;
  object-storage anonymity was closed separately on 2026-08-07.
- `ISSUE-SYNC-LINK-TOKEN-AUTH-01` — **Fixed 2026-08-07.** `syncAuthMiddleware`
  accepted any valid download token as a repository credential. Reproduced live
  as an unauthorized cross-library block write by an anonymous share-link
  visitor, plus an escalation through `/download-info` into a full repository
  sync token, and a total absence of token-to-route repository binding.
  `isRepositorySyncToken`
  now validates `Source == ""`, `Path == "/"` and a route-bound `RepoID` before
  the bearer becomes an identity. All three clauses mutation-verified against
  the live server; ordinary sync tokens unaffected.
- `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` — **Closed 2026-08-07. Never
  affected production.** The `mc anonymous set download` lines existed only in
  the four development/test Compose files, not in `docker-compose.prod.yml`,
  which ships no MinIO and targets provider-native S3 that is private by
  default. The lines are removed; nothing depended on them, since every MinIO
  consumer in the repository authenticates. The old entry overstated the
  finding by not separating the dev Compose files from the production one.
- `ISSUE-STREAMBLOCKS-VOID-01` — **Fixed 2026-08-03**; `StreamBlocks` now
  returns errors and the seafhttp caller records delivered bytes only.
- `ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01` — **Fixed 2026-09-19**;
  the post-renewal gone-check no longer removes the repair-owned `pub:` from
  a non-authoritative local absence. The existing 35-day TTL bounds any
  over-retention; renewal-after-classify, the concurrent settlement race, X1
  and GC remain separate.

## Blockers that keep destructive GC disabled

X1 is OPEN. The accepted closure architecture is documented
([GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md));
it is not implemented and does not enable deletion. X2 closed 2026-08-14
(implemented 2026-08-13), proven on a real three-DC cluster.
`gc.enabled: false` remains required on every replica in every DC — activation
rests on X1 and then on the PRE-GC / A1 gate
([X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md) §6).

**Read X1 as the whole fence-and-physical-identity workstream, not just the stale
DELETE.** Never-reused physical keys are necessary but not sufficient: a shared
per-candidate claim id lets one worker drop the publication fence while another is
still deleting under it, and the reuse probe can then hand a writer back the very
incarnation being destroyed — which physical-key uniqueness cannot prevent, because no
new incarnation is created. Closure criteria are in Registry X1.

**Closure architecture is frozen in
[GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md)**
(D0, 2026-09-02): documentation only; X1 remains OPEN; `GC_ENABLED=false`.
Historical option comparison remains in
[GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md) and is **not** the
active roadmap. P1 locator authority, P0/R12, P2/R9/R24 mint/install, P3 writer
boundary, and P4a/P4b exact `(P,D)` handoff are implemented foundations, not
X1 closure. W1 BorrowedFS own-liveness through HEAD is implemented and evidence-backed; W2/R31 still covers full writer continuity. PC-0 ([PUBLICATION-PROTOCOL-CHARACTERIZATION.md](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md)) characterizes the current publication protocol and recommends a future multi-DC PublicationCoordinator; it does not close W2/R31. PC-1 (2026-09-11) introduced the coordinator skeleton and the common publication types in `internal/publication` (attempt identity, tri-state HEAD outcome, settlement disposition, opaque evidence boundary) with zero funnels migrated, zero productive importers, and zero runtime/schema/CL/TTL change; it does not execute any publication sequence and does not close W2/R31. PC-D1 (2026-09-12) closes the inherited-dependency architecture decision (`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`): the certified baseline frontier owns continuity, and `WorkSetScopeNewlyLive` is incremental only with a valid durable witness. PC-D1A authority foundation (canonical witness columns, fail-closed validity, HEAD-fenced baseline CAS, and atomic global-SERIAL HEAD+witness authority) landed 2026-09-19. PR #228 implements the exact-P/GC-aware fail-closed certifier; PR #233 adds byte-proven PC-D1B.3 mapping authority and cold-path M18/M19 promotion. Lifecycle fencing, the first productive consumer, and PC-2 remain future work. No funnel is migrated. PC-D1B (2026-09-20) adds a distinct metadata prerequisite ahead of the certifier (`ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`, decided in [PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md)). The order is authority-only identity primitive (#230), writer/deleter wiring (#231), then PR #228 read-only certifier consumption. The certifier verifies the captured commit and every reachable fs_object against durable claims, preserves canonical fs_id keys byte-for-byte, accepts the authority-verified zero-block Cassandra representation, and revalidates source identity before witness settlement; it returns `identity_unproven` without a witness for SHA-1-only identities needing unauthoritative mapping. PC-D1B.3 mapping authority and cold-path M18/M19 promotion are implemented in PR #233. The certification-window fence is decided by PC-D1B.4 (per-library destruction epoch plus pending destruction intents predicated by the witness CAS; `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01`) and its runtime (PC-D1B.5) is required before destructive GC only (2026-09-29 reset). Historical backfill remains a greenfield non-goal; no productive consumer or PC-2 work has started, and neither is an X1 prerequisite: the order of work is [X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md) (W2 exit checklist → G4 → G5 → E1); preserve GC_ENABLED=false.

P4c-orphan exact
identity and minimum durable discovery are implemented by PR #207, the G2
PREPARED-to-COMMITTED handoff is implemented by PR #209, and G3 canonical
retirement after committed handoff is implemented by PR #212 (writer fencing
unchanged; physical S3 deletion remains out of scope). G4/G5, the P4c-orphan
logical-block PK replacement, and X1 remain open.

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` | Blocker | Physical-delete ABA **plus** the publication-fence race. Accepted architecture: exact `(P,D)` handoff into orphan as continuation authority; not strict physical non-overlap | Registry X1 · [handoff plan](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md) · [historical options](./GC-X1-CLOSURE-OPTIONS.md) |
| `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` | ✅ Closed 2026-08-14 | Destructive liveness reads at `EACH_QUORUM` behind a topology gate; five-leg three-DC evidence green, both mutations (`LOCAL_QUORUM` and `QUORUM`) confirmed red | [Registry X2](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) · [X2 runbook](./GC-X2-MULTIDC-VALIDATION.md) |

## High / Medium — open (audit follow-ups)

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01` | ✅ Closed 2026-08-22 | The three handlers now distinguish canonical ownership from organization owner/admin/superadmin overrides; owner API keys require `read-write`, role overrides require `admin`, and content shares/repo tokens remain insufficient. Negative tests cover all three handlers | [known issue](./KNOWN_ISSUES.md) |
| `ISSUE-SYNC-PUTCOMMIT-NOT-WRITE-ONCE-01` | HIGH (P1) ✅ Fixed by merged PR #208 | `PutCommit` now arbitrates immutable (parent_id, root_fs_id) with first-writer-wins LWT; identical retries are idempotent and conflicts are rejected | [known issue](./KNOWN_ISSUES.md#issue-sync-putcommit-not-write-once-01) · PR #208 merged; #206 is rebased and being reaudited for its scoped W2 follow-ups |
| `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01` | MEDIUM (P2) — OPEN PRE-GC (E1-16: W2-13 X1 safety CLOSED-EVIDENCE) discovery/convergence/scale | No hard visit/completion bound. E1-13 measured Office backlog-delay pre-D subset CLOSED-EVIDENCE: unvisited durable target directly vetoes D; native fresh-process UNKNOWN discovery/renewal. Finite backlog proves no SLA/fairness or scale bound; no sweep-health gate added | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-discovery-scale-01) · [E1-13 evidence](./E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md); bounded discovery design unstarted |
| `ISSUE-GC-PUB-REF-ZERO-REF-01` | MEDIUM (P2) — OPEN PRE-GC (E1-16: W2-14 X1 safety CLOSED-EVIDENCE) transition/convergence | Runtime-confirmed by E1-14: a real Office `pub:` retired by TTL to zero refs is never discovered (no candidate/queue/D); P1/K1 retained. Measured safety subset CLOSED-EVIDENCE / NOT-X1-RED. Causal mutation: an `up:`-style expiry projection makes it a productive candidate | [known issue](./KNOWN_ISSUES.md#issue-gc-pub-ref-zero-ref-01) · [E1-14 evidence](./E1-14-PUB-ZERO-REF-TRANSITION.md); projection design unstarted, must re-run E1-14 with the guard-omission control |
| `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01` | HIGH (P1) — ✅ closed for X1 (E1-16, 2026-10-10: W2-3 CLOSED-EVIDENCE; Sync validates every added block regardless of provenance) | Once `up:sync:<repo>:<block>` expires before the pre-HEAD readiness gate runs, the block is indistinguishable from one with no PutBlock provenance. Its upload pin is not renewed or fabricated (provenance scopes renewal only). Its publication is still protected: since W2-4, both Sync HEAD paths capture the block's exact placement, acquire the durable repair and re-validate exact P before HEAD, and the repair guard covers post-HEAD | [known issue](./KNOWN_ISSUES.md#issue-sync-putblock-expired-provenance-01) |
| `ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01` | HIGH (P1) ✅ Resolved 2026-09-08 | `syncBlockHasOwnLivenessProvenanceFn` now falls back to an exact-referrer `EACH_QUORUM` read on a clean local miss (fan-out bounded fail-fast via `errgroup.WithContext`), recovering provenance acknowledged in another DC; real 3-DC RED->GREEN evidence, all-cross-DC-hit cost on a real 3-DC Cassandra fixture (single Docker host, not geographic WAN), and a one-DC-down fail-closed leg | [known issue](./KNOWN_ISSUES.md#issue-sync-putblock-cross-dc-provenance-visibility-01) - the resulting availability-domain trade-off for genuinely-unprovenanced blocks is analyzed and accepted explicitly, not just measured; concurrency tuning tracked separately in `ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01` |
| ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 | HIGH (P1) — OPEN / FOLLOW-UP / GENERAL | Both pre-#239 mixed writer/GC combinations reproduce D(P)+HEAD; future incompatible rolling upgrade outside v1 first activation. Covered current-version W2-0 is CLOSED-EVIDENCE, and the adopting W2-1/2/6a are CLOSED for X1 under the v1 contract (E1-16). That closure does not cover this unsupported mixed-version deployment | [known issue](./KNOWN_ISSUES.md#issue-w2-incompatible-mixed-rollout-01-incompatible-rolling-upgrade-can-publish-head-after-d) · [W2 checklist](./X1-CRITICAL-PATH.md) |
| `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` | HIGH (P1) — ✅ closed for X1 (E1-16, 2026-10-10: each listed funnel is closed pre-HEAD, by CLOSED-FIX or CLOSED-EVIDENCE as X1 §4 records per row; post-HEAD through the shared repair guard; W2-10 tracked separately) | Publication authority before HEAD was not uniform by provenance. Every listed funnel now re-validates its exact placement after durable repair and before HEAD: CreateFileFromBlocks, UploadFile (W2-6), CreateFile Office templates (W2-6a), OnlyOffice (E1-4), SeafHTTP single-shot and streaming (E1-5a/5b), cross-repo copy/move (E1-09) and Sync (W2-3/W2-4). Pre-HEAD: CLOSED-FIX for the measured RED→GREEN fixes; CLOSED-EVIDENCE for W2-3 (Sync expired provenance/other pod) and for W2-9 directories/multi-block, which run the same verified code. Post-HEAD R31 safety is CLOSED-EVIDENCE through the shared repair guard (E1-16). R31 convergence is PRE-GC; content resurrection (W2-10) is tracked separately. Exact-P is not prescribed as the only fix | [known issue](./KNOWN_ISSUES.md#issue-pc0-exact-p-funnel-gap-01) · [PC-0](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md) · [W2 exit checklist](./X1-CRITICAL-PATH.md) |
| `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01` | DECIDED / implementation follow-up | PC-D1 assigns inherited continuity to the certified baseline frontier; `WorkSetScopeNewlyLive` is incremental only with a valid witness, and absent/stale evidence fails closed to baseline certification | [known issue](./KNOWN_ISSUES.md#issue-pc0-inherited-dependency-continuity-01) · [PC-D1](./PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md) |
| `ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01` | HIGH (P1) — OPEN, PC-D1A authority foundation and #228 certifier gate landed | PR #231 wires supported commits/fs_objects writers and deleters through the identity gateway. PR #228 verifies the captured commit and every reachable fs_object against durable claims before physical/liveness work, revalidates source identity before witness settlement, and fails closed for mapping-dependent SHA-1-only files. FROZEN by the 2026-09-29 X1 reset and re-evaluated in E1; remaining items (certification-window fence runtime, first productive consumer, PC-2) are off the X1 critical path, and the fence is PRE-GC only. Historical backfill is a greenfield non-goal; preserve `GC_ENABLED=false` | [known issue](./KNOWN_ISSUES.md#issue-pcd1-certified-baseline-implementation-01) · [PC-D1](./PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md)
| `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01` | HIGH (P1) — decision CLOSED / mergeable 2026-09-24; runtime OPEN (PC-D1B.5, PRE-GC only since the 2026-09-29 reset; re-scope to what D1–D3 need before implementing) | Per-library E/P/S fence remains the architecture. CW-M27..CW-M34 decision contracts are closed. CW-M31 proves cross-DC visibility. CW-M33 requires the SERIAL proof-read itself to observe absence, plus the EACH_QUORUM recheck; the isolated Cassandra 5.0.9 tests cover pre-existing and post-barrier accepted Paxos, with G21/G22 removal mutations RED. CW-M34 requires no admitted write at `w <= g` remain able to succeed hidden after GC; model, G20 mutation and 3-DC success-but-hidden characterization exist, implementation remains PC-D1B.5. §14/§16 account for SERIAL+EACH_QUORUM proof reads and lifetime tracking/recovery cost. Also frozen: `ClockSafetyLease`, UUIDv5 vectors, canonical `created_at` sentinel, whole-row W, persisted durable `identity_at`, no v1 batch amortization, and repeat reaffirmation on UNKNOWN | [known issue](./KNOWN_ISSUES.md#issue-pcd1b4-certification-window-fence-01) · [PC-D1B.4](./PC-D1B-CERTIFICATION-WINDOW-FENCE.md) |
| `ISSUE-PCD1B-MAPPING-PROJECTION-STABILITY-01` | HIGH (P1) ✅ Closed in PR #233 (2026-09-25) | The dominant timestamp freeze is verified with `EACH_QUORUM`, productive resolution pins `LOCAL_QUORUM`, the production mutation inventory prohibits higher-timestamp ordinary writes and deletes, and M20/T2/H3/DEL1 turn RED if these guarantees regress | [known issue](./KNOWN_ISSUES.md#issue-pcd1b-mapping-projection-stability-01) · [PR #233](https://github.com/Sesame-Disk/sesamefs/pull/233)
| `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01` | LOW — PRE-GC | Any continuity LWT (witness CAS today, destruction intent in PC-D1B.5) whose ballot is later than a concurrent plain hard delete leaves a HEAD-less `libraries` row: never a valid witness, but GC's canonical-existence checks see a library | [known issue](./KNOWN_ISSUES.md#issue-pcd1b-continuity-lwt-ghost-row-01) |
| `ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01` | MEDIUM — PRE-GC | A superseded destruction generation's row tombstone can shadow fs_object display metadata that reaffirmation (identity projection only) does not restore | [known issue](./KNOWN_ISSUES.md#issue-pcd1b-stale-tombstone-display-metadata-01) |
| `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01` | MEDIUM — was CURRENT-RUNTIME / CONFIG-DEPENDENT and PRE-GC ✅ Resolved in PR #234 (2026-09-29) | Every hard-delete lease LWT (library, user, org) uses explicit global `SERIAL`, independent of a `LOCAL_SERIAL` session default; an acquire with an unknown CAS outcome makes a best-effort own-token release and never reports success (single-owner liveness residual recorded); isolated 3-DC evidence confirmed one owner and the existing lease lifecycle | [known issue](./KNOWN_ISSUES.md#issue-gc-hard-delete-lease-serial-domain-01) · [PR #234](https://github.com/Sesame-Disk/sesamefs/pull/234) |
| `ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01` | HIGH (P1) — CURRENT-RUNTIME / FOLLOW-UP; also required PRE-GC; pre-existing, not a #232 blocker | The authenticated `PermanentDeleteRepo` API synchronously hard-deletes library rows without a `GC_ENABLED` gate; a stale lease owner can resume after restore/delete takeover and apply its unconditional batch | [known issue](./KNOWN_ISSUES.md#issue-gc-hard-delete-lease-nonfencing-01) |
| `ISSUE-GC-USER-HARD-DELETE-RESTORE-SERIALIZATION-01` | HIGH (P1) — PRE-GC / A1; pre-existing, registered in the #234 cross-audit (2026-09-30) | `activateUser` only reads the user hard-delete lock and never owns it, while `HardDeleteUser` deletes unconditionally; a restore between the worker's post-lock stale check and its final delete is erased (single DC too) | [known issue](./KNOWN_ISSUES.md#issue-gc-user-hard-delete-restore-serialization-01) |
| `ISSUE-PCD1B-AUTHORITY-CLAIM-RETIREMENT-01` | MEDIUM (P2) — accepted debt, registered 2026-09-20 | Identity authority claims are write-once and non-expiring and survive deletion of their source row, so claims for failed initializations, losing commits and deleted identities/libraries accumulate with no retirement protocol. Greenfield cost, not historical residue; not a merge precondition for #228/#229 | [known issue](./KNOWN_ISSUES.md#issue-pcd1b-authority-claim-retirement-01) · [PC-D1B](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md) |
| `ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01` | P2 (performance) | #206's declared O(N)-per-block readiness cost has no tuned concurrency default, no redundant-read elimination, and no per-block pipeline; three independent follow-up lines, not a #206 blocker | [known issue](./KNOWN_ISSUES.md#issue-sync-putblock-readiness-hotpath-cost-01) - tech debt, unstarted |
| `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01` | MEDIUM (P2) — OPEN PRE-GC cleanup/convergence (E1-16: W2-12 X1 safety CLOSED-EVIDENCE) | E1-12 Office pre-D safety subset CLOSED-EVIDENCE: actual CAS-loser → SIGKILL → zero refs → productive GC veto → fresh-process UNKNOWN retention. No durable loser witness or eventual cleanup authority; W2-12 overall OPEN | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-known-loser-durability-01) · [E1-12 evidence](./E1-12-KNOWN-LOSER-CRASH-SAFETY.md) |
| `ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01` | HIGH | Six upload-link and file-share mutation handlers apply the user's underlying library authority or creator identity without applying the current credential's API-key scope; a `read` key can exceed its advertised authority | Verified preexisting 2026-08-22; direct API keys and derived sessions are affected. See [known issue](./KNOWN_ISSUES.md) and [technical debt](./TECHNICAL-DEBT.md#14-api-key-scope-hardening-follow-up-2026-04-04) |
| `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01` | ✅ Resolved (`333db3c5e`, P4a; see KNOWN_ISSUES) — the separate `ISSUE-GC-STALE-CLAIM-SETTLE-RACE-01` remains open PRE-GC | `ReleaseStaleBlockClaim` decides "no claim to release" from a session-consistency read, and that zero makes the caller consume the candidate — so a claim taken by a GC worker in ANOTHER datacenter (RF 1 per DC: the quorums do not intersect) can be missed, stranding a live block behind `gc_state='deleting'`. No data loss; the cost is a permanent upload refusal. Found auditing X2; the clean fix depends on X1's serial-domain decision (EACH_QUORUM here would couple ordinary queue drain to every DC being up; SERIAL collides with R12) |
| `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` | MEDIUM (multi-DC only) ✅ **Resolved 2026-09-14** | The six canonical HEAD-authority LWTs (four legacy writers plus the two PC-D1A authority-only primitives) pin `SerialConsistency(db.LibraryHeadSerialConsistency)` = global `SERIAL` and no longer inherit `serial_consistency`. Session default `LOCAL_SERIAL` is still allowed for other LWTs. PC-0 chain-pins the domain; mutation M1–M28 RED plus PC-D1A's 13 directed mutation legs RED; real 3-DC concurrent advance + initial HEAD under session `LOCAL_SERIAL` have exactly one winner. Satisfies the PC-D1 global SERIAL prerequisite; PC-D1A foundation landed and PR #228 implemented the fail-closed certifier gate; mapping promotion is implemented by PR #233 (merged), while productive frontier activation remains OPEN | [known issue](./KNOWN_ISSUES.md#issue-library-head-serial-domain-01) |
| `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` | HIGH (P1) ✅ **Resolved 2026-09-11** | HEAD initialization now goes through `FSHelper.InitializeLibraryHeadIfUnset` (`IF head_commit_id = null AND created_at != null`); the two unconditional initializers are gone, `GET /commit/HEAD` returns the Paxos-settled HEAD from a blind DC instead of overwriting it; unit + single-cluster + real 3-DC handler-level evidence; `TestPC0NoUnconditionalHeadUpdateRemains` and mutation leg M10 pin the resolution; the blind-DC guarantee is stated for the shipped global `SERIAL` domain (`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` closed 2026-09-14: HEAD LWTs pin global SERIAL even when the session default is `LOCAL_SERIAL`). Fourth round: `rollbackNewLibrary` takes authority with `DELETE ... IF head_commit_id = null` so a creator's own failure cannot destroy a HEAD another initializer published. `PublicationCoordinator` prerequisite met. Second review round (same day) closed 4 more runtime gaps: v4 CAS-write-timeout misclassification, creation-handler `503 Retry-After` that promised a resumption it could not deliver (now an honest `500` + preserved `repo_id`; durable resumption split out as `ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01`), KNOWN_LOSER cleanup ordering, and unattributed commit rows on definitive rejection | [known issue](./KNOWN_ISSUES.md#issue-library-initial-head-concurrency-01) · [PC-0 §3.4](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md) · `scripts/h1-initial-head-multidc-validation.sh` |
| `ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01` | MEDIUM ✅ **Resolved 2026-09-11** | Durable `library_rollback_pending` marker (effective no TTL, 32 recovery buckets) is written before the HEAD LWT; a Server-owned bounded fair reaper re-enters `deleteUnpublishedLibraryRow` then idempotent derived cleanup. Marker is discovery only — never cleanup authority. Fault-injection: authority applied → crash → `RecoverPendingLibraryRollbacks` clears ghosts | [known issue](./KNOWN_ISSUES.md#issue-library-rollback-ghost-projections-01) |
| `ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01` | MEDIUM — follow-up, not an H1 blocker | A group-library creation preserved on an UNKNOWN HEAD publish, a share write error past publication, or a rollback refused because another initializer already published, is durable and cannot be resumed by repeating the POST (which mints another library); its group share is `not_attempted` or `unconfirmed` depending on the failure phase. Worked design — single-owner LWT claim keyed by org/owner/group/name, ownership-checked release, fixed share identity, no TTL — parked on `feat/group-library-creation-claims` with its audit's 10-test evidence list | [known issue](./KNOWN_ISSUES.md#issue-group-library-creation-resumability-01) |
| `ISSUE-LIBRARY-HEAD-ADOPTED-TREE-VISIBILITY-01` | MEDIUM/HIGH — follow-up, not an H1 blocker | Adopting a blind-DC HEAD only proves its own `commits` row is locally servable, not the root/subtree `fs_objects` behind it; the 3-DC suite doesn't catch this because every initial HEAD is the same deterministic empty root, which the losing initializer already wrote locally itself | [known issue](./KNOWN_ISSUES.md#issue-library-head-adopted-tree-visibility-01) · [PC-0 §3.4](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md) |
| `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` | HIGH (P1) | W2-10 overall OPEN. E1-10e reproduces real COMMITTED/TERMINAL unpublished-source RED and fixes measured RevertDirents non-directory file admission: settled historical fs: via LQ→EQ fallback, original exact-P capture and per-HEAD revalidation. CLOSED-FIX only for that subset; no own up:/pub:/repair or continuous liveness proof. Retained-history evidence for the four funnels remains narrow; directories/batches, other handlers, retention/Phase5/6 and R31 remain OPEN. | [known issue](./KNOWN_ISSUES.md#issue-pc0-content-resurrection-publication-01) · [E1-10e evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md) · [PC-0 §3.5](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md) |
| `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` | **P0 latent** — PRE-GC, mandatory before any `GC_ENABLED=true` with `version_ttl_days > 0` | Phase 5's expired-version cascade deletes content-addressed fs_objects still reachable from HEAD (no keep-set); executable counterexample in `internal/gc`; refutes "ordinary GC protects inherited dependencies" as-is | [known issue](./KNOWN_ISSUES.md#issue-gc-phase5-cascade-shared-fsobjects-01) · [PC-0 §6 PUBL-10](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md) |
| `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` | HIGH (P1) ✅ **Resolved 2026-09-12** — PRE-X1 / R31 convergence | Resumable SERIAL-anchored walk with durable next-unread cursor; later HEAD movement does not restart in-flight work; clean genesis is persisted as exhausted before re-observing HEAD and may re-anchor (second 1024-node chunk in the same 30s context); a re-anchor CAS loser does not replay an exhausted snapshot; missing repair row is a no-op; UNKNOWN still retains; per-row `pub:<repo:commit:fsID>` is renewed after classify while unresolved; Sync success does not pay per-block repair-owned DELETE | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-reachability-convergence-01) |
| `ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01` | HIGH (P1) — PRE-GC OPEN (PRE-X1 until E1-16, 2026-10-10), confirmed 2026-10-08 (E1-15B) | A CreateFile writer resumed after COMMITTED D stages `pub:` before its final fence; SIGKILL leaves it durable (35d); after repair queueing, every sweep renews it post-D (measured twice). Pre-HEAD holds. The residue is a logical reference of L: no HEAD advances and P1 is not reinstalled (E1-15C), so it is retention of later lives, not an X1 violation (E1-16). Shared fix needs repair settlement too | [known issue](./KNOWN_ISSUES.md#issue-publication-post-d-liveness-before-validation-01) · [E1-15B evidence](./E1-15B-WRITER-POST-D-STAGING-CRASH.md) |
| `ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01` | HIGH (P1) — PRE-GC OPEN residual (PRE-X1 until E1-16, 2026-10-10); mitigated 2026-10-07 (E1-15A) | A stale visitor past its durable re-check renewed `pub:` after a real loser clear and exact COMMITTED D(P1); RED on current runtime. Post-renewal local absence now escalates to EACH_QUORUM; confirmed global absence withdraws exactly that pin. Not a fence: the post-D write still happens, and a crash, a check or DELETE error, or an ambiguous/partial renewal leaves it until TTL. That residual pin is a logical reference of L, with no reachable HEAD and no P1 reinstall: retention, not an X1 violation (E1-16) | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-stale-renewal-after-clear-01) · [E1-15A evidence](./E1-15A-STALE-REPAIR-VISITOR-CANCELLATION.md) |
| `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01` | MEDIUM (P2) — follow-up, PRE-GC (PRE-X1 until E1-16), not an R31-C1 blocker | Concurrent renewal of the same repair row can re-create its `pub:` after the worker removes it and before the row is deleted; leftover `pub:` is TTL-bounded over-retention. Ordinary Sync success does not attempt this cleanup | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-owned-pub-cleanup-race-01) |
| `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01` | **CLOSED-EVIDENCE — covered current-version pre-D safety** | E1-11 measures real TTL expiry during/after classification, clean UNKNOWN retention, native classifier/guard errors and exact COMMITTED omission RED. Existing non-expiring repair/EACH_QUORUM guard prevents a new D; fs: precedes guard removal. No runtime protocol change or health gate. Historical #220/#222/#224 rejected designs remain rejected; discovery, known-loser, pub: transition and concurrent cleanup are separate OPEN contracts | [evidence/disposition](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md) · [historical diagnosis](./KNOWN_ISSUES.md#issue-publish-repair-renewal-after-classify-01) |
| `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` | MEDIUM (P2) — OPEN — **PRE-GC since 2026-10-12** (was FOLLOW-UP / GENERAL), over-retention, measure first (plan in the issue; Phase 1 queries in PUBLISH-REPAIR-OBSERVABILITY); E1-15C (2026-10-08): an abandoned repair vetoes D of every later life of L (measured); E1-15D (2026-10-09): its commit stays publishable via Sync auto-merge and the classifier still says UNKNOWN afterwards; no settlement authority existed; E1-15E (2026-10-09): superseded repairs (HEAD left the commit's parent without it) now settle, unblocking D; still retained when HEAD = parent or the commit row is missing — narrowed, still OPEN | A genuinely dead/unreachable publication attempt whose repair row survived request-local cleanup (proven loser + failed clear; pre-HEAD abort/rollback + failed clear; equivalent dead residues) has no terminal settlement path: it stays discoverable and is revisited under retry/backoff; successful unresolved visits can repeatedly refresh the 35 d `pub:`, so over-retention can continue indefinitely. Post-success clear failure is not this leak (REACHABLE, worker retries). No under-retention; does not block X1 safety closure. As PRE-GC, it must be measured and dispositioned before destructive-GC activation (A1). Observed while adding repair-worker observability | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-dead-row-retention-01-a-deadunreachable-publications-repair-row-surviving-request-local-cleanup-can-be-retained-and-repeatedly-re-pinned-indefinitely) |
| `ISSUE-PUBLISH-REPAIR-CROSS-CHUNK-CYCLE-01` | MEDIUM (P2) — follow-up, not an R31-C1 blocker | Resumed reachability chunks seed only the anchored HEAD, so a corrupt cycle longer than one chunk that lies entirely below HEAD rotates the cursor forever (UNKNOWN/retain, never cleanup). Cycles through HEAD and cycles within one chunk are detected | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-cross-chunk-cycle-01) |
| `ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01` | MEDIUM (P2) — follow-up, PRE-GC (PRE-X1 until E1-16), not an R31-C1 safety blocker | Progress SERIAL LWTs live on the 32-bucket discovery table and mix with ordinary INSERT/DELETE; unrelated repairs in one bucket share a Paxos domain. `created_at` is millisecond TIMESTAMP, not a UUID generation. Intended fix is a per-repair LWT-only progress table | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-progress-paxos-domain-01) |
| `ISSUE-PUBLISH-HEAD-TREE-STATS-COST-01` | P2 (performance) | Every HEAD publish walks the whole directory tree for stats inside the stage→HEAD window; Sync does it twice before its CAS; PC-0 §12 corrected, optimization is a follow-up | [known issue](./KNOWN_ISSUES.md#issue-publish-head-tree-stats-cost-01) |
| `ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01` | ✅ CLOSED 2026-09-06 (W2 authority defect) | Timeout/lease is no longer cleanup authority in shared W2 repair; R31 residuals remain under `ISSUE-GC-PUB-REF-ZERO-REF-01` and `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`; the shared repair reachability classifier is closed by #213 while broader R31 remains open | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-timeout-cleanup-01) · [zero-ref](./KNOWN_ISSUES.md#issue-gc-pub-ref-zero-ref-01) · [reachability](./KNOWN_ISSUES.md#issue-publish-repair-reachability-01) · [known loser](./KNOWN_ISSUES.md#issue-publish-repair-known-loser-durability-01) |
| `ISSUE-PUBLISH-REPAIR-REACHABILITY-01` | ✅ CLOSED 2026-09-10 (shared repair classifier) | Tri-state classification now uses a 30s/1024-node cold-path bound, `SERIAL` HEAD and `EACH_QUORUM` commit reads; inconclusive evidence never authorizes cleanup. No durable negative producer was invented | [known issue](./KNOWN_ISSUES.md#issue-publish-repair-reachability-01) · broader [R31](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md#16-r31-remains-a-blocker-open) remains open |
| `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01` | SUPERSEDED / LEGACY-NOT-REACHABLE (greenfield v1; G4/G5) | A `gc_s3_orphans` row refused for still having references falls out of the working set once the day cursor passes it, then TTLs out at 90 days — storage leak, and the alerting counter goes quiet with it | Historical X2 counterexample; G5 removes the empty-state/day-cursor executor and recovers current protocol from durable roots; no quarantine mechanism required |
| `ISSUE-GC-LOGICAL-MAPPING-RETENTION-01` | LOW/MEDIUM | R11a intentionally preserves SHA-1 → SHA-256 mappings after physical GC; without a separate logical-death reaper, stale rows accumulate and may resolve to a 404 until rematerialization | R11a/B.3 accepted tradeoff · [known issue](./KNOWN_ISSUES.md) |
| `ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01` | ✅ Closed 2026-08-22 | The superadmin GC surfaces did not check `GC.Enabled`: manual triggers answered `{"started":true}` on nodes where nothing ran, and the DLQ requeue/delete path *claimed the GC lease* from a disabled replica. All are now explicitly lifecycle-gated; manual triggers additionally require current leadership | Found re-verifying the kill switch post-#181; defence in depth, never a live bypass |
| `ISSUE-GC-DRYRUN-OVERRIDE-STICKY-01` | MEDIUM | The `dry_run` field of `POST /admin/gc/run` is not scoped to the run it accompanies: an accepted trigger replaces the node's runtime mode for the life of the process, so one superadmin call can lower a configured `GC_DRY_RUN=true` — the rung directly below `GC_ENABLED` — and it stays lowered, unaudited. Unreachable while GC is disabled fleet-wide; live from the moment destructive GC is activated | Verified preexisting 2026-08-23 (inherited from `main`, not the 2026-08-22 branch, which only stopped a *refused* trigger from committing the override). See [known issue](./KNOWN_ISSUES.md) |
| `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01` | HIGH | `pack-fs` materializes the whole response: ~409k repeats of one valid id, `PermissionR` only. `check-fs` shares the fan-out | Found auditing X9; the fs-id equivalent of the closed X11 |
| `ISSUE-SYNC-RECVFS-NOT-WRITE-ONCE-01` | HIGH (P1) ✅ Fixed by merged PR #208 | `RecvFS` validates lowercase `fs_id`, compares logical SHA-1 identity across legacy/canonical layouts, completes pre-existing placeholders, rejects semantic conflicts, fails closed on storage errors, and avoids new child upserts/Paxos | [known issue](./KNOWN_ISSUES.md#issue-sync-recvfs-not-write-once-01) · PR #208 merged; #206 is being revalidated on main |
| `ISSUE-ZIP-STREAM-LATEFAIL-01` | MEDIUM | ZIP download can truncate after `200 OK` | Readiness DL-2. Severity corrected 2026-08-22 to match [KNOWN_ISSUES.md](./KNOWN_ISSUES.md), which has rated it Medium since the 2026-05-27 preflight narrowing — truncated/retryable download, not corruption |
| `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | MEDIUM | Cross-library block read (BOLA), gated only by knowing the 256-bit hash | Readiness B2/SEC-1 |
| `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | MEDIUM | Download cap and `single_use` are race-bypassable | Readiness NF-2 / SH-5 |
| `ISSUE-SYNC-METADATA-CONCURRENCY-01` | MEDIUM | Sync metadata routes bound one body, not N — 16 concurrent `recv-fs` ≈ 2 GiB | Successor to X9; X10's equivalent for the block routes is closed |
| `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | MEDIUM | `audit_log` records deletions but never grants | Readiness NF-6 / RB-3 |
| `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` | MEDIUM | S3 PUT precedes durable intent; minted keys broaden the possible undiscoverable-object leak beyond process crash to ambiguous PUT, unresolved install uncertainty, and failed exact cleanup. Pre-INSTALL retries now retain one target and exact cleanup has a final-outcome metric, but that metric covers only attempted cleanups — deliberate retention and the pre-INSTALL sentinel returns are uncounted | Registry X3; bounded request-local cleanup is not a durable outbox, sweeper, or eventual guarantee, so P2 still does not close this issue |
| `ISSUE-QUOTA-RESERVATION-01` | MEDIUM | TOCTOU between quota pre-check and publish | Readiness UP-3 |
| `ISSUE-DOWNLOAD-NO-404-01` | MEDIUM | Deleted file answers 503 forever; layers disagree | Registry X8 — **accepted cost of PR-6** |
| `ISSUE-ACCOUNTS-M2M-PATH-01` | MEDIUM | Admin API key channel works; M2M lacks `source=accounts` + idempotency | Code-verified 2026-07-25 |
| `ISSUE-MID-OPERATION-REVOCATION-01` | MEDIUM | Revoke mid chunked-upload / mid-stream / ZIP does not abort in flight | UP-5 / DL-5 / RB-4 |
| `ISSUE-SHARELINK-NO-ORG-SCOPE-01` | MEDIUM | No org-internal share-link scope (token-only, anonymous) | SH-2 — product / BY DESIGN option |
| `ISSUE-SHARELINK-CREATOR-KEY-01` | MEDIUM | Encrypted share links decrypt with creator's key | SH-3 |
| `ISSUE-TRAFFIC-RECORDER-DROPS-01` | MEDIUM | Saturated traffic recorder drops events silently | No counter / log |
| `ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01` | MEDIUM | **Preference validation improved 2026-08-22:** `ChangeStorageClass` rejects cold classes and rejects a class outside the currently read strict region. **Still open:** the endpoint read/write TOCTOU, policy-authoritative placement for stale preferences across v2/Sync/SeafHTTP, policy-gated failover, historical/reused content semantics and migration | Partial: endpoint validation is not a concurrency or placement fence |

## Low / latent / deferred hardening

| Issue | Sev | One line |
|---|---|---|
| `ISSUE-IWORK-PREVIEW-413-NO-MESSAGE-01` | LOW (UX) | An iWork document above the D6 source cap renders the raw JSON error inside the preview modal, and the image→PDF fallback re-requests it. Frontend only; carries the two product decisions D6 left open — raising the cap, or a separate admission profile (needs a §12 amendment) |
| `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | LOW (latent) | Cross-tenant gate is copy-pasted into ~50 handlers, not middleware. No current gap. |
| `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | LOW–MED | `handleAutoLogin` hardcodes cookie `Secure=false` |
| `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | LOW (config) | Both chunked-upload size guards can be disabled together; staging guard is `0` in **every** shipped config |
| `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01` | LOW (docs) | Bootstrap/rotation/smoke/revoke runbook for the Accounts platform API key was never written |
| `ISSUE-ORG-ADMIN-DELETION-BANNER-01` | LOW (UX) | Soft-delete lifecycle exists; org bootstrap omits `deleted_at`/grace + frontend banner TODO |
| `ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01` | LOW (hardening) | Optional HMAC/JWT request signing beyond admin API key — **not required for v1**; channel is API key by design |
| `ISSUE-SHARELINK-BCRYPT-COST-01` | LOW | Share-link passwords use `bcrypt.DefaultCost` (10), not Argon2id |
| `ISSUE-ENCRYPTED-VIEW-CONTENT-LENGTH-01` | LOW | v2 inline-view/share-raw omit `Content-Length` on encrypted downloads |
| `ISSUE-DOWNLOAD-NO-REHASH-01` | MEDIUM (defense-in-depth) | Download does not re-hash blocks; trusts object store |
| `ISSUE-OIDC-ROLE-RECONCILIATION-01` | MEDIUM (design) | OIDC claims ↔ manual role overrides: authoritative-source rule undefined |

## Performance — deferred pending measurement

| Item | Sev | One line |
|---|---|---|
| `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01` (= X4 / P-4 / UP-2) | HIGH (perf) | One `SERIAL` LWT/Paxos transaction per block invocation that reaches metadata registration when the effective setting is `SERIAL`; browser/sync preflight can bypass fully deduplicated blocks. The shipped default uses `SERIAL`, but env and topology determine effective WAN cost. **P0 is not an X4 performance fix**. **PR-11, not started** — characterize latency, permit wait, Paxos settings and placement identity first. See [X1/X4 characterization](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md). |
| `ISSUE-CANONICAL-READ-FANOUT-01` (= X5) | MEDIUM | Canonical read fan-out never validated against a real cluster |
| `ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01` | MEDIUM (deferred) | D bounds aggregate accepted work, not bytes per second; measure node egress at D6 before choosing shaping |
| `ISSUE-READ-AFTER-WRITE-CROSS-DC-01` (= X6) | MEDIUM | Read-after-write across DCs; 3×25 ms retry covers local lag only |

## Verification debt

Not findings — things nobody has proven either way.

- **A multi-DC harness now exists, and only X2 uses it.**
  `docker-compose.cassandra-3dc.yaml` (three DCs, RF 1) plus
  `scripts/x2-multidc-validation.sh` reproduce cross-DC consistency behaviour at the
  wire level, and X2 is closed on that evidence — divergent-state visibility,
  fail-closed with a DC down, reference-DC-down with the `QUORUM` mutation, both
  topology-gate halves, and both consistency mutations. **X6 and the remaining cross-DC assumptions are still derived
  from the production consistency contract and have never been reproduced**, even
  though the instrument to do it is now checked in.
- **No production latency measurement** for the per-block LWT (X4 / `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01`); the characterization is documented in [UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md), but no runtime measurement has landed.
- **The six older upload funnels** have never been driven individually under a
  live fence; coverage proves the three retry wrapper mechanisms instead.
- **PR-10 did not run the full Compose integration suite** (PR-2..PR-6 did).
- `TECHNICAL-DEBT.md` §32 — the three retry wrappers were never consolidated.
- **No repo-root CI workflow** yet (see `TECHNICAL-DEBT.md` / roadmap) — tracked
  as verification debt, not duplicated as a security ISSUE here.

---

## Document consolidation (2026-07-25)

The contradictions this index exists to prevent came from **seven** places all
claiming to say what was pending. Consolidated as follows.

**Removed** (each item disposition is in the migration table below):

| File | Why | Where its content lives now |
|---|---|---|
| `PENDING-ISSUES-AUDIT-2026-05-14.md` | Orphan — linked from nothing, 2.5 months stale | migration table + roadmap / `KNOWN_ISSUES` / new ISSUEs |
| `sesamefs-pending-issues.txt` (root) | Header already named the four docs as canonical | those docs |
| `quotas-pending-issues.txt` (root) | Stale copy; many items already DONE in code | migration table (code-verified 2026-07-25) |
| `UPLOAD-PERFORMANCE-PR58-AUDIT.md` | Half of one PR58 research archive | `UPLOAD-PR58-RESEARCH-ARCHIVE.md` |
| `UPLOAD-S3-RELAY-BOTTLENECK.md` | (same) | (same) |

**Marked historical, kept for evidence:** `SECURITY-ASSESSMENT-2026-04.md`,
`-v2`, `-v3`. Each carries a banner saying its status column is a dated
snapshot. `-v4` stays current-ish; the readiness report supersedes it for
upload/download/sharing specifically.

**Still to decide** — left alone rather than merged blindly:

- `KNOWN_ISSUES.md` vs `TECHNICAL-DEBT.md` — defect vs architectural debt boundary
  is real but inconsistently applied.
- `V1-PRODUCTION-ROADMAP.md` vs `IMPLEMENTATION_STATUS.md` vs `CURRENT_WORK.md`
  — three readiness percentages / blocker lists. Candidate for one file.

---

## Tracker migration table — all unique items

Every **unique** open or noteworthy item from the three deleted trackers is
mapped below. Duplicate rows that only restated the same work are not repeated;
see the `sesamefs-pending-issues.txt` note at the end. Status verified against
**code** on 2026-07-25 where marked. Disposition codes: **DONE** =
implemented (with concrete symbol/evidence); **OPEN** = still work; **PARTIAL** =
usable mechanism exists but the original tracker’s full contract is unfinished;
**DECISION** = product choice, not a defect; **ROADMAP** = owned by
roadmap/PLANS, not this index's defect list; **DUPLICATE** = already had an ISSUE.

### From `quotas-pending-issues.txt`

| Old item | Disposition | Canonical | Notes (code-verified) |
|---|---|---|---|
| 0. V2 mutations bypass quota | DONE | `ISSUE-QUOTA-COVERAGE-01` | Fixed 2026-05-14 |
| 1. Per-user storage quota on uploads | DONE | `ISSUE-USER-STORAGE-ENFORCE-01` | Fixed 2026-05-14 |
| 2. Formalizar path M2M Accounts → SesameFS | OPEN (partial) | `ISSUE-ACCOUNTS-M2M-PATH-01` | **Channel works today:** `RequireSuperAdmin` + `apikeys.ScopeAdmin` on `/admin`; `UpdateOrganization`, `AdminAddOrgUser` / update / delete exist. Still missing: `source=accounts` audit tag (hardcoded `manual-superadmin`), idempotency |
| 3. preview/evaluate plan change | DONE | `PreviewOrganizationPlanChange` | Route live |
| 4. Invite/create users vía Accounts | DONE (operational) | Admin user CRUD + Accounts remote | **Live today:** Accounts provisions/invites users via a dedicated superadmin API key calling `AdminCreateUser` / `AdminAddOrgUser` (org-local writes gated by `Accounts.DisableOrgUserWrites`, default true; `max_users` enforced). Residual hygiene is tracked separately as `ISSUE-ACCOUNTS-M2M-PATH-01` (audit source + idempotency), not as missing invite/provisioning. Optional Phase 4.5 UX polish in `PLANS-AND-PERMISSIONS.md` is roadmap, not a blocker for this item. |
| 5. External org identifier | DECISION | `PLANS-AND-PERMISSIONS.md` | No `external_org_id` in schema — wait for Accounts contract |
| 6. External billing IDs | DECISION | `PLANS-AND-PERMISSIONS.md` | No billing columns yet |
| 7. Snapshot pre-downgrade users | DECISION / ROADMAP | `PLANS-AND-PERMISSIONS.md` | Preview computes counts; no persisted activation snapshot |
| 8. Quota error messages | PARTIAL | `traffic.TrafficQuotaExceededResponse` + `file-uploader.js` `getQuotaErrorMessage` | Upload UI maps `storage` / `traffic-*` reasons. Residual: some handlers pass `includeReason=false`; sync still uses plain strings — polish, not a new ISSUE |
| 9. Quota user → org visualization | DONE | `frontend/.../sys-admin/users/user-info.js` + `AdminGetOrgUser` | Shows effective + `(inherited)` for storage/upload/download |
| 10. Traffic quotas admin UI | DONE | `set-org-traffic-quotas.js` + `UpdateOrganization` traffic fields | Sysadmin can view/edit org traffic quotas |
| 11. Audit log quotas/provisioning | OPEN (partial) | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` + `ISSUE-ACCOUNTS-M2M-PATH-01` | Org updates write `audit_log` but always `manual-superadmin`; user CRUD writes nothing |
| 12. Recorder drop metrics | OPEN | `ISSUE-TRAFFIC-RECORDER-DROPS-01` | `Recorder.recordAsync` silent drop |
| 13. Risk dashboard / notifications | ROADMAP | `PLANS-AND-PERMISSIONS.md` Phase 3 | Soft `X-Quota-Warning` exists; no dashboard |
| Missing Accounts runbook | OPEN | `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01` | File never existed |

### From `PENDING-ISSUES-AUDIT-2026-05-14.md`

| Old item | Disposition | Canonical | Notes |
|---|---|---|---|
| 1. Per-user storage quota | DONE | `ISSUE-USER-STORAGE-ENFORCE-01` | |
| 1b. Quota coverage gaps | DONE | `ISSUE-QUOTA-COVERAGE-01` | |
| 2. Backup / DR | ROADMAP | `V1-PRODUCTION-ROADMAP.md` | No backup scripts / DR doc yet |
| 3. Persistent audit / activity | OPEN | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | |
| 4. Batch move false-success | OPEN | `ISSUE-BATCH-MOVE-FALSE-SUCCESS-01` | `FileHandler.moveBatchFiles` still TODO + `success:true`; UI path `SyncBatchMove` is real |
| 5. GC queue recounts | DONE / historical | GC docs | Moved out of hot path |
| 6. Public/upload-link resume | OPEN / BY DESIGN | resume analyses | Safe stub |
| 7. Upload "do not replace" | DONE | `ISSUE-UPLOAD-REPLACE-01` | |
| 8. File-operation statistics | OPEN | `ISSUE-FILE-STATS-01` | Already in `KNOWN_ISSUES` |
| 9. Antivirus | ROADMAP | `V1-PRODUCTION-ROADMAP.md` §6 | No `internal/av/` |
| 10. Storage class / lifecycle | ROADMAP | roadmap + cold-storage notes | |
| 11. Default repo stub | OPEN | `ISSUE-DEFAULT-REPO-01` | Already in `KNOWN_ISSUES` |
| 12. Compatibility stubs | ROADMAP / debt | `TECHNICAL-DEBT.md` | |
| 13. Cold storage restore placeholder | ROADMAP | `V1-PRODUCTION-ROADMAP.md` | `InitiateRestore` placeholder |
| 14. Text diff | OPEN | `KNOWN_ISSUES` version-diff gap / `TECHNICAL-DEBT` | Frontend URL commented out |
| 15. Org-admin stats platform leak | OPEN | `ISSUE-ORG-STATS-SCOPE-01` | Already in `KNOWN_ISSUES` |
| 16. Traffic recorder drops | OPEN | `ISSUE-TRAFFIC-RECORDER-DROPS-01` | Same as quotas item 12 |
| 17. Devices/license stubs | ROADMAP | sysadmin stubs | |
| 18. CI / E2E maturity | ROADMAP / debt | `TECHNICAL-DEBT.md` | No repo-root `.github/workflows` |

### From `sesamefs-pending-issues.txt`

Every **unique** item from this tracker is already mapped above (via the quotas
table or the May audit table). Duplicate rows that only pointed at those same
canonical docs are not repeated here. Hard-blocker GC multi-instance wording is
superseded by the upload-fence X1/X2 + `gc.enabled: false` posture. Accounts
work is covered by the quotas migration rows (`ISSUE-ACCOUNTS-M2M-PATH-01`,
runbook; invite/create via Accounts is DONE operationally).

---

## Completed series (for context, not work)

- **Upload-fence / canonical-storage series, PR-1..PR-10** (#137–#146): all
  fourteen `F` findings closed. See
  [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) for PR merge progress
  and [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
  for the findings themselves. **Defect status lives in `KNOWN_ISSUES.md`.**
- **Org-scoped block deletion (P10)**, PR-1..PR-3 (#134–#136): block keys are
  org-scoped end to end; cross-org delete isolation closed.

## E1-7 / W2-5 scoped evidence update — 2026-10-04

[RecvFS-first characterization](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md) is GREEN
for six named direct ordered legs, including pre-existing P1 and productive
COMMITTED/TERMINAL retirement before PutBlock. Early HEAD rejects; PutBlock
creates P2 and publication settles normally. Fresh metadata alone creates no P;
that absence is not extrapolated to reused content. No runtime fix is added.
CLOSED-EVIDENCE applies only to that measured ordered contract. W2-5/E1-05
overall, R31/W2-11..14, W2-10, E1 and X1 remain OPEN; production GC remains OFF.


## E1-09 / W2-9 source-purge publication fix — 2026-10-05

`ISSUE-W2-CROSSREPO-RETIRED-LIFE-PUBLICATION-01`: productive source purge and
GC COMMITTED/TERMINAL reproduce destination HEAD/fs: after P1 retirement in
both copy and move. Fixed by per-attempt source P capture, staged-ID equality
and final exact-P after durable repair, before HEAD. Scope: single file/block,
plaintext, same org/representation pre-HEAD CLOSED-FIX. Directories/multiblock,
multi-DC and post-HEAD/R31 stay OPEN; E1/X1 and production GC OFF unchanged.
Async task progress also had a reproduced data race; fields are now snapshotted
under the TaskStore lock. Move source disappearance after safe destination
publication remains existing partial-operation semantics, not rollback.
[Evidence and limits](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md).


### E1-10e: unpublished source counterexample and scoped fix (2026-10-06)

Base main@5071d1701624b291a95db9ade8b5ff6abdbd63d7. Productive web upload
followed by Sync PutCommit/RecvFS persists an unpublished commit/tree/file with
SHA-1 block IDs and a canonical mapping, but no permanent fs: or repair.
Only the owned temporary upload reference exists. After its controlled lapse,
real GC reaches COMMITTED or TERMINAL for exact P1 without removing history.
The original RevertDirents handler then successfully publishes HEAD depending
on that retired P1. This is a source-admission counterexample outside Phase5/6;
metadata existence does not prove retained permanent liveness.

RevertDirents non-directory items now require their own settled
fs:<repo>:<historical-file> for every canonical block, capture original exact P,
and recheck those references plus existing exact-P authority immediately before
each HEAD CAS. Missing/malformed/unknown evidence fails the item with HEAD intact.
Live unpublished metadata is deliberately rejected too; normal Sync publication
followed by deletion and restoration remains supported. No pin/pub:/repair is
acquired, no bytes are rematerialized and commit ancestry is not certified.

Measured one root file/path/plaintext block, same repo/org and one DC:
RevertDirents source-admission subset = CLOSED-FIX. Directories, other handlers,
broader batches/layouts, concurrent retention/cleanup, Phase5/6 and R31 remain
OPEN. Foreign settled fs: is not a continuous own-pin proof through HEAD.
W2-10/E1/X1 remain OPEN; production GC OFF, shared development GC unchanged.
See [frozen plan and evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md).

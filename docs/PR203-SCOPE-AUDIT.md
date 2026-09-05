# PR #203 scope audit and disposition

**Audit date:** 2026-09-05
**Baseline:** `main` / `origin/main` at `7c71449d2` (PR #202 merged)
**Reference only:** `fix/w2-sessionupload-liveness-parity` remains intact and is
not an implementation source. This register covers its commits, tests, and
documentation before the new W2 slice.

The rule for this PR is strict: code enters only when omitting it would make the
narrow SessionUpload guarantee through the pre-HEAD `CreateFileFromBlocks` cut false. The `Origin` column distinguishes findings introduced by the abandoned #203 implementation from preexisting behavior that #203 merely discovered or characterized. The `Exists in main?` column is about the baseline above, not about the historical reference branch. `INTRODUCED-BY-203` findings are preserved as pitfalls or constraints and are not silently reintroduced.

| Severity | Scope | Origin | Exists in main? | Impact on narrow guarantee | Documentary destination | Decision |
|---|---|---|---|---|---|---|
| P1 | Destructive cleanup selected from timeout or lease expiry | PREEXISTING; discovered/characterized during #203 | Yes | Post-HEAD repair only; no effect on the narrow pre-HEAD guarantee | [ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01](KNOWN_ISSUES.md#issue-publish-repair-timeout-cleanup-01); R31/X1 docs | Existing follow-up; lease expiry is not destructive authority |
| P1 | Initial-library HEAD, shared-root initialization, and initial commit nonce | INTRODUCED-BY-203 W2a | No | None for an existing SessionUpload block placement | `CHANGELOG.md`; X1 handoff plan | Historical lifecycle scope; do not import |
| P1 | Resurrection TOCTOU between initial state and HEAD publication | INTRODUCED-BY-203 W2a | No | None before HEAD for this block-level gate | `CHANGELOG.md`; X1 handoff plan | Historical pitfall; no lifecycle change |
| P1 | Legacy-NULL publication-state compatibility removal and read tolerance | INTRODUCED-BY-203 W2a | No | None; schema/protocol migration is out of scope | `CHANGELOG.md`; PRE-GC/X1 docs | Historical migration scope; do not import |
| P1 | `owner_id` and HEAD-writer authority invariant | INTRODUCED-BY-203 W2a | No | None for exact physical placement validation | `CHANGELOG.md`; R31/X1 docs | Historical authority overlay; do not import |
| P1 | Library lifecycle mutation serialization | INTRODUCED-BY-203 W2a | No | None before HEAD; would change lifecycle ordering | `CHANGELOG.md`; X1 handoff plan | Follow-up/PRE-X1; no code in W2 |
| P1 | Cross-DC library lifecycle handoff | INTRODUCED-BY-203 W2a | No | None for an already materialized block | `CHANGELOG.md`; X1 handoff plan | Follow-up/PRE-X1; no multi-DC lifecycle code |
| P1 | Soft-delete authority settleability | INTRODUCED-BY-203 W2a | No | None; soft-delete/restore is explicitly excluded | `CHANGELOG.md`; `KNOWN_ISSUES.md` lifecycle entries | Historical finding; no lifecycle code |
| P1 | ACTIVE versus TERMINAL publication state | INTRODUCED-BY-203 W2a | No | None for the existing block-level pre-HEAD fence | `CHANGELOG.md`; R31/X1 docs | Historical schema/lifecycle scope; no state changes |
| P1 | Absence of a library row treated as terminal authority | INTRODUCED-BY-203 W2a | No | None for block placement authority | `CHANGELOG.md`; R31/X1 docs | Historical pitfall; no terminal-authority overlay |
| P1 | Terminal publication authority in HEAD SERIAL domain | INTRODUCED-BY-203 W2a | No | Not needed for the LOCAL_QUORUM block placement check | `CHANGELOG.md`; X1 handoff plan | PRE-X1 follow-up; no SERIAL HEAD change |
| P1 | Terminal authority versus ordinary `ErrLibraryHeadConflict` | INTRODUCED-BY-203 W2a | No | Broader HEAD error semantics, after this cut | `CHANGELOG.md`; `OPEN-WORK-INDEX.md` | Historical distinction; do not generalize in W2 |
| P1 | HEAD reachability across DCs for repair cleanup | PREEXISTING; discovered/characterized during #203 | Yes | Post-HEAD repair only; cannot justify changing this pre-HEAD gate | [ISSUE-PUBLISH-REPAIR-REACHABILITY-01](KNOWN_ISSUES.md#issue-publish-repair-reachability-01); [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md#19-multiregion-head-safety-follow-ups-2026-05-18) | Existing follow-up; no code in W2 |
| P1 | Unbounded ancestry reads in repair | PREEXISTING; discovered/characterized during #203 | Yes | No effect before HEAD; changing it is publish-repair scope | [ISSUE-PUBLISH-REPAIR-REACHABILITY-01](KNOWN_ISSUES.md#issue-publish-repair-reachability-01); [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md#19-multiregion-head-safety-follow-ups-2026-05-18) | Existing follow-up; no code in W2 |
| P1 | Repair HEAD lookup by `orgID` versus `libraries_by_id` | INTRODUCED-BY-203 repair fix | No | Only repair reachability, not block placement | `CHANGELOG.md`; R31 docs | Historical correction; no repair redesign |
| P1 | Repair ancestry `EACH_QUORUM` and missing-row fail-closed behavior | INTRODUCED-BY-203 repair fix | No | Only applied-CAS repair promotion after HEAD | `CHANGELOG.md`; R31/X1 docs | Historical constraint; no repair code in W2 |
| P1 | Ambiguous HEAD CAS and applied-CAS/unconfirmed repair promotion | INTRODUCED-BY-203 tests/fixes | No | Post-HEAD convergence only | `CHANGELOG.md`; R31 docs | Historical evidence; no repair promotion in W2 |
| P1 | Commit re-verification immediately before HEAD CAS | DEPENDENCY from #202, narrowed by #203 | Partial: BorrowedFS exists in main | Directly closes the SessionUpload pre-HEAD interval | `R3-LIVENESS-CONTINUITY.md`; `TESTING.md` | Included, generalized to all ready placements |
| P1 | HEAD-CAS/lease race at the root | PREEXISTING; characterized during #203 | Yes | Post-HEAD/repair race; no new lease authority is required for W2 | [ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01](KNOWN_ISSUES.md#issue-publish-repair-timeout-cleanup-01); R31/X1 docs | Existing follow-up; exact placement is the only W2 gate |
| P1 | Lease timeout is not revocation for publication/repair | PREEXISTING; characterized during #203 | Yes | None for the SessionUpload block gate; do not infer revocation | [ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01](KNOWN_ISSUES.md#issue-publish-repair-timeout-cleanup-01); R31/X1 docs | Existing constraint; no code in W2 |
| P1 | `confirmed-lost` versus `ErrLibraryHeadConflict` cleanup asymmetry | PREEXISTING; surfaced by #203 | Yes | Broader failed-publish cleanup, not placement evidence | [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md#pending-published-fs_object-cleanup-row-per-owner-model); [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md) | Existing debt; no error-semantics expansion |
| P1 | Soft-delete, restore, and hard-delete races | PREEXISTING; analyzed by #203 | Yes | Outside block commit and explicitly excluded | [KNOWN_ISSUES.md](KNOWN_ISSUES.md#issue-lib-deleted-fence-01); PRE-GC/X1 docs | Existing follow-up; no lifecycle code |
| P1 | Storage-accounting crash/retry convergence | PREEXISTING split-phase accounting, surfaced by #203 | Yes | No effect on placement, liveness, or exact-P | [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md#12d-storage-quota-publishcounter-atomicity) | Existing debt; no accounting/quota changes |
| P1 | Global reconciliation versus normal HEAD/counter write can apply a stale delta and over/under-count | INTRODUCED-BY-203 attempted accounting design | No (abandoned design) | None; outside W2 | This audit; `CHANGELOG.md` | Historical pitfall; do not reintroduce |
| P1 | Reconciler T1 can delete T2 when DELETE is scoped only by aggregate scope, without generation/attempt identity | INTRODUCED-BY-203 attempted accounting design | No (abandoned design) | None; outside W2 | This audit; `CHANGELOG.md` | Historical pitfall; do not reintroduce |
| P1 | Canonical soft-delete CAS can crash before its derived counter batch without a marker or reconciliation discovery path | INTRODUCED-BY-203 attempted accounting design | No (abandoned design) | None; outside W2 | This audit; `CHANGELOG.md` | Historical pitfall; do not reintroduce |
| P1 | Retry evidence could hit the committed-session early return | INTRODUCED-BY-203 test defect | No (test defect) | Would not prove a second renewal | `CHANGELOG.md`; `TESTING.md` | Fixed: first attempt fails pre-HEAD, releases claim, second attempt renews |
| P1 | `up:` row count alone cannot prove renewal | INTRODUCED-BY-203 evidence defect | No (evidence defect) | Could false-green identity/liveness evidence | `CHANGELOG.md`; `TESTING.md` | Fixed: one identity plus TTL movement and real retry |
| P1 | Renewal wording hid recreate-after-expiry | INTRODUCED-BY-203 documentation defect | No (wording defect) | Could hide lapsed-reference behavior | `R3-LIVENESS-CONTINUITY.md`; `CHANGELOG.md` | Fixed: idempotent upsert renews or recreates |
| P1 | Recreating `up:` after D must not revoke D | DEPENDENCY for GC-first evidence | No | Directly relevant to SessionUpload GC ordering | `R3-LIVENESS-CONTINUITY.md`; W2 evidence tests | Included in tests; exact-P still rejects retired placement |
| P1 | Blocked, changed, or fully retired placement must be rejected | DEPENDENCY from #202 exact-P authority | Partial: BorrowedFS path exists in main | Required before HEAD for SessionUpload parity | `R3-LIVENESS-CONTINUITY.md`; `TESTING.md` | Included for every ready provenance |
| P2 | O(N distinct blocks) renewal and authority reads | DEPENDENCY / #203 review constraint | No | Required bounded cost of the narrow guarantee | `R3-LIVENESS-CONTINUITY.md`; `TESTING.md` | Included; deduplicated by block ID |
| P2 | Per-block SERIAL/EACH_QUORUM, S3 scans, or global locks | #203 review constraint | No | Would violate the narrow hot-path budget | `R3-LIVENESS-CONTINUITY.md`; `TESTING.md` | Explicitly prohibited; contract test guards SERIAL absence |
| P2 | Evidence-gate contamination between W1 and W2 | INTRODUCED-BY-203 test/docs defect | No (harness defect) | Could make directed evidence falsely green | `TESTING.md`; `CHANGELOG.md` | Fixed: separate W2 gate and explicit empty unrelated gates |
| P2 | Global Dockerization of the X2/P3 harness added `.env`, image, network, and backend preconditions outside W2 | INTRODUCED-BY-204 initial revision; reverted | No (reverted) | None; unrelated multi-DC workflow must remain unchanged | `TESTING.md`; `CHANGELOG.md` | Reverted from the current PR; keep X2/P3 as its existing separate workflow |
| P2 | Go-format/test-runner wiring drift in #203 | INTRODUCED-BY-203 test/docs defect | No (harness defect) | No product effect, but invalidates evidence | `TESTING.md`; `CHANGELOG.md` | Fixed and validated in Docker |
| P2 | R31 `up -> pub -> HEAD -> fs` crash continuity | PREEXISTING roadmap | Yes, open | This funnel stops before post-HEAD crash reconciliation | [R31 handoff](GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md#16-r31-remains-a-blocker-open); [CURRENT_WORK](../CURRENT_WORK.md) | Remains OPEN; follow-up PR |
| P2 | X1 physical retirement and destructive GC activation | PREEXISTING roadmap | Yes, open | Writer gate does not close physical-delete ABA | [`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`](KNOWN_ISSUES.md#issue-gc-upload-fence-rematerialization-01); [X1 handoff](GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md) | Remains OPEN; `GC_ENABLED=false` remains required |

## Commit, test, and documentation coverage

The rows above cover every historical commit family on the reference branch:

- `3020f2b28`, `4b08c4bdc`, `df5af4231`, `2cd47fb03`, `6a630c8ef`,
  `059b5c6e3`, `c09fd6981`, `24e840d77`, and `caa7ebad6` cover the lifecycle,
  legacy-NULL, ownership, soft-delete, restore, hard-delete, and accounting
  findings named above, including the three abandoned accounting-design
  pitfalls.
- `0832caeb2`, `2f319acc5`, `8c69a2347`, `71ecfcefc`, and `fc04b0f07` cover
  terminal authority, timeout/lease, HEAD-CAS race, and repair lookup findings
  named above.
- `fcfea7fec`, `f82fb2fe4`, `b527988fe`, `62f6a718`, `5b0bbadde`, and
  `b26729b6b` cover repair ancestry, applied-CAS, wording, and harness findings
  named above.
- `4574c4c3d`, `acc981470`, `708e7ac31`, `46c8b132`, `e74c6f286`, and
  `c9653b352` cover the exact pre-HEAD recheck, SessionUpload parity, retry,
  scope, documentation, and test-format findings named above.

The current PR carries only the rows explicitly marked **Included**. All other
rows remain documentary constraints or separately scoped work; their presence
in this audit is not permission to import their code.

## Evidence boundary

The implementation contains only shared internal placement types, bounded
own-liveness upserts, and the final exact-placement advisory check. The evidence
gate is separate from W1 and names six required real Cassandra/MinIO legs:
`renewalVisibleBeforeHead`, `renewalExtendsNearExpiredTTL`, `writerFirst`,
`gcFirst`, `gcFullyRetiredBeforeRenewal`, and `renewalRetryIsIdempotent`.
Contract tests pin call order and reject a SERIAL hot-path authority call. All
directed runs explicitly disable unrelated evidence gates; the canonical Docker
runners enable the complete set. The W2 unit, contract, and six-leg integration
evidence runs in Docker. The pre-existing X2/P3 multi-DC harness is intentionally
not part of this PR.

This register is historical audit documentation, not authorization to fold the
listed follow-ups into this PR. A future PR must choose its own narrow scope and
acceptance evidence.

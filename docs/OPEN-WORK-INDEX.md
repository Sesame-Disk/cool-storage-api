# Open Work Index

**Last updated:** 2026-07-25
**Purpose:** one page that answers "what is still open, how bad is it, and where
do I read about it". Nothing is described here in depth — every row points at the
issue id that owns it.

## How this repo tracks work

Three layers, and each has exactly one job. Most of the contradictions this index
was created to fix came from a finding living in two layers at once with only one
of them updated.

| Layer | File(s) | Owns | Does **not** own |
|---|---|---|---|
| **Registry of record** | [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) | The `ISSUE-*` id, current status, severity, fix direction | The deep reasoning behind a finding |
| **Audit / analysis docs** | [PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md), [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), `SECURITY-ASSESSMENT-*`, `UPLOAD-*` | Evidence, severity rationale, why alternatives were rejected, verification performed | Current status — audits are dated snapshots |
| **This index** | `OPEN-WORK-INDEX.md` | The one-screen list and the cross-references between the above | Any detail at all |

**Rules that keep it honest:**

1. A finding gets **one** `ISSUE-*` id, even when two audits found it
   independently. When that happens, say so in the issue (see
   `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`, which is B4 and X10).
2. **Status changes go in `KNOWN_ISSUES.md` only.** An audit document is a
   snapshot of a date; do not retro-edit its verdict, add a dated note.
3. Cite code by **symbol name**, not `file.go:1234`. Line numbers rot — PR-10
   shifted `sync.go` by ~30 lines and silently invalidated a dozen citations
   across three documents.
4. A finding lives in exactly one table within a document. Duplicating a row into
   "open" and "closed" is how F13 ended up recorded as both.

---

## Production blockers — must close before go-live

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | HIGH | Password-protected share links serve file content **and an OnlyOffice download token** to anonymous callers | Readiness NF-1 |
| `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | HIGH | No rate/concurrency limit on the seafhttp upload, download and block routes | Readiness B4 = registry X10 |
| `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | HIGH | Desktop-SSO pending token is in-memory per process — multi-instance only | Readiness B5 |
| `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | HIGH | Chunked-upload state is node-local; non-sticky routing silently loses files | Readiness B1 |

The last two are **multi-instance only** and moot on a single sticky-routed node.
The first is single-node reachable.

## Blockers that keep destructive GC disabled

Both are open, neither has a closed design, and `gc.enabled: false` is required
on every replica in every DC until both close.

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` | Blocker | Physical-delete ABA: an authorized S3 delete can land after a byte-identical re-upload | Registry X1, incl. the rejected design space |
| `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` | Blocker (multi-DC) | `LOCAL_QUORUM` references can be invisible to GC in another DC | Registry X2 |

X1 needs never-reused generational physical keys, which requires changing the
canonical reader's derived-key contract — real design work, not configuration.
See the "X1 design space" section of the registry before proposing anything.

## High / Medium — open

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | HIGH | `sesamefs_auth` is a replayable bearer in a JS-readable cookie → XSS = token theft | Readiness SEC-3 / NF-3 |
| `ISSUE-STREAMBLOCKS-VOID-01` | HIGH | `StreamBlocks` returns void → false "complete" log and over-billed traffic | Readiness DL-1 |
| `ISSUE-ZIP-STREAM-LATEFAIL-01` | HIGH | ZIP download can truncate after `200 OK` | Readiness DL-2 |
| `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | MEDIUM | Cross-library block read (BOLA), gated only by knowing the 256-bit hash | Readiness B2/SEC-1 |
| `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | MEDIUM | Download cap and `single_use` are race-bypassable | Readiness NF-2 / SH-5 |
| `ISSUE-SYNC-UNBOUNDED-BODIES-01` | MEDIUM | Four sync handlers still read the body unbounded | Registry X9 |
| `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | MEDIUM | `audit_log` records deletions but never grants | Readiness NF-6 / RB-3 |
| `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` | MEDIUM | S3 PUT precedes durable intent; a crash leaves an undiscoverable object | Registry X3 |
| `ISSUE-QUOTA-RESERVATION-01` | MEDIUM | TOCTOU between quota pre-check and publish | Readiness UP-3 |
| `ISSUE-DOWNLOAD-NO-404-01` | MEDIUM | Deleted file answers 503 forever; layers disagree | Registry X8 — **accepted cost of PR-6**, decision deferred |

## Low / latent

| Issue | Sev | One line |
|---|---|---|
| `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | LOW (latent) | Cross-tenant gate is copy-pasted into ~50 handlers, not middleware. No current gap. |
| `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | LOW–MED | `handleAutoLogin` hardcodes cookie `Secure=false` |
| `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | LOW (config) | Both chunked-upload size guards can be disabled together; staging guard is `0` in **every** shipped config |

## Performance — deferred pending measurement

| Item | Sev | One line |
|---|---|---|
| Registry **X4** / P-4 / readiness UP-2 | HIGH (perf) | One global Paxos round per block, ~128 cross-region rounds per GB. **PR-11, deliberately not started** — there is no per-statement latency metric yet. Add the metric, get the production number, then decide. |
| Registry **X5** | MEDIUM | Canonical read fan-out never validated against a real cluster; the existing benchmark substitutes an in-memory function for Cassandra |
| Registry **X11** | MEDIUM | The 100k check-blocks id cap bounds the parser, not the ~100k sequential Cassandra reads an accepted request triggers |
| Registry **X6** | MEDIUM | Read-after-write across DCs; the 3×25 ms retry covers local lag only |

## Verification debt

Not findings — things nobody has proven either way.

- **No multi-DC test exists.** X2, X6 and the whole cross-DC line of reasoning
  are derived from the production consistency contract, never reproduced. The
  `config-usa/eu.cluster.yaml` profiles use `LOCAL_SERIAL` and do not model
  production's `SERIAL`.
- **No production latency measurement** for the per-block LWT (X4). PR-11 cannot
  be decided without it.
- **The six older upload funnels** have never been driven individually under a
  live fence; coverage proves the three retry wrapper mechanisms instead.
- **PR-10 did not run the full Compose integration suite** (PR-2..PR-6 did). It
  touches no concurrency, so `-race` was not required, but the integration run
  was skipped.
- `TECHNICAL-DEBT.md` §32 — the three retry wrappers were never consolidated;
  `finalizeUploadStreaming` and template CreateFile keep non-cancellable backoff.

---

## Completed series (for context, not work)

- **Upload-fence / canonical-storage series, PR-1..PR-10** (#137–#146): all
  fourteen `F` findings closed. See
  [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) for what each PR
  did and [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
  for the findings themselves.
- **Org-scoped block deletion (P10)**, PR-1..PR-3 (#134–#136): block keys are
  org-scoped end to end; cross-org delete isolation closed.

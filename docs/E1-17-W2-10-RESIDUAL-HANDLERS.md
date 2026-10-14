# E1-17 / W2-10 — residual content-resurrection handlers

## Frozen plan

Base: main@747408b39 (#277 and #278 merged). Branch:
`fix/e1-17-w2-10-residual-handlers`. Production GC stays OFF.

**Question.** E1-10e reproduced a real X1 violation on `RevertDirents` for one
root file. The unpublished-source schedule was:
1. real web block upload;
2. Sync `PutCommit` + `RecvFS`, which persist an unpublished commit and tree;
3. the owned temporary `up:` lapses;
4. real GC reaches exact COMMITTED/TERMINAL D(P1);
5. the handler publishes HEAD that depends on retired P1.

E1-10e fixed only that file shape. X1 §4 keeps W2-10 OPEN for the remaining
handlers and shapes. Every one of them reads a client-supplied `commit_id`
and builds HEAD from its historical tree, with no own `up:`/`pub:`/repair and
no physical-life check before the HEAD CAS:

| Shape | Handler | Current admission |
|---|---|---|
| file | `RevertFile` | none |
| file | `RestoreTrashItem` | none |
| directory | `RestoreTrashItem` | none |
| directory | `RevertDirectory` | none |
| directory item | `RevertDirents` | none (files only since E1-10e) |

**Demonstrate first.** No runtime change until each shape is measured. The
test reuses the E1-10e productive fixture unchanged and calls each actual
handler. Legs per shape:
- `committed` and `terminal`: the RED candidates.
- `live-unpublished`: the policy control, mirroring E1-10e.

That is 15 named legs, run on the isolated GC-OFF `sesamefs_e19` daemon with
the owned manual GC, like E1-10e. Directory shapes use an unpublished tree
root → directory → file, with the same single plaintext block. RED means:
the handler succeeds and HEAD reaches the historical file while exact P1 is
COMMITTED or retired. The RED evidence is kept.

**Fix only what is RED, reusing E1-10e.** The fix follows the existing
source-admission policy, with no new mechanism:
- **Files.** `captureRetainedHistoricalFile` before the retry loop, and
  `validateRetainedHistoricalFile` immediately before every HEAD CAS.
- **Directories.** A strict recursive walk of the historical subtree,
  reading directory objects through the same LQ→EQ admission read. A
  missing, partial or malformed directory object fails closed; it never
  defaults to empty. Every nested file is then captured and re-validated
  in the same way.
- **Failure contract.** Unchanged per handler. Items that fail admission
  produce the handler's existing failure response with HEAD intact:
  `RevertDirents` keeps its 200 success/failed list; the other three return
  their existing error status.
- **Not added:** no own pin, `pub:`, repair, rematerialization, coordinator
  or ancestry certification.

**Controls.** The existing retained-history suites are the positive controls
and must stay GREEN after the fix:
- E1-6 RevertFile;
- E1-10 RestoreTrashItem file and E1-10b RestoreTrashItem directory;
- E1-10c RevertDirectory;
- E1-10d RevertDirents;
- E1-10e, including its published Sync history leg.

Their histories carry real permanent `fs:`, so admission must accept them.

**Evidence and audit.**
- A required named-leg gate, wired into `TestMain` and both compose
  runners, with a completeness contract test.
- An omission mutation in a disposable copy: removing only the new call
  sites must bring RED back.
- Normal and integration vet, and the existing PC-0 inventory guards.
- Standard Docker `go-all-test` last.

**Integration budget.** The standard suite finished at 2399.5 s against its
40 min budget (E1-16). These legs push it over that budget. Any timeout change
is made explicitly and recorded against the TECHNICAL-DEBT entry, not
silently.

**Out of scope.**
- Phase 5/6 history removal between observation and HEAD, and concurrent
  retention cleanup: both PRE-GC.
- R31 and other funnels.
- Ancestry certification.

If a shape is not RED, record the measured result and the source argument,
and leave that handler unchanged. Close W2-10 only if every listed shape is
CLOSED-FIX or CLOSED-EVIDENCE.

## Result

### RED on main@747408b39 (no runtime change)

`TestE117ResidualResurrectionSourceAdmission` ran on the isolated GC-OFF
`sesamefs_e19` daemon with the owned manual GC. **All 15 legs published HEAD**
(22.3s).

| Shape | live-unpublished | committed | terminal |
|---|---|---|---|
| `RevertFile` | HEAD published | **HEAD on COMMITTED P1** | **HEAD on retired P1** |
| `RestoreTrashItem` file | HEAD published | **COMMITTED** | **TERMINAL** |
| `RestoreTrashItem` directory | HEAD published | **COMMITTED** | **TERMINAL** |
| `RevertDirectory` | HEAD published | **COMMITTED** | **TERMINAL** |
| `RevertDirents` directory item | HEAD published | **COMMITTED** | **TERMINAL** |

Each RED leg first verified that the new HEAD reaches the historical file:
- directly for the file shapes;
- inside the restored directory for the directory shapes.

That verification ran while exact P1 was COMMITTED or already retired. The
committed and terminal legs are real X1 violations: HEAD depends on retired
P1. D came from the real owned block worker; no permanent reference, history
metadata, mapping or D was fabricated. The RED log is kept at
`tmp/e117-red-main-747408b39.log` (ignored).

### Fix (scoped)

`internal/api/v2/resurrection_admission.go`:

- **`captureRetainedHistoricalEntry`** applies the E1-10e file admission to
  a whole restored entry:
  - a file is admitted exactly as in E1-10e;
  - a directory is admitted only if every file in its historical subtree is.
- **`readRetainedHistoricalDirectory`** reads each directory object with the
  same LQ→EQ admission read. Unlike `GetDirectoryEntries`, which turns a
  missing object into an empty directory, it fails closed when the object is
  missing, untyped, not a directory, or malformed, or when an entry has no
  fs_id. `""`, `[]` and `null` are a real empty directory
  (`parseRetainedHistoricalDirectory`, unit-tested). The walk
  (`walkRetainedHistoricalEntry`) has **no depth limit**, matching restore
  before admission (`collectDirStats` has none). Content-addressed fs_ids,
  verified at RecvFS and computed by v2, cannot form a cycle. A unit test
  admits a 1000-level tree.
- **`validateRetainedHistoricalSource`** rechecks immediately before every
  HEAD CAS:
  - each block-bearing file's own permanent `fs:`;
  - then one exact-P authority pass over the union of placements.

  It replaces `validateRetainedHistoricalFile`, which had no caller left.

**Handlers.** `RevertFile`, `RevertDirectory`, `RestoreTrashItem` and
`RevertDirents` capture the entry after reading the historical tree. They
validate it before every HEAD CAS, including retries. `RevertDirents` now
admits directory items too.

**Failure contracts are unchanged.** `RevertDirents` reports the item in its
200 `failed` list. The other three return their existing generic error
(500). In all four handlers the reason is logged both at capture ("not
admitted") and at the pre-HEAD recheck ("failed pre-HEAD recheck").

**Contract.** Every block that a restored entry depends on must have its
file's own settled permanent `fs:` and pass exact P before every HEAD CAS.
A file without blocks (size 0) has no physical life to depend on. It is
admitted without a reference, unchanged from E1-10e and physically safe.

**Not added:** no own pin, `pub:`, repair, rematerialization or ancestry
certification.

**Known effects.**
- **Ordering.** As in E1-10e, admission runs before conflict, skip and
  same-content resolution. A non-admitted history therefore fails even when
  that request would not have published anything. This is fail-closed.
- **Cost.** In `main`, a directory restore already walked the subtree once
  for the quota delta (`fsEntryDelta` → `collectDirStats`): one
  `fs_objects` read per node, sequential, on every HEAD attempt. E1-17 adds
  about seven reads per single-block file. Four per block are the admission
  itself: `fs:` and exact P at capture, and again before HEAD. The rest are
  partly avoidable:
  - the `libraries` row is re-read for every file, though it is the same
    row;
  - the file layout read duplicates the stats walk;
  - the SHA-1 mapping applies only to Sync blocks.

  Files are processed sequentially; blocks within a file run in parallel
  (bound 20). Large-directory latency has not been measured; it is
  registered as `ISSUE-RESURRECTION-ADMISSION-DIRECTORY-COST-01` (P2,
  measure first).

**Post-HEAD.** The published entries keep their historical fs_ids:
- `contentModifiedFileEntry` changes only name, mtime and modifier;
- directories reuse the historical entry.

They therefore stay protected by the same permanent `fs:` that admission
required. That `fs:` can be removed between observation and HEAD only by GC
history removal (Phase 5/6), which remains PRE-GC.

### Evidence

- **GREEN.** E1-17 passes 15/15 with the required gate:
  - every leg is rejected before HEAD (head barrier not reached);
  - HEAD is unchanged;
  - no repair or publication writes;
  - the live source keeps its `up:`;
  - COMMITTED and TERMINAL are confirmed.
- **Positive controls, same run.**
  - E1-6 RevertFile;
  - E1-10 RestoreTrashItem file;
  - E1-10b RestoreTrashItem directory;
  - E1-10c RevertDirectory;
  - E1-10d RevertDirents;
  - E1-10e, including the published-Sync-history restore.

  All pass: settled history, files and directories, still restores.
  `TestEveryEvidenceGateIsWiredIntoTestMain` passes.
- **Omission mutation** in a disposable container copy: the four capture
  call sites are replaced with an empty source, the four sites becoming zero.
  All 15 legs return to VIOLATION. The guard is causal.
- **Race.** `-race -count=2`: 4/4 PASS, no data race.
- **Unit tests.**
  - `TestHistoricalAdmissionReadsUseScopedFallback` now pins six fallback
    reads (`fs_objects` twice), and each of the six survives the five
    consistency mutations.
  - `TestParseRetainedHistoricalDirectory` covers 8 cases.
- **Static checks.** Normal and integration vet, and gofmt on LF copies,
  are clean.
- **Standard Docker `go-all-test`** on the rebuilt stack, with every daemon
  verified to run the current image, exited **0**:
  - unit PASS;
  - integration 536 PASS / 0 FAIL / 75 SKIP in 759.757s, including E1-17
    under its required gate;
  - API 20/20;
  - OIDC PASS.
- **Cross-audit corrections and re-validation** (HEAD 274c691c5):
  - the depth bound was removed (unit test: a 1000-level tree is admitted);
  - rejection causes are logged at capture and at the pre-HEAD recheck;
  - the contract wording was tightened.
  Focused E1-17 and retained-history controls PASS, with the logs naming
  every rejection. Standard `go-all-test` on the rebuilt stack (daemons
  verified on the current image) exited **0**: integration 536 PASS / 0 FAIL
  / 75 SKIP in 967.355s, API 20/20, OIDC 25/25.
- **Integration budget.** The standard runner timeout is raised explicitly
  from 40m to 50m. E1-17 adds about 25s to a suite that ended at 2399.5s
  (TECHNICAL-DEBT, "E1-16 test-infrastructure follow-ups").

### Disposition

W2-10 is **CLOSED-FIX** for X1:
- the measured shapes are fixed with RED→GREEN evidence and an omission
  mutation;
- the same per-item, per-file and recursive code covers batches,
  multi-block files and deeper nesting, which is CLOSED-EVIDENCE.

**Still out of scope:**
- concurrent GC history removal (Phase 5/6) between observation and HEAD,
  which is PRE-GC;
- ancestry certification, which this policy deliberately does not attempt.

**Before X1 CLOSED:**
- E1-01, after inventorying the G1–G5 crash evidence;
- the three-DC residuals;
- the D0 §25 reclassification list;
- the identity claim-table PRE-X1 audit debt.

Production GC stays OFF.

### Pre-HEAD recheck witness (2026-10-14)

The 15 residual legs above reject at capture and assert `headVisits == 0`, so
neither the pre-HEAD barrier nor `validateRetainedHistoricalSource` is reached in
them: the recheck's rejection path had no behavioural witness. It does now.

`TestE117PreHEADRecheckRejectsLostAuthority`
(`internal/integration/e117b_prehead_recheck_test.go`, gate
`SESAMEFS_REQUIRE_E117_REHEAD_RECHECK_EVIDENCE`) runs the supported restore path
on settled history so capture passes, then removes the permanent `fs:` reference
the recheck reads at the existing pre-HEAD barrier — the same callback e16's
`head-conflict` leg uses — and requires the recheck to reject:

- control leg: mutation withheld, the revert succeeds and HEAD advances;
- trip leg: the pre-HEAD barrier is reached (`headVisits == 1`), the handler logs
  `historical source failed pre-HEAD recheck … lacks settled permanent
  liveness`, the response is not 200, HEAD is unchanged and no repair is
  installed.

Observed 2026-10-14 in the isolated stack (real Cassandra and MinIO): both legs
PASS, and `-race -count=2` over this leg plus the six e16 legs passes with no
data race. The standard integration/API/OIDC gates were not run for this
addition.

This closes the evidence gap on W2-10's second half. It changes no runtime,
schema, guard or gate state; X1 remains OPEN and `GC_ENABLED=false` remains
mandatory.

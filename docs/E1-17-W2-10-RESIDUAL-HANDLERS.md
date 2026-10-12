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

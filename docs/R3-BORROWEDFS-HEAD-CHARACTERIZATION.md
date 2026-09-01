# R3 BorrowedFS HEAD characterization

**Characterization parent:** `1009e80b2` (`main` containing #199)
**Branch:** `test/r3-borrowedfs-head-characterization`
**Scope:** characterization only. Nop publication barriers and a test-only harness.
No production slow path, no `worker.go`, no schema, no H, no F0b/F2, no orphan/020,
no `GC_ENABLED` change.
**Production/fleet status:** X1 remains OPEN. R3 remains OPEN. Destructive GC remains
disabled for deployment (`GC_ENABLED=false` fleet-wide).

The real Cassandra+MinIO evidence gate is `SESAMEFS_REQUIRE_BORROWEDFS_HEAD_CHARACTERIZATION=1`.
`TestMain` requires all **6 named legs** after `m.Run()`. Completeness is `missing()` by
name, never a counter. A missing stack or a skip cannot report green when the gate is armed.

X1 D2 stopped at post-cut `pub:` staging; subsequent HEAD is not characterized
there. This PR measures that last dangerous point on the current
`CreateFileFromBlocks` BorrowedFS path, and a test-only harness that is
**not production protocol**.

## Question

After a BorrowedFS classify (foreign `fs:` only, no session `up:`), can
`CreateFileFromBlocks` still stage `pub:`, CAS HEAD, and promote a new `fs:`
after GC has obtained destructive authority? If a test-only own pin and a
`beforeHead` fence are injected, does HEAD stop?

## Why in-process

The barriers live in the test binary. HTTP to the Docker `sesamefs` node cannot
see them. The fixture therefore:

1. Creates the library and block-upload session over HTTP (same Cassandra).
2. Seeds a physical object plus `blocks.sha1` (real SHA-1) and a plaintext
   `block_id_mappings` row. `x1SeedPhysical` installs `sha1=""` and would
   `needs_upload`.
3. Adds a **foreign** `fs:` referrer. No `up:<session>` at classify.
4. Calls `v2.NewFileHandler` + `gin.CreateTestContext` against that Cassandra
   and a MinIO-backed `storage.Manager`.

A single small file is valid as the only/final block versus the 8MiB session
block size.

## Productive seams (+0 protocol)

Default `fileFromBlocksAfterVerifiedFn` / `AfterStagedFn` / `BeforeHeadFn` are
nop FuncLits. Production does not call `BlockDeleteFenceActive` or
`ProbeBlockReuse` at those sites. Tests replace them via
`SetFileFromBlocksPublicationBarriersForTest` and restore from `t.Cleanup`.

```text
classify (BorrowedFS ready)
  -> afterVerified barrier
  -> ClaimBlockUploadSessionForCommit
  -> finalize: tree, stage pub:, afterStaged barrier, insertCommit
  -> beforeHead barrier
  -> UpdateLibraryHeadFromSnapshot
  -> promote fs:, drop pub:
```

`UploadFile` shares `finalizeStoredUploadMetadataOnce`. Default nop must not
change that path. The fence is **not** installed in production.

## Own pin is `up:<session>`, not `pub:<commit>`

`pub:<attempt>` does not exist at classify. Own `pub:` is not commit-ready on
retry (`classifyBlockReferrerProvenance` treats `pub:` as `None`). The harness
therefore pins `up:<session>` with `AddProvisionalBlockReferenceWithExpiry`.

## Named evidence legs

| Field | Plane | Property |
|-------|-------|----------|
| `currentHeadAfterCut` | current | afterVerified: drop `fs:`, claim, EACH_QUORUM=0, handoff. HEAD+promote still land. D unrevoked. New-request `ProbeBlockReuse=BlockedByGC`. **This is the last dangerous point.** |
| `currentPubRevokesZeroProof` | current | afterVerified: drop `fs:` only. afterStaged: claim; EACH_QUORUM sees `pub:` and releases. HEAD proceeds. `pub:` is quorum-visible. |
| `currentPubAfterZeroProof` | current | C2 analog: zero-proof without handoff, then `pub:`, then handoff. Late `pub:` does not revoke D. Current HEAD still proceeds. |
| `harnessWriterWins` | harness | pin `up:<session>` before removing `fs:` / zero-proof. EACH_QUORUM releases. HEAD+`fs:`. **not production protocol** |
| `harnessCutAfterClassify` | harness | D2 interleaving then `BlockDeleteFenceActive` in beforeHead → `ErrBlockDeleteInProgress`. HEAD must not occur. D unrevoked. **not production protocol** |
| `harnessLatePubStillFenced` | harness | post-cut `pub:` may land; fence still aborts HEAD. **not production protocol** |

## Measured results

Gated Docker run on 2026-09-01 (`go-integration-test` directed
`TestBorrowedFSHeadCharacterization`, `SESAMEFS_REQUIRE_BORROWEDFS_HEAD_CHARACTERIZATION=1`).
`missing=[] complete=true` in 11.78s. Each row is an observation, not a production change.

| Field | Observation | Notes |
|-------|-------------|-------|
| `currentHeadAfterCut` | RED (unguarded HEAD) | `pub:` staged after the cut; HEAD+promote landed; `fs:` present; D stayed `deleting`+handoff; new-request `BlockedByGC`. Last dangerous point on the current BorrowedFS path |
| `currentPubRevokesZeroProof` | GREEN | post-stage `pub:` was EACH_QUORUM-visible; GC released; HEAD proceeded |
| `currentPubAfterZeroProof` | RED (unguarded HEAD) | C2 analog: zero-proof, then `pub:`, handoff still committed; D unrevoked; current HEAD still proceeded |
| `harnessWriterWins` | GREEN (test-only) | `up:<session>` before dropping `fs:` revoked the authorizing read; HEAD+`fs:` landed. Not production protocol |
| `harnessCutAfterClassify` | GREEN (test-only) | D2 interleaving then beforeHead `BlockDeleteFenceActive` returned `ErrBlockDeleteInProgress` (HTTP 409); HEAD did not advance; D unrevoked. Not production protocol |
| `harnessLatePubStillFenced` | GREEN (test-only) | post-cut `pub:` landed; fence still aborted HEAD. Not production protocol |

## What this PR does not do

- Does not implement a production writer slow path or change readiness policy.
- Does not touch `worker.go`, schema, H, F0b/F2, orphan/020, or `GC_ENABLED`.
- Does not inflate X1's 15 named legs.
- Does not close X1 or R3.

## Verdict

Terminal state of this document must be exactly one of:

- `PROMISING`
- `PROMISING_WITH_PREREQUISITE`
- `REJECT`

**Current verdict: PROMISING_WITH_PREREQUISITE**

Current `CreateFileFromBlocks` BorrowedFS publication is still unguarded through
HEAD: that is the last dangerous point X1 D2 left unmeasured. The harness shows
that an own `up:<session>` pin can revoke zero-proof and that a beforeHead fence
can abort HEAD, including after post-cut `pub:`. That harness is **not production
protocol**. A later PR must choose a production continuity protocol that keeps
the +0 hot-path authority-read contract.

X1 remains OPEN. R3 remains OPEN. Production remains `GC_ENABLED=false` fleet-wide.

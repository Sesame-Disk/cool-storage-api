# Live-library metadata reclamation (P6-3)

Source of record for `ISSUE-GC-LIVE-LIBRARY-METADATA-RECLAMATION-01` (PRE-GC / A1).
Since P6-2 (#319), nothing reclaims unreachable fs_objects in a live library, or the
permanent `fs:` references and blocks they hold. A1 needs a disposition: a safe
reclamation, or an explicit, measured acceptance of the retention. The constraints
any implementation must meet are listed in the issue. This document starts with
the measurement, because the disposition depends on its numbers.

## P6-3a: read-only retention report (2026-10-20)

Base main@d99fdc5c2 (#319). Scope: measurement only. No deletion, enqueue, guard,
fence or reclamation design; no change to GC, the queue or any API.

### What it measures

For one live library (canonical `libraries` row present, `deleted_at` unset):

| Field | Meaning |
|---|---|
| `commits`, `head_chain_commits`, `off_chain_commits` | commit rows; those on HEAD's parent chain; the rest |
| `fs_objects` | fs_object rows in the library partition |
| `reachable_from_head_chain` | objects reached from the root of any HEAD-chain commit |
| `reachable_only_off_chain` | objects reached only from commits outside HEAD's chain (Phase 5's domain) |
| `unreferenced` | objects no commit reaches: in-flight or failed publications, residue of retired commits |
| `retained_files`, `retained_logical_bytes` | file objects outside HEAD's chain and the sum of their `size_bytes` |
| `retained_fs_refs` | distinct (object, block) pairs among those files: the permanent `fs:` references they keep alive |
| `dangling_references` | distinct fs_ids named by a tree reached from some commit but with no row (the all-zero empty-directory id is excluded; unreferenced trees are not walked) |

`retained_*` covers everything outside HEAD's chain, which is the upper bound a
reclamation could ever touch. `reachable_only_off_chain` is reported separately
because retained history is a product decision (`version_ttl_days`,
[TECHNICAL-DEBT §18](./TECHNICAL-DEBT.md#18-library-history-retention-and-auto-delete-semantics-2026-05-15)),
not garbage by definition.

### Limits, stated in the output

- **Not physical bytes.** `retained_logical_bytes` sums file sizes; blocks shared
  with HEAD, other objects or other libraries are not subtracted. It is an upper
  bound on what reclamation could free.
- **In-flight writes are included.** fs_objects carry no creation time, so an
  upload between `RecvFS` and its HEAD counts as `unreferenced`. Repeated runs
  separate stable residue from transient work.
- **A snapshot, not a consistent cut.** HEAD, commits and fs_objects are three
  reads. A concurrent publication can shift counts between categories; the
  report never acts on them.
- **Block ids as stored.** No SHA-1↔SHA-256 mapping is resolved; references are
  counted per stored id, as the `fs:` rows are keyed.

### Shape

- `internal/gc`: `MeasureLibraryMetadataRetention` takes a reader interface
  that contains read methods only, so it cannot mutate state by construction.
  `CassandraStore` and `MockStore` implement two new reads: the canonical HEAD with
  `deleted_at`, and the fs_object rows with `size_bytes`. Three reads per library:
  HEAD, the commit partition, the fs_object partition; the graph walk is in memory.
  A failed read, a missing HEAD commit or an fs_object whose `dir_entries` cannot
  be decoded makes the library `complete: false` with the error; no partial
  numbers are reported as complete. A canonical row with no HEAD (the ghost row
  of `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01`, which GC treats as absent) is
  counted as `libraries_without_head`, not measured.
- Enumeration reads `(library_id, org_id)` from `libraries_by_id`: one scan, or
  one point read for `-library`. Libraries that exist only in `deleted_libraries`
  are already hard-deleted, so not live. There is no org lookup with a fallback
  that could turn a read error into "not found".
- `cmd/sesamefs`: `gc-metadata-retention-report [-library ID] [-org ID]
  [-max-libraries N]` prints one JSON line per measured or incomplete library and
  a summary line. Exit 0 when every library in scope is complete; 1 for a usage,
  configuration, connection or output error (including positional arguments);
  2 when the enumeration fails or any library is incomplete. Its cost is one
  `libraries_by_id` scan plus three partition reads per live library.

### Usage

```sh
# Read-only. Same configuration and environment as the server (CONFIG_PATH,
# CASSANDRA_*). JSON lines on stdout; diagnostics on stderr.
sesamefs gc-metadata-retention-report -library <library_id>
sesamefs gc-metadata-retention-report -org <org_id> -max-libraries 100 > retention.jsonl
```

`-max-libraries` counts measured or incomplete libraries; libraries that are not
live or have no HEAD are skipped without counting. The last line is the summary
(`"summary": true`); its `libraries_listed` equals measured + incomplete + not
live + without HEAD for the scope. Run it with GC OFF, at least twice some days
apart.

### Verification

- Unit (`MockStore`): exact counts for a library with a HEAD chain, an off-chain
  commit that shares a subtree with HEAD, an unreferenced orphan tree with sized
  files, duplicate block ids and a dangling entry; soft-deleted and missing
  libraries are not live; a failing read and a missing HEAD commit are incomplete,
  never complete; HEAD-less, org-less and hard-deleted rows; empty `obj_type`;
  strict `dir_entries` decoding; the enumeration honours its filters and limit.
  The command rejects bad usage with exit 1 before touching configuration.
- Real stack: productive Sync writes on Cassandra/S3 (HEAD publication, an
  off-chain commit, an unreferenced received tree), then the report on that
  library through `CassandraStore`, with exact counts and an unchanged queue, refs
  and fs_objects before and after. Read-only, so it needs no GC isolation and
  runs in the ordinary integration suite.
- `go-all-test` in a private Compose project, plus vet, gofmt and link checks.

Backward compatible: yes. A new read-only subcommand and store reads; no schema,
API, queue, configuration or GC behaviour change. PRE-GC/A1 remain OPEN;
ordinary GC stays OFF.

After merge: run the report on a representative deployment (GC OFF) at least
twice, some days apart, and record the numbers here before choosing the
disposition.

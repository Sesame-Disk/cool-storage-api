# Storage-class placement options for canonical blocks

**Date:** 2026-08-20
**Status:** Detailed analysis. No option is approved for implementation.
**Priority:** Option B is the preferred candidate for the next design phase.

**Deployment scope:** The target deployment is greenfield. There is no
production data that must be preserved, so the Cassandra schema can be designed
directly for the selected identity model and the service can be deployed from
an empty database. The analysis still covers runtime compatibility because
empty initial state does not remove races between concurrent writers, failover,
or GC.

This document analyzes the three placement domains described in
[UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md).
It is intentionally more specific about the effect on the current Cassandra
schema, upload paths, readers, references, garbage collection, failover and
product behavior.

The document is an analysis, not an implementation plan approval. In
particular, making placement deterministic does not by itself close X1, does
not authorize destructive GC, and does not prove that the metadata LWT can be
removed safely.

## Executive verdict

Option B is the cleanest boundary if a storage class represents a genuinely
different physical namespace:

```text
logical block = (org_id, storage_class, representation_id, sha256)
```

The shorter form proposed in the hot-path analysis is also valid if the system
first proves that `representation_id` is completely determined by the content
hash:

```text
logical block = (org_id, storage_class, sha256)
```

The current implementation does not make that assumption silently. The block
metadata path persists `representation_id` and rejects a conflicting value for
an existing hash. Therefore the preferred design must either include
`representation_id` in the logical identity or make its determinism an
explicit invariant before the install LWT is removed.

The recommendation is:

1. Make library placement explicit and immutable.
2. Scope block identity by `storage_class`.
3. Keep the metadata LWT while the new identity and lifecycle protocol are
   being designed and measured.
4. Treat cross-class failover as a separate replication or migration feature,
   not as transparent placement.
5. Only evaluate removal of the normal install LWT after exact physical
   identity, stale-writer safety and ambiguous-outcome settlement are proven.

Option A is a useful simplification only if the product accepts one placement
class per organization. With per-library classes and org-global deduplication,
it does not remove the placement race.

Option C preserves global deduplication, but it conflicts with the current
invariant that every block of a file uses one storage class. It would also make
regional placement a function of content rather than a library policy. It is a
larger architectural change than B.

## Verified current state

### Identity and schema

The current active identity model is:

```text
physical block:  (org_id, internal_sha256)
external alias:  (org_id, representation_id, external_sha1)
```

The relevant current tables are:

| Surface | Current identity | Consequence |
|---|---|---|
| `blocks` | `PRIMARY KEY ((org_id, block_id))` | One canonical metadata row exists for one org and hash, regardless of class proposed by a writer. |
| `block_references` | `PRIMARY KEY ((org_id, block_id), referrer)` | Liveness is global for the org/hash and cannot distinguish the same hash in two classes. |
| `gc_block_candidates` | `PRIMARY KEY ((org_id, block_id))` | One candidate row can represent only one logical block. |
| `gc_provisional_block_refs` | `PRIMARY KEY ((org_id, block_id), referrer)` | Provisional liveness and expiry are also global for the org/hash. |
| `gc_s3_orphans` | `PRIMARY KEY ((org_id, block_id))` | Recovery cannot represent two class-scoped physical lives with the same hash. |
| `block_id_mappings` | `PRIMARY KEY ((org_id, representation_id, external_id))` | This maps SHA-1 to SHA-256 content identity. It is not currently a physical locator map. |

The schema is defined in `internal/db/migrations/001_initial_schema.cql` and
the representation-aware forward mapping in
`internal/db/migrations/009_block_representation_mappings.cql`.

### Placement resolution

The current priority is:

```text
library.storage_class != empty -> library class
library.storage_class == empty   -> request hostname/region/default
```

This is implemented by `Manager.ResolveStorageClass` and
`resolveLibraryBlockStoreForRequestContext`.

The empty value is therefore not equivalent to a fixed default. It means that
the request can select a different class depending on the endpoint that
received it. A Cassandra read error is a third state and is intended to fail
closed, not to select a default backend.

### Physical locator

The current S3 locator is derived from the hash:

```text
blocks/<org_id>/<first-two-hash-chars>/<next-two-hash-chars>/<hash>
```

The key does not contain the class. The class selects the S3 namespace, so the
same key can exist in two physically different buckets. The current readers,
reuse checks and delete paths still derive this key. A persisted non-empty
`storage_key` is currently checked against the derived value rather than being
used as an arbitrary authoritative locator.

### Install LWT and measured design numbers

`UpsertBlockMetadataWithRepresentationAndSHA1` uses:

```sql
INSERT INTO blocks (...)
VALUES (...)
IF NOT EXISTS
```

The LWT exists because two writers can currently propose different physical
classes for the same `(org_id, block_id)`. A blind last-writer-wins update could
point metadata to a class that does not contain the object.

The repository records these current numbers:

| Quantity | Current value or relationship |
|---|---|
| Canonical CAS block size | 8 MiB by default |
| New blocks in 1 GiB | Approximately 128 |
| Metadata LWTs for 1 GiB reaching registration | Approximately 128 |
| Metadata LWTs for 1,000 registering blocks | Approximately 1,000 |
| SeafHTTP chunked-finalization metadata materialization concurrency | 1 per process for `finalizeUploadStreaming`; other upload paths are not all covered by this permit |
| Web upload per-user block concurrency | 8 by default |
| Shipped serial consistency | `SERIAL` in `configs/config.prod.yaml`; runtime env and topology determine the effective setting and WAN scope |

The numbers are for blocks that reach metadata registration. Existing complete
deduplicated blocks can be classified before registration and avoid that LWT.
The LWT count is not a single global Cassandra partition lock: different block
partitions can run concurrently, but each registering block still pays its own
`SERIAL` LWT/Paxos transaction when that is the effective setting. Network
round-trip count depends on Cassandra's Paxos variant.

### Existing file and client invariants

The architecture documentation requires all blocks of one uploaded file to use
the same storage class and says that the policy is evaluated at the start of the
upload session. The current web block-upload session schema does not yet store a
`storage_class` snapshot, and the upload handler resolves the library placement
when handling block requests. A new design must freeze the placement on the
session and verify it again at commit; the documented invariant is not enough
if the implementation does not persist its value.

At the Seafile boundary, the internal SHA-256 layout must not leak into the
desktop protocol for the v2 and SeafHTTP writers. The `RecvFS` desktop-sync
path intentionally retains the legacy layout in which `fs_objects.block_ids`
contains SHA-1 and `seafile_block_ids_sha1` is empty, because the client can
send `recv-fs` before `put-block`. In every layout, the Seafile `fs_id` remains
SHA-1-derived. A placement change must preserve both the canonical and legacy
boundaries.

### GC and X1 status

The current GC sequence includes:

1. local reference discovery;
2. a claim LWT on `blocks.gc_state`;
3. an `EACH_QUORUM` reference check before destructive deletion;
4. orphan recovery state persisted in Cassandra with the current 90-day TTL;
5. canonical row deletion;
6. S3 deletion and recovery cleanup.

The destructive GC path remains disabled because X1 physical-delete ABA and
related publication/claim races are not closed. Deterministic placement only
removes one part of the normal install race. It is not a GC authorization.

## Option A: class fixed by library

### Definition

The library is the complete placement authority:

```text
library.storage_class = hot-s3-na
```

Every block written through that library uses that class and never derives a
class from the request hostname.

### What A fixes

1. A single library no longer proposes NA on one request and EU on another
   merely because traffic reached a different endpoint.
2. File-level placement remains simple because a library already represents a
   natural placement boundary.
3. `fs_objects` can continue storing only block hashes if the library class is
   immutable and every block in the file follows it.
4. The schema change can be smaller than B if the organization is guaranteed to
   use one class globally.

### What A does not fix

With the current org-global deduplication, this race remains:

```text
library A -> hot-s3-na -> hashX
library B -> hot-s3-eu -> hashX
```

Both libraries still address the same current Cassandra row:

```text
PRIMARY KEY ((org_id, block_id))
```

The writers still compete to establish the canonical class. The LWT remains
necessary unless one of the following product rules is introduced:

1. one class is allowed for the entire organization;
2. the block identity also includes the library;
3. deduplication across libraries with different classes is disabled, which is
   effectively a form of B.

Making the class fixed by library does not guarantee that a block used by an EU
library is physically in EU. The first writer from another library can still
win the org-global canonical row.

### Required changes under A

| Area | Required change |
|---|---|
| Library creation | Persist a non-empty class for every library. |
| Empty class behavior | Remove hostname/default routing for block placement or define a one-time assignment at library creation. |
| `ChangeStorageClass` | Make immutable or implement a complete block migration before changing the value. |
| Failover | Do not silently persist a different physical class as the library's placement. |
| Initial schema | Do not carry a legacy org/hash-only block schema into the greenfield deployment. |
| LWT | Keep it if different libraries may still use different classes for the same org/hash. |

### Product effect

A is attractive when an organization should have one data-residency location.
It is not sufficient for the current product model, which exposes per-library
storage selection and multiple regional classes.

### Verdict on A

A is a low-complexity partial fix. It is not a complete solution while
deduplication remains org-global and libraries can select different classes.

## Option B: class included in logical identity

### Definition

The logical identity becomes:

```text
L = (org_id, storage_class, content_hash)
```

For the current representation-aware implementation, the safer complete form
is:

```text
L = (org_id, storage_class, representation_id, internal_sha256)
```

The class is part of the block identity, not just a mutable attribute on one
org/hash row.

### What B fixes

1. `hot-s3-na/hashX` and `hot-s3-eu/hashX` are different logical blocks.
2. Writers for the same logical block necessarily propose the same class.
3. A first writer in NA cannot force an EU library to reuse the NA object.
4. Deduplication remains available between libraries using the same class.
5. GC candidates, references and orphans can be scoped to the exact class.
6. Regional storage policy becomes a property of the materialized block rather
   than a race outcome.
7. The design matches the repository's append-only storage namespace contract.

### What B costs

Identical bytes used in two classes require two physical objects. With three
active classes, a corpus that must exist in all three can reach 3x the physical
storage of global deduplication.

Example:

```text
1 GiB content = 128 blocks at 8 MiB
same content in NA + EU + Asia = 384 class-scoped block materializations
```

This is a worst-case or deliberate multi-class materialization cost. Content
used only in one class has no additional cost. Content shared by many libraries
within one class still deduplicates once.

### Required schema changes

The current block-partition tables cannot represent both class-scoped lives if
`storage_class` remains only a regular column.

If the complete representation-aware identity is selected, the conceptual keys
become:

```cql
blocks:
PRIMARY KEY ((org_id, storage_class, representation_id, block_id))

block_references:
PRIMARY KEY ((org_id, storage_class, representation_id, block_id), referrer)

gc_block_candidates:
PRIMARY KEY ((org_id, storage_class, representation_id, block_id))
```

If a separate proof establishes that `representation_id` is a deterministic
function of `(org_id, storage_class, block_id)`, it can be omitted from the
partition key. That is a design decision that must be made before coding, not
an assumption to hide in a migration.

For a new deployment these keys can be adopted directly in the initial schema.
An existing deployment would additionally need a backfill, dual-read or
dual-write period, and an explicit cutover plan; no migration strategy is
approved by this analysis.

The exact CQL table names are an implementation decision, but the identity
constraint is not. The following surfaces must carry the class, and the
representation when it is part of the selected identity, as identity or as a
verified immutable snapshot:

| Surface | B requirement |
|---|---|
| `blocks` | Partition by org, class, representation and SHA-256, unless representation determinism is made an explicit invariant. |
| `block_references` | A reference to class A must not keep class B alive, and vice versa. If representation is part of the identity, references carry it too. |
| `gc_block_candidates` | Candidates for the same hash in two classes must coexist. |
| `gc_block_candidates_by_day` | Discovery identity must distinguish class A from class B. |
| `gc_provisional_block_refs` | Expiry tracking must be scoped to the class-scoped block. |
| `gc_provisional_block_refs_by_day` | Discovery must preserve the same identity. |
| `gc_s3_orphans` | Recovery must distinguish same-hash orphans in different classes. |
| `gc_s3_orphans_by_day` | The projection must not collapse those identities. |
| GC queue items | Block operations must carry class even where the queue primary key already includes time. |
| Publish repair rows | Staged block IDs need a class snapshot or a reliable library placement snapshot. |
| OnlyOffice pending rows | Pending blocks need class identity when they can outlive the request. |

### `fs_objects` compatibility under B

`fs_objects.block_ids` can remain a list of internal SHA-256 IDs only under a
strong invariant:

```text
every library has one immutable storage_class
every upload session stores that class as an immutable snapshot
every block in every file of that library uses that class
```

Under that invariant, readers obtain the class from the library or the durable
file/session snapshot and query the class-scoped block identity. The current
web session model must be extended because it currently stores the repository
but not the resolved class.

If a library may contain blocks from multiple classes, `fs_objects` needs a
parallel class list or another internal block identity representation. The
class list must remain positional and must not be exposed as a Seafile block ID.

### `ChangeStorageClass` under B

The current endpoint updates the library row and leaves block migration as a
TODO. That behavior is unsafe under B.

The supported choices are:

| Choice | Result |
|---|---|
| Make placement immutable | Smallest safe product change. A class change returns a conflict unless a migration exists. |
| Migrate blocks before cutover | Copy every referenced block, create class-scoped identities, update the library atomically, and retain rollback/recovery state. |
| Add placement versions per file | More flexible, but requires class metadata for each file/block and broad reader/GC changes. |
| Update only `libraries.storage_class` | Unsafe. Existing `fs_objects` continue pointing logically to blocks in the old class. |

The preferred first implementation is immutable library placement.

### Failover under B

The current health-aware selector may return a failover class different from the
preferred class. That is incompatible with deterministic class identity if the
fallback is another physical namespace.

B requires one of these contracts:

1. Failover stays inside the same physical namespace, for example alternate
   endpoints or credentials for one class.
2. Writes fail when the canonical class is unavailable.
3. A separate replication/migration operation creates a new class-scoped copy.
4. The actual fallback class becomes a new explicit placement identity and is
   recorded consistently for the whole affected library/file.

The unsafe behavior is to let one writer use `hot-s3-na` and another use
`hot-s3-eu` because health state differed, then claim placement is deterministic.

### Does B remove the metadata LWT?

B removes the reason for the LWT that chooses between different classes. It does
not automatically remove every safety reason for conditional writes.

The following conditions still have to be proven:

1. All writers of one identity produce the same immutable metadata.
2. `representation_id`, SHA-1, size and storage key cannot conflict for one
   identity.
3. A stale writer cannot recreate a retired incarnation after GC.
4. Repairing an existing physical incarnation cannot recreate a missing
   canonical row incorrectly.
5. An ambiguous install timeout cannot cause key reuse or speculative cleanup.
6. GC claims, orphan records and deletes refer to the same exact physical tuple.

The current hash-derived key is reused across physical lives. Therefore B should
first be implemented with the metadata LWT retained. Removing the LWT belongs
after the exact-`P` protocol described in
[GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md) is designed and proven.

### Product and storage effect

B makes storage class meaningful as physical placement. It improves regional
locality and residency at the cost of class-crossing deduplication. The product
must explicitly accept that the same bytes can consume one object per required
physical class.

### Verdict on B

B is the preferred architecture if storage classes represent distinct physical
namespaces and regional placement is a product requirement. In this greenfield
deployment it is a direct initial-schema redesign, not a production data
migration, but it remains a broad code-path change rather than a one-line
optimization.

## Option C: home class derived from org and hash

### Definition

For example:

```text
home = H(org_id, block_id) % availableClasses
```

Every library and writer for the same org/hash uses the same home class.

### What C fixes

1. It preserves org-global deduplication.
2. It removes request-host dependence from normal placement.
3. It gives the same hash the same class while the placement function and class
   set remain stable.
4. It can avoid the class election without duplicating identical content across
   classes.

### File-level placement conflict

The current architecture requires all blocks of one file to use one class.
With three uniformly selected classes and a 128-block GiB file:

```text
P(all 128 blocks in one class) = 3 * (1/3)^128 = 3^-127
```

That is effectively zero. A normal large file will be spread across regions.
Approximately two-thirds of its blocks will be remote from any one region under
uniform placement.

To adopt C, the system would need to choose between:

1. abandoning the one-class-per-file invariant;
2. storing a class alongside every block ID in `fs_objects`;
3. changing the function to choose a home per file, which is no longer the
   proposed per-block hash placement;
4. maintaining local replicas, which adds a second physical replication model.

Each choice affects readers, previews, ZIP, copy, sync, GC, references and
storage accounting.

### Class-set changes

The naive modulo function is not stable when the class set changes. With the
same class order, changing from three to four classes causes approximately 75%
of hashes to receive a different result.

Therefore C needs:

1. a versioned placement function;
2. stable class membership;
3. a consistent-hashing or equivalent remapping policy;
4. a persisted placement version for existing blocks;
5. a migration policy for removed or newly added classes;
6. explicit handling for classes that are unhealthy or unavailable.

Removing a class from the active set cannot be treated as a normal config
reload. Existing hashes must continue resolving to their old class.

### Library policy and residency

C transfers authority from `library.storage_class` to the hash function. An EU
library can receive a hash whose home is NA. That conflicts with regional
residency and with the current library override priority.

Possible mitigations are all architectural additions:

1. restrict the allowed class set per organization;
2. reject content whose home is outside the policy;
3. replicate content to the requested region;
4. reinterpret the library class as a replica preference instead of canonical
   placement.

None is a small change to the current resolver.

### Failover and hot/cold policy

If a hash's home class is unavailable, writing into another class violates the
deterministic function. Failing the write reduces availability. Replicating the
block introduces physical copies and a new liveness/GC contract.

Hot/cold transitions also become a separate materialization operation. Changing
the class set or tier cannot silently move the canonical object without changing
the identity and recovery rules.

### Verdict on C

C is appropriate only if global deduplication is more important than regional
locality and file-level placement. It requires a larger redesign than B and is
not compatible with the current file placement invariant without additional
metadata.

## Cross-option comparison

| Criterion | A: library fixed | B: class in identity | C: hash home |
|---|---:|---:|---:|
| Same hash can have distinct class lives | No | Yes | No |
| Removes current cross-class placement race | No, unless one class per org | Yes | Yes, if function is stable |
| Preserves cross-class deduplication | Yes | No | Yes |
| Preserves one class per file | Yes if library is immutable | Yes if library is immutable | No |
| Preserves library residency authority | Yes | Yes | No |
| New schema scope | Low to medium | High | Medium to high |
| GC identity changes | Medium | High | High |
| Sensitivity to class-set changes | Low | Low | High |
| Storage duplication | None from placement | Up to number of classes | None from placement |
| Regional locality | Good only after first-writer issue is removed | Good | Poor for large files |
| Fit with current architecture | Partial | Best for a greenfield schema redesign | Weak |

## Recommended implementation sequence

### Phase 0: characterize before changing semantics

Do not change identity, consistency or LWT behavior in the measurement phase.
Instrument:

1. metadata registration count;
2. LWT attempts and latency;
3. `applied=true` and `applied=false` outcomes;
4. retries and timeout distributions;
5. classes proposed for the same org/hash across libraries and DCs;
6. deduplication loss under `(org, storage_class, hash)`;
7. class changes during active upload;
8. failover writes and the class actually persisted.

### Phase 1: freeze placement semantics

1. Require a non-empty, canonical `storage_class` for each new library.
2. Do not support an empty-class library in the new deployment schema.
3. Make class changes immutable or route them through migration.
4. Prevent cross-namespace health failover from silently changing canonical
   placement.
5. Keep production `SERIAL` and keep destructive GC disabled.

### Phase 2: introduce the class-scoped identity

1. Define the initial schema with class-scoped block, reference, candidate,
   provisional-tracker and orphan identities.
2. Include `representation_id` in those identities unless its determinism is
   explicitly proven.
3. Add `storage_class` to the upload-session admission snapshot.
4. Validate that each newly committed file's blocks agree with its library and
   session placement.
5. Do not add backfill, dual-write or legacy-table compatibility code for the
   empty greenfield database.

### Phase 3: migrate every block path

There is no production data migration in this deployment. This phase is a code
and contract migration: every path must construct and consume the new identity
from its first write.

Update the identity passed through:

1. web block upload;
2. SeafHTTP upload and finalize;
3. sync `PutBlock` and receive paths;
4. OnlyOffice;
5. copy and move materialization;
6. block check and reuse probing;
7. canonical readers and previews;
8. ZIP and download;
9. permanent and provisional references;
10. GC candidates, claims, orphans and recovery.

The SHA-1 to SHA-256 mapping remains a content mapping and must not become an
accidental class lookup unless that is explicitly designed.

### Phase 4: prove B while retaining the LWT

Required tests:

| Test | Expected result |
|---|---|
| Same org/hash/representation, same class, concurrent writers | One class-scoped canonical identity and one logical object. |
| Same org/hash/representation, two classes | Two independent identities and two physical objects. |
| Reuse from another library, same class | Reuse succeeds. |
| Reuse from another library, different class | Reuse does not cross the class boundary. |
| GC candidate in class A, live reference in class B | Class A operation cannot delete or clear class B state. |
| Class A unavailable during write | No silent write to class B unless replication is explicit. |
| Library class change during upload | Operation is rejected or follows a durable migration state. |
| Seafile desktop client reads new data | SHA-1 block list and SHA-1-derived `fs_id` remain unchanged; the intentional `RecvFS` legacy layout still works. |

### Phase 5: separately evaluate LWT removal

Only after B is proven should the project evaluate removing the normal install
LWT. That work must also prove:

1. exact physical tuple identity `P = (storage_class, storage_key)`;
2. non-reused physical keys for new incarnations;
3. locator-authoritative reads and existence checks;
4. install and repair are separate operations;
5. stale writers cannot reinstall retired lives;
6. claims and finalization are tuple-bound;
7. ambiguous outcomes are settled before reuse or deletion.

GC LWTs and other conditional lifecycle operations must not be weakened merely
because normal block installation no longer uses Paxos.

## Final decision proposal

The next design decision should be:

```text
Adopt B as the target logical identity, with immutable library placement,
but do not remove the install LWT in the same change.
```

This separates two questions that are currently coupled:

1. Which logical block is being addressed?
2. Which physical incarnation may be installed or destroyed?

B answers the first question. X1, exact physical locators and the generation or
incarnation protocol answer the second. Treating them as one mechanical LWT
removal would create a safety regression.

## Source references

The analysis was checked against these current sources:

- `internal/db/migrations/001_initial_schema.cql`: `blocks`,
  `block_references`, GC candidates, provisional trackers and S3 orphans.
- `internal/db/migrations/009_block_representation_mappings.cql`:
  representation-aware SHA-1 mapping.
- `internal/db/block_references.go`: metadata LWT, reuse probe, references and
  consistency pins.
- `internal/storage/storage.go`: class resolution and health failover.
- `internal/storage/blocks.go`: hash-derived physical key and delete API.
- `internal/api/v2/storage_resolution.go`: library/request placement resolution.
- `internal/db/block_upload_sessions.go`: current web upload-session admission
  fields and the absence of a placement snapshot.
- `internal/api/v2/blocks.go`: session block-store resolution and the returned
  actual class after health-aware selection.
- `internal/api/sync.go`: intentional `RecvFS` legacy fs-object layout.
- `internal/api/v2/libraries.go`: current `ChangeStorageClass` behavior.
- `internal/api/v2/upload_reuse.go`: canonical class and derived-key checks.
- `internal/streaming/canonical_block_reader.go`: canonical block lookup.
- `internal/gc/worker.go`: claim, global liveness verification and deletion flow.
- `internal/gc/store_cassandra.go`: block identity queries, claims and finalization.
- `configs/config.prod.yaml`: production serial consistency and regional classes.
- `docs/ARCHITECTURE.md`: placement policy, file-level placement and retrieval.
- `docs/UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md`: current LWT numbers and
  candidate placement domains.
- `docs/GC-X1-CLOSURE-OPTIONS.md`: exact-`P` and physical ABA requirements.
- `docs/SHA256-CANONICAL-BLOCK-IDS.md`: Seafile SHA-1 boundary invariants.

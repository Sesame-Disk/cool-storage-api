# Storage-class placement options for canonical blocks

**Date:** 2026-08-20
**Status:** Detailed analysis. No option is approved for implementation.
**Priority:** The current reference model is mutable library preference plus
org-global canonical blocks. Options B and C are deliberate product changes.

**Deployment scope:** This analysis may be evaluated against a greenfield
deployment, but that is a deployment assumption, not a runtime property proved
by the code. If existing data must be preserved, any class-scoped identity
requires an explicit migration/cutover plan.

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

The current product contract is:

```text
canonical block row identity = (org_id, internal_sha256)
representation_id = conflict-checked once established on that row

library.storage_class
    = mutable preferred class for future first materialization

blocks.storage_class
    = actual canonical physical namespace selected for this block
```

`representation_id` is deliberately not described as immutable. An empty stored
value is backfilled: `ensureBlockIdentityRow` calls
`backfillBlockRepresentationIDFn` when the canonical row carries none
(`internal/db/block_references.go`). What the row refuses is a *different*
non-empty value, which it rejects as a permanent conflict. The invariant is "once
established, conflict-checked", not "never written".

The current `blocks` table is keyed by `(org_id, block_id)`. A first writer with
no canonical row proposes the class selected from the library preference,
request routing, or health failover. The actual class returned by that selection
is persisted on the canonical row. A concurrent writer proposing another class
does not create a second canonical row: the first-writer LWT chooses the winner.

When a canonical row already exists, the library preference does not relocate or
reinterpret it. Reuse and repair resolve the row's persisted `storage_class` and
derive the same org-scoped hash key. A normal reusable block therefore avoids a
physical PUT; a missing canonical object is repaired in its existing canonical
class. Upload paths that reach metadata registration still execute the metadata
LWT even when the physical PUT was skipped.

Changing `libraries.storage_class` is therefore a cheap preference change:
existing block rows and objects remain where they were, while later first
materializations prefer the new class. `ChangeStorageClass` currently updates
the library row and its administrative read model in one logged batch. The code
contains a TODO for a separate future migration operation; it does not move
bytes today.

The current physical key is still hash-derived:

```text
blocks/<org_id>/<first-two-hash-chars>/<next-two-hash-chars>/<sha256>
```

`storage_key` is persisted as metadata but current readers and writers derive
this key and reject a conflicting persisted value; it is not yet an arbitrary
locator authority. The physical tuple today is therefore the canonical class
plus the derived key, not a free-form `storage_key`.

The recommendation is:

1. Keep `library.storage_class` mutable and document it as future-placement
   preference only.
2. Keep the current `(org_id, block_id)` global-deduplication domain unless a
   product decision explicitly changes it.
3. Keep the first-writer metadata LWT while that global arbitration is part of
   the product contract; measure and optimize its cost instead of assuming it
   is accidental.
4. Keep canonical readers, reuse, repair and GC bound to `blocks.storage_class`.
5. Treat data migration as a separate explicit operation that must account for
   blocks shared by multiple libraries and report transferred GB/cost.
6. Do not evaluate LWT removal until an alternative arbitration model and the
   exact physical-incarnation protocol are both proven.

Option B remains a technically clean alternative only if the product accepts
class-scoped deduplication and the physical locator/liveness model is redesigned
with it. It is not the current product reference because a preference change
would otherwise cause a second materialization of content already present in
another class.

Option C preserves global deduplication but removes library preference as the
placement authority. It conflicts with regional library policy and the current
file-level placement expectations, so it is also a product change.

## Confirmed Product Contract

The current code confirms the following split:

```text
library.storage_class
    mutable preference for a new canonical materialization

blocks.storage_class
    immutable class recorded for the canonical block row

existing canonical block
    reused or repaired in blocks.storage_class, regardless of the library's
    current preference

ChangeStorageClass(A -> B)
    changes future preference only; it does not rewrite old block rows or bytes
```

The class name itself remains an append-only physical namespace identity under
R23b. A class name must not be rebound or reused for another bucket/backend.
That namespace rule is independent from the mutability of a library's
preference.

The upload-session boundary is not fully implemented as a class snapshot. The
web session schema has no `storage_class` field, and block requests read the
library preference when resolving a preferred store. If the preference changes
while a session is active, successive first materializations can therefore see
different preferences; a single-class-per-file guarantee would require a future
session snapshot and commit check. This document must not claim that snapshot is
already enforced.

The future migration TODO in `internal/api/v2/libraries.go` is intentionally
separate. A UI may eventually offer an explicit "migrate existing data" option,
estimate bytes and transfer cost, and run a durable job. Changing the preference
alone must not copy, delete, or reinterpret shared canonical blocks.

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
library.storage_class != empty -> library preferred class
library.storage_class == empty   -> request hostname/region/default preference
```

This is implemented by `Manager.ResolveStorageClass` and
`resolveLibraryBlockStoreForRequestContext`.

The empty value is therefore not equivalent to a fixed default. It means that
the request can select a different preferred class depending on the endpoint
that received it. A Cassandra read error is a third state and is intended to
fail closed, not to select a default backend.

### Residency policy, failover and preference changes

The organization residency policy and the storage-manager failover policy are
separate controls:

| Control | Current scope | Does not do |
|---|---|---|
| `organizations.storage_config.data_residency: flexible` | New-library creation: requested class, request hostname/region, organization `default_region`, then global hot default | Does not relocate existing libraries or blocks |
| `organizations.storage_config.data_residency: strict` | New-library creation: requires `default_region`; a requested class must be configured, hot-tier and mapped to the allowed region | Does not make later block materialization fail closed or constrain backend failover |
| `libraries.storage_class` | Mutable preferred class persisted for a concrete library | Does not identify the physical class of historical blocks |
| `storage.classes.<name>.failover_class` | New-materialization backend selection after the preferred class is already marked `Unhealthy` or `Failed` | Is not inferred from residency, hostname, region mapping or the library value |

The creation resolver is `resolveCreateStorageClassForOrg` in
`internal/api/v2/storage_policy.go`. Flexible creation follows requested class,
hostname region, organization `default_region`, then the global hot class.
Strict creation requires a configured hot class for `default_region`; a
requested class must resolve to that region, and no requested class selects the
region's hot class. The resolved value is persisted in `libraries.storage_class`
by the library and administrative create paths. The policy is not consulted by
the block-store selector after creation.

`ChangeStorageClass` in `internal/api/v2/libraries.go` validates that the class
is known, reads the live library and updates `libraries.storage_class` together
with the administrative read model. It does not revalidate the organization's
strict residency policy, update `blocks`, modify `fs_objects` or references,
copy/delete S3 objects, calculate cost, or enqueue a migration job. Therefore a
strict organization can currently change an existing library to any known class
accepted by that endpoint, even when that class is outside the organization's
creation region. This is a confirmed policy boundary, not a migration feature,
and it is tracked as `ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01` in
[KNOWN_ISSUES.md](./KNOWN_ISSUES.md).

For a new block, the handlers first resolve a preferred class and then call
`GetHealthyBlockStoreForOrg`. `GetHealthyBackend` returns the preferred class
when its state is `Unknown`, `Healthy` or `Degraded`; it follows
`failover_class` only for `Unhealthy` or `Failed`. The returned `actualClass`,
not the library preference, is passed to metadata registration and persisted on
the canonical block row. A failover can therefore cross the strict creation
region for a new block while leaving `libraries.storage_class` unchanged. That
branch is latent rather than live: as recorded below, no non-test caller marks a
class `Unhealthy` or `Failed`, so no request can currently reach it. The
reachable residency gap is the `ChangeStorageClass` one above.

`CheckHealth` probes `Exists("__health_check__")`. A connection or other probe
error sets the backend to `Unhealthy`; a response slower than five seconds sets
it to `Degraded`; a not-found response is considered `Healthy`. The
`HealthFailed` result is returned for an unknown backend name, while an exhausted
failover chain returns an error rather than marking a class automatically. The
only production caller found for `UpdateHealth` is `CheckHealth`, and no
periodic production caller of `CheckHealth` or `CheckAllHealth` exists. A normal
S3 error in a PUT/GET path therefore does not itself update the health map or
guarantee that the next request fails over. A failover cycle is supported
configuration, not a misconfiguration: `config.prod.yaml` ships `hot-s3-na` and
`hot-s3-eu` pointing at each other precisely so either region can be primary. The
requirement is that a fully unhealthy cycle fails closed rather than looping, and
`GetHealthyBackend`'s visited-set walk does exactly that, returning a cycle error
on revisit. Startup validates that referenced class names exist; it does not
validate same-region policy or strict-policy compliance, and it is not expected to
reject cycles.

Legacy `storage.backends` entries are registered with an empty failover class,
so that compatibility format has no configured fallback unless the deployment
uses the modern `storage.classes` format.

The production modern classes currently define these failover edges in
`configs/config.prod.yaml`:

| Preferred class | Configured failover class |
|---|---|
| `hot-s3-na` | `hot-s3-eu` |
| `hot-s3-eu` | `hot-s3-na` |
| `hot-s3-asia` | `hot-s3-eu` |

The same file maps `na`, `eu` and `asia` to their corresponding hot classes.
Those mappings select the preferred class; they do not infer or override the
explicit `failover_class` edges.

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

The numbers are for blocks that reach metadata registration. Some web/sync
preflight paths can classify a complete existing block before registration and
avoid that metadata LWT, but a path that reaches registration still executes it
even when the physical PUT is skipped. The LWT count is not a single global
Cassandra partition lock: different block partitions can run concurrently, but
each registering block still pays its own `SERIAL` LWT/Paxos transaction when
that is the effective setting. Network round-trip count depends on Cassandra's
Paxos variant.

### Existing file and client invariants

The current web block-upload session schema does not store a `storage_class`
snapshot. Each block request resolves the library preference, while an existing
canonical block is resolved from its `blocks` metadata. A preference change or a
health failover during an active session can therefore produce different actual
classes for different first materializations unless a future session snapshot is
introduced. The current code does not enforce one class per file.

At the Seafile boundary, the internal SHA-256 layout must not leak into the
desktop protocol for the v2 and SeafHTTP writers. The `RecvFS` desktop-sync
path intentionally retains the legacy layout in which `fs_objects.block_ids`
contains SHA-1 and `seafile_block_ids_sha1` is empty, because the client can
send `recv-fs` before `put-block`. In every layout, the Seafile `fs_id` remains
SHA-1-derived. Readers resolve the internal block ID to the canonical
`blocks.storage_class` and derived key, so a library may contain files whose
canonical blocks were materialized in different classes; the current reader
does not infer historical placement from the library's current preference.

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

## Option A-prime: mutable library preference + global canonical block

This is the current product model and the reference option for this analysis.

### Definition

```text
logical/canonical block row = (org_id, internal_sha256)

library.storage_class
    = preferred class when that row has no canonical physical placement

blocks.storage_class
    = actual class selected by the first successful materialization
```

The class selected for a first writer may come from the library preference,
request routing when the preference is empty, or health failover. The actual
class returned by the store resolver is persisted on `blocks`. Later readers,
reuse and repair use that persisted class.

### What A-prime preserves

1. A library can change its future-placement preference without rewriting old
   `blocks` rows or physical objects.
2. Existing content remains globally deduplicated within the organization.
3. A library whose preference changed from NA to EU can reuse a canonical block
   already in NA; no second object is required merely because the preference
   changed.
4. `fs_objects.block_ids` remains a content identity list. Readers resolve each
   internal ID through canonical block metadata, so a library can contain files
   whose blocks were materialized in different classes.
5. A health failover for a new block is recorded as the actual canonical class;
   an existing block is never re-resolved through health failover.

### Current writer and reader coverage

The production writers converge on the same `probe -> store/repair -> metadata`
contract. Their preferred-store resolution is not an assertion that the
preferred class is canonical for an existing block:

| Surface | New/absent block | Existing canonical block |
|---|---|---|
| V2 session `internal/api/v2/blocks.go` | Resolves the library preference, selects a healthy store and persists the returned class | Reuse/repair resolves the probe's persisted class |
| V2 file/template `internal/api/v2/files.go` | Resolves a preferred healthy store, then `ResolveNeedsPutBlockStore` records the effective class | `EnsureReusableBlockPresent` verifies/repairs the canonical class |
| Sync `internal/api/sync.go` | `resolvePreferredBlockStore` selects the preferred/failover class | A non-empty canonical metadata class is selected exactly; fallback routing is only for the lookup/missing-metadata boundary |
| SeafHTTP `internal/api/seafhttp.go` | Resolves a preferred healthy store for upload/finalization | Canonical reader resolves the block metadata class before reading |
| OnlyOffice `internal/api/v2/onlyoffice.go` | Resolves a preferred healthy store for edited content | Existing content follows the canonical block path |
| Canonical reader `internal/streaming/canonical_block_reader.go` | Not a writer | Uses `blocks.storage_class`, the exact org-scoped store and the derived-key invariant; it never follows failover |

This coverage is why changing the library preference can produce a file whose
blocks span classes without making historical reads depend on the current
preference. The remaining legacy/fallback branches are deliberately called out
in the source comments and must not be used as evidence that canonical reads
fail over.

There is one availability qualification for upload/reuse callers: the current
handlers resolve an initial preferred/fallback store before running the reuse
probe. If that preferred class is already `Unhealthy` or `Failed` and has no
usable failover, the request can fail before it reaches the canonical probe even
when the block's canonical row is in another healthy class. This is an early
writer-admission dependency, not a canonical-reader redirect; a dedicated
future migration/replica design would be needed to remove it.

### The real first-writer race

Two libraries can propose different classes for the same absent org/hash:

```text
library A prefers hot-s3-na -> B1, K
library B prefers hot-s3-eu -> B2, K
```

Both writers address the same `blocks` row:

```text
PRIMARY KEY ((org_id, block_id))
```

The first-writer `INSERT ... IF NOT EXISTS` is therefore not accidental. It
chooses which physical namespace becomes canonical while preserving global
deduplication. A plain last-writer-wins write could point the row at a class
where the winning writer did not put the object. The losing writer's physical
side effects remain part of the existing orphan/fence and X1 work; the LWT only
chooses canonical metadata.

### What `ChangeStorageClass` does today

| Event | Current behavior |
|---|---|
| `ChangeStorageClass(A -> B)` | Updates `libraries.storage_class` and the administrative read model. |
| Existing canonical block | Keeps its `blocks.storage_class`, derived key and physical object. |
| Later upload of an already-canonical hash | Reuses or repairs the canonical class; it does not move to B. |
| Later first materialization of an absent hash | Prefers B, subject to routing and health failover. |
| Active web upload session | No persisted class snapshot; each absent-block request reads the current preference. |
| SeafHTTP upload/finalization | Resolves the preferred health-aware store once per operation and reuses the selected class while processing its blocks. A later preference change does not affect that active operation. |
| Optional migration | Not implemented; the code has a TODO for a separate future job. |

The future migration must be explicit because a block may be referenced by more
than one library. Moving a shared object for one library could break another;
the safe feature may need a copy/replica or class-scoped physical incarnation,
reference accounting, progress and recovery state, and a reported transfer cost.

### Reader and manifest behavior

The canonical reader loads `blocks.storage_class`, obtains that exact org-scoped
backend, derives `hashToKey(block_id)`, and checks any persisted `storage_key`
against the derived value. It does not use the current library preference as the
locator for historical blocks. This is true for v2 file reads, SeafHTTP,
sync/download, share-link and ZIP paths that use the canonical reader.

The current code does not persist a placement snapshot in a web upload session or
enforce one class per committed file. SeafHTTP instead holds the class selected
when its upload/finalization operation resolves the store; that is an operation
boundary, not a durable file-level snapshot. A future session snapshot may be
desirable, but it is not part of the current contract.

### Verdict on A-prime

A-prime is the only option here that preserves the stated product semantics:
mutable future preference, global org/hash deduplication and canonical physical
placement recorded per block. It also preserves the reason for the metadata LWT:
concurrent first writers with different preferences need an arbitration winner.

## Option B: class included in logical identity

B is a technically coherent but semantics-changing alternative. It is not the
current reference model and is not preferred merely because it could remove the
current class-election LWT.

### Definition

The base class-scoped domain would be:

```text
B-base logical block = (org_id, storage_class, internal_sha256)
```

If representation is also made part of the logical identity, the stronger form
would be:

```text
B-representation logical block =
    (org_id, storage_class, representation_id, internal_sha256)
```

B deliberately changes deduplication from `(org_id, hash)` to a class-scoped
domain. A library preference change could therefore cause a second physical
materialization of bytes that already exist in another class.

### What B fixes and costs

1. `hot-s3-na/hashX` and `hot-s3-eu/hashX` are different logical blocks.
2. Writers for the same class-scoped identity no longer elect between classes.
3. References, candidates and orphan state can be scoped to a class-scoped life.
4. Identical bytes used in two classes require two physical objects; three
   active classes can reach 3x the physical storage in a deliberate worst case.
5. The product loses the current global cross-class deduplication semantics.

This is a product decision, not a mechanical optimization. The existing
`ChangeStorageClass` behavior is correct for A-prime, but B would require every
reader and liveness path to carry enough class identity to distinguish rows.

Example of the deliberate duplication cost:

```text
1 GiB content = 128 blocks at 8 MiB
same content in NA + EU + Asia = 384 class-scoped block materializations
```

Content used only in one class has no additional cost. Content shared by many
libraries within one class still deduplicates once.

### Representation and physical-locator blocker

The current locator does not contain `representation_id`:

```text
K = blocks/<org_id>/<hash-prefix>/<internal_sha256>
```

The current `blocks` row is unique on `(org_id, block_id)` and
`ensureBlockIdentityRow` rejects a conflicting `representation_id` for that row.
That exclusion currently prevents two independently managed representation rows
from sharing one physical hash-derived locator.

It is not safe to make `representation_id` part of a future Cassandra primary
key while leaving the physical key unchanged. If two rows can exist for the same
`(org_id, storage_class, internal_sha256)` with different representations, both
can name the same `(storage_class, K)`. One row could then lose its references,
authorize GC and delete bytes still live through the other row.

Before representation-scoped rows are approved, the design must choose one of:

1. Keep representation out of the physical lifecycle identity and retain an
   explicit same-hash conflict/invariant. This is the safer B-base direction.
2. Make the physical locator distinct as well, for example by including
   representation and a non-reused incarnation in `K`, before creating
   independently GC-able rows.
3. Share one physical object and one liveness domain across representations. In
   that case the rows are not independent physical lifecycles and GC must retain
   shared ownership.

The forbidden intermediate is two independently collectible logical rows with
one physical `(storage_class, storage_key)` tuple. This rule must precede any
schema split.

### Required schema changes

If B-base is selected, the conceptual keys become:

```cql
blocks:
PRIMARY KEY ((org_id, storage_class, block_id))

block_references:
PRIMARY KEY ((org_id, storage_class, block_id), referrer)

gc_block_candidates:
PRIMARY KEY ((org_id, storage_class, block_id))
```

If B-representation is selected, the same surfaces must also carry
`representation_id`, and the physical locator rule above must be solved in the
same change. A greenfield schema changes migration cost but does not remove this
runtime safety requirement.

| Surface | B requirement |
|---|---|
| `blocks` | Partition by org, class and hash; add representation only with a matching physical locator/liveness design. |
| `block_references` | A reference to class A must not keep class B alive, and vice versa. |
| `gc_block_candidates` and by-day projection | Candidate identity must distinguish class A from class B. |
| `gc_provisional_block_refs` and by-day projection | Expiry identity must preserve the class. |
| `gc_s3_orphans` and by-day projection | Recovery must distinguish same-hash classes and exact physical tuples. |
| GC queue items | Block operations must carry the class and exact physical identity. |
| Readers and reuse probes | A hash-only lookup is ambiguous; class must come from a durable file/block identity or equivalent canonical mapping. |

### `fs_objects` compatibility under B

Under the current A-prime schema, `fs_objects.block_ids` can remain a list of
internal SHA-256 IDs because there is one canonical row per org/hash and that
row records the actual class. Under B, multiple class rows for one hash make a
hash-only manifest ambiguous. B would need a durable class-scoped block identity
in the file/reference path, a canonical mapping that selects exactly one class
for each file entry, or an equivalent representation. The current library's
mutable preference is not sufficient to read historical blocks.

### `ChangeStorageClass` under B

`ChangeStorageClass` may remain a mutable future-placement preference, but B must
define how an existing file identifies its class-scoped blocks after the change.
Making the library preference immutable is not required by the current product
contract and is not a valid substitute for that identity design.

### Failover under B

For a first materialization, the current health-aware selector can return a
failover class different from the preferred class; the actual returned class is
persisted today. Under B, that actual class becomes part of the new logical
identity and must be carried through the file, references and GC. Existing
canonical blocks still resolve without failover.

### Does B remove the metadata LWT?

B removes the specific cross-class election from an install only if every writer
of one selected identity produces identical metadata. It does not remove the
conditional operations needed for stale-writer, repair, GC and ambiguous-outcome
safety. The representation/locator blocker must be closed before treating the
LWT as removable.

### Product and storage effect

B makes storage class meaningful as physical placement. It improves regional
locality and residency at the cost of class-crossing deduplication. The product
must explicitly accept that the same bytes can consume one object per required
physical class.

### Verdict on B

B is technically clean for a product that explicitly wants class-scoped
deduplication or explicit class-scoped replicas. It is not preferred for the
current contract because it makes a mutable library preference alter the
deduplication domain. Any future B design must sequence representation identity,
physical locator identity and GC liveness together.

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

The current product preference may be evaluated per materialization request, and
the current web session does not persist a class snapshot. C would force a
different rule: every hash has a home class. With three uniformly selected
classes and a 128-block GiB file:

```text
P(all 128 blocks in one class) = 3 * (1/3)^128 = 3^-127
```

That is effectively zero. A normal large file will be spread across regions.
Approximately two-thirds of its blocks will be remote from any one region under
uniform placement.

To adopt C while preserving a one-class-per-file product rule, the system would
need to choose between:

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

C is appropriate only if global deduplication is more important than library
placement preference and the product accepts the file-level consequences. It
requires a larger redesign than B and changes placement authority from the
current library preference to a hash function.

## Cross-option comparison

| Criterion | A-prime: mutable preference | B: class in identity | C: hash home |
|---|---:|---:|---:|
| Same hash can have distinct class lives | No | Yes | No |
| Removes current cross-class placement race | No; first-writer arbitration is the contract | Yes | Yes, if function is stable |
| Preserves cross-class deduplication | Yes | No | Yes |
| Preserves one class per file | Not enforced by current session code | Requires class identity in file/reference path | No without extra design |
| Preserves library preference authority | Yes | Only for new class-scoped lives | No |
| New schema scope | Current schema | High | Medium to high |
| GC identity changes | Medium | High | High |
| Sensitivity to class-set changes | Low | Low | High |
| Storage duplication | None from placement | Up to number of classes | None from placement |
| Regional locality | Best-effort for new unique content; deduplicated reuse can stay in another class indefinitely | Good | Poor for large files |
| Fit with current architecture | Reference model | Product-changing redesign | Weak |

On the locality row: under A-prime this is the designed behaviour, not a defect
awaiting the removal of the first-writer LWT. Global `(org, hash)` deduplication
and per-request regional locality are in tension by construction, and A-prime
resolves it in favour of deduplication. A library that now prefers EU reuses a
hash already canonical in NA on purpose — reading it from NA is the correct
outcome, not a placement failure. Only new unique content follows the preference.
Removing the LWT would not change this; it would only change which writer wins the
race to establish a class for content that is genuinely new.

## Recommended implementation sequence

### Phase 0: characterize before changing semantics

Do not change identity, consistency or LWT behavior in the measurement phase.
Instrument:

1. metadata registration count;
2. LWT attempts and latency;
3. `applied=true` and `applied=false` outcomes;
4. retries and timeout distributions;
5. classes proposed for the same org/hash across libraries and DCs;
6. current global-dedupe behavior versus the deliberate loss under
   `(org, storage_class, hash)`;
7. class changes during active upload;
8. failover writes and the class actually persisted.

### Phase 1: preserve the current placement semantics

1. Keep `library.storage_class` mutable and treat it as future-placement
   preference only.
2. Keep the current empty-class fallback semantics unless a separate product
   decision removes request-based preference selection.
3. Keep `blocks.storage_class` as the actual canonical class and never rewrite it
   merely because a library preference changes.
4. Record the actual failover class for a first materialization; existing
   canonical blocks must resolve without failover.
5. Keep production `SERIAL` and keep destructive GC disabled.

### Phase 2: only if the product chooses B

1. Define class-scoped block, reference, candidate, provisional-tracker and
   orphan identities together.
2. Start with B-base unless representation determinism is explicitly proven.
3. If representation is added to the identity, separate the physical locator or
   preserve shared liveness in the same change; never split Cassandra identity
   first and physical identity later.
4. Add class identity to the file/reference path so readers do not use the
   mutable library preference for historical blocks.
5. Define migration/cutover behavior for any non-empty deployment; greenfield
   scope changes cost, not the safety proof.

### Phase 3: migrate every block path

This phase is a code and contract migration: every path must construct and
consume the new identity from its first write, and an existing deployment would
also need a data migration/cutover plan.

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

### Phase 4: prove the chosen model while retaining the LWT

Required tests:

| Test | Expected result |
|---|---|
| Existing hash canonical in NA, library preference changes to EU | Reuse/read stays in canonical NA; no second object is created by the preference change. |
| Absent hash, concurrent NA/EU preferences | Exactly one canonical class wins through the current first-writer LWT. |
| Existing metadata with missing object | Repair writes the persisted canonical class, not the current library preference. |
| Library class change | Existing rows/objects remain unchanged; future absent hashes prefer the new class. |
| Same org/hash/representation, same class under B | One class-scoped canonical identity and one logical object. |
| Same org/hash/representation, two classes under B | Two physical identities only if B is explicitly selected. |
| Same org/class/hash, different representation under B | Reject as conflict or use physically distinct keys; never two independently GC-able rows sharing P. |
| GC candidate in class A, live reference in class B under B | Class A operation cannot delete or clear class B state. |
| Class A unavailable during write | Persist the actual failover class and carry it through the selected identity. |
| Seafile desktop client reads new data | SHA-1 block list and SHA-1-derived `fs_id` remain unchanged; the intentional `RecvFS` legacy layout still works. |

### Phase 5: separately evaluate LWT optimization/removal

The current A-prime contract gives the first-writer LWT a semantic job. Any
optimization or removal must first provide an equivalent arbitration mechanism
or explicitly change the deduplication product semantics. It must also prove:

1. exact physical tuple identity `P = (storage_class, storage_key)`;
2. non-reused physical keys for new incarnations;
3. locator-authoritative reads and existence checks;
4. install and repair are separate operations;
5. stale writers cannot reinstall retired lives;
6. claims and finalization are tuple-bound;
7. ambiguous outcomes are settled before reuse or deletion.

GC LWTs and other conditional lifecycle operations must not be weakened merely
because a future normal-install arbitration mechanism no longer uses Paxos.

## Final decision proposal

The current design decision should be:

```text
Keep A-prime as the reference model: mutable library preference, global
org/hash canonical identity, canonical block placement in metadata, and
first-writer arbitration retained while X4 is measured.
```

This separates two questions that are currently coupled:

1. Which logical block is being addressed under global deduplication?
2. Which physical incarnation may be installed or destroyed?

A-prime answers the first without introducing class-scoped duplicate logical
blocks. X1, exact physical locators and the generation or incarnation protocol
answer the second. Treating B, representation scoping and LWT removal as one
mechanical change would create a safety regression.

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
- `internal/config/config.go`: class registration and failover-reference validation.
- `internal/api/server.go`: modern and legacy backend registration.
- `internal/api/v2/storage_policy.go`: flexible/strict new-library placement.
- `internal/api/v2/storage_resolution.go`: library/request placement resolution.
- `internal/db/block_upload_sessions.go`: current web upload-session admission
  fields and the absence of a placement snapshot.
- `internal/api/v2/blocks.go`: session block-store resolution and the returned
  actual class after health-aware selection.
- `internal/api/v2/files.go`, `internal/api/seafhttp.go` and
  `internal/api/v2/onlyoffice.go`: new-materialization writers and canonical
  read plumbing.
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

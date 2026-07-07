# Block Representation-Aware Mappings

**Date:** 2026-07-07  
**Status:** PR1 implemented on branch `feat/representation-aware-mappings-pr1`

## Problem

SesameFS already uses SHA-256 as the canonical physical block identity, but some
runtime surfaces still accept or emit Seafile-style SHA-1 block IDs. An org-wide
`SHA-1 -> SHA-256` mapping is safe only while a given external SHA-1 can refer to
exactly one physical byte sequence inside that org.

That assumption breaks once encrypted libraries are considered as a first-class
storage representation:

- compatibility surfaces still exchange Seafile-style SHA-1 IDs for the logical
  plaintext block content;
- encrypted libraries can materialize different encrypted physical block bytes for
  that same plaintext because the library-specific encryption material differs;
- the same external SHA-1 can therefore legitimately resolve to different
  physical SHA-256 block objects in different representation domains.

An org-only mapping can therefore resolve a SHA-1 from one representation domain
to the wrong SHA-256 from another domain.

## Design

PR1 moves the runtime contract to two distinct identities:

1. Physical block identity: `(org_id, internal_sha256)`
2. External mapping identity: `(org_id, representation_id, external_sha1)`

`representation_id` identifies the storage/materialization domain in which that
external SHA-1 must resolve. The current model is:

- `plain:v1` for plaintext libraries
- `library:<library_id>` for encrypted libraries

This makes the mapping explicit: the same external SHA-1 may exist more than once
inside an org, but only across different representation domains.

## Schema and persistence

PR1 adds a new migration because `001_initial_schema.cql` is already tracked by
checksum in deployed environments.

Migration `009_block_representation_mappings.cql` adds:

- `libraries.block_representation_id`
- `libraries_by_id.block_representation_id`
- `blocks.representation_id`
- `gc_s3_orphans.representation_id`
- `gc_s3_orphans_by_day.representation_id`
- `block_id_mappings ((org_id, representation_id, external_id), internal_id)`

Under the clean-cut rollout accepted for this branch, migration `009` also drops
the legacy org-scoped `block_id_mappings` contents and recreates the table with
the representation-aware primary key. New library creation paths now persist
`block_representation_id` explicitly. Runtime fallback remains only for missing
representation metadata on surviving library/block rows; it does not preserve the
old org-wide forward-mapping namespace.

## PR1 runtime scope

PR1 updates the live runtime consumers that resolve or clean mappings:

- upload materialization and verified web block mapping writes
- batch SHA-1 resolution for file view, share-link view, sync, and seafhttp reads
- direct sync/seafhttp single-block lookups
- canonical block metadata writes so GC can later delete the exact forward mapping
- GC orphan and mapping cleanup, now keyed by `representation_id`
- cross-repo batch copy/move guard: same-representation allowed, different
  representation rejected before copying `fs_objects`

PR1 also removes the unsafe fallback that treated an external SHA-1 as if it were
always a valid internal block ID on direct read paths.

## Non-goals of PR1

PR1 does **not** implement a cross-representation transform path. Copying or moving
content between libraries with different representation domains would require
re-materializing blocks in the destination domain, not reusing the source
`fs_object` block list.

PR1 also does not provide a mixed-mode compatibility layer for the dropped
org-wide mapping table. This branch assumes a clean-cut rollout before production
uploads. Separate follow-up work can still backfill `block_representation_id` and
any missing `blocks.representation_id` deterministically on imported/legacy rows.

## Operational rules

- Resolve external SHA-1 block IDs only within the target library's effective
  `representation_id`.
- Treat `blocks.representation_id` as the exact forward-mapping cleanup domain for
  GC; do not infer it from the current destination library at delete time.
- Do not delete forward mappings from GC without an explicit `representation_id`;
  exact cleanup must come from canonical block metadata, not a guessed default.
- Add future schema changes as new migrations; do not rewrite migration `001`.

## Follow-up work

- Backfill legacy libraries and canonical block rows so runtime fallback becomes an
  exception, not the steady state.
- Implement an explicit cross-representation copy path that decrypts/re-encrypts or
  otherwise re-materializes destination blocks safely.
- Extend any future encrypted web-upload flow to resolve/write mappings through the
  same `representation_id` contract.
- Reuse or cache `representation_id` on the remaining legacy SHA-1 read hotpaths
  beyond sync and SeafHTTP, especially v2 file/share download flows.
- Authorize the hash-only block surfaces (bare-SHA GET, `CheckBlocks`, mapping
  resolution) by real library membership rather than org + content hash. Tracked as
  `KNOWN_ISSUES.md → ISSUE-BLOCK-CROSS-LIBRARY-READ-01`.

## Block ID canonicalization

External SHA-1 and internal SHA-256 identifiers are canonicalized to trimmed
lowercase via `db.NormalizeBlockID` at every mapping write, lookup, delete, and on
`blocks.sha1`, plus the streaming and GC resolvers. Hex is case-insensitive, so this
keeps a single content-address from splitting across partition keys or missing a
lookup on letter case. Server-derived IDs are already lowercase; the canonicalization
is applied consistently as defense-in-depth against any non-server-derived id.
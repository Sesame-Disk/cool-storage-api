# Phase 5: preserve shared fs_objects

## Bounded plan (2026-10-17)

Base main@1b01e5d77 (#313). Scope: ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01.

1. Invert the existing unsafe characterization into a preservation regression.
   Add real Cassandra/S3 evidence using owned random org/library fixtures and
   ProcessOrgOnce, exercising the real Phase 5 scanner selection and worker.
   Record RED before changing runtime; no shared/global worker activation.
2. Make unguarded commit retirement metadata-only. A commit root is not owned by
   that commit: HEAD and retained history may share every node. Preserve guarded
   library-deletion cascades and their existing fail-closed tests. No schema,
   queue format, API or GC-default change; existing queued unguarded commits get
   the safe behavior too.
3. Prove HEAD and retained history trees, permanent refs, canonical metadata and
   actual S3 bytes survive; the expired commit is removed, with no descendant
   queue work. Cover root identity and shared subtrees plus guarded cleanup.
4. Run relevant Docker normal/race/unit and isolated real-stack checks, audit
   diff/compatibility/cleanup and update current issue/gate/history tracking.
   Refresh main and open a focused PR; report failures, skips and unrun checks.

Trade-off: unguarded expired commits no longer reclaim their tree by cascade.
Exclusive fs_objects and references may be retained until a sharing-aware pass;
Phase 6's independent execute-time TOCTOU remains OPEN, not certified here.
Concurrent commit publication/metadata destruction and hard-delete fencing stay
under their existing PRE-GC obligations. No universal retention/concurrency bound
is claimed. PRE-GC/A1 OPEN; ordinary GC OFF in every DC.

Backward compatible: yes for API/schema/queue contracts. Unsafe deletion of shared
data is corrected; reduced reclamation is explicit and needs disposition before A1.

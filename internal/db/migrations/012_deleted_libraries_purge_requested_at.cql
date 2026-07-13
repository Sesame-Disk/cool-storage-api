-- Migration 012: durable purge-request marker for permanently deleted libraries
--
-- Phase 13 (scanExpiredDeletedLibraries) enqueues the full library cascade only for
-- deleted_libraries rows whose deleted_at is older than TrashRetentionDays. That is
-- correct for a normal trash → retention → auto-purge flow, but a *permanent* delete
-- (org-admin / superadmin "delete now") re-inserts the marker with deleted_at = now(),
-- which resets the retention clock and defers the cascade ~30 days from the delete
-- action (ISSUE-GC-ORG-TRASH-NO-CASCADE-01, P1/P1b).
--
-- purge_requested_at is the durable signal that "this library must be reclaimed on the
-- retention-independent schedule, not after TrashRetentionDays". Permanent-delete
-- writers stamp it; Phase 13 then treats the row as eligible on its next scan when
-- purge_requested_at IS set OR deleted_at < cutoff. This does NOT bypass the GC grace
-- period: the cascade item is still enqueued with QueuedAt = deleted_at and the worker
-- only processes it after the configured grace_period (default 1h). The net effect is
-- reclamation on the order of the grace period instead of ~30 days — the grace window is
-- intentionally preserved.
-- Normal soft-delete leaves the column null and keeps the retention behavior unchanged.
-- The column is a rebuildable scanner signal only — the deleted_libraries marker remains
-- a discovery projection and the cascade still revalidates canonical state before any
-- destructive work.

ALTER TABLE deleted_libraries
	ADD IF NOT EXISTS purge_requested_at TIMESTAMP;

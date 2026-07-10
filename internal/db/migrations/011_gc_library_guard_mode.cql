-- Migration 011: distinguish deleted-library cascades from orphan revalidation
--
-- The legacy boolean cannot express whether a child item must match one
-- deleted_at identity or must instead prove that the canonical library row is
-- absent. Keep the boolean for rolling-upgrade compatibility and persist the
-- explicit mode beside it.

ALTER TABLE gc_queue
	ADD IF NOT EXISTS library_guard_mode TEXT;

ALTER TABLE gc_failed_items
	ADD IF NOT EXISTS library_guard_mode TEXT;
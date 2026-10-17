-- Preparatory library leases still expire. A finalizing lease must not expire
-- or be stolen while its final lifecycle batch may be in flight. Old rows have
-- finalizing=null and retain the preparatory behavior. Rollout requires drained
-- old binaries; rollback retains this additive column and must settle latches.
ALTER TABLE gc_library_hard_delete_locks ADD finalizing boolean;

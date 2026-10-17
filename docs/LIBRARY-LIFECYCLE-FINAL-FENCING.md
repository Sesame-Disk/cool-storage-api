# Library lifecycle final-write exclusion

## Frozen plan (2026-10-17)

Defect: a library lease holder can pause after its last renewal, lose the lease,
then execute an unconditional restore/permanent-delete batch after the new owner.
This affects authenticated APIs even with ordinary GC disabled.

Scope: the final library lifecycle batches in API permanent delete, restore and
GC library/org cascades. User/org leases, Phase 6, metadata publication and
sharing-aware reclamation remain separate. Preserve HTTP fields/statuses.

Protocol: add an additive `finalizing` column to the existing library lease.
A global SERIAL LWT may promote only the current preparatory token to a durable,
non-expiring final section. Takeover, renewal and ordinary release must not erase
or expire that section. Run the existing batch while holding it, then conditionally
release only after acknowledged completion. Stale preparatory tokens cannot enter.
Unknown/error outcomes retain exclusion; never rerun an ambiguous batch automatically.

Trade-off: a crashed/failed final section blocks that library until recovery.
No automatic timeout recovery is safe against a paused owner or delayed batch.
Recovery and rollout instructions must require quiescing every old owner and
settling its outstanding writes before repair/release. Drain all old binaries
before using this protocol; leave the additive column on rollback. Mixed versions
are not a certified lifecycle configuration. Ordinary GC remains OFF.

Evidence: first add deterministic real-Cassandra HTTP regressions for both stale
orders, using a query observer to pause after successful renewal. Record RED on
main, then GREEN after the fix. Verify final-section takeover refusal, TTL removal,
ordinary-release refusal, error/unknown retention and acknowledged completion;
exercise the public package mutation API and real worker finalization too.
Run affected Docker unit/race packages, existing restore guards, vet and formatting.
Audit all lease entry points and final batch call sites, link/status consistency,
cleanup and remote-main freshness before opening a focused PR. Report every skip
and unrun suite; no X1 or PRE-GC/A1 activation decision is made here.

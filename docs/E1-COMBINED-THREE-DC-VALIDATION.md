# E1 combined three-DC validation and reconciliation

## Frozen plan (2026-10-15)

Base: main@221997f4552c2246c87b9b7a475e97e906cc0e36 (#303).
Scope: a single serial Go integration invocation containing E1-3 (3), E1-7 (6),
E1-09 (14), E1-10e (4), E1-11 (6), E1-12 (6), E1-13 (5), plus E1-17 (15):
59 named behavioral legs. Existing process-death children remain enabled, but
family-level isolated-URL delegation is unset so the matrices share one process.

1. Reuse the dedicated healthy Cassandra three-DC RF1/S3 fixture, ordinary GC OFF.
   Snapshot recovery/repair tables and retained object probes before execution.
2. Require all seven existing family evidence gates and the existing completeness,
   inventory, isolation and no-global-GC-fanout checks. E1-3 has no family gate:
   independently verify an explicit 59-name result manifest, not only exit 0.
3. Run the union normally and with race, each in one fresh Go process. Do not
   change productive/test sources or silently increase standard-suite budgets.
   Check exact named PASS events and reject skips, missing cases or failures.
4. Negative controls: each existing gate rejects omitted functional evidence;
   the manifest rejects a filtered E1-3 run. Preserve original per-family
   behavioral mutation evidence by reference, without claiming it was rerun.
5. Audit owned teardown and retained unrelated state before/after. Any reproduced
   independent runtime defect receives its own bounded fix, not scope expansion.
6. Reconcile the historical seven-family fixture failures against current union
   evidence, preserving unsupported-attribution corrections and the other three
   historical integration failures, special-script skip and sync residual.
   Refresh active/recent summaries and tracking; do not claim full-suite PASS.
7. Fetch main before final review, audit links/diff/provenance and open the PR.

This advances E1 three-DC evidence toward X1. It closes neither the full-suite
budget issue nor E1-01, D0/identity audit, X1, PRE-GC or A1. Older retained
one-DC authorities stay untouched. No schema, API, client or GC activation change.

Backward compatible: yes, planned scope is documentation/evidence only.

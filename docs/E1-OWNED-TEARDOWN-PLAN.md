# Scoped E1-10e / E1-17 owned teardown plan

Base: main@d4e375cfbe2e80e5380b2506daf79109f0e22e76 (#302).

Problem: COMMITTED fixtures delete their exact object and block/lifecycle rows,
but retain canonical orphan, independent recovery root and discovery projection.
This is P2 FOLLOW-UP / GENERAL test hygiene, not a productive data-loss finding.

1. Add a teardown regression to the existing E1-10e (four) and E1-17 (fifteen)
   public-handler legs. Require exact owned block/refs/lifecycle/K and recovery
   metadata absence after original cleanup. Reproduce six COMMITTED failures.
2. Correct only these two fixture families. Capture the actual returned delete
   authority; preserve all original COMMITTED/admission assertions. After original
   teardown and verified block/refs/lifecycle/K absence, dispose only that exact
   authority through existing CassandraStore APIs, then verify all three recovery
   surfaces are absent at EACH_QUORUM. Use a bounded background object context:
   testing.T.Context is already canceled during Cleanup. No global helper,
   productive worker, schema, protocol or configuration changes.
3. Run Docker RED/GREEN against isolated one-DC and three-DC Cassandra/S3;
   repeat with race, retain required gates and admission controls. Explicitly
   distinguish regression failures from setup failures. Preserve unrelated rows.
4. Audit ownership, LIFO cleanup ordering, early-failure behavior, unchanged
   functional assertions, source diff and tracking. Update from main, open a
   focused PR, and report checks actually run and skipped.

Automatic cleanup is fixture disposal, not productive recovery evidence. Older
retained authorities and auxiliary discovery probes remain outside this scope.
No combined/full-suite PASS or X1/PRE-GC/A1 closure; ordinary GC stays OFF.

Backward compatible: yes, test-only teardown; public behavior is unchanged.

# I1 — Identity claim execution inventory

## Frozen plan (2026-10-16)

Base: main@1244db038e337ce23ad6a1727f216ff2643b57dd (#309).
Scope: I1 in [the remaining X1 work](./E1-FINAL-DISPOSITION.md#remaining-x1-work).

1. Reuse the existing mapping Query/Batch source resolver, dynamic-site
   classification and repository traversal. Extend the identity immutability
   guard to require exactly one approved write-once INSERT and one exact SELECT
   at their present owners. Preserve literal and migration checks.
2. Reject identity CQL assembled across files, extra/moved execution sites,
   Batch/Bind/BatchEntry routes and unclassified dynamic statements. Reuse
   current classifications without adding productive registries or exceptions.
3. Add directed mutations to the existing identity mutation workflow. Demonstrate
   at least one old-guard blind spot before the change; require compilation and
   the intended new guard diagnostic for each negative, plus legitimate controls.
4. Run relevant identity/mapping tests and negatives in Docker on an isolated
   source archive, with normal/race checks and restored-source validation. This
   is test infrastructure; no Cassandra/S3 runtime behavior is changed.
5. Audit the final diff, documentation links, scope and updated main; record
   results/limits and open a focused PR. M1 remains separate, as do maintainer
   E1/X1 acceptance and PRE-GC/A1. Ordinary GC stays disabled.

This addresses a real guard completeness gap, not a reproduced production data
loss defect. No runtime, schema, API or dependency change is planned.
Backward compatible: yes, test and documentation changes only.

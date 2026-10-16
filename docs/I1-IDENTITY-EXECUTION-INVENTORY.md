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

## Result

Test/script source: `90f9ca457`; frozen plan: `daaa5a99e`.
**I1's P2 guard-completeness criterion is CLOSED-FIX for the inventoried
current source contract.** This fixes test infrastructure, not a demonstrated
production data-loss defect. M1's ten independent CAS terminal negatives,
maintainer E1/X1 acceptance and PRE-GC/A1 remain separate. Ordinary GC stays OFF.

The identity immutability test now calls the existing mapping execution
inventory. The mapping test uses that same helper; source resolution, dynamic
classifications and traversal are shared rather than copied. Exactly two
identity Query sites are admitted, with exact CQL and their existing owners:
`ClaimIdentityAuthorityAt`'s INSERT IF NOT EXISTS and `ReadIdentityAuthority`'s
SELECT. Batch/Bind/BatchEntry routes are rejected; unknown Query arguments keep
the existing fail-closed classification. Duplicate approved INSERT sites cannot
hide behind one literal. The old literal/schema checks remain in force.

This is a conservative AST source guard with the existing resolver's limits,
not a proof about arbitrary Go execution or Cassandra outcomes. A formatted
statement that cannot be resolved is rejected as dynamic; it is not silently
classified as a resolved identity statement. Existing reviewed dynamic sites
retain their fixed-table/count contracts; no exception or registry was added.

## Evidence and reproduction

All Go commands ran inside `sesamefs-e1-residuals-diagnostics` (Go 1.25.14),
on versioned-source archives, with updated test files copied into the archive.
Ignored historical diagnostic Go files in a developer checkout are excluded
from that evidence snapshot; no production file or directory was deleted.
No Cassandra/S3/API stack was started or destructive GC exercised.

Before the fix, the exact main-base identity guard passes two compiled source
mutations: an UPDATE with table fragments split across production files, and
two INSERT Query sites using one CQL literal. The new guard rejects both. These
are executable guard blind spots, not observed production writers.

| Docker validation | Measured result |
|---|---|
| Relevant identity/mapping selection, `-short -count=1` | 118 named test/subtest PASS, zero SKIP |
| `go test -json -short -count=1 ./internal/db` | Package PASS; 528 named PASS, nine seed SKIP |
| Same DB package with `-race` | Package PASS; 528 named PASS, same nine SKIP, no race report |
| `PCD1B_MUTATION_SCOPE=i1 bash scripts/pcd1b-identity-authority-mutation-validation.sh` inside the Docker source archive | 13 independent mutations compile then trip their required guard diagnostic; two legitimate controls stay GREEN |
| Restored source: exact identity and mapping inventory tests | Both PASS; primitive/gateway bytes match the independent unmutated source copy |

The thirteen negatives cover cross-file helper UPDATE, concatenated DELETE,
formatted TRUNCATE, INSERT TTL, SELECT outside its owner, Batch.Query, Batch.Bind,
BatchEntry, Query method aliases, an opaque dynamic argument, duplicate approved
INSERT, approved INSERT through Batch.Query, and SELECT projection drift. The
controls admit the genuine INSERT through a local binding and an unrelated
resolved Query. Every negative first runs a compile-only package check; a build
failure cannot satisfy the expected guard rejection. The existing mutation
workflow keeps its default full mode; `PCD1B_MUTATION_SCOPE=i1` selects this slice.

Nine short-mode skips are TestSeedDatabase_FirstRun, TestSeedDatabase_Idempotent,
TestSeedDatabase_DevModeUsers, TestSeedDatabase_PlatformOrg,
TestSeedDatabase_SuperAdmin, TestSeedDatabase_UserIndexing,
TestCreateDefaultOrganization, TestCreateDefaultAdmin and TestCreateTestUsers.
Those seed fixtures were not tested against Cassandra. No full-Go, integration,
three-DC, API/OIDC, client or entire legacy identity-mutation matrix was rerun.

The first negative batch stopped because its formatted-TRUNCATE expectation
asked for a resolved-statement diagnostic. The guard correctly rejected that
case as an unclassified dynamic Query. The script now requires that exact
classification; the complete 13-case batch passes. No resolver was weakened to
make the negative pass.

Final audit checks changed documentation links/anchors, shell syntax, exact
source scope, normal/race JSON outcomes and restoration. Local ignored artifacts
are `tmp/i1-*`. The owned runner is stopped after validation. Backward compatible:
yes, tests/script/docs only; no runtime, schema, API, dependency or GC-default change.

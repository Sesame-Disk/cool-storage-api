# M1 — Independent H8 CAS-terminal evidence

## Frozen plan (2026-10-16)

Base: main@dce181ef1db0b8833c639fc1fff641c476acc13d (#310).
Scope: M1 in [the remaining X1 work](./E1-FINAL-DISPOSITION.md#remaining-x1-work).

1. Replace H8's single ten-terminal mutation with ten separately selectable
   mutations in the existing mapping workflow. Each injects one CAS terminal
   through the actual upload pre-check call graph and must compile before its
   exact terminal diagnostic can count as RED. Preserve the legacy H8 selector.
2. Keep the existing cold-path guard and runtime unchanged. Add an ordinary
   non-CAS control so rejection of every injected helper cannot count as proof.
3. Run H8 in Docker on a clean source archive, then check byte restoration and
   a GREEN baseline. Demonstrate that disabling one terminal recognition can
   stay hidden in old aggregate H8 but cannot satisfy the new selected case.
4. Run relevant mapping/identity checks normally and with race in Docker.
   No Cassandra/S3 runtime behavior changes or destructive stack run is needed.
5. Audit exact cases, diagnostics, source restoration, documentation and current
   main, then create a focused PR. Report skips/unrun checks; maintainers retain
   final E1/X1 acceptance. PRE-GC/A1 and ordinary GC OFF remain unchanged.

This closes a real limitation in mutation evidence, not a demonstrated runtime
failure. No new productive guard, schema, API or dependency is planned.
Backward compatible: yes, script/evidence only; existing H8 selection retained.

## Result and scope

Script source: `1b26a03da`; frozen plan: `08d9d991f`.
**M1's P2 independent-terminal evidence criterion is CLOSED-EVIDENCE.**
No production defect is inferred. The existing cold-path guard, runtime,
CQL/schema, dependencies and GC defaults are unchanged. H1/I1/M1 now supply
their named results; maintainer E1/X1 acceptance and PRE-GC/A1 remain separate.
Ordinary destructive GC stays OFF.

H8 has ten separately listed/selectable cases in the existing mutation script:

| Driver receiver | Independent terminals |
|---|---|
| Query | ScanCAS, MapScanCAS, ScanCASContext, MapScanCASContext |
| Batch | ExecCAS, MapExecCAS, ExecCASContext, MapExecCASContext |
| Session | ExecuteBatchCAS, MapExecuteBatchCAS |

Every case inserts one terminal through the actual upload pre-check helper
call graph, requires package compilation and then the exact
`reaches an LWT (<selected terminal>)` diagnostic. Another CAS terminal or the
fixture's conditional CQL cannot satisfy that predicate. These are compiled
source mutations, not live Cassandra LWT execution or performance evidence.

The legacy `h8_upload_precheck_all_cas_terminals` selector remains accepted;
it now runs the ten independent cases and an ordinary non-CAS Query/Exec control.
Default full-mode enumeration includes the ten cases, and `--list` exposes them.
A failed call-site insertion aborts the mutation, including on Windows CRLF
sources; appending an unreachable helper is insufficient.

## Docker validation

All Go commands ran in Docker using `sesamefs-e1-residuals-diagnostics`,
Go 1.25.14, on clean versioned-source archives of the stated base with the new
script copied in. No Cassandra/S3/API stack was started, no GC was enabled and
no production/test-guard Go file is changed by this PR.

- Existing targeted-contract baseline: GREEN before H8.
- Legacy H8 selector: ten distinct compile checks and ten exact terminal
  rejections, followed by the non-CAS control GREEN. Script exit 0.
- Cold-path, identity immutability and shared execution-inventory guards:
  three named PASS normally and three with `-race`; zero skips/race reports.
- Masking counterexample on a disposable guard copy: disabling only ScanCAS
  recognition leaves the old aggregate criterion satisfied by other terminals.
  The old aggregate fixture compiles; its guard FAIL is still acceptable to
  the old broad predicate. The new standalone ScanCAS selector compiles but
  exits 1 for the missing ScanCAS diagnostic, refusing to count conditional-CQL
  failure as terminal evidence. The production guard is untouched.
- Official and masked-selector source copies restore the writer exactly to
  their original archive bytes, with no mutation backup remaining. `--list`
  contains ten distinct H8 selectors; shell syntax and documentation checks pass.

Reproduce from a clean source checkout/archive using Bash with Docker available:

```bash
PCD1B3_MUTATION_IMAGE=sesamefs-e1-residuals-diagnostics   bash scripts/pc-d1b3-mapping-authority-mutation-validation.sh h8_upload_precheck_all_cas_terminals
```

The image is a local validation image, not a distributed artifact. Without that
override the script retains its existing `golang:1.25.12-trixie` default, which
was not rerun here. Other legacy mutation legs, full DB/Go/integration suites,
real-service/three-DC/native-fault, API/OIDC and desktop/mobile matrices were
not rerun; no historical failure becomes PASS. M1 changes evidence granularity,
not runtime GC/publication safety or overall activation status.

Initial validation was not passing: CRLF prevented the first mutator from
linking the helper, so the guard stayed GREEN and the harness correctly failed.
H8 now accepts LF/CRLF and requires call insertion before appending the helper.
A first restoration comparison against `git show` also differed by line endings;
comparison to the actual archive entry proves exact restoration, and normalized
content agrees with the tracked source. No failed run is counted as PASS.

Ignored local artifacts: `tmp/m1-*`. The owned audit runner is stopped after
validation; mutation runners are removed by their existing cleanup. Backward
compatible: yes, script/docs only, with the legacy H8 selector retained.

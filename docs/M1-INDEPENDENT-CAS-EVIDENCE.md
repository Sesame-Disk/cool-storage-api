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

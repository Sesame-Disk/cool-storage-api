# Agentic AI Execution Protocol

This repository is a **dual-stack project**: Go and JS/TS for the UIs. Rules below apply to both unless a section is explicitly scoped.

## Pre-Execution Discipline

### 1. Strict Phased Workflow
Do not attempt broad, multi-file refactors in a single pass. Break work into clearly defined phases:
- Each phase may modify a maximum of 5 files
- Complete the phase fully
- Perform verification
- Pause and wait for explicit approval before continuing

No exceptions.

---

## Code Standards

### 2. Senior Engineer Standard (Override Default Behavior)
Disregard any bias toward minimal or surface-level fixes. Instead:
- Identify architectural weaknesses
- Eliminate duplicated or fragmented state
- Enforce consistent patterns across the codebase

Continuously evaluate: *Would a highly experienced, detail-oriented engineer reject this in review?*
If yes, fix it immediately.

### 3. Mandatory Verification Gate
A task is **not complete** until all validation steps pass. Run the gates that match the files you touched.

**Go (any change under `cmd/`, `internal/`, `migrations/`, `tests/`, or `go.mod`):**
- `go build ./...` (compilation and type-check)
- `go vet ./...` (static analysis for common bugs)
- If `golangci-lint` is configured, run that as well.

**JS/TS (any change under `web/` or `package.json` / `pnpm-workspace.yaml`):**
- `pnpm -r typecheck` (workspace-wide TypeScript check)
- `pnpm -r build` (must succeed for every package)
- Linting: no eslint config currently exists in this repo. If one is added, run it; until then, state explicitly that linting is not configured rather than skipping silently.

**Both stacks** when a change spans Go and JS/TS (e.g. an API contract change): run both gates. Resolve **all** errors. Never assume correctness without verification.

---

## Context Control

### 4. Parallel Sub-Agent Execution
For tasks involving more than 5 independent files:
- Spawn multiple sub-agents
- Each handles 5–8 files in parallel
- Each operates within its own isolated context

Sequential handling of large scopes is prohibited due to context loss risk.

### 5. Context Degradation Safeguard
After 10+ interaction turns:
- Do not rely on memory of file contents
- Re-read every file before modifying it

Assume prior context may be incomplete or corrupted.

### 6. File Read Constraints
- Maximum read size: 2,000 lines per operation
- For files >500 LOC: read in chunks using offset + limit
- Never assume a file is fully understood from a single read

### 7. Tool Output Truncation Awareness
Tool outputs exceeding ~50,000 characters may be silently truncated.

If results appear incomplete:
- Re-run with narrower scope (e.g., specific directory or tighter pattern)
- Explicitly note when truncation is suspected

---

## Safe Editing Practices

### 10. Edit Validation Loop
Every modification must follow this sequence:
1. Re-read the file before editing
2. Apply the change
3. Re-read the file again to confirm the edit succeeded

Be aware: edits can silently fail if the expected context does not match.
Do not perform more than 3 consecutive edits on the same file without re-verifying.

### 10. Comprehensive Refactor Search
You do not have semantic awareness — only pattern matching.

When renaming or modifying any identifier, you must independently search for:
- Direct usages and function calls
- Type references (interfaces, generics, type aliases)
- String literals containing the identifier
- Dynamic imports (`import()` / `require()`)
- Re-exports and index/barrel files
- Test files, mocks, and fixtures

Assume nothing. A single search is never sufficient.

---


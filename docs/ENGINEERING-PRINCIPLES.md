# Engineering Principles - SesameFS

**Last Updated**: 2026-01-24

This document defines core engineering principles that guide development decisions for SesameFS.

---

## 🎯 Core Principle: No Quick Fixes in Early Development

**Established**: 2026-01-24

### The Rule

> **During early development stage, we prioritize proper engineering over quick fixes.**
>
> When facing bugs or missing features, we choose comprehensive solutions that address root causes, even if they take longer.

### Why This Matters

**Early development is the BEST time to do things right:**
- Codebase is still small and manageable
- No production users depending on stability
- Technical debt is easiest to avoid before it accumulates
- Proper patterns established now become templates for future work

**Quick fixes create compounding problems:**
- Inconsistent code patterns across the codebase
- Technical debt that becomes harder to fix later
- Band-aids on band-aids when issues recur
- Wasted time revisiting the same problems multiple times

### When to Apply This Principle

✅ **Choose comprehensive solution when:**
- Issue affects core functionality (auth, permissions, data integrity)
- Fix requires touching multiple endpoints or components
- Quick fix would create inconsistency with existing patterns
- Problem will likely recur or expand to other areas
- You have time to implement properly (no production emergency)

❌ **Quick fix acceptable when:**
- Production is down (not applicable in dev stage)
- Issue is truly isolated and won't spread
- Quick fix is well-documented as temporary with TODO for proper fix
- External dependency/library bug requiring workaround

### Examples from SesameFS Development

#### ✅ GOOD: Comprehensive Permission Rollout (2026-01-24)
**Situation**: Manual testing revealed permission checks missing on 95% of endpoints
**Quick Fix Option**: Add ownership check to just `ListLibraries` (1-2 hours)
**Comprehensive Option**: Systematically apply permission middleware to all endpoints (2-3 days)
**Decision**: Chose comprehensive approach
**Result**: Will have consistent, auditable permission system across entire API

#### ❌ AVOID: Patching Individual Endpoints
**Anti-pattern**: Discovering permission issues one by one during testing, adding ad-hoc checks to each endpoint as bugs are reported
**Why bad**: Inconsistent implementations, some endpoints missed, hard to audit security posture
**Better**: Systematic audit and implementation plan

---

## 🏗️ Other Core Principles

### Test Before Freezing
- Protocol changes must pass `./run-sync-comparison.sh` and `./run-real-client-sync.sh`
- Desktop client compatibility is non-negotiable
- See `docs/DECISIONS.md` for protocol-driven workflow

### Documentation is Code
- Update docs in same session as code changes
- `CURRENT_WORK.md` keeps sessions connected
- Architecture decisions recorded in `docs/DECISIONS.md`

### Frontend-Driven Development
- Let existing frontend UI dictate backend priorities
- Many features have working UI but stubbed backends
- Implement backend to match what frontend already expects

### Incremental but Complete
- Break large features into phases
- But each phase must be complete within its scope
- Don't leave half-implemented features

### Production-Ready from the Start
- Assume early code will reach production
- Write with production quality even in dev stage
- Easier to maintain high standards than to retrofit later

---

## 🔢 API Status Code Conventions

**Established**: 2026-06-19

### Library access: 404 for missing, 403 for forbidden

> For **authenticated** library/repo endpoints, a request for a library that does
> not exist (or is soft-deleted) returns **404 Not Found**. A request for a
> library that *exists* but the caller cannot access returns **403 Forbidden**.

This is the established contract across the API: the `RequireLibraryPermission`
middleware and the read-model handlers (tags, starred, monitored, share/upload
links) resolve library existence and return 404 *before the library-level
permission check* (ownership / share lookup). Handlers that perform an inline
permission check must do the same — when access is denied, call
`respondIfLibraryMissing()` (see `internal/api/v2/library_live.go`) to surface a
missing library as 404 instead of a misleading 403.

One coarse gate runs ahead of the existence check: `RequireLibraryPermission`
rejects a request whose **API-key scope** is insufficient with `403 insufficient
api key scope` before it looks up the library. That is intentional — an API key
that lacks the scope is denied regardless of which repo it targets, so it reveals
nothing about a specific repo's existence.

**Why not hide existence behind a uniform 403?**
- Repo IDs are high-entropy UUIDv4 (~122 bits); 404-vs-403 is not a practical
  enumeration oracle on an authenticated, UUID-keyed lookup.
- The codebase's anti-enumeration policy targets **unauthenticated** oracles only
  (share-link tokens → uniform 404, avatar email enum, OIDC config — see
  `docs/SECURITY-ASSESSMENT-2026-04-v4.md`, findings H-5/M-4). Authenticated repo
  lookups are explicitly out of that scope.
- A misleading 403 ("Permission denied / Leave Share") on a typo'd or deleted URL
  is a worse UX than an honest "not found", with no real confidentiality gain.

This convention would only change if product policy became "never reveal the
existence of a private repo under any circumstance" — which is **not** the
current policy.

---

## 📋 Decision Framework

When facing a technical decision, ask:

1. **Is this production-quality?** Would I be comfortable shipping this?
2. **Is this consistent?** Does it match patterns used elsewhere?
3. **Is this complete?** Or am I leaving work for "later"?
4. **Is this testable?** Can I verify it works correctly?
5. **Is this documented?** Will next session understand this?

If answer is "no" to any question, **choose the better solution** even if it takes longer.

---

## 🎓 Philosophy

> "Weeks of coding can save you hours of planning."
> — Traditional programming wisdom (inverted)

In early development, **bias toward doing it right the first time.**

The time "saved" by quick fixes is often spent:
- Debugging weird edge cases from incomplete solutions
- Refactoring when the hack spreads to other code
- Explaining workarounds to other developers (or future you)
- Fighting technical debt when it's harder to fix

**Better engineering now = faster development later.**

---

## Related Documents

- [DECISIONS.md](DECISIONS.md) - Architecture decisions and protocol-driven workflow
- [CURRENT_WORK.md](../CURRENT_WORK.md) - Session priorities and active work
- [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) - Known issues and cleanup tasks

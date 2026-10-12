# End-of-Session Documentation Checklist

**Purpose**: Ensure all documentation is updated when features are added/modified

Run this checklist at the END of every session before delivering final summary to user.

---

## ✅ Required Updates (Every Session)

### 0. After EACH Completed Task (Do Immediately — Do NOT Defer)
- [ ] Update **ALL docs** that reference the completed feature: counts, percentages, status labels
- [ ] Check at minimum: `CURRENT_WORK.md`, `docs/IMPLEMENTATION_STATUS.md`, `docs/TECHNICAL-DEBT.md`, `CLAUDE.md`
- [ ] Grep for old status text (e.g., "~90 remaining", "15 fixed") and correct every occurrence
- [ ] This prevents stale data from surviving across sessions

### 1. CURRENT_WORK.md
- [ ] Update timestamp and session ID at top
- [ ] Move completed items from "What's Next" → "What Was Just Completed"
- [ ] Update "What's Next" priorities (reorder/remove completed items)
- [ ] Add new known issues if discovered
- [ ] List all files modified in the session

### 2. IMPLEMENTATION_STATUS.md (if component status changed)
- [ ] Update component status (🔒 FROZEN / ✅ COMPLETE / 🟡 PARTIAL / ❌ TODO)
- [ ] Update verification date
- [ ] Add notes about what was completed/changed

---

## ✅ Feature-Specific Updates (When Applicable)

### If Sync Protocol Changed

**Files to update:**
- [ ] `docs/SEAFILE-SYNC-PROTOCOL.md` - Update protocol details, examples
- [ ] `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` - Update formal spec if behavior changed
- [ ] `docs/API-REFERENCE.md` - Update endpoint status/documentation
- [ ] Update "Last Verified" date in all updated docs

**What to document:**
- New/changed endpoints and their exact formats
- Field types (critical - wrong types break desktop client)
- Authentication requirements
- Response examples from stock Seafile
- Any quirks or edge cases discovered

### If API Endpoint Implemented/Fixed

**Files to update:**
- [ ] `docs/API-REFERENCE.md` - Change status from ❌ TODO → ✅ COMPLETE
- [ ] Add endpoint documentation (parameters, responses, examples)
- [ ] Add verification date
- [ ] Update endpoint count in summary section

**Example format:**
```markdown
**Endpoint Name:**
```http
METHOD /path/to/endpoint?param={value}
Authorization: Token {token}
```

**Parameters:**
- `param` - Description

**Response:** `200 OK`
```json
{
  "field": "value"
}
```

**Verified:** YYYY-MM-DD - Brief description of testing
```

### If New Test Framework/Tool Created

**Files to update:**
- [ ] `docs/TESTING.md` - Add new test framework documentation
- [ ] `docs/SEAFILE-SYNC-PROTOCOL.md` - Update "Testing Your Implementation" section
- [ ] `README.md` - Add to quick start if user-facing
- [ ] Create dedicated guide (e.g., `COMPREHENSIVE_TESTING.md`) for complex frameworks

### If Frontend Feature Added/Fixed

**Files to update:**
- [ ] `docs/FRONTEND.md` - Update relevant section
- [ ] `docs/API-REFERENCE.md` - Mark backend endpoints as tested with frontend
- [ ] Add screenshots to `docs/screenshots/` if UI changed

### If Encryption/Security Changed

**Files to update:**
- [ ] `docs/ENCRYPTION.md` - Update encryption details
- [ ] `CLAUDE.md` - Update "CRITICAL: Encrypted Library Flow" section
- [ ] Add security implications and verification steps

### If Database Schema Changed

**Files to update:**
- [ ] `docs/DATABASE-GUIDE.md` - Update table definitions, queries
- [ ] `internal/db/db.go` - Ensure inline comments match documentation
- [ ] Add migration notes if schema change is breaking

---

## ✅ CLAUDE.md Special Updates

**Update CLAUDE.md when:**
- [ ] New frozen component identified (add to frozen components list)
- [ ] New critical constraint discovered (add to "Critical Constraints" section)
- [ ] New key code location created (add to "Key Code Locations" table)
- [ ] New documentation created (add to "Documentation" tables)
- [ ] Recent changes section needs update (last ~7 days of major changes)

---

## 🤖 Automation Reminder

**FOR CLAUDE: Run this checklist automatically before ending session**

1. Review all files you modified this session
2. For each file type, check if corresponding docs need updates
3. Update verification dates to today's date
4. Add "Verified: YYYY-MM-DD" lines to changed features
5. Ensure all changes are user-discoverable (no undocumented features)

**Example thinking process:**
```
Modified: internal/api/sync.go (added new endpoint)
→ Check: docs/API-REFERENCE.md (add endpoint)
→ Check: docs/SEAFILE-SYNC-PROTOCOL.md (if sync-related)
→ Check: CURRENT_WORK.md (mark as completed)
→ Check: IMPLEMENTATION_STATUS.md (update component status)
```

---

## ✅ Release Criteria Updates (See [RELEASE-CRITERIA.md](RELEASE-CRITERIA.md))

### If Tests Were Added or Modified
- [ ] Update the **Component Test Map** entry in `docs/RELEASE-CRITERIA.md`
- [ ] Re-measure coverage if significant tests were added
- [ ] Check if any component now meets freeze prerequisites (→ promote to 🟢)

### If Bugs Were Fixed
- [ ] Confirm regression test exists for the fix
- [ ] If component was 🟢 RELEASE-CANDIDATE, reset soak counter to 0 in Component Test Map
- [ ] Update `docs/KNOWN_ISSUES.md`

### At Session End — Soak Period Tracking
- [ ] For each 🟢 RELEASE-CANDIDATE component: did all its tests pass this session?
  - Yes + no new bugs → increment session counter in Component Test Map
  - No → reset counter to 0
- [ ] If any 🟢 component reached 3 sessions → eligible for 🔒 FROZEN promotion

---

## Verification

**Before ending session, confirm:**
- [ ] All modified files are listed in CURRENT_WORK.md
- [ ] All new features are documented in relevant docs
- [ ] All changed endpoints have updated status in API-REFERENCE.md
- [ ] "Last Verified" dates are current (today or recent)
- [ ] User will know how to test new features (clear instructions)
- [ ] No orphaned code (everything has a purpose documented somewhere)

---

## Notes for Future Sessions

If you find this checklist incomplete, add new items and commit the changes. Documentation is living - it should evolve with the codebase.

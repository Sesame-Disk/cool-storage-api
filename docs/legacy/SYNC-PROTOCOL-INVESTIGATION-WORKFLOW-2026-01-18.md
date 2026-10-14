# Sync Protocol Investigation Workflow

**Last Updated**: 2026-01-18

---

## Golden Rule

**Stock Seafile (app.nihaoconsult.com) is ALWAYS correct for sync protocol.**

When sync issues arise:
1. Stock Seafile behavior is the reference implementation
2. SesameFS must match stock Seafile exactly
3. Do NOT implement based on assumptions - verify first

---

## Investigation Steps

### Step 1: Check Desktop Client Logs

**Location:** `~/.ccnet/logs/seafile.log` (macOS/Linux)

**What to look for:**
- Error messages with specific endpoint names
- HTTP errors (40x, 50x)
- Timeout errors
- Failed requests with full URLs

**Example:**
```
[01/18/26 10:13:48] http-tx-mgr.c(842): libcurl failed to GET
http://localhost:8080/seafhttp/repo/01920c46.../permission-check/...
Timeout was reached.
```

**If logs are 100% conclusive:**
- Proceed to Step 3 (verify against stock Seafile)

**If logs are NOT conclusive (ambiguous, missing details):**
- Proceed to Step 2 (use debug container with proxy)

---

### Step 2: Use Debug Container with Proxy (When Needed)

**When to use:**
- Client logs don't show exact request/response
- Need to see binary protocol data
- Need to compare stock Seafile vs SesameFS side-by-side
- Uncertain about field types, headers, or response format

**Location:** `docker/seafile-cli-debug/`

**Run comparison test:**
```bash
cd docker/seafile-cli-debug

# Quick test (single file)
./run-comprehensive-with-proxy.sh --test-scenario single_small_file

# Full test suite
./run-comprehensive-with-proxy.sh --test-all
```

**Traffic captured to:** `captures/remote/` and `captures/local/`

**View captured traffic:**
```bash
cd captures/remote
mitmweb -r single_small_file_remote_traffic.mitm
# Opens http://127.0.0.1:8081 in browser
```

**Compare specific endpoint:**
```bash
cd captures
mitmdump -nr remote/single_small_file_remote_traffic.mitm | grep "permission-check"
mitmdump -nr local/single_small_file_local_traffic.mitm | grep "permission-check"
```

---

### Step 3: Verify Against Stock Seafile

**Never implement based on assumptions. Always verify exact protocol first.**

**Get credentials:** See `.seafile-reference.md`

**Test specific endpoint:**
```bash
# Get auth token
TOKEN=$(curl -s -d "username=abel.aguzmans@gmail.com&password=Qwerty123!" \
  "https://app.nihaoconsult.com/api2/auth-token/" | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Get sync token
REPO_ID="eafc83e1-e62c-464a-8a87-94f2ec8d4fde"
SYNC_TOKEN=$(curl -s -H "Authorization: Token $TOKEN" \
  "https://app.nihaoconsult.com/api2/repos/$REPO_ID/download-info/" | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Test endpoint with FULL verbose output
curl -v -H "Seafile-Repo-Token: $SYNC_TOKEN" \
  "https://app.nihaoconsult.com/seafhttp/repo/$REPO_ID/permission-check?..." \
  2>&1 | tee /tmp/stock_seafile_response.txt

# Check:
# - HTTP status code
# - Response headers
# - Response body (if any)
# - Field types (integer vs string vs boolean)
```

---

### Step 4: Document in RFC

**File:** `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`

**What to document:**
1. Endpoint path and method
2. Request headers (exact names, values)
3. Request parameters (query string, body)
4. Response status code
5. Response headers
6. Response body format
7. Field types (CRITICAL - integer vs string vs boolean)
8. Edge cases (404, 403, timeouts, etc.)

**Format:**
```markdown
### X.Y Endpoint Name

**Endpoint:** `METHOD /path`

**Request:**
```http
METHOD /path?param=value HTTP/1.1
Header-Name: value
```

**Response:**
```http
HTTP/1.1 200 OK
Content-Type: application/json

{"field": "value"}
```

**Schema:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `field` | string | REQUIRED | Description |

**Verified:** YYYY-MM-DD against production Seafile (app.nihaoconsult.com)
```

---

### Step 5: Add Test to Debug Container

**After documenting, add automated test comparing local vs remote.**

**Location:** `docker/seafile-cli-debug/scripts/`

**Example test structure:**
```python
def test_permission_check(self):
    """
    Test permission-check endpoint matches stock Seafile
    Verifies: status code, headers, response body format
    """
    # Test against stock Seafile
    remote_response = self.call_remote_permission_check()

    # Test against SesameFS
    local_response = self.call_local_permission_check()

    # Compare
    assert remote_response.status_code == local_response.status_code
    assert remote_response.headers['Content-Type'] == local_response.headers['Content-Type']
    # ... etc
```

---

### Step 6: Implement

**Only after Steps 1-5 are complete.**

**Implementation checklist:**
- [ ] Exact HTTP status code matches stock Seafile
- [ ] Exact response headers match
- [ ] Exact field types match (int/string/bool)
- [ ] Exact field names match (including case)
- [ ] Empty values correct (empty string "" vs null)
- [ ] Test passes: `./run-comprehensive-with-proxy.sh --test-scenario {scenario}`

---

### Step 7: Verify with Real Client

**After implementation:**
```bash
# Check client logs
tail -f ~/.ccnet/logs/seafile.log

# Should see:
# - No timeout errors
# - Successful sync state transitions
# - "synchronized" state reached
```

---

## Common Mistakes to Avoid

### ❌ Implementing based on assumptions
**Wrong:** "The endpoint probably returns 200 OK with empty body"
**Right:** Test stock Seafile first, verify exact response

### ❌ Ignoring field types
**Wrong:** `"encrypted": true` (boolean)
**Right:** `"encrypted": 1` (integer) in download-info, `"encrypted": "true"` (string) in commits

### ❌ Using null instead of empty string
**Wrong:** `"repo_desc": null`
**Right:** `"repo_desc": ""`

### ❌ Skipping protocol verification
**Wrong:** Implement → test → debug issues
**Right:** Verify protocol → document → implement → test

---

## Decision Tree

```
Sync issue detected
    │
    ├─→ Check client logs (~/.ccnet/logs/seafile.log)
    │      │
    │      ├─→ Logs show exact error & endpoint?
    │      │      ├─→ YES → Verify against stock Seafile (Step 3)
    │      │      └─→ NO  → Use debug container with proxy (Step 2)
    │      │
    │      └─→ Document findings in RFC (Step 4)
    │             │
    │             └─→ Add test to debug container (Step 5)
    │                    │
    │                    └─→ Implement (Step 6)
    │                           │
    │                           └─→ Verify with real client (Step 7)
    │
    └─→ SUCCESS: Sync working, client shows "synchronized"
```

---

## Example: permission-check Investigation (2026-01-18)

**Issue:** Client logs show permission-check timeouts

**Step 1 - Client logs:**
```
[01/18/26 10:13:48] http-tx-mgr.c(842): libcurl failed to GET
http://localhost:8080/seafhttp/repo/01920c46.../permission-check/?op=download...
Timeout was reached.
```

**Step 2 - Not needed** (logs were conclusive about endpoint name)

**Step 3 - Verify stock Seafile:**
```bash
$ curl -v -H "Seafile-Repo-Token: $TOKEN" \
  "https://app.nihaoconsult.com/seafhttp/repo/$REPO_ID/permission-check?..."

< HTTP/1.1 404 Not Found
404 page not found
```

**Finding:** Stock Seafile returns 404 for permission-check!

**Step 4 - Run debug container to see if endpoint is even called:**
```bash
./run-comprehensive-with-proxy.sh --test-scenario single_small_file
# Check captures/ for permission-check requests
```

**Next steps:** Analyze captured traffic to determine:
- Is permission-check called during normal sync?
- If yes, what does stock Seafile return?
- If no, why is client calling it on localhost but not remote?

---

## Tools Reference

| Tool | Purpose | Command |
|------|---------|---------|
| Client logs | First place to check | `tail -f ~/.ccnet/logs/seafile.log` |
| Debug container | Protocol comparison | `./run-comprehensive-with-proxy.sh` |
| mitmproxy viewer | View captured traffic | `mitmweb -r capture.mitm` |
| Stock Seafile | Protocol reference | See `.seafile-reference.md` |
| RFC documentation | Document verified protocol | `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` |

---

## Remember

> **Stock Seafile is always correct. Match it exactly.**
>
> When in doubt: verify first, implement second.

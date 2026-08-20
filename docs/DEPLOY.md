# Deploying SesameFS to Production

This guide is organized around the repository's default production path: multi-region SesameFS nodes deployed from published images, with the same `docker-compose.prod.yml` on every server and node-local differences carried only in `.env`.
Single-region is still supported, but it is the legacy compatibility path and should be treated as a simplified fallback rather than the primary operating model.
---

## Architecture

```
Internet
   │
   ├── 443 (HTTPS) ──► Central nginx (TLS termination)
   │                        │
   │                        └─► sfs-net (Docker network)
   │                              │
  │                              └─► sesamefs-frontend-<DEPLOY_ID>:80   (nginx:alpine — SPA + API proxy)
  │
  ├── 443 (HTTPS) ──► External OnlyOffice deploy (separate service)
  │                        │
  │                        └─► sesamefs public HTTPS endpoints + shared JWT secret
   │
   └── 80 (HTTP) ──► Central nginx ──► 301 redirect to HTTPS

internal (Docker network, not routed):
   sesamefs ──► cassandra:9042

Outbound HTTPS:
   sesamefs ──► S3 (per-region bucket)
   sesamefs ──► accounts.sesamedisk.com (OIDC discovery + JWKS + token exchange)
```

The central nginx is **external** to `docker-compose.prod.yml` (the bundled
`nginx` service in the compose file is commented out as optional). In the
supported topology the central nginx joins `sfs-net` and proxies to the
per-deploy `sesamefs-frontend-<DEPLOY_ID>` container alias, which in turn proxies API
paths to `sesamefs` on the private deploy network. OnlyOffice is deployed
separately from this compose stack and integrates with SesameFS through its
public HTTPS endpoints plus the shared JWT secret. None of the application containers expose ports to the host
— only `expose:` on the Docker networks. See [Config Resolution](#config-resolution)
for how `SERVER_TRUSTED_PROXIES` interacts with this two-nginx chain.

**Files involved:**

| File | Purpose |
|---|---|
| `docker-compose.prod.yml` | Production stack (published images, no MinIO, no dev tools). Expects an external reverse proxy on `sfs-net`. |
| `configs/config.prod.yaml` | Structural config — mounted over the baked image config |
| `frontend/nginx.conf` | nginx:alpine config baked into the frontend image (SPA routing + API proxy_pass) |
| `.env.prod.example` | Template for the single `.env` file you create on the server |

`docker-compose.yaml` is not a production fallback; it is the local
development/integration stack. Production behavior is defined by
`docker-compose.prod.yml` and `configs/config.prod.yaml`, with `.env` overrides.

---

## Read This First

- Multi-region is the default production path for this repo. Read [Default Production Path (Multi-Region)](#default-production-path-multi-region) first, then come back to the shared preparation steps below.
- `docker-compose.prod.yml` does **not** publish the app directly to the host. By default it expects an external reverse proxy that can reach the per-deploy `sesamefs-frontend-$DEPLOY_ID` container alias on the external Docker network `sfs-net`.
- You must create `sfs-net` before the first `docker compose up`:

```bash
docker network inspect sfs-net >/dev/null 2>&1 || docker network create sfs-net
```

- Public health checks for operators and load balancers are `GET /ping` and the compatibility endpoint `GET /api2/ping`.
- `GET /ready` and metrics are intentionally internal-only. Run them from the node itself, from a private network, or with `docker compose exec sesamefs ...`.
- For upload finalize monitoring, alerting, and dashboard queries, see [Upload Concurrency Observability](./UPLOAD-CONCURRENCY-OBSERVABILITY.md).
- Published images are the deployment artifact. Production rollouts should update `SESAMEFS_IMAGE` and `FRONTEND_IMAGE` with full image references, pull the new images, and restart the services. `--build` is not part of the normal prod path.

---

## Config Resolution

SesameFS resolves configuration in this order:

1. `internal/config.DefaultConfig()` provides built-in defaults.
2. The YAML file pointed to by `CONFIG_PATH` is loaded on top of those defaults.
  In production compose, that is `configs/config.prod.yaml` mounted as `/app/config.yaml`.
3. Environment variables are applied last via `applyEnvOverrides()`, so env always wins.

### Storage namespace contract

This repository's deployment scope is greenfield. Once a `storage_class` name is
used, bind it permanently to exactly one physical namespace and never reuse it after
retirement. Credentials, account/tenant or provider scope, region, endpoint and bucket
may change only if the effective configuration still reaches exactly that namespace.
For multi-tenant S3-compatible providers, account/tenant scope is immutable even when
SesameFS cannot infer it from configuration. Create a new class name for new placement.
No migration or startup preflight of historical `storage_class` values is required.

Startup validation rejects two configured names with the same conservative
endpoint+bucket collision key. Canonicalization covers host case, default ports,
trailing URL slashes, equivalent AWS S3 spellings, one terminal DNS root dot and
bucket case. It does not perform DNS resolution, inspect credential scope or discover
provider account/tenant boundaries. Path/query/bucket rules may over-reject exotic
providers and fail startup; that is conservative collision detection, not proof of
universal physical identity. Use one canonical endpoint spelling per physical service
across all class and legacy-backend declarations.

A durable fingerprint is optional hardening against a historical rebind within one metadata history: the same Cassandra remembers what `hot-v1` meant, so a repoint between boots can be caught. It cannot help a fresh install, whose binding table is empty and which therefore has no memory of what a class name meant elsewhere. A namespace claim marker is the cross-install one: written inside the physical namespace, it lets a foreign or fresh install discover that the namespace is already owned. Neither is part of R23, R24 or X1, and neither belongs on the request hot path. Library placement reads fail closed: only a successful empty
`libraries.storage_class` permits hostname/default routing. Any Cassandra read error
is UNKNOWN and must not fall back to another backend, and a missing `libraries` row
counts as UNKNOWN too — the caller already validated the library, so an absent row is
dangling metadata rather than permission to route to the default class. Expect
storage-unavailable responses, not 404s, for that case.

Download admission is one of the safety defaults: `DefaultConfig()` carries the
measured D6 values, so a config file that does not pin the `download_admission`
section still starts protected. YAML overlays `DefaultConfig()` field by field:
keys omitted from a present section retain those defaults. Set
`download_admission.enabled: false` explicitly to opt out; omission is not an
implicit disable.

Auto mode is the clean-deployment default. It derives the process-local
download-memory design budget and the node/profile capacities from the effective
container memory, rather than distributing a fixed slot combination to every
machine:

```yaml
download_admission:
  enabled: true
  capacity_mode: auto
  memory_budget_percent: 25
  raw_capacity_percent: 33
  safety_margin_percent: 20
  # Optional, and commented out on purpose: setting it turns the derivation off.
  # Uncomment only where cgroup discovery is unavailable or the detected
  # percentage is deliberately overridden.
  # memory_budget_bytes: 2147483648
```

`memory_budget_bytes` is a process-local configured design budget, not an OS
reservation and not an RSS limit. When it is omitted, `Load()` derives it from
`memory_budget_percent` of the cgroup limit; if no cgroup limit is exposed, it
uses the 2 GiB reference fallback. That fallback is not a deduction about the
machine — without a cgroup limit the process knows nothing about its memory — so
a deployment smaller than the 8 GiB baseline should set an explicit budget or a
container limit rather than inherit it. An explicit byte value overrides that
derivation, and may claim up to `memory_budget_percent` of an exposed cgroup
limit. The safety margin leaves `safety_margin_percent` of the configured design
budget for HTTP structures, goroutines, SDK buffers, allocator fragmentation and
measured variation; derivation and the final validation apply the same value.
Auto mode also caps derived node capacity at 64 slots and raw capacity at 32.

The `max_active_*` fields are generated outputs in auto mode. To hand-author
those values, set `capacity_mode: manual`; otherwise the next validation derives
them again from the budget and measured costs. **The numbers in the shipped
files are therefore a reference for the 2 GiB fallback, not a promise about any
particular host.** The values a node actually runs with are printed at startup:

```text
INFO download admission capacity mode=auto
  budget_source="25% of the detected cgroup limit 8589934592"
  memory_budget_bytes=2147483648 safety_margin_percent=20
  max_active_per_node=16 max_active_raw=4 max_active_file=12 ...
```

`budget_source` is one of `configured explicitly`, `N% of the detected cgroup
limit X`, or `no cgroup limit detected, reference fallback` — so the line says
not just what the node runs with but where the number came from. The same
capacities are exported as `download_admission_capacity{setting="..."}` and
`download_admission_memory_budget_bytes`, which is how the D6 drills discover
the ceiling they have to saturate instead of assuming one.

These YAML and `.env` values are clean-deployment baselines. A smaller
container automatically derives fewer capacities when cgroup discovery is
available:

| Container limit | Download design budget | Required configuration |
|---|---:|---|
| 4 GiB | 1 GiB | Auto derives a smaller node/profile combination. |
| 8 GiB | 2 GiB | Auto derives the clean baseline of 16 active slots, 4 raw and 12 other streams. |
| 16 GiB | 4 GiB | Auto scales within the absolute policy ceilings; fairness caps remain bounded. |

**There is a floor, and a container below it will not start.** Auto mode has to
fit at least one raw slot and one stream slot, and at the shipped costs that is
`192 MiB + 72 MiB = 264 MiB` of usable budget — `330 MiB` before the 20% safety
margin. At the default 25% share that means a container limit of about
**1.3 GiB**; below roughly 660 MiB no share within the 50% ceiling is enough.
Measured: a 330 MiB budget derives 2 slots and starts, 329 MiB does not.

There are three ways forward, and the startup error names the ones that apply to
your case rather than listing all of them:

1. **Give admission a larger share of the container** — raise
   `memory_budget_percent`, up to its 50% ceiling. An explicit
   `memory_budget_bytes` is *not* a separate escape: a detected container limit
   caps it at that same share, so setting it alone on a small node is rejected
   on the next check. It selects a budget *within* the allowed share. Below
   roughly 660 MiB even the 50% ceiling falls short, and the error says so
   instead of offering either setting.
2. **Make a slot cheaper** — the floor is one raw slot plus one stream slot:

   ```text
   stream = max(4 MiB, 4.5 × seafhttp.sync_block_max_bytes)
   raw    = max(6 × max_iwork_source_bytes,
                stream,
                stream + max_iwork_source_bytes + max_iwork_preview_bytes)
   ```

   Both terms are in the failing sum, so the error names the raw term that
   currently dominates *and* the stream lever. It withholds
   `sync_block_max_bytes` once the stream cost has bottomed out on its 4 MiB
   plaintext floor — below about `0.89 MiB` of block size — because lowering it
   further changes nothing. Validation only requires that setting to be
   positive, so that band is reachable.
3. **Turn the guard off** — `download_admission.enabled: false`, accepting that
   the node has no aggregate download bound.

The guard is deliberately not degraded to "whatever fits": a design that
silently exceeds its stated budget is what this validator exists to prevent.

The first lever's cost, measured against the 2 GiB fallback, since raising the
iWork source cap is the usual reason to want more:

| `max_iwork_source_bytes` | raw slot cost | node slots | raw | stream |
|---:|---:|---:|---:|---:|
| 16 MiB | 138 MiB | 18 | 5 | 13 |
| **32 MiB (shipped)** | **192 MiB** | **16** | **4** | **12** |
| 48 MiB | 288 MiB | 12 | 3 | 9 |
| 64 MiB | 384 MiB | 9 | 2 | 7 |
| 128 MiB | 768 MiB | 6 | 1 | 5 |
| 256 MiB | 1536 MiB | 2 | 1 | 1 |

Raising the cap from 32 MiB to 64 MiB reduces the same-budget node ceiling from
16 to 9 concurrent downloads. Documents above the cap answer `413` on the
inline-preview branch only; downloading them is unaffected. See
`ISSUE-IWORK-PREVIEW-413-NO-MESSAGE-01` for what the viewer currently shows.

Manual mode is available for special deployments. It requires
`memory_budget_bytes`, `max_active_per_node`, `max_active_raw` and the other
capacity fields; the same safety-adjusted memory validator still checks the
complete combination. User, link, client, waiter and timeout values remain
policy/fairness controls in either mode, with absolute validation ceilings.

When an explicit byte budget exceeds `memory_budget_percent` of an exposed
cgroup limit, `Load()` rejects it. That share defaults to 25% and is itself
capped at 50%, so the guard and the setting can never disagree about what is
allowed. The process must run in a container sized for the configured budget;
host RAM is never used as the source of truth.

A section that is present with `enabled: false` is an explicit opt-out, but its
effective values must satisfy the section's structural validation if the guard
were enabled. This catches a zeroed or half-written section without charging it
against the configured memory design. A structurally complete section can still
exceed that combined budget and is rejected only when enabled.
The rule is completeness rather than any fixed shape, since one
`DOWNLOAD_ADMISSION_*` override is enough to defeat a fixed-shape check while
leaving the section just as unusable. A server started on such a section exits
with

```text
download_admission is disabled with a structurally incomplete configuration:
remove the section to inherit the measured defaults, or keep enabled: false
alongside complete structural values (<what is missing>)
```

The rule covers the section's own values only. The configured memory design is not
charged against a disabled section, because it multiplies these caps by
`seafhttp.sync_block_max_bytes` and the `fileview` limits, which other
subsystems own and set for their own reasons — a disabled download guard must
not stop a deployment from booting over an upload-side value. A design that
overshoots the budget is refused when the section is enabled.

The check runs after environment overrides, so a clean deployment that supplies
real values through `DOWNLOAD_ADMISSION_*` is validated as one effective
configuration. `server.write_timeout` is not part of the structural test because
it conflicts only with an active guard.

That is why `configs/config.prod.yaml` can look thinner than `configs/config.docker.yaml` or a local test config:
production only needs to pin the non-secret structural values that differ from the code defaults,
while local/test often pins more knobs explicitly for reproducibility.

`server.trusted_proxies` follows the same rule. The secure code default is empty, which disables trust in `X-Forwarded-For` and `X-Real-IP` entirely. When SesameFS is deployed behind nginx, Traefik, HAProxy, or a cloud load balancer, set `server.trusted_proxies` in YAML or `SERVER_TRUSTED_PROXIES` in `.env` to the exact proxy IPs or CIDRs that are allowed to define the client IP.

For the standard production topology used here:

`client -> central nginx -> internal SesameFS nginx -> Go backend`

the central nginx is the authority that resolves the real client IP. The internal SesameFS nginx, which in this deployment is typically the nginx inside the `frontend` container, preserves that canonicalized `X-Real-IP` and `X-Forwarded-For` chain when proxying to Go, instead of overwriting them with its own private proxy IP.

That means the Go backend only needs to trust the proxy hop directly in front of it: the internal SesameFS nginx.

This model assumes that the internal nginx is not exposed directly to the internet and only accepts traffic from the central nginx or from a private, trusted internal network. If that assumption does not hold, do not preserve incoming `X-Real-IP` / `X-Forwarded-For` headers as-is; configure explicit nginx real-IP trust rules instead.

Example:

```env
SERVER_TRUSTED_PROXIES=172.20.0.0/16
```

Why this is the supported deploy model:

- if the variable is left empty, Gin uses the direct peer IP and every request appears to come from the internal nginx
- if the variable trusts the internal nginx network, Gin accepts the already-canonicalized client IP preserved by that nginx
- the central nginx does not need to be listed in `SERVER_TRUSTED_PROXIES` as long as the internal nginx is the only hop that talks directly to Go and it preserves the forwarded headers instead of rewriting them
- this remains safe only while that internal nginx hop stays private and unreachable from arbitrary external callers

If you use a different proxy chain and do not preserve the canonicalized client IP at the last nginx hop, adjust `SERVER_TRUSTED_PROXIES` for that topology instead.

### Download admission (D0-D6, enabled)

Download admission is **on by default**. It bounds how many storage-backed
transfers a node accepts at once across every producer — seafhttp file and ZIP,
authenticated raw and history, public share raw, inline share text and sync block
GET — through one process-local coordinator.

The capacities are measured, not guessed. D6 benchmarked the heap a single
admitted transfer holds, and auto mode divides the effective budget between the
expensive raw/iWork profile and ordinary streams:

| Transfer shape | Measured peak per admission |
|---|---|
| Plaintext stream (any size) | 4.0 MiB |
| Encrypted stream | 36.0 MiB at 8 MiB; 72 MiB design cost at the accepted 16 MiB block size |
| `raw` iWork preview, 32 MiB source cap | ~184.5 MiB measured; 192 MiB design cost |

Encrypted prefetch reads and decrypts whole blocks rather than streaming them,
so the design cost scales at 4.5x the accepted block size. The iWork preview
buffers the entire source document, which is why `fileview.max_iwork_source_bytes`
caps it at 32 MiB separately from the 1 GiB general preview limit — without that
cap a single request could touch several gigabytes.

With the shipped 2 GiB fallback and 20% safety margin, auto mode derives
`4 x 192 MiB + 12 x 72 MiB = 1632 MiB` (~1.59 GiB). This is a hard startup
invariant, not advisory arithmetic: the validator rejects an enabled
configuration whose safety-adjusted design exceeds the configured budget, so
changing the budget, percentages, source/block sizes or manual caps can make
the service refuse to boot until the complete combination fits again.

`max_active_raw=0` means there is no additional raw profile sub-cap; it does not
remove raw work from the memory calculation. The validator charges all node
slots at the raw/iWork worst-case cost in that case. `max_active_block` remains a
profile sub-cap and must not exceed `max_active_per_node`; the remaining node
capacity is charged at the encrypted streaming cost.

Refused transfers answer `503` with `Retry-After`; the real `seaf-cli` fault drill
must record a profile=`block` refusal before claiming recovery. A client that stops
reading is released on `idle_write_timeout` — verified through the supported
nginx, where `proxy_buffering off` and `gzip off` on the transfer locations are
what let the application see a slow client at all rather than the proxy absorbing
it.

`idle_write_timeout` bounds the interval **without progress**, not the transfer:
a multi-hour download of a very large file is unaffected by the shipped 60s as
long as bytes keep flowing. What it tolerates is a stalled peer, or a stalled
object-store fetch between blocks under load.

That only holds while the proxy does not fire first. The supported nginx configs
therefore set `proxy_read_timeout` and `send_timeout` strictly above every
deadline the application will accept — see `config.MinNginxProxyReadTimeout` and
`config.MinNginxSendTimeout`, which derive from the validation ceilings. **If you
run a different proxy, apply the same ordering**, or the application's guard
stops being the one that ends a transfer and a raised `idle_write_timeout` will
silently do nothing.

These startup rules apply:

- `max_active_per_node`, every identity cap, `preparation_deadline`, `idle_write_timeout` and `retry_after` must be positive.
- `admission_wait` and both waiter caps may be zero; zero means refuse immediately rather than queue.
- `server.write_timeout` must remain `0`; the coordinator owns the long-transfer idle-write deadline.
- Public-link client attribution must use the trusted-proxy configuration above; do not derive a client IP from forwarded headers in application code.
- D is process-local, so fleet capacity scales with the number of application nodes. It is not a cluster-global quota.
- Bootstrap constructs exactly one coordinator and shares that pointer with every protected producer (including SyncHandler.GetBlock); D1 intentionally does not enforce a process-global singleton so package tests can create isolated coordinators.

The coordinator updates `active_current`, `active_by_profile`, `entries_current`
and `waiters_current` under one internal state transition, but Prometheus gathers
independent gauges one at a time. Do not alert on strict equality from a single
concurrent scrape; the occupancy invariants are guaranteed for coordinator state
and stable snapshots, not for every mixed-time scrape.
`waiters_by_gate` is the last reevaluated blocker observation, not a scrape-time
scan of all parked requests.

GC is a good example:

- If `user_grace_days`, `org_grace_days`, `trash_retention_days`, or `audit_retention_days` are omitted from YAML, SesameFS keeps the built-in defaults from `DefaultConfig()`.
- If you set `GC_USER_GRACE_DAYS`, `GC_ORG_GRACE_DAYS`, `GC_TRASH_RETENTION_DAYS`, or `GC_AUDIT_RETENTION_DAYS` in `.env`, those override both the YAML and the code defaults.
- `trash_retention_days` controls how long a soft-deleted library remains restorable before GC Phase 11 may enqueue `library_cascade`.
- Historical restore inside a live library is separate: it depends on each library's history setting (`keep_days` / `version_ttl_days`), not on the global GC env overrides above.

---

## Server Requirements

| Resource | Minimum | Recommended |
|---|---|---|
| CPU | 2 vCPU | 4 vCPU |
| RAM | 6 GB | 8–16 GB |
| Disk | 40 GB | 100 GB+ |
| OS | Ubuntu 22.04 / Debian 12 | same |

> Cassandra needs ~2 GB RAM heap. OnlyOffice needs ~1 GB. sesamefs itself is lightweight.

---

## Step 0 — Before you touch the server

### 0.0 Choose ingress mode and create `sfs-net`

The supported default is:

- external central nginx or load balancer outside `docker-compose.prod.yml`
- that proxy attaches to `sfs-net`
- it forwards web traffic to `sesamefs-frontend-$DEPLOY_ID:80`
- the frontend nginx proxies API routes to `sesamefs:8080` on the private deploy network
- `DEPLOY_ID` is required and must be unique per deploy that joins `sfs-net`. The short service name `frontend` is also resolvable on `sfs-net` but collides between stacks — never target it from the central nginx, only the regional alias above.

Before the first deploy on every host:

```bash
docker network inspect sfs-net >/dev/null 2>&1 || docker network create sfs-net
```

If you prefer to terminate TLS inside the SesameFS stack, uncomment the optional `nginx` service in `docker-compose.prod.yml` and treat that as the owner of host ports `80/443`. The rest of this guide assumes the default external-proxy model unless stated otherwise.

### 0.1 Create an S3 bucket

1. Create a bucket in your S3-compatible provider (AWS S3, Cloudflare R2, MinIO, etc.)
2. Create an API/IAM user with `s3:*` permission on that bucket
3. Save the `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY`
4. **Keep the bucket private.** SesameFS reads blocks with its own credentials
   and never needs anonymous access. A bucket that grants public read lets
   anyone who can reach the storage endpoint enumerate keys and download block
   content directly, bypassing SesameFS authentication, authorization, quota
   checks, traffic recording and download admission. AWS S3 and Cloudflare R2
   are private by default, so the standard deployment is fine as-is; a
   self-hosted MinIO is only private if you leave it that way.

   If you want to confirm it, an unauthenticated request must be refused:

   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' "$S3_ENDPOINT/$S3_BUCKET/?list-type=2"   # expect 403
   ```

### 0.2 Register an OIDC client

In `accounts.sesamedisk.com`, create a new application/client:

- **Grant type**: Authorization Code
- **PKCE**: Required (optional but recommended)
- **Redirect URIs**: register **both** callback paths for **every** production login hostname:
  - `https://<your-domain>/sso/`
  - `https://<your-domain>/oauth/callback/`
- **Scopes**: `openid profile email`

Save the `client_id` and `client_secret`.

> **Why two redirect URIs?**
> - `/sso/` — web login flow (React frontend maneja el callback)
> - `/oauth/callback/` — desktop client SSO (el servidor canjea el código, marca el pending token como exitoso y redirige a `seafile://client-login/`)

### 0.3 Generate secrets

Run these locally and save the output — you'll paste them into `.env`:

```bash
openssl rand -hex 32   # → OIDC_JWT_SIGNING_KEY
openssl rand -hex 32   # → ONLYOFFICE_JWT_SECRET
openssl rand -hex 32   # → SHARE_LINK_HMAC_KEY
```

> **`SHARE_LINK_HMAC_KEY`** signs the password-unlock cookies for password-protected share
> and upload links. When a visitor enters the correct password, an HMAC cookie is set so
> they are not re-prompted on every page navigation. Without this key sesamefs **refuses to
> start in production**. Rotating it invalidates all active share-link password sessions
> (visitors will need to re-enter the share password once).

### 0.4 Set up DNS

Point every production hostname at the node or proxy that will actually serve it.

- Multi-region default: each region-specific hostname points to that region's public IP or regional load balancer.
- Single-region fallback: `files.yourdomain.com` and `office.yourdomain.com` can both point to the same server.

Examples:

```text
# Multi-region
na.files.yourdomain.com      A   <na-public-ip>
eu.files.yourdomain.com      A   <eu-public-ip>
na.office.yourdomain.com     A   <na-public-ip>
eu.office.yourdomain.com     A   <eu-public-ip>

# Single-region legacy
files.yourdomain.com         A   <server-ip>
office.yourdomain.com        A   <server-ip>
```

Wait for DNS to propagate before requesting certificates.

---

## Step 1 — Install dependencies on the server

```bash
# Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
newgrp docker

# Certbot + tooling used later in pre-flight checks
sudo apt install -y certbot jq dnsutils

# Verify
docker compose version
certbot --version
```

---

## Step 2 — Get SSL certificates

Request certificates for the hostnames served by **this node**.

- Single-region legacy: one SAN certificate covering `files.yourdomain.com` and `office.yourdomain.com` is typical.
- Multi-region default: repeat this per node using that node's `files` and `office` hostnames.

If you use Certbot standalone, port 80 must be free while Certbot runs:

```bash
sudo certbot certonly --standalone \
  -d na.files.yourdomain.com \
  -d na.office.yourdomain.com
```

Certbot stores the certificate under the first `-d` hostname, for example:
`/etc/letsencrypt/live/na.files.yourdomain.com/`

Point the nginx server blocks for both hostnames on that node at the same certificate directory when they were issued together in one SAN cert.

**Auto-renewal** (certbot installs a systemd timer automatically — verify it):
```bash
systemctl status certbot.timer
```

---

## Step 3 — Clone the repo

```bash
git clone <your-repo-url> /opt/sesamefs
cd /opt/sesamefs
```

---

## Step 4 — Create `.env`

```bash
cp .env.prod.example .env
nano .env
```

On a fresh host, the recommended flow is:

```bash
./scripts/prod-preflight.sh --init-env
nano .env
```

`--init-env` creates `.env` from `.env.prod.example` when needed and fills only the auto-generatable secrets. It does **not** guess external values such as S3 credentials, OIDC settings, public URLs, or image refs.

Fill in these values. The examples below assume the default multi-region path; single-region notes follow immediately after the relevant settings.

```bash
# Release images (full refs, including tag)
SESAMEFS_IMAGE=yoilier/sesamefs:2026.05.01-abc1234
FRONTEND_IMAGE=yoilier/sesamefs-frontend:2026.05.01-abc1234

# Public URLs
# Leave SERVER_URL unset in the standard multi-domain deploy.
# SesameFS will use the current request host / X-Forwarded-Host by default.
# SERVER_URL=https://<files-hostname>
ONLYOFFICE_API_JS_URL=https://<office-hostname>/web-apps/apps/api/documents/api.js

# S3 credentials
# Default credential pair used by any storage class without its own override.
S3_ACCESS_KEY_ID=<from step 0.1>
S3_SECRET_ACCESS_KEY=<from step 0.1>

# Explicit storage mode (default production mode)
STORAGE_MODE=multi

# Multi-region per-class bucket mapping. Real bucket names stay in .env, not in
# configs/config.prod.yaml.
S3_CLASS_HOT_S3_NA_BUCKET=<your-na-bucket>
S3_CLASS_HOT_S3_EU_BUCKET=<your-eu-bucket>
S3_CLASS_HOT_S3_ASIA_BUCKET=<your-asia-bucket>

# Optional per-class credential overrides. If omitted, SesameFS falls back to
# S3_ACCESS_KEY_ID / S3_SECRET_ACCESS_KEY.
# S3_CLASS_HOT_S3_NA_ACCESS_KEY_ID=<optional-class-key>
# S3_CLASS_HOT_S3_NA_SECRET_ACCESS_KEY=<optional-class-secret>

# OIDC callback allow-list. Add every production login domain registered in your IdP.
OIDC_REDIRECT_URIS=https://<files-hostname>/sso/,https://<files-hostname>/oauth/callback/

# Single-region only: set STORAGE_MODE=single, leave SERVER_REGION empty, and
# uncomment both required location values to point the legacy hot backend at one bucket.
# In multi-region, keep these unset and use S3_CLASS_*_BUCKET above.
# S3_BUCKET=<your-bucket-name>
# S3_REGION=us-east-1
# S3_ENDPOINT=
# S3_SERVER_SIDE_ENCRYPTION=AES256
# S3_SSE_KMS_KEY_ID=

# CORS (required in prod; wildcard "*" is rejected)
# Include EVERY production browser origin that can call SesameFS.
CORS_ALLOWED_ORIGINS=https://<files-hostname>,https://<additional-browser-origin>

# OnlyOffice JWT token lifetime (seconds). Default 3600 (1h). Range: 300–28800.
ONLYOFFICE_JWT_TTL_SECONDS=3600

# Cassandra
CASSANDRA_CLUSTER_NAME=sesamefs-prod
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M
CASSANDRA_DATA_DIR=/srv/sesamefs/cassandra
CASSANDRA_SUPERUSER_PASSWORD=<generated by --init-env or openssl rand -hex 32>
CASSANDRA_USERNAME=sesamefs_app
CASSANDRA_PASSWORD=<generated by --init-env or openssl rand -hex 32>
CASSANDRA_KEYSPACE=sesamefs

# OIDC
OIDC_ENABLED=true
AUTH_DEV_MODE=false
OIDC_CLIENT_ID=<from step 0.2>
OIDC_CLIENT_SECRET=<from step 0.2>
OIDC_JWT_SIGNING_KEY=<from step 0.3 — first openssl output>

# Share link password cookies (REQUIRED in production)
SHARE_LINK_HMAC_KEY=<from step 0.3 — third openssl output>

# External billing portal used by /billing/
BILLING_URL=https://accounts.yourdomain.com/billing/

# External Accounts URLs used by /accounts/delete/
ACCOUNTS_DELETE_ACCOUNT_URL=https://accounts.yourdomain.com/accounts/delete/
# Org user management link in org-admin panel ({org_id} replaced at runtime).
# Leave empty to hide the "Manage in Accounts" button.
ACCOUNTS_ORG_USER_MANAGEMENT_URL=https://accounts.yourdomain.com/orgs/{org_id}/members/
ACCOUNTS_DISABLE_ORG_USER_WRITES=true

# Reverse proxy IPs/CIDRs allowed to define the client IP via X-Forwarded-For.
# Supported prod topology here is:
#   client -> central nginx -> internal SesameFS nginx -> Go
# The internal nginx preserves the client IP already resolved by the central
# nginx, so Go only needs to trust the internal nginx hop.
# Example:
# SERVER_TRUSTED_PROXIES=172.20.0.0/16
# If you use the bundled nginx on Docker's external sfs-net network, inspect the
# actual subnet first and use that exact CIDR instead of guessing.

# Destructive GC safety gate: X1 is open (X2 is closed under its stable-topology
# operational contract). Keep false on EVERY backend replica in EVERY DC. Do not
# designate a GC replica yet.
GC_ENABLED=false

# External OnlyOffice deployment: SesameFS only needs these three values.
ONLYOFFICE_ENABLED=true
ONLYOFFICE_API_JS_URL=https://<office-hostname>/web-apps/apps/api/documents/api.js
ONLYOFFICE_JWT_SECRET=<from step 0.3 — second openssl output>
```

Before the first launch, create the host directories used by the bind mounts:

```bash
mkdir -p "$CASSANDRA_DATA_DIR"
```

`docker-compose.prod.yml` now brings Cassandra up with auth enabled, but the
administrative bootstrap is explicit. A normal production app deploy does not
run `cassandra-bootstrap` automatically and does not modify Cassandra auth,
roles, keyspaces, or replication as a side effect.

Run the phases in this order:

```bash
# Phase 1: bring up the local Cassandra node first.
docker compose -f docker-compose.prod.yml up -d cassandra

# Phase 2: once the multi-DC cluster is formed and healthy, run bootstrap once
# from the designated admin node only.
docker compose -f docker-compose.prod.yml --profile bootstrap up cassandra-bootstrap

# Phase 3: start the application services normally.
docker compose -f docker-compose.prod.yml up -d sesamefs frontend
```

The bootstrap step rotates the built-in `cassandra` superuser password to
`CASSANDRA_SUPERUSER_PASSWORD` on first boot, creates or updates the
`CASSANDRA_USERNAME` app role, and creates or converges `CASSANDRA_KEYSPACE`.

If the keyspace is missing, the backend now fails fast with a clear error
instead of attempting `CREATE KEYSPACE` through the restricted app role.

> Production compose uses published images, not local `build:` steps.
>
> Set `SESAMEFS_IMAGE` and `FRONTEND_IMAGE` in `.env` to the exact image references you want to run, including tag. Example:
> - backend → `yoilier/sesamefs:2026.05.01-abc1234`
> - frontend → `yoilier/sesamefs-frontend:2026.05.01-abc1234`

> Single-region and multi-region share the same production compose and the same `configs/config.prod.yaml`.
> The operational switch is `STORAGE_MODE`: use `multi` for the shared multi-region topology and `single` only for the legacy single-bucket path. In `multi`, set `SERVER_REGION` per node.

> Leave `SERVER_URL` unset in the standard deploy. SesameFS will derive the public host from the current request / forwarded host headers, which is the right default when one deploy serves multiple domains.

> Set `SERVER_URL` only if you need an explicit fallback for unusual reverse-proxy paths or absolute-link generation when the request itself does not carry usable host context.

> In single-region mode, the legacy `hot` backend consumes `S3_BUCKET`, `S3_REGION`, and optional `S3_ENDPOINT` / SSE overrides from `.env`. Setting `S3_BUCKET` activates the backend, so `S3_REGION` is required; startup fails if either an active legacy backend or an active modern class has no explicit region.

> In multi-region mode, `configs/config.prod.yaml` keeps the public topology: storage classes, provider regions, and failover chains. Real bucket names come from per-class `S3_CLASS_*_BUCKET` env vars.

> Per-class credentials are optional. If `S3_CLASS_*_ACCESS_KEY_ID` / `SECRET_ACCESS_KEY` are unset for a class, SesameFS falls back to the default `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` pair.

> `OIDC_REDIRECT_URIS` can contain callback URLs for multiple production domains in the same deploy, as long as the same list is allow-listed in your IdP.

> `CORS_ALLOWED_ORIGINS` must include every production browser origin that will call SesameFS. If a public domain is missing here, browsers on that domain will fail with CORS errors even if nginx and OIDC are configured correctly.

> `BILLING_URL` is different: it is the external billing portal destination. Users click SesameFS `/billing/`, and the backend redirects authenticated sessions to this external URL in a new tab.

> `ACCOUNTS_DELETE_ACCOUNT_URL` works the same way for `/accounts/delete/`.

> `ACCOUNTS_DISABLE_ORG_USER_WRITES` should normally stay `true`. That keeps tenant org-admin user lifecycle writes disabled so Accounts remains the operational authority. Platform superadmins still bypass that tenant lock as an operational fallback, but Accounts should prefer the `/admin/...` surface.

> `gc.enabled` defaults to `false` in YAML. While X1 (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`) remains open, keep `GC_ENABLED=false` on **every replica in every DC**. X2 is closed under the stable-topology operational contract; changing the replication DC set or RF with existing reference state requires a separately certified migration before GC can be reconsidered. The LWT lease does not close X1. Only after X1 closes may designated replicas in one DC set `GC_ENABLED=true` and participate under the lease; all replicas in every other DC must remain false.

> `SERVER_TRUSTED_PROXIES` should never be set to `0.0.0.0/0`, `::/0`, or another blanket range. In the supported two-nginx topology above, trust only the internal SesameFS nginx network that talks directly to Go. If you leave it unset, SesameFS ignores forwarded-IP headers and uses the direct socket peer instead.

> The simplified two-nginx model above depends on the internal nginx being reachable only from trusted internal paths. If you ever expose that hop more broadly, switch to explicit nginx real-IP trust configuration before relying on preserved forwarded headers.

---

## Step 5 — Configure firewall

```bash
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 80/tcp    # HTTP (nginx redirect to HTTPS)
sudo ufw allow 443/tcp   # HTTPS
sudo ufw enable
sudo ufw status
```

Cassandra (9042), OnlyOffice (80), sesamefs (8080), and the frontend (80) are
exposed only on internal Docker networks (`expose:`, not `ports:`) — they are
unreachable from the internet. Public traffic enters exclusively through the
nginx hop on 443. In multi-region, Cassandra additionally binds gossip/CQL
(7000/7001/9042) to the private vRack IP via `CASSANDRA_BIND_IP`, never
`0.0.0.0`.

> **Multi-region:** You also need to allow Cassandra gossip and CQL on the
> private network. See [Multi-Region Firewall](#step-m3--firewall-private-network).

---

## Step 5.5 — Pre-flight checks

Run these on the server **before** `docker compose up`. They catch the most common
silent misconfigurations (placeholder values left in `.env`, DNS not propagated,
S3 unreachable, OIDC discovery blocked) before the stack starts and starts emitting
real traffic. Multi-region nodes should ALSO run [Step M3.5](#step-m35--pre-flight-multi-region).

### 5.5.1 — Validate `.env` with `prod-preflight.sh`

```bash
# Optional on a fresh host: seed .env and generate only the random secrets.
./scripts/prod-preflight.sh --init-env

# Edit the remaining external values by hand.
nano .env

# Load .env into the shell for validation mode.
set -a; source .env; set +a

# Validate the production env contract.
./scripts/prod-preflight.sh
```

The script already checks the parts that are purely environment/config driven, including:

- required image refs (`SESAMEFS_IMAGE`, `FRONTEND_IMAGE`)
- auth and OIDC safety flags
- share-link and OnlyOffice secrets
- Cassandra auth/bootstrap secrets and bind-mount paths
- core external URLs (`BILLING_URL`, `ACCOUNTS_DELETE_ACCOUNT_URL`, optional `SERVER_URL`)
- storage-mode consistency and required S3 env vars
- browser-origin allowlist and related prod-only guards

Use the remaining checks below for host-level and network-level probes that the script intentionally does **not** perform.

### 5.5.2 — DNS resolves to this server

```bash
PUBLIC_IP=$(curl -s https://api.ipify.org)
for host in na.files.yourdomain.com na.office.yourdomain.com; do
  resolved=$(dig +short "$host" | tail -n1)
  if [ "$resolved" = "$PUBLIC_IP" ]; then
    echo "  [OK]   $host → $resolved"
  else
    echo "  [FAIL] $host → $resolved (expected $PUBLIC_IP)"
  fi
done
```

### 5.5.3 — SSL certs exist

```bash
# Replace CERT_NAME with the first hostname passed to certbot on this node.
CERT_NAME=na.files.yourdomain.com

if sudo test -f "/etc/letsencrypt/live/$CERT_NAME/fullchain.pem"; then
  echo "  [OK]   cert present at /etc/letsencrypt/live/$CERT_NAME/"
  sudo openssl x509 -enddate -noout -in "/etc/letsencrypt/live/$CERT_NAME/fullchain.pem"
else
  echo "  [FAIL] no cert at /etc/letsencrypt/live/$CERT_NAME/ — run certbot (Step 2)"
fi
```

### 5.5.4 — OIDC issuer is reachable

```bash
curl -fsS "$OIDC_ISSUER/.well-known/openid-configuration" | jq '{issuer, jwks_uri, authorization_endpoint, token_endpoint}'
# Must echo a JSON document. A 404 / connection error here means OIDC will fail at runtime.
```

### 5.5.5 — S3 bucket is reachable and writable

```bash
# Single-region:
docker run --rm -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
                -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
                amazon/aws-cli s3api head-bucket --bucket "$S3_BUCKET" --region "$S3_REGION" \
  && echo "  [OK]   $S3_BUCKET reachable" \
  || echo "  [FAIL] $S3_BUCKET not reachable"

# Multi-region: repeat for every configured class bucket.
for var in S3_CLASS_HOT_S3_NA_BUCKET S3_CLASS_HOT_S3_EU_BUCKET S3_CLASS_HOT_S3_ASIA_BUCKET; do
  bucket="${!var}"
  [ -z "$bucket" ] && continue
  docker run --rm -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
                  -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
                  amazon/aws-cli s3api head-bucket --bucket "$bucket" \
    && echo "  [OK]   $var=$bucket" \
    || echo "  [FAIL] $var=$bucket not reachable"
done
```

### 5.5.6 — Ports 80/443 are free

```bash
# Only required if you plan to run the bundled nginx service or Certbot in
# standalone mode on this host. If an external host nginx already owns 80/443,
# that is expected in the default topology.
sudo ss -ltnp | grep -E ':(80|443) ' || echo "  [OK] ports 80/443 currently unused on this host"
```

If every line is `[OK]`, proceed to Step 6.

---

## Step 6 — Deploy

```bash
cd /opt/sesamefs

# Required once per host for the default external-proxy topology
docker network inspect sfs-net >/dev/null 2>&1 || docker network create sfs-net

# Pull the release images referenced in SESAMEFS_IMAGE / FRONTEND_IMAGE
docker compose -f docker-compose.prod.yml pull

# Phase 1: start the local Cassandra node first
docker compose -f docker-compose.prod.yml up -d cassandra

# Phase 2: from the designated admin node only, run the one-shot Cassandra bootstrap
docker compose -f docker-compose.prod.yml --profile bootstrap up cassandra-bootstrap

# Phase 3: start the normal app services
docker compose -f docker-compose.prod.yml up -d sesamefs frontend

# Watch logs during startup
docker compose -f docker-compose.prod.yml logs -f
```

Cassandra takes ~60–90 seconds to become healthy on first boot.
Do not start SesameFS before the explicit bootstrap step has completed at least
once for the cluster, or the backend will fail fast because the keyspace does
not exist yet.

---

## Step 7 — First superadmin

On first startup, SesameFS seeds the database automatically. **Set `FIRST_SUPERADMIN_EMAIL`
in `.env` to your real email before the first deploy** — that's it, no DB work needed.

```bash
# In .env (already in .env.prod.example):
FIRST_SUPERADMIN_EMAIL=you@yourdomain.com
```

The seed creates a **superadmin** account in the **platform org** with that email.
When you log in via OIDC with that address, SesameFS matches you to the pre-seeded
superadmin account. You can then manage organizations at `/sys/organizations/`.

> **Note:** The seed only runs once (idempotent). Changing `FIRST_SUPERADMIN_EMAIL`
> after the first deploy has no effect.

### If you forgot FIRST_SUPERADMIN_EMAIL, or need to promote a user later

```bash
# From the project root (use -f for production compose file):
./scripts/make-superadmin.sh -f docker-compose.prod.yml you@yourdomain.com "Your Name"
```

The script places the user in the platform org with `role=superadmin`, updates all
lookup tables (`users_by_email`, `users_by_oidc`), and invalidates existing sessions.
See `./scripts/make-superadmin.sh --help` for options.

### Auto-assign roles via OIDC claim (for teams with multiple admins)

In `accounts.sesamedisk.com`, add a `roles` claim with value `admin` or `superadmin`
to the relevant users. Then add to `.env`:

```bash
OIDC_ROLES_CLAIM=roles
```

Roles are synced from the OIDC token on every login.

---

## Step 8 — Verify

### 8.1 Liveness / readiness

```bash
# Replace with the public app hostname served by this node or regional proxy.
FILES_HOST=https://na.files.yourdomain.com
OFFICE_HOST=https://na.office.yourdomain.com

# Public liveness (should return: pong)
curl "$FILES_HOST/ping"

# Legacy compatibility liveness for older monitors/clients
curl "$FILES_HOST/api2/ping"

# Public process liveness
curl "$FILES_HOST/health"

# Internal readiness — run from the node or inside the container
docker compose -f docker-compose.prod.yml exec sesamefs \
  wget -qO- http://127.0.0.1:8080/ready

# Expected readiness payload checks Cassandra and S3 connectivity
# Expected: {"database":"ok","storage":"ok"}

# OIDC is configured
curl "$FILES_HOST/api/v2.1/auth/oidc/config"
# Expected: {"issuer":"https://accounts.sesamedisk.com", ...}

# Auth is enforced (unauthenticated request must return 401)
curl -s -o /dev/null -w "%{http_code}\n" "$FILES_HOST/api2/repos/"
# Expected: 401

# OnlyOffice is up
curl "$OFFICE_HOST/healthcheck"
# Expected: {"status":"ok"}

# Frontend is being served
curl -s -o /dev/null -w "%{http_code}\n" "$FILES_HOST/"
# Expected: 200
```

### 8.2 Trusted-proxy hardening

If `SERVER_TRUSTED_PROXIES` is misconfigured, SesameFS will trust forged
`X-Forwarded-For` headers and corrupt rate-limiting and audit logs. Confirm
that an external attacker cannot spoof their client IP:

```bash
# Send a forged X-Forwarded-For from outside. The backend should ignore it.
curl -H "X-Forwarded-For: 1.2.3.4" "$FILES_HOST/api/v2.1/auth/oidc/config" -v 2>&1 | head -1
# Then check sesamefs logs for the request — the recorded client IP MUST be
# the central nginx hop / your real source IP, NOT 1.2.3.4.
docker compose -f docker-compose.prod.yml logs --tail=20 sesamefs | grep -i 'oidc/config'
```

### 8.3 OIDC end-to-end smoke

Open your public SesameFS hostname in a browser (for example `https://na.files.yourdomain.com/`) and complete the OIDC login
with the `FIRST_SUPERADMIN_EMAIL` account. After redirect:

- you should land on the SPA logged in as superadmin
- `/sys/organizations/` should be reachable
- `/billing/` should redirect to `BILLING_URL`

---

## Operations

### View logs

```bash
# All services
docker compose -f docker-compose.prod.yml logs -f

# Single service
docker compose -f docker-compose.prod.yml logs -f sesamefs
docker compose -f docker-compose.prod.yml logs -f frontend
docker compose -f docker-compose.prod.yml logs -f cassandra
```

### Deploy an update

```bash
cd /opt/sesamefs
git pull

# Update SESAMEFS_IMAGE and/or FRONTEND_IMAGE in .env first, then pull and restart.

# Pull the new published images
docker compose -f docker-compose.prod.yml pull

# Roll the stack to the new image refs
docker compose -f docker-compose.prod.yml up -d

# Service-scoped rollouts are also fine when only one image changed
docker compose -f docker-compose.prod.yml up -d sesamefs
docker compose -f docker-compose.prod.yml up -d frontend
```

Do not use `--build` for the normal prod path. The production compose consumes published images, not local Docker builds.

### Stable public-link source IDs on a clean deployment (migration 013)

SesameFS currently supports this schema contract for greenfield deployments
only. Apply all Cassandra migrations before serving traffic, and verify
`schema_migrations` contains version 13 and `access_tokens` exposes `source_id`.
Do not start the application against a schema where migration 013 has not
completed.

Every newly minted public-link upload or download token must carry a non-empty,
stable, non-secret source ID. Token writers reject a blank source ID, and
`HandleUpload`, `HandleDownload` and `HandleZipDownload` fail closed if a
link-origin token without one is read. There is no rolling or legacy-token
fallback. Reminting a short-lived seafhttp URL preserves the exact source ID,
so it does not reset the A1 attempt-rate keys or A2 in-flight source key on that
node.

The live Cassandra integration tests
`TestAccessTokenSourceIDPersistsAcrossLinkUploadTokenRemints` and
`TestAccessTokenSourceIDPersistsAcrossLinkDownloadTokenRemints` verify that
migration 013 installed the column, distinct remints read back the exact same
source ID, and the Cassandra token writer rejects a blank source ID.

The upload-link rate buckets and concurrency counters remain process-local. They
bound admission to permission, body and storage work only after a valid token has
resolved as link-origin; they are not a total request-rate guard for the endpoint.
Rate admission reserves the stable source before creating or consulting the
per-client (IP, source) key. Once a link's source budget is exhausted, requests
from attacker-controlled IPs therefore cannot grow the per-client limiter's
retained key cardinality. If the later per-client check rejects, only the source
reservation is rolled back; successful A1 admissions remain consumed even when
the subsequent A2 in-flight guard rejects.
Token lookup occurs first because `Source` is not known beforehand, so arbitrary
invalid-token requests remain outside these guards and Cassandra token lookup is
not protected by A1. The guards protect later node capacity, not a cluster-global
quota; aggregate fleet admission can scale with node count.

### Upgrading GC to the library guard-mode build (migration 011) — stop-the-world

> **Not applicable to a greenfield deploy.** This procedure exists for clusters that already ran
> an older GC binary against populated `gc_queue` / `gc_failed_items` tables. A fresh install
> applies every migration at first boot, before any scanner or worker runs, so there is no
> version skew and no pre-011 backlog to drain. Deploy normally, but keep GC disabled
> fleet-wide while X1 remains open.

Migration 011 adds `library_guard_mode` to `gc_queue` / `gc_failed_items` and the
scanner starts stamping orphan work as `canonical_absent` (P6b execution-time
revalidation). This is **not** safe under a mixed-version GC fleet: a new scanner
enqueues `canonical_absent`, but an **old** worker ignores `library_guard_mode`,
sees the legacy `requires_library_deleted_check=true` boolean, resolves it to
`deleted_at_identity`, finds no matching delete marker, and drops the orphan as
stale — completing the item without doing the canonical point-read guard.

Because GC mutates shared state destructively, upgrade GC as a stop-the-world
operation across the **whole fleet** (every region/DC), not as a rolling deploy:

1. Set `GC_ENABLED=false` on every backend replica in every DC and roll them so
   no scanner/worker runs anywhere. (The single-leader lease does not help here —
   the risk is version skew, not concurrency.)
2. Confirm no old GC worker is still running: check `M5.5` (GC lease is
   single-leader) shows no active leader, and that every replica now reports
   `GC_ENABLED=false`.
3. Apply migration 011 (runs automatically on next boot, or `sesamefs migrate`).
   Verify `schema_migrations` contains version 11 and both tables expose the
   `library_guard_mode` column.
4. Deploy the new image to **all** replicas in all DCs while GC stays disabled.
5. Confirm the deployed image/commit is identical on every replica — no old
   binary remains anywhere.
6. Keep `GC_ENABLED=false` on every replica in every DC while X1 remains open.
   After X1 closes, designated replicas in one DC may be re-enabled under the LWT
   lease; every replica in every other DC must remain disabled. A topology/RF change
   requires a separately certified migration before GC can be reconsidered.

Legacy queue/DLQ rows written before the migration (NULL `library_guard_mode` +
`requires_library_deleted_check=true`) remain correct: the new binary hydrates
them as `deleted_at_identity`, matching their original intent.

**Legacy orphan rows are the exception.** Pre-011 scanner Phase 3/4 enqueued orphan
commit/fs_object work with `requires_library_deleted_check=false` and no guard mode.
Those rows hydrate as `LibraryGuardNone` and are purged **without** the canonical
point-read guard — the same behavior as the old binary, but P6b does not
retroactively protect them. On a cluster that previously ran GC in production,
**drain `gc_queue` and the DLQ (or let them complete) before re-enabling GC**, or run
the fail-closed preflight described in ISSUE-GC-LEGACY-ORPHAN-UNGUARDED-01. On a fresh
/ pre-production deployment there is no such backlog and no action is needed.

### Upgrading from a pre-NetworkTopologyStrategy build

Older builds bootstrapped the `sesamefs` keyspace as
`SimpleStrategy{replication_factor: 1}` and required a manual
`ALTER KEYSPACE` to switch to NetworkTopologyStrategy. The current bootstrap
applies the declared replication policy to both `sesamefs` and `system_auth`
on every `docker compose up`, so the first deploy of this build will
`ALTER KEYSPACE` the existing cluster.

Before pulling, on each existing node:

1. Confirm the cluster's actual DC name (`docker compose -f docker-compose.prod.yml exec cassandra nodetool status`) and make sure `CASSANDRA_DC` in `.env` matches it. The bootstrap will refuse to apply a policy that does not include the local DC.
2. Set `CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy` and `CASSANDRA_REPLICATION_DCS` to the full topology you want (single-region: `dc-name:1`; multi-region: `dc-na:1,dc-eu:1,...`).
3. Keep `CASSANDRA_CONSISTENCY=LOCAL_QUORUM` and `CASSANDRA_SERIAL_CONSISTENCY=SERIAL` unless you are deliberately changing the Cassandra consistency contract. In multi-region, `SERIAL` is the safe default for LWT/CAS on shared rows such as library HEAD and block refcounts.
4. Pull and `docker compose up -d` as usual. The bootstrap container reapplies the policy idempotently.
5. Single-region single-DC `SimpleStrategy{rf=1} → NetworkTopologyStrategy{dc:1}` places replicas identically; repair is a no-op but harmless. Multi-DC migrations or RF changes still require `nodetool repair sesamefs` and `nodetool repair system_auth` after the ALTER.

### Restart a service

```bash
docker compose -f docker-compose.prod.yml restart sesamefs
```

### Stop everything

```bash
docker compose -f docker-compose.prod.yml down
# Persistent data stays on the host bind-mount paths from .env.
```

### Check resource usage

```bash
docker stats
```

---

## Configuration reference

### `configs/config.prod.yaml`

Contains structural settings with no secrets. Mounted over the config
baked into the Docker image. Edit and restart sesamefs to apply changes.

Settings that **cannot** be set via env vars and must be in this file:
- `onlyoffice.server_url` — optional explicit SesameFS URL for OnlyOffice. Leave empty to derive it from the current request host.
- `onlyoffice.internal_url` — optional explicit OnlyOffice URL for callback downloads. Leave empty to reuse the host from `ONLYOFFICE_API_JS_URL`.
- `onlyoffice.view_extensions` / `edit_extensions`
- `cors.allowed_origins` — required in production. Set it explicitly, for example `["https://files.yourdomain.com"]`. SesameFS now fails closed when the list is empty.

### All env var overrides

| Env var | Config field | Notes |
|---|---|---|
| `AUTH_DEV_MODE` | `auth.dev_mode` | Set `false` in prod |
| `OIDC_ENABLED` | `auth.oidc.enabled` | Set `true` in prod |
| `OIDC_ISSUER` | `auth.oidc.issuer` | Default in configs/config.prod.yaml |
| `OIDC_CLIENT_ID` | `auth.oidc.client_id` | Secret |
| `OIDC_CLIENT_SECRET` | `auth.oidc.client_secret` | Secret |
| `OIDC_REDIRECT_URIS` | `auth.oidc.redirect_uris` | Required when OIDC is enabled. Can list callback URLs for multiple production domains. |
| `OIDC_JWT_SIGNING_KEY` | `auth.oidc.jwt_signing_key` | Secret. When set, sessions are signed JWTs instead of opaque tokens. **NEVER change after deploy** — all active sessions are immediately invalidated. Revoked JWTs (logout/user deactivation) are verified against the DB so revocation is effective even in JWT mode. |
| `OIDC_DEFAULT_ROLE` | `auth.oidc.default_role` | |
| `OIDC_AUTO_PROVISION` | `auth.oidc.auto_provision` | |
| `OIDC_DEFAULT_ORG_ID` | `auth.oidc.default_org_id` | Optional fallback org UUID when the IdP does not provide one. |
| `OIDC_DEFAULT_ORG_NAME` | `auth.oidc.default_org_name` | Optional fallback org name for auto-provisioned org creation. |
| `OIDC_ALLOWED_ORG_CLAIMS` | `auth.oidc.allowed_org_claims` | Optional comma-separated allowlist for tenant claim values. |
| `OIDC_SESSION_TTL` | `auth.oidc.session_ttl` | Web browser sessions (default: 24h) |
| `OIDC_API_TOKEN_TTL` | `auth.oidc.api_token_ttl` | Desktop/mobile client tokens (default: 180 days) |
| `OIDC_REFRESH_TOKEN_TTL` | `auth.oidc.refresh_token_ttl` | Refresh-token lifetime (default: 7 days) |
| `OIDC_ALLOW_OFFLINE_TOKEN` | `auth.oidc.allow_offline_token` | Enables offline/refresh-token behavior when supported by the IdP flow. |
| `OIDC_VALIDATE_AUDIENCE` | `auth.oidc.validate_audience` | Defaults to `true`; keep enabled unless you have a specific interoperability reason. |
| `OIDC_ALLOWED_CLOCK_SKEW` | `auth.oidc.allowed_clock_skew` | Token validation tolerance (default: `2m`). |
| `OIDC_ORG_CLAIM` | `auth.oidc.org_claim` | Optional custom organization claim name. |
| `OIDC_ROLES_CLAIM` | `auth.oidc.roles_claim` | Optional custom roles claim name. |
| `OIDC_PLATFORM_ORG_ID` | `auth.oidc.platform_org_id` | Optional platform-org UUID override. |
| `OIDC_PLATFORM_ORG_CLAIM_VALUE` | `auth.oidc.platform_org_claim_value` | Optional claim value that maps into the platform org. |
| `OIDC_GROUPS_CLAIM` | `auth.oidc.groups_claim` | Optional claim containing group memberships. |
| `OIDC_DEPARTMENTS_CLAIM` | `auth.oidc.departments_claim` | Optional claim containing department memberships. |
| `OIDC_SYNC_GROUPS_ON_LOGIN` | `auth.oidc.sync_groups_on_login` | Sync group memberships at login. |
| `OIDC_SYNC_DEPARTMENTS_ON_LOGIN` | `auth.oidc.sync_departments_on_login` | Sync department memberships at login. |
| `OIDC_FULL_SYNC_GROUPS` | `auth.oidc.full_sync_groups` | Remove memberships absent from the claim instead of additive-only sync. |
| `OIDC_FULL_SYNC_DEPARTMENTS` | `auth.oidc.full_sync_departments` | Remove department memberships absent from the claim instead of additive-only sync. |
| `STORAGE_MODE` | `storage.mode` | `multi` or `single`. Standard production default is `multi`. |
| `SERVER_REGION` | — (server metadata) | Region id: `na`, `eu`, etc. Empty = single-region |
| `SERVER_TRUSTED_PROXIES` | `server.trusted_proxies` | Comma-separated exact proxy IPs/CIDRs that are allowed to define client IP headers. |
| `CASSANDRA_HOSTS` | `database.hosts` | Default: `cassandra:9042`. Multi-region: private IPs |
| `CASSANDRA_KEYSPACE` | `database.keyspace` | |
| `CASSANDRA_CONSISTENCY` | `database.consistency` | Non-serial query consistency. Recommended default: `LOCAL_QUORUM`. |
| `CASSANDRA_SERIAL_CONSISTENCY` | `database.serial_consistency` | LWT/CAS serial-phase consistency. Recommended multiregion default: `SERIAL`. |
| `CASSANDRA_LOCAL_DC` | `database.local_dc` | |
| `CASSANDRA_USERNAME` | `database.username` | Required by the prod compose bootstrap. Use a dedicated non-superuser role. |
| `CASSANDRA_PASSWORD` | `database.password` | Required by the prod compose bootstrap. |
| `S3_BUCKET` | `storage.backends.hot.bucket` | Single-region legacy path only. A non-empty value activates the backend and requires `S3_REGION`. |
| `S3_REGION` | `storage.backends.hot.region` | Required when `S3_BUCKET` activates the single-region legacy backend. No runtime region fallback is applied. |
| `S3_ENDPOINT` | `storage.backends.hot.endpoint` | Single-region legacy path; empty = real AWS |
| `S3_SERVER_SIDE_ENCRYPTION` | `storage.backends.hot.server_side_encryption` | Single-region legacy path |
| `S3_SSE_KMS_KEY_ID` | `storage.backends.hot.sse_kms_key_id` | Single-region legacy path |
| `S3_CLASS_<CLASS>_BUCKET` | `storage.classes.<class>.bucket` | Multi-region only. Required per class. Example: `S3_CLASS_HOT_S3_NA_BUCKET`. |
| `S3_CLASS_<CLASS>_REGION` | `storage.classes.<class>.region` | Per-class region override. Every class with a non-empty bucket must have a region after YAML and env overrides. |
| `S3_CLASS_<CLASS>_ENDPOINT` | `storage.classes.<class>.endpoint` | Optional per-class endpoint override. |
| `S3_CLASS_<CLASS>_ACCESS_KEY_ID` | — | Optional per-class credential override. Falls back to `S3_ACCESS_KEY_ID`. |
| `S3_CLASS_<CLASS>_SECRET_ACCESS_KEY` | — | Optional per-class credential override. Falls back to `S3_SECRET_ACCESS_KEY`. |
| `S3_ACCESS_KEY_ID` | — | Default credential pair for any storage class without a class-specific override. |
| `S3_SECRET_ACCESS_KEY` | — | Default credential pair for any storage class without a class-specific override. |
| `CORS_ALLOWED_ORIGINS` | `cors.allowed_origins` | Comma-separated browser origins. Must include every production web domain. Wildcard `"*"` is rejected in production. |
| `ONLYOFFICE_JWT_TTL_SECONDS` | `onlyoffice.jwt_ttl_seconds` | OnlyOffice editor JWT lifetime in seconds. Default `3600` (1h). Range: 300–28800. |
| `FIRST_SUPERADMIN_EMAIL` | `auth.first_superadmin_email` | Optional bootstrap email used only on the first successful seed. |
| `ACCOUNTS_DELETE_ACCOUNT_URL` | `accounts.delete_account_url` | External Accounts account-deletion URL. |
| `ACCOUNTS_ORG_USER_MANAGEMENT_URL` | `accounts.org_user_management_url` | Optional external Accounts org-member management base URL. |
| `ACCOUNTS_DISABLE_ORG_USER_WRITES` | `accounts.disable_org_user_writes` | Defaults to `true`; keeps tenant org-admin user writes disabled. |
| `SHARE_LINK_HMAC_KEY` | `auth.share_link_hmac_key` | **Required in prod** — signs password-unlock cookies for share/upload links. Generate with `openssl rand -hex 32`. sesamefs refuses to start without it. |
| `ONLYOFFICE_ENABLED` | `onlyoffice.enabled` | |
| `ONLYOFFICE_JWT_SECRET` | `onlyoffice.jwt_secret` | Secret |
| `SERVER_URL` | — (runtime env) | Optional explicit fallback for absolute links and relay metadata. Leave unset to use the current request host. |
| `ONLYOFFICE_API_JS_URL` | `onlyoffice.api_js_url` | Public OnlyOffice JS loader URL. |
| `SEAFHTTP_SYNC_BLOCK_MAX_BYTES` | `seafhttp.sync_block_max_bytes` | Per-request body cap for the desktop-sync block PUT route. Default `16777216` (16 MiB). Must be `1`–`67108864`; **zero is rejected**, it does not mean unlimited. |
| `SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_NODE` | `seafhttp.sync_block_max_inflight_per_node` | Concurrent block uploads allowed to hold a buffered body on one process. Default `24`. Three clean-process full-lifetime trials sampled request ramp, held-body plateau and post-release drain, measuring a worst 59.5 MiB/admission raw and 74.4 MiB after the 1.25 factor; the design cost is rounded to 80 MiB. Ceiling `4096`; `0` disables the process-local cap. |
| `SEAFHTTP_SYNC_BLOCK_MEMORY_BUDGET_BYTES` | `seafhttp.sync_block_memory_budget_bytes` | Explicit memory budget assigned to admitted block PUTs. Default `2147483648` (2 GiB). Validation uses the measured 80 MiB design cost as a floor, scales it upward when `sync_block_max_bytes` exceeds 16 MiB, and refuses node-cap combinations that exceed this budget. |
| `SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_USER` | `seafhttp.sync_block_max_inflight_per_user` | Concurrent block uploads allowed for one (org, user) on one process. Fairness, not memory. Default `16`, sized just above the ~15 concurrent PUTs one official client issues (5 sync tasks x 3 block threads); all of a user's devices share it. Ceiling `4096`; `0` disables. Must not exceed the per-node cap when both are enabled. |
| `SEAFHTTP_SYNC_BLOCK_ADMISSION_WAIT` | `seafhttp.sync_block_admission_wait` | How long an over-cap block upload queues for a slot before being answered **503 + `Retry-After`** — never 429, which the desktop sync client does not treat as retryable. Default `10s`; ceiling `2m`; must stay below the client's own request timeout. `0` refuses immediately instead of waiting, which is what the bounded wait exists to avoid. |
| `SEAFHTTP_SYNC_BLOCK_MAX_WAITERS_PER_USER` | `seafhttp.sync_block_max_waiters_per_user` | Maximum parked block uploads for one (org, user). Default `16`; `0` rejects immediately when that user's active gate is full. When the per-user gate is enabled this must not exceed the node waiter cap. Queue-full responses are 503 and leave the body unread. |
| `SEAFHTTP_SYNC_BLOCK_MAX_WAITERS_PER_NODE` | `seafhttp.sync_block_max_waiters_per_node` | Maximum parked block uploads for the process-wide gate. Default `128`; `0` rejects immediately when the node gate is full. A pre-gate ticket additionally bounds active plus transitioning/parked requests before any per-user map entry is created. |
| `SEAFHTTP_SYNC_BLOCK_ADMITTED_LIFETIME` | `seafhttp.sync_block_admitted_lifetime` | Processing deadline after both gates admit a block upload, covering body read through storage. Default `5m`; must be positive and no greater than `30m`. With `server.read_timeout=0`, the effective request deadline is installed on the connection; a positive server timeout is preserved and therefore must be no greater than this lifetime. Expiry interrupts a real stalled body, cancels object-storage I/O, stops Cassandra work at callback boundaries, and returns 503 + `Retry-After`. An already-running Cassandra query may finish within the separately required finite DB timeout. |
| `SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE` | `seafhttp.upload_link_writes_per_minute` | Anonymous upload-link writes allowed per (client IP, stable public-link identity), per process. Default `600`. `0` disables. Seafhttp token remints share this budget. Behind an untrusted proxy, clients using the same link share the proxy-IP bucket; different links remain isolated. |
| `SEAFHTTP_UPLOAD_LINK_WRITE_BURST` | `seafhttp.upload_link_write_burst` | Burst for the above. Default `1200`. Must be `> 0` while the rate is non-zero. |
| `SEAFHTTP_UPLOAD_LINK_SOURCE_WRITES_PER_MINUTE` | `seafhttp.upload_link_source_writes_per_minute` | Writes allowed against one stable public-link identity across all IPs on one node. Default `12000`. `0` disables; aggregate fleet capacity scales with the number of nodes. |
| `SEAFHTTP_UPLOAD_LINK_SOURCE_WRITE_BURST` | `seafhttp.upload_link_source_write_burst` | Burst for the per-link bound. Default `24000`. Must be `> 0` while that rate is non-zero. |
| `SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE` | `seafhttp.upload_link_max_inflight_per_source` | Non-blocking concurrent anonymous-write cap per stable public-link identity on one process. Default `16`; ceiling `4096`; `0` disables. Remints share the same source count. When both in-flight caps are enabled, this value must not exceed the per-node value. |
| `SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE` | `seafhttp.upload_link_max_inflight_per_node` | Non-blocking concurrent anonymous-write cap across one process/node. Default `128`; ceiling `65536`; `0` disables. This is not cluster-global; aggregate fleet capacity scales with node count. |
| `DOWNLOAD_ADMISSION_ENABLED` | `download_admission.enabled` | Ships `true`. Disabling it removes the only aggregate bound on storage-backed downloads. |
| `DOWNLOAD_ADMISSION_CAPACITY_MODE` | `download_admission.capacity_mode` | `auto` derives node/profile capacities from the memory budget. `manual` uses the explicit capacity fields and still validates their combined design. |
| `DOWNLOAD_ADMISSION_MEMORY_BUDGET_PERCENT` | `download_admission.memory_budget_percent` | Auto-mode percentage of the **exposed cgroup limit**, and the share an explicit `memory_budget_bytes` may claim of it. Default `25`, accepted range `1`-`50`. It does **not** scale the fallback: with no limit exposed, auto mode uses the fixed 2 GiB reference budget whatever this is set to, and `memory_budget_bytes` is the way to change that. |
| `DOWNLOAD_ADMISSION_RAW_CAPACITY_PERCENT` | `download_admission.raw_capacity_percent` | Auto-mode target share of node slots assigned to the expensive raw/iWork profile. Default `33`, range `1`-`99`. The achieved share is allowed to fall to three quarters of the request so the integer split lands; a smaller share buys more stream slots, a larger one reserves more raw slots and lowers the node total. |
| `DOWNLOAD_ADMISSION_SAFETY_MARGIN_PERCENT` | `download_admission.safety_margin_percent` | Memory headroom reserved outside the modeled download work, applied identically by auto derivation and by the final validation. Default `20`, range `0`-`99`; must leave at least one raw and one stream slot. |
| `DOWNLOAD_ADMISSION_MEMORY_BUDGET_BYTES` | `download_admission.memory_budget_bytes` | Process-local configured download-memory design budget, not an OS reservation. **In auto mode `0` is the instruction to derive** — from the cgroup limit, or from the 2 GiB reference fallback when none is exposed — and is what the `sesamefs-cgroup-probe` service sets to force that path. A positive value is an explicit budget and switches derivation off; it must be `1` to `1099511627776` and must not exceed `memory_budget_percent` of an exposed cgroup limit, a share that defaults to 25% and is capped at 50%. **Manual mode has no derivation, so it requires a positive value.** |
| `DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE` | `download_admission.max_active_per_node` | Manual-mode process-local aggregate cap. Auto mode derives it from budget and costs, with an absolute 64-slot ceiling. |
| `DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER` | `download_admission.max_active_per_auth_user` | Manual-mode authenticated `(org, user)` cap. Auto mode derives a bounded fairness cap from the node ceiling. |
| `DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_LINK_SOURCE` | `download_admission.max_active_per_link_source` | Manual-mode stable public-link source cap. Auto mode derives a bounded fairness cap from the node ceiling. |
| `DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_CLIENT_LINK` | `download_admission.max_active_per_client_link` | Manual-mode stable public-link plus trusted client-IP cap. Auto mode derives a bounded fairness cap from the node ceiling. |
| `DOWNLOAD_ADMISSION_MAX_WAITERS_PER_IDENTITY` | `download_admission.max_waiters_per_identity` | Parked requests per identity. `0` refuses immediately; validation ceiling `1024`. |
| `DOWNLOAD_ADMISSION_MAX_WAITERS_PER_NODE` | `download_admission.max_waiters_per_node` | Parked requests per process. `0` refuses immediately; validation ceiling `4096`. |
| `DOWNLOAD_ADMISSION_ADMISSION_WAIT` | `download_admission.admission_wait` | Queue duration before `503 + Retry-After`. Shipped value is `2s`; maximum `5m`. |
| `DOWNLOAD_ADMISSION_PREPARATION_DEADLINE` | `download_admission.preparation_deadline` | Positive D6-selected preparation deadline; maximum `1h`. |
| `DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT` | `download_admission.idle_write_timeout` | Positive D6-selected idle response-write deadline; maximum `15m`. |
| `DOWNLOAD_ADMISSION_RETRY_AFTER` | `download_admission.retry_after` | Explicit retry hint for long-lived download slots; maximum `1h`. |
| `DOWNLOAD_ADMISSION_MAX_ACTIVE_<PROFILE>` | `download_admission.max_active_<profile>` | Fixed profiles: `BLOCK`, `FILE`, `RAW`, `HISTORY`, `LINK_RAW`, `ZIP`, `LINK_INLINE`. `0` means no additional profile cap; validation ceiling `1024`. |
| `FILEVIEW_MAX_IWORK_SOURCE_BYTES` | `fileview.max_iwork_source_bytes` | Source cap for the buffered iWork preview branch. Default `33554432` (32 MiB); must be positive and no greater than `fileview.max_preview_bytes` when download admission is enabled. Raw streams do not use this preview-only cap. |
| `METRICS_ENABLED` | `monitoring.metrics_enabled` | |
| `DESKTOP_CUSTOM_BRAND` | — (server-info response) | Brand name shown in desktop client (default: `Sesame Disk`) |
| `DESKTOP_CUSTOM_LOGO` | — (server-info response) | Full URL to logo image shown in desktop client (optional) |
| `FRONTEND_URL` | — | Internal URL of the frontend container used by share link pages to resolve hashed JS/CSS bundle names via `asset-manifest.json`. Default: `http://frontend:80` on the private deploy network. Only change if the frontend container runs on a different host/port. |
| `DEPLOY_ID` | — | **Required.** Suffix for this stack's public frontend alias on the shared `sfs-net` network: `sesamefs-frontend-$DEPLOY_ID`. Must be unique across every deploy that joins `sfs-net`. In multi-region this is typically the same value as `SERVER_REGION`; in single-region pick anything stable (e.g. `prod`). |

---

## Troubleshooting

### sesamefs exits immediately on startup

Check the logs:
```bash
docker compose -f docker-compose.prod.yml logs sesamefs
```

Common causes:
- **Cassandra not ready yet** — wait 90s and retry, or check `docker compose ps`
- **S3 connection failed** — in single-region verify `S3_BUCKET`/`S3_REGION`; in multi-region verify `S3_CLASS_*_BUCKET` vars plus `storage.classes` topology in `configs/config.prod.yaml`; in both modes verify S3 credentials in `.env`
- **Config parse error** — check `configs/config.prod.yaml` for YAML syntax errors

### `/ready` returns storage error

sesamefs can't reach S3. Check:
1. `S3_ACCESS_KEY_ID` and `S3_SECRET_ACCESS_KEY` in `.env`
2. In single-region: `S3_BUCKET` and `S3_REGION` target the correct bucket and region
3. In multi-region: `S3_CLASS_*_BUCKET` env vars are set and `configs/config.prod.yaml` defines the matching `storage.classes` regions/failover topology
4. The IAM user has `s3:HeadBucket` and `s3:*` on the configured buckets
5. Any custom `S3_ENDPOINT` or `storage.classes[*].endpoint` values are correct for your object store

### OIDC login fails

1. Verify **both** redirect URIs are registered in accounts.sesamedisk.com for every production login hostname:
  - `https://<files-hostname>/sso/`
  - `https://<files-hostname>/oauth/callback/`
2. Check `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` in `.env`
3. Check that `OIDC_REDIRECT_URIS` is not empty and exactly matches every callback URL you registered for all production domains. SesameFS now rejects OIDC login in fail-closed mode when no redirect allowlist is configured.
4. Check sesamefs logs for OIDC errors

### Browser requests fail with CORS errors

1. Check `cors.allowed_origins` in `config.yaml`
2. In production, make sure every public browser origin is listed explicitly, for example `https://files.yourdomain.com,https://files-alt.yourdomain.com`
3. If the allowlist is empty, SesameFS now fails closed instead of allowing all origins

### OnlyOffice not loading in documents

1. Verify `https://<office-hostname>/healthcheck` returns `{"status":"ok"}`
2. OnlyOffice takes ~2 minutes to start — check the logs for the external
   OnlyOffice deployment.
3. Verify `ONLYOFFICE_JWT_SECRET` in `.env` matches what sesamefs expects

### Cassandra won't start

Memory issue — reduce heap size in `.env`:
```bash
CASSANDRA_MAX_HEAP_SIZE=1G
CASSANDRA_HEAP_NEWSIZE=256M
```

Then restart:
```bash
docker compose -f docker-compose.prod.yml up -d cassandra
```

### SSL cert not found (nginx fails to start)

Run certbot before starting Docker:
```bash
sudo certbot certonly --standalone \
  -d <files-hostname> \
  -d <office-hostname>
```

Verify the cert exists:
```bash
ls /etc/letsencrypt/live/<cert-name>/
```

---

---

## Default Production Path (Multi-Region)

The same `docker-compose.prod.yml` supports multi-region. Each VPS runs the
identical stack; the only difference is the `.env` file on each server.

This is the default production model for the repo. Use single-region only when you intentionally want the legacy single-bucket path.

### Architecture

```
                         Internet (public IPs)
                 ┌──────────┼──────────┐
                 │          │          │
            ┌────▼───┐ ┌───▼────┐ ┌───▼────┐
            │ VPS-US │ │ VPS-EU │ │ VPS-AS │
            │ nginx  │ │ nginx  │ │ nginx  │
            │ sesame │ │ sesame │ │ sesame │
            │ office │ │ office │ │ office │
            │ cassan.│ │ cassan.│ │ cassan.│
            └───┬────┘ └───┬────┘ └───┬────┘
                │          │          │
            ────┴──────────┴──────────┴────  Private network (vRack / VPN)
                    10.0.x.x / wireguard
              Cassandra gossip (7000) + CQL (9042)
```

Each server is a self-contained SesameFS instance. Cassandra nodes find each
other over the private network and replicate data automatically.

### Prerequisites

- **Private network** between all VPS (OVH vRack, Hetzner vSwitch, WireGuard, etc.)
- Each VPS can reach the others on ports 7000, 7001, 9042 via private IPs
- All servers use the **same** `CASSANDRA_CLUSTER_NAME`
- DNS for each region pointing to its respective VPS public IP

### Step M1 — Prepare `.env` for each server

Start from `.env.prod.example` and uncomment the multi-region section.
Example for a 2-region setup (NA + EU):

**VPS NA** (private IP: `10.0.1.10`):
```bash
# --- Standard (same as single-region) ---
CASSANDRA_CLUSTER_NAME=sesamefs-prod
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M
# ... (S3, OIDC, OnlyOffice — same as single-region)

# --- Multi-Region ---
SERVER_REGION=na
# CASSANDRA_HOSTS not needed — SesameFS talks to local Cassandra via Docker (cassandra:9042)
CASSANDRA_CONSISTENCY=LOCAL_QUORUM
CASSANDRA_SERIAL_CONSISTENCY=SERIAL
CASSANDRA_DC=dc-na
CASSANDRA_LOCAL_DC=dc-na
CASSANDRA_RACK=rack1
CASSANDRA_SEEDS=10.0.1.10,10.0.2.20
CASSANDRA_BIND_IP=10.0.1.10
CASSANDRA_RPC_ADDRESS=0.0.0.0
CASSANDRA_BROADCAST_ADDRESS=10.0.1.10
CASSANDRA_BROADCAST_RPC_ADDRESS=10.0.1.10
```

> **Note:** Do NOT set `CASSANDRA_LISTEN_ADDRESS` — Cassandra auto-detects the
> container's internal IP. Docker port mapping routes traffic from the host's
> private IP (`CASSANDRA_BIND_IP`) to the container. `BROADCAST_ADDRESS` and
> `BROADCAST_RPC_ADDRESS` are the real private IP that other nodes use to reach
> this node. `RPC_ADDRESS=0.0.0.0` allows CQL connections from any interface
> inside the container.

**VPS EU** (private IP: `10.0.2.20`):
```bash
# --- Standard ---
CASSANDRA_CLUSTER_NAME=sesamefs-prod          # MUST match all nodes
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M

# --- Multi-Region ---
SERVER_REGION=eu
# CASSANDRA_HOSTS not needed — SesameFS talks to local Cassandra via Docker (cassandra:9042)
CASSANDRA_CONSISTENCY=LOCAL_QUORUM
CASSANDRA_SERIAL_CONSISTENCY=SERIAL
CASSANDRA_DC=dc-eu
CASSANDRA_LOCAL_DC=dc-eu
CASSANDRA_RACK=rack1
CASSANDRA_SEEDS=10.0.1.10,10.0.2.20
CASSANDRA_BIND_IP=10.0.2.20
CASSANDRA_RPC_ADDRESS=0.0.0.0
CASSANDRA_BROADCAST_ADDRESS=10.0.2.20
CASSANDRA_BROADCAST_RPC_ADDRESS=10.0.2.20
```

> **Important:** Do NOT set `CASSANDRA_HOSTS` in multi-region. SesameFS connects
> to its local Cassandra via Docker network (`cassandra:9042`). Cross-DC
> replication is handled by Cassandra itself using `CASSANDRA_SEEDS` and
> `BROADCAST_ADDRESS`.

### Step M2 — Storage config

`configs/config.prod.yaml` is now the shared multi-region structural config.
All nodes run the same file, and `SERVER_REGION` selects the node-local default
region when requests arrive through the shared public hostname.

### Current status of the region-aware library feature

What already works in the backend/frontend stack:

- new libraries can persist an explicit `storage_id`
- when no `storage_id` is provided, the backend can derive the default region from the request hostname or, for the shared global hostname, from `SERVER_REGION`
- orgs can now persist `storage_policy` with `data_residency: strict|flexible`; `default_region` is an org fallback in `flexible` mode and is required in `strict` mode
- new-library create flows honor org policy for personal libraries, group-owned libraries, org-admin group-owned libraries, and superadmin-created libraries
- later writes and reads follow the persisted library `storage_class` instead of the request host default
- focused integration tests cover create-library, raw serving, historic reads, and share-link raw serving

What the stock production deploy does **not** provide by itself yet:

- sys-admin and org-admin info pages can now edit org storage policy; direct API writes remain available for automation
- there is no built-in migration workflow for existing non-empty libraries that need to move from one storage class to another
- org policy only affects **new library creation** in this slice; it does not relocate existing libraries
- create-time placement is intentionally limited to hot classes; cold-tier primary placement remains future design work
- GC has a Cassandra LWT lease (`gc_leader` row), but the lease does not close X1. While that blocker remains open, set `GC_ENABLED=false` on every replica in every DC. After X1 closes, designated replicas in one DC may participate and the lease will select one leader; all other DCs remain disabled. Lease takeover from a crashed leader is supported via the admin endpoint without waiting for TTL. X2 is closed under the stable-topology operational contract: changing the replication DC set or RF with existing reference state requires a separately certified migration before GC can be reconsidered.

For production multi-region, treat this feature as requiring operator-provided topology plus the shared config and `.env` values below.

### Step M2.1 — Required config for region-pinned libraries

In production multi-region, `configs/config.prod.yaml` must define all of these:

- `storage.classes` — one hot class per region
- `storage.default_class` — fallback when no region context resolves
- `storage.region_classes` — region → class mapping; this is what `SERVER_REGION` looks up
- `storage.endpoint_regions` — **optional**. Only needed if a single deploy serves multiple public hostnames that map to different regions. In the standard global-domain topology with one public host per region, leave it empty (`{}`) and rely on `SERVER_REGION` set per node.

Use `configs/config.example.yaml` as the canonical structure. At minimum, you need one hot class per region, for example:

```yaml
storage:
  default_class: hot-s3-na
  classes:
    hot-s3-na:
      type: s3
      tier: hot
      bucket: sesamefs-usa
      region: us-east-1
    hot-s3-eu:
      type: s3
      tier: hot
      bucket: sesamefs-eu
      region: eu-west-1

  endpoint_regions:
    "na.files.yourdomain.com": "na"
    "eu.files.yourdomain.com": "eu"
    "*": "na"

  region_classes:
    na:
      hot: hot-s3-na
    eu:
      hot: hot-s3-eu
```

### Step M2.2 — Ingress requirement

The backend must receive the original external hostname. If you terminate TLS or proxy through nginx/traefik/LB, preserve `Host` or forward `X-Forwarded-Host` correctly.

Without that, hostname-derived default region selection on library creation will silently fall back to the global default region.

### Step M2.3 — Optional org-level residency policy

By default, organizations behave as `flexible`: new libraries prefer the request hostname region, then the org `default_region`, then the global storage default.

If an organization must pin new libraries to a specific region regardless of ingress hostname, set:

- `data_residency: strict`
- `default_region: <region-name>`

Current write path:

```bash
curl -X PUT https://admin.yourdomain.com/api/v2.1/admin/organizations/<org_id>/ \
  -H "Authorization: Token <superadmin-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "storage_policy": {
      "data_residency": "strict",
      "default_region": "na"
    }
  }'
```

Read back the effective value with:

```bash
curl -H "Authorization: Token <superadmin-token>" \
  https://admin.yourdomain.com/api/v2.1/admin/organizations/<org_id>/
```

Notes:

- `flexible` is the default when no `storage_policy` is stored
- `default_region` must map to a configured `storage.region_classes.<region>.hot`
- in `flexible`, `default_region` is only a fallback after hostname-based resolution
- in `strict`, `default_region` is required; invalid strict configs make create requests fail explicitly so operators can correct the org policy
- this slice affects only **new** libraries; existing libraries keep their persisted `storage_class`

### Step M3 — Firewall (private network)

Allow Cassandra inter-node traffic **only on the private interface**:

```bash
# Allow gossip and CQL from private network only
sudo ufw allow in on ens1 to any port 7000 proto tcp    # Gossip
sudo ufw allow in on ens1 to any port 7001 proto tcp    # Gossip TLS
sudo ufw allow in on ens1 to any port 9042 proto tcp    # CQL

# Replace "ens1" with your private network interface name
# Verify with: ip addr show
```

> Never open 7000/7001/9042 on the public interface.

### Step M3.5 — Pre-flight (multi-region)

Run [Step 5.5](#step-55--pre-flight-checks) on every node first. Then add these
multi-region-specific checks:

```bash
set -a; source .env; set +a
```

#### M3.5.1 — `SERVER_REGION` matches the local node

Every node must have a `SERVER_REGION` value that exists in
`storage.region_classes` in `configs/config.prod.yaml`:

```bash
case "$SERVER_REGION" in
  na|eu|asia) echo "  [OK]   SERVER_REGION=$SERVER_REGION" ;;
  "")         echo "  [FAIL] SERVER_REGION is empty (required when STORAGE_MODE=multi)" ;;
  *)          echo "  [FAIL] SERVER_REGION=$SERVER_REGION not in {na,eu,asia} — must match storage.region_classes" ;;
esac
```

#### M3.5.2 — Cassandra DC vars are consistent

```bash
[ "$CASSANDRA_DC" = "$CASSANDRA_LOCAL_DC" ] \
  && echo "  [OK]   CASSANDRA_DC=CASSANDRA_LOCAL_DC=$CASSANDRA_DC" \
  || echo "  [FAIL] CASSANDRA_DC ($CASSANDRA_DC) and CASSANDRA_LOCAL_DC ($CASSANDRA_LOCAL_DC) MUST match"

# Convention: dc-na ↔ na, dc-eu ↔ eu, dc-asia ↔ asia
[ "$CASSANDRA_DC" = "dc-$SERVER_REGION" ] \
  && echo "  [OK]   CASSANDRA_DC follows SERVER_REGION convention" \
  || echo "  [WARN] CASSANDRA_DC=$CASSANDRA_DC vs SERVER_REGION=$SERVER_REGION (acceptable but unconventional)"
```

#### M3.5.3 — Broadcast IPs use the private interface

```bash
# All three must be the same private IP — and it must NOT be a public address.
echo "  CASSANDRA_BIND_IP=$CASSANDRA_BIND_IP"
echo "  CASSANDRA_BROADCAST_ADDRESS=$CASSANDRA_BROADCAST_ADDRESS"
echo "  CASSANDRA_BROADCAST_RPC_ADDRESS=$CASSANDRA_BROADCAST_RPC_ADDRESS"
[ "$CASSANDRA_BIND_IP" = "$CASSANDRA_BROADCAST_ADDRESS" ] \
  && [ "$CASSANDRA_BIND_IP" = "$CASSANDRA_BROADCAST_RPC_ADDRESS" ] \
  && echo "  [OK]   broadcast vars match" \
  || echo "  [FAIL] BIND_IP / BROADCAST_ADDRESS / BROADCAST_RPC_ADDRESS must all equal the private IP"

# Sanity: the private IP must actually be configured on this host
ip -4 addr show | grep -q "inet $CASSANDRA_BIND_IP/" \
  && echo "  [OK]   $CASSANDRA_BIND_IP is configured on this host" \
  || echo "  [FAIL] $CASSANDRA_BIND_IP is NOT a local interface address"
```

#### M3.5.4 — Other DCs are reachable on the private network

```bash
# Comma-separated, includes every seed across all DCs.
for ip in $(echo "$CASSANDRA_SEEDS" | tr ',' ' '); do
  if [ "$ip" = "$CASSANDRA_BIND_IP" ]; then
    continue   # don't probe ourselves
  fi
  for port in 7000 9042; do
    if timeout 5 bash -c "</dev/tcp/$ip/$port" 2>/dev/null; then
      echo "  [OK]   $ip:$port reachable"
    else
      echo "  [FAIL] $ip:$port NOT reachable from this node"
    fi
  done
done
```

#### M3.5.5 — Per-class buckets exist for every region in `region_classes`

Every node — not just the local one — must be able to talk to every regional
bucket, because reads of foreign-region libraries are served from the
originating bucket via the local backend.

```bash
for var in S3_CLASS_HOT_S3_NA_BUCKET S3_CLASS_HOT_S3_EU_BUCKET S3_CLASS_HOT_S3_ASIA_BUCKET; do
  bucket="${!var}"
  [ -z "$bucket" ] && { echo "  [WARN] $var unset — skipping"; continue; }
  docker run --rm \
    -e AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID" \
    -e AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY" \
    amazon/aws-cli s3api head-bucket --bucket "$bucket" \
    && echo "  [OK]   $var=$bucket reachable from $SERVER_REGION" \
    || echo "  [FAIL] $var=$bucket NOT reachable from $SERVER_REGION"
done
```

If every line is `[OK]`, proceed to Step M4.

### Step M4 — Deploy order

1. **Declare Cassandra replication in `.env` before first boot** on every node:

  ```bash
  # The project default is NetworkTopologyStrategy.
  # Single-region remains compatible as a one-DC topology:
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy
  CASSANDRA_REPLICATION_DCS=dc-na:1
  CASSANDRA_CONSISTENCY=LOCAL_QUORUM
  CASSANDRA_SERIAL_CONSISTENCY=SERIAL

  # On every multi-region node, expand that topology before `docker compose up`:
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy
  CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1
  CASSANDRA_CONSISTENCY=LOCAL_QUORUM
  CASSANDRA_SERIAL_CONSISTENCY=SERIAL

  # Or for 3 regions:
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy
  CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1
  CASSANDRA_CONSISTENCY=LOCAL_QUORUM
  CASSANDRA_SERIAL_CONSISTENCY=SERIAL
  ```

  > **This is required in multi-region mode.** The bootstrap service applies
  > the declared replication policy to both the SesameFS keyspace and
  > `system_auth`, and it reapplies that policy on each explicit bootstrap run.
  > Treat `.env` as the source of truth for Cassandra replication.
  >
  > The recommended app-level consistency pair is `LOCAL_QUORUM` + `SERIAL`.
  > `LOCAL_SERIAL` lowers the serial phase to the local DC only, which is not
  > the safe default for SesameFS multiregion LWT on shared rows.
  >
  > `CASSANDRA_REPLICATION_FACTOR` is only used if you deliberately opt back
  > into `CASSANDRA_REPLICATION_CLASS=SimpleStrategy` for legacy compatibility.
  > In the default `NetworkTopologyStrategy` flow, it is ignored.
  >
  > `RF=1` per DC means each DC has exactly one copy — no in-region HA. For
  > intra-region high availability, deploy multiple Cassandra nodes per DC and
  > raise the per-DC RF (e.g. `'dc-na': 3`).

2. **Start the first seed node's Cassandra service** (e.g., VPS NA):

  ```bash
  docker compose -f docker-compose.prod.yml up -d cassandra
  # Wait for it to be healthy (~90s)
  docker compose -f docker-compose.prod.yml logs -f cassandra
  ```

3. **Start Cassandra on every additional node** (e.g., VPS EU, then Asia):

  ```bash
  docker compose -f docker-compose.prod.yml up -d cassandra
  # Wait for it to join the cluster
  ```

4. **Verify cluster status** (from any node):

  ```bash
  docker compose -f docker-compose.prod.yml exec cassandra nodetool status
  # Should show every node as UN (Up/Normal) in its respective DC
  ```

5. **Run Cassandra bootstrap once from the designated admin node only**:

  ```bash
  docker compose -f docker-compose.prod.yml --profile bootstrap up cassandra-bootstrap
  ```

  > Do **not** target `cassandra-bootstrap` on more than one node. In a
  > multi-DC cluster that would reapply auth/keyspace/replication changes from
  > multiple places and can leave Cassandra metadata inconsistent.

6. **Verify the applied replication policy** (from any node):

  ```bash
  docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "DESCRIBE KEYSPACE sesamefs"'
  docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "DESCRIBE KEYSPACE system_auth"'
  ```

7. **Run repair to sync existing data when expanding an existing cluster** (on each new node):

  ```bash
  docker compose -f docker-compose.prod.yml exec cassandra nodetool repair sesamefs
  docker compose -f docker-compose.prod.yml exec cassandra nodetool repair system_auth
  ```

  > This is required when you add a new DC after data already exists, including
  > existing auth metadata in `system_auth`. For a fresh empty cluster where
  > all DCs are bootstrapped before SesameFS starts serving traffic, this step
  > can be skipped.

8. **Start SesameFS + frontend** on all nodes:

  ```bash
  docker compose -f docker-compose.prod.yml up -d sesamefs frontend
  ```

### Step M5 — Verify

In addition to [Step 8 — Verify](#step-8--verify) on every node, run these
multi-region cluster checks:

#### M5.1 — Per-region API health

```bash
for url in https://na.files.yourdomain.com \
           https://eu.files.yourdomain.com \
           https://asia.files.yourdomain.com; do
  echo "── $url"
  curl -fsS "$url/ping"   && echo
  curl -fsS "$url/api2/ping" && echo
done

# Readiness stays internal-only; run it from each node instead of over the
# public internet:
docker compose -f docker-compose.prod.yml exec sesamefs \
  wget -qO- http://127.0.0.1:8080/ready
```

#### M5.2 — Cassandra cluster shape

```bash
docker compose -f docker-compose.prod.yml exec cassandra nodetool status
# Every node must appear as UN (Up/Normal) under its expected DC label
# (dc-na / dc-eu / dc-asia). Any DN (Down/Normal) or UJ (Up/Joining) means
# do NOT proceed with traffic until it resolves.
```

#### M5.3 — Replication is actually multi-DC

A frequent silent failure: the cluster joined fine but the keyspace replication
does not list every expected DC because the `.env` topology was incomplete or
used the wrong DC names. Verify directly:

```bash
docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "SELECT keyspace_name, replication FROM system_schema.keyspaces WHERE keyspace_name='sesamefs';"'
# Expected: replication includes 'class': 'NetworkTopologyStrategy' AND
# every DC name listed in nodetool status appears with an RF >= 1.
```

#### M5.4 — Cross-region write/read works

Pick any node and write a sentinel row, then read it from a different DC at
LOCAL_QUORUM. If replication is healthy the read succeeds without falling
back to remote DCs.

```bash
# 1. Create a one-off sentinel table (idempotent, safe to run repeatedly):
docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "
  CREATE TABLE IF NOT EXISTS sesamefs.deploy_sentinel (
    id uuid PRIMARY KEY,
    region text,
    written_at timestamp
  );
"'

# 2. On the NA node — insert a sentinel row with 1h TTL so it self-cleans:
docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "
  INSERT INTO sesamefs.deploy_sentinel (id, region, written_at)
  VALUES (uuid(), 'na', toTimestamp(now())) USING TTL 3600;
"'

# 3. On the EU node — read at LOCAL_QUORUM:
docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "
  CONSISTENCY LOCAL_QUORUM;
  SELECT region, written_at FROM sesamefs.deploy_sentinel
  WHERE region = 'na' ALLOW FILTERING;
"'
# Expected: the row appears within seconds. If empty, replication is not
# flowing — re-check Step M4.5 (bootstrap replication step) and Step M4.7 (nodetool repair).
```

> The sentinel table is purely diagnostic; you can `DROP TABLE
> sesamefs.deploy_sentinel;` after the cutover or just let the TTL'd rows
> age out and leave it in place.

#### M5.5 — GC remains disabled while X1 is open

Current production verification is that every replica in every DC reports
`GC_ENABLED=false`; do not use the lease as permission to enable destructive GC.
After X1 closes, if multiple designated replicas in one DC participate,
confirm that the lease selects only one active leader:

```bash
docker compose -f docker-compose.prod.yml exec cassandra sh -lc 'cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" -e "SELECT role, instance_id, heartbeat, ttl(instance_id) FROM sesamefs.gc_leases WHERE role='gc';"'
# Before X1 closes: no active GC leader is expected.
# After X1 closes and one-DC activation: exactly one row, with a positive TTL
# and a recent heartbeat (< 90s old).
# instance_id format is <hostname>-<pid>-<unix_nanos>. Re-run on every node —
# the answer must be identical (it's the same Cassandra row, replicated to
# every DC).
```

### Step M6 — Verify region-pinned library behavior

After deploying the multi-region config, verify the behavior that matters for data integrity.
These are post-deploy validation checks from a test-capable workspace or CI environment, not the primary rollout mechanism on the production node:

```bash
# Focused integrity checks (run from a test-capable environment)
docker compose run --build --rm -e SESAMEFS_URL=http://sesamefs:8080 \
  gotest go test -tags integration \
  -run 'TestCreateLibraryStorageSelection|TestRegionPinnedLibraryReadPaths|TestRegionPinnedHistoricReadPaths|TestRegionPinnedShareLinkRaw' \
  -count=1 -v ./internal/integration/...
```

These are the minimum checks that prove a library created in one region does not start reading from another region just because the request lands on a different hostname.

For org-level residency policy on new libraries, also run:

```bash
docker compose run --build --rm -e SESAMEFS_URL=http://sesamefs:8080 \
  go-integration-test go test -tags integration \
  -run 'TestOrgStoragePolicyStrictAcrossCreateFlows|TestOrgStoragePolicyFlexibleAcrossCreateFlows' \
  -count=1 -v ./internal/integration/...
```

These checks prove that `strict` and `flexible` policy modes are enforced consistently for user, group, org-admin, and superadmin create flows.

### Cassandra tips for multi-DC

- **Consistency:** `LOCAL_QUORUM` reads/writes stay within the local DC
  (no cross-DC latency for normal operations). Replication happens async.
- **Replication strategy:** Must be `NetworkTopologyStrategy` — see Step M4.4.
  Without it, data stays in a single DC and does NOT replicate.
- **Seeds:** Use 2 seeds per DC max. If every node is a seed, gossip degrades.
- **Adding a region:** Deploy a new VPS with its `.env`, add its IP to
  `CASSANDRA_SEEDS` on existing nodes, restart Cassandra, run `nodetool status`.

---

## Single-Region Legacy Path

Single-region is still supported with the same `docker-compose.prod.yml`, but it is no longer the primary production path for the repo.

- set `STORAGE_MODE=single`
- leave `SERVER_REGION` empty
- populate `S3_BUCKET`, `S3_REGION`, and any optional single-bucket S3 overrides
- keep the rest of the shared steps from this document the same

Everything else in the shared preparation, OIDC setup, published-image rollout, and verification flow still applies.

---

## Known limitations

### Seafile desktop client SSO ✅ Works via browser-based OAuth

The Seafile desktop client (v9+) uses browser-based SSO (`client-sso-via-local-browser`)
with a pending token + polling mechanism (compatible with seahub's `ClientSSOToken` design).
When the user clicks "Single Sign On" in the client:

1. Client calls `POST /api2/client-sso-link` → server creates a pending token T
2. Client opens the returned link (`https://your-domain/oauth/login/?sso_token=T`) in the system browser
3. User authenticates at the OIDC provider
4. Server marks T as success and redirects to `seafile://client-login/` (no token in URL)
5. Client polls `GET /api2/client-sso-link/<T>` until `status == "success"`
6. Client extracts the API token from the response and is logged in

**Requirements**: `https://your-domain/sso/` y `https://your-domain/oauth/callback/`
deben estar registradas como redirect URIs en el proveedor OIDC (ver paso 0.2).

---

### ⚠️ Seafile CLI (`seaf-cli`) does not work in OIDC-only mode

`POST /api2/auth-token/` (the endpoint `seaf-cli` uses to get a token via
username+password) **always returns 401** when `AUTH_DEV_MODE=false`.

**Affected:** `seaf-cli` and any script using username+password auth.
**Not affected:** Seafile desktop app and mobile app (both use browser SSO).

**Workaround for testing:** Keep `AUTH_DEV_MODE=true` with specific tokens in
`configs/config.prod.yaml → auth.dev_tokens`.

**Permanent fix:** See [docs/TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) for options
(Personal Access Tokens, OIDC Device Flow).

---

### Other limitations

- **OIDC ID token verification** is full RFC-compliant: cryptographic signature
  via JWKS (RS256/384/512, ES256/384/512), `kid`-based key selection with
  automatic key-rotation refresh, plus issuer, audience, nonce, expiry, and
  configurable clock-skew validation. See `internal/auth/oidc.go` (`parseIDToken`,
  `fetchJWKS`, `getSigningKey`).
- **Rate limiting** is implemented via two nginx zones: API calls (100r/s, burst 200) and file transfers
  (`/seafhttp/`, `/d/`, `/u/d/`) at a separate 20r/s zone (burst 40) to prevent large uploads/downloads
  from starving API traffic. For stricter application-layer protection add a WAF or API gateway.
- **Single Cassandra node** (single-region default) — suitable for testing
  and early production. For HA, deploy multi-region (see above).
- **No Cassandra backup** configured — set up snapshots before storing
  important data.
- **Existing-library migration is still manual** — the current feature set safely pins new libraries and preserves consistent reads/writes, but does not yet provide a production migration workflow for moving already-populated libraries between storage classes or regions.

# Deploying SesameFS to Production

This guide covers deploying SesameFS on a single VPS using Docker Compose, Nginx, and Let's Encrypt SSL.
The same `docker-compose.prod.yml` supports both single-region and multi-region deployments — the only difference is the `.env` file. See [Multi-Region Deployment](#multi-region-deployment) below.

---

## Architecture

```
Internet
   │
  ├── 443 (HTTPS) ──► Nginx (Docker) ──► frontend:80    (Desktop React SPA)
  │                                  ├──► sesamefs:8080 (API backend)
  │                                  └──► onlyoffice:80 (Document editor)
   │
   └── 80  (HTTP)  ──► Nginx (Docker) ──► 301 redirect to HTTPS

Cassandra (Docker) ← sesamefs (internal Docker network, not exposed)
AWS S3             ← sesamefs (outbound HTTPS)
accounts.sesamedisk.com ← sesamefs (OIDC, outbound HTTPS)
```

**Files involved:**

| File | Purpose |
|---|---|
| `docker-compose.prod.yml` | Production stack (no MinIO, no dev tools) |
| `config.prod.yaml` | Structural config — mounted over the baked image config |
| `nginx/nginx.conf.template` | Nginx config — `${DOMAIN}` substituted at container start |
| `.env.example` | Template for the single `.env` file you create on the server |

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

### 0.1 Create an S3 bucket

1. Create a bucket in AWS S3 (or an S3-compatible service like Cloudflare R2)
2. Create an IAM user with `s3:*` permission on that bucket
3. Save the `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`

### 0.2 Register an OIDC client

In `accounts.sesamedisk.com`, create a new application/client:

- **Grant type**: Authorization Code
- **PKCE**: Required (optional but recommended)
- **Redirect URIs** (register **both** — web login and desktop client SSO):
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

Point two DNS A records to your server's public IP:

```
files.yourdomain.com   A   <server-ip>
office.yourdomain.com  A   <server-ip>
```

Wait for DNS to propagate before running certbot.

---

## Step 1 — Install dependencies on the server

```bash
# Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
newgrp docker

# Certbot (for SSL)
sudo apt install -y certbot

# Verify
docker compose version
certbot --version
```

---

## Step 2 — Get SSL certificates

Certbot needs port 80 free. Run this **before** starting Docker:

```bash
sudo certbot certonly --standalone \
  -d files.yourdomain.com \
  -d office.yourdomain.com
```

This creates a single certificate covering both domains, stored at:
`/etc/letsencrypt/live/files.yourdomain.com/`

Both nginx server blocks reference this same cert path.

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

Fill in these values (everything else can stay as the example default):

```bash
# Domains
DOMAIN=files.yourdomain.com
OFFICE_DOMAIN=office.yourdomain.com

# S3
AWS_ACCESS_KEY_ID=<from step 0.1>
AWS_SECRET_ACCESS_KEY=<from step 0.1>
S3_BUCKET=<your-bucket-name>
S3_REGION=us-east-1
S3_ENDPOINT=          # leave empty for real AWS S3

# Cassandra
CASSANDRA_CLUSTER_NAME=sesamefs-prod
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M

# OIDC
OIDC_ENABLED=true
AUTH_DEV_MODE=false
AUTH_ALLOW_ANONYMOUS=false
OIDC_CLIENT_ID=<from step 0.2>
OIDC_CLIENT_SECRET=<from step 0.2>
OIDC_JWT_SIGNING_KEY=<from step 0.3 — first openssl output>

# Share link password cookies (REQUIRED in production)
SHARE_LINK_HMAC_KEY=<from step 0.3 — third openssl output>

# External billing portal used by /billing/
BILLING_URL=https://accounts.yourdomain.com/billing/

# OnlyOffice
ONLYOFFICE_JWT_SECRET=<from step 0.3 — second openssl output>
```

> **Note:** `docker-compose.prod.yml` automatically computes `SERVER_URL`,
> `OIDC_REDIRECT_URIS`, and `ONLYOFFICE_API_JS_URL` from `${DOMAIN}` and
> `${OFFICE_DOMAIN}`. You don't need to set those manually.

> `BILLING_URL` is different: it is the external billing portal destination. Users click SesameFS `/billing/`, and the backend redirects authenticated sessions to this external URL in a new tab.

---

## Step 5 — Configure firewall

```bash
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 80/tcp    # HTTP (nginx redirect to HTTPS)
sudo ufw allow 443/tcp   # HTTPS
sudo ufw enable
sudo ufw status
```

Cassandra (9042), OnlyOffice (8088), and sesamefs (8080) are bound to
`127.0.0.1` only — they are not reachable from the internet.

> **Multi-region:** You also need to allow Cassandra gossip and CQL on the
> private network. See [Multi-Region Firewall](#step-m3--firewall-private-network).

---

## Step 6 — Deploy

```bash
cd /opt/sesamefs

# First deploy: builds the backend image, desktop frontend image, and supporting services.
# Takes ~5–10 minutes the first time.
docker compose -f docker-compose.prod.yml up -d --build

# Watch logs during startup
docker compose -f docker-compose.prod.yml logs -f
```

Cassandra takes ~60–90 seconds to become healthy on first boot.
sesamefs waits for Cassandra before starting (health check dependency).

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

```bash
# Basic health (should return: pong)
curl https://files.yourdomain.com/ping

# Readiness — checks Cassandra and S3 connectivity
curl https://files.yourdomain.com/ready
# Expected: {"database":"ok","storage":"ok"}

# OIDC is configured
curl https://files.yourdomain.com/api/v2.1/auth/oidc/config
# Expected: {"issuer":"https://accounts.sesamedisk.com", ...}

# Auth is enforced (unauthenticated request must return 401)
curl -s -o /dev/null -w "%{http_code}" https://files.yourdomain.com/api2/repos/
# Expected: 401

# OnlyOffice is up
curl https://office.yourdomain.com/healthcheck
# Expected: {"status":"ok"}
```

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
docker compose -f docker-compose.prod.yml logs -f onlyoffice
```

### Deploy an update

```bash
cd /opt/sesamefs
git pull

# Backend-only change (API, sync protocol, business logic)
docker compose -f docker-compose.prod.yml up -d --build sesamefs

# Frontend-only change (React UI, styles, components)
docker compose -f docker-compose.prod.yml up -d --build frontend

# Both changed (or when unsure)
docker compose -f docker-compose.prod.yml up -d --build sesamefs frontend
```

### Restart a service

```bash
docker compose -f docker-compose.prod.yml restart sesamefs
```

### Stop everything

```bash
docker compose -f docker-compose.prod.yml down
# Add --volumes to also wipe Cassandra data (destructive!)
```

### Check resource usage

```bash
docker stats
```

---

## Configuration reference

### `config.prod.yaml`

Contains structural settings with no secrets. Mounted over the config
baked into the Docker image. Edit and restart sesamefs to apply changes.

Settings that **cannot** be set via env vars and must be in this file:
- `onlyoffice.server_url` — internal Docker URL for OnlyOffice → sesamefs
- `onlyoffice.internal_url` — internal Docker URL for sesamefs → OnlyOffice
- `onlyoffice.view_extensions` / `edit_extensions`
- `cors.allowed_origins` — required in production. Set it explicitly, for example `["https://files.yourdomain.com"]`. SesameFS now fails closed when the list is empty.

### All env var overrides

| Env var | Config field | Notes |
|---|---|---|
| `AUTH_DEV_MODE` | `auth.dev_mode` | Set `false` in prod |
| `AUTH_ALLOW_ANONYMOUS` | `auth.allow_anonymous` | Set `false` in prod |
| `OIDC_ENABLED` | `auth.oidc.enabled` | Set `true` in prod |
| `OIDC_ISSUER` | `auth.oidc.issuer` | Default in config.prod.yaml |
| `OIDC_CLIENT_ID` | `auth.oidc.client_id` | Secret |
| `OIDC_CLIENT_SECRET` | `auth.oidc.client_secret` | Secret |
| `OIDC_REDIRECT_URIS` | `auth.oidc.redirect_uris` | Required when OIDC is enabled. Computed by compose in the standard production setup. |
| `OIDC_JWT_SIGNING_KEY` | `auth.oidc.jwt_signing_key` | Secret. When set, sessions are signed JWTs instead of opaque tokens. **NEVER change after deploy** — all active sessions are immediately invalidated. Revoked JWTs (logout/user deactivation) are verified against the DB so revocation is effective even in JWT mode. |
| `OIDC_DEFAULT_ROLE` | `auth.oidc.default_role` | |
| `OIDC_AUTO_PROVISION` | `auth.oidc.auto_provision` | |
| `OIDC_SESSION_TTL` | `auth.oidc.session_ttl` | Web browser sessions (default: 24h) |
| `OIDC_API_TOKEN_TTL` | `auth.oidc.api_token_ttl` | Desktop/mobile client tokens (default: 180 days) |
| `SERVER_REGION` | — (server metadata) | Region id: `usa`, `eu`, etc. Empty = single-region |
| `CASSANDRA_HOSTS` | `database.hosts` | Default: `cassandra:9042`. Multi-region: private IPs |
| `CASSANDRA_KEYSPACE` | `database.keyspace` | |
| `CASSANDRA_LOCAL_DC` | `database.local_dc` | |
| `CASSANDRA_USERNAME` | `database.username` | Optional |
| `CASSANDRA_PASSWORD` | `database.password` | Optional |
| `S3_BUCKET` | `storage.backends.hot.bucket` | |
| `S3_REGION` | `storage.backends.hot.region` | |
| `S3_ENDPOINT` | `storage.backends.hot.endpoint` | Empty = real AWS |
| `AWS_ACCESS_KEY_ID` | (AWS SDK) | Auto-picked by SDK |
| `AWS_SECRET_ACCESS_KEY` | (AWS SDK) | Auto-picked by SDK |
| `SHARE_LINK_HMAC_KEY` | `auth.share_link_hmac_key` | **Required in prod** — signs password-unlock cookies for share/upload links. Generate with `openssl rand -hex 32`. sesamefs refuses to start without it. |
| `ONLYOFFICE_ENABLED` | `onlyoffice.enabled` | |
| `ONLYOFFICE_JWT_SECRET` | `onlyoffice.jwt_secret` | Secret |
| `ONLYOFFICE_API_JS_URL` | `onlyoffice.api_js_url` | Computed by compose |
| `METRICS_ENABLED` | `monitoring.metrics_enabled` | |
| `DESKTOP_CUSTOM_BRAND` | — (server-info response) | Brand name shown in desktop client (default: `Sesame Disk`) |
| `DESKTOP_CUSTOM_LOGO` | — (server-info response) | Full URL to logo image shown in desktop client (optional) |
| `FRONTEND_URL` | — | Internal URL of the frontend container used by share link pages to resolve hashed JS/CSS bundle names via `asset-manifest.json`. Default: `http://frontend:80`. Only change if the frontend container runs on a different host/port. |

---

## Troubleshooting

### sesamefs exits immediately on startup

Check the logs:
```bash
docker compose -f docker-compose.prod.yml logs sesamefs
```

Common causes:
- **Cassandra not ready yet** — wait 90s and retry, or check `docker compose ps`
- **S3 connection failed** — verify bucket name, region, and credentials in `.env`
- **Config parse error** — check `config.prod.yaml` for YAML syntax errors

### `/ready` returns storage error

sesamefs can't reach S3. Check:
1. `S3_BUCKET`, `S3_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` in `.env`
2. The bucket exists in the specified region
3. The IAM user has `s3:HeadBucket` and `s3:*` on the bucket
4. `S3_ENDPOINT` is empty for real AWS (not set to a MinIO URL)

### OIDC login fails

1. Verify **both** redirect URIs are registered in accounts.sesamedisk.com:
   - `https://files.yourdomain.com/sso/`
   - `https://files.yourdomain.com/oauth/callback/`
2. Check `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` in `.env`
3. Check that `OIDC_REDIRECT_URIS` is not empty and exactly matches the callback URLs you registered. SesameFS now rejects OIDC login in fail-closed mode when no redirect allowlist is configured.
4. Check sesamefs logs for OIDC errors

### Browser requests fail with CORS errors

1. Check `cors.allowed_origins` in `config.yaml`
2. In production, make sure the browser origin is listed explicitly, for example `https://files.yourdomain.com`
3. If the allowlist is empty, SesameFS now fails closed instead of allowing all origins

### OnlyOffice not loading in documents

1. Verify `https://office.yourdomain.com/healthcheck` returns `{"status":"ok"}`
2. OnlyOffice takes ~2 minutes to start — check logs:
   ```bash
   docker compose -f docker-compose.prod.yml logs -f onlyoffice
   ```
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
  -d files.yourdomain.com \
  -d office.yourdomain.com
```

Verify the cert exists:
```bash
ls /etc/letsencrypt/live/files.yourdomain.com/
```

---

---

## Multi-Region Deployment

The same `docker-compose.prod.yml` supports multi-region. Each VPS runs the
identical stack; the only difference is the `.env` file on each server.

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
Example for a 2-region setup (USA + EU):

**VPS USA** (private IP: `10.0.1.10`):
```bash
# --- Standard (same as single-region) ---
DOMAIN=us.files.sesamedisk.com
OFFICE_DOMAIN=us.office.sesamedisk.com
CASSANDRA_CLUSTER_NAME=sesamefs-prod
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M
# ... (S3, OIDC, OnlyOffice — same as single-region)

# --- Multi-Region ---
SERVER_REGION=usa
# CASSANDRA_HOSTS not needed — SesameFS talks to local Cassandra via Docker (cassandra:9042)
CASSANDRA_DC=dc-usa
CASSANDRA_LOCAL_DC=dc-usa
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
DOMAIN=eu.files.sesamedisk.com
OFFICE_DOMAIN=eu.office.sesamedisk.com
CASSANDRA_CLUSTER_NAME=sesamefs-prod          # MUST match all nodes
CASSANDRA_MAX_HEAP_SIZE=2G
CASSANDRA_HEAP_NEWSIZE=400M

# --- Multi-Region ---
SERVER_REGION=eu
# CASSANDRA_HOSTS not needed — SesameFS talks to local Cassandra via Docker (cassandra:9042)
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

The default `config.prod.yaml` uses single-region storage (`backends:`).
For multi-region you need the `classes:` + `region_classes:` format.

Create a `config.prod.yaml` per region (or a single one that uses
`SERVER_REGION` to resolve the right storage class). See
`configs/config-usa.yaml` and `configs/config-eu.yaml` for the structure —
replace MinIO endpoints with real S3 buckets.

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

### Step M4 — Deploy order

1. **Start the first seed node** (e.g., VPS USA):
   ```bash
   docker compose -f docker-compose.prod.yml up -d cassandra
   # Wait for it to be healthy (~90s)
   docker compose -f docker-compose.prod.yml logs -f cassandra
   ```

2. **Start the second node** (e.g., VPS EU):
   ```bash
   docker compose -f docker-compose.prod.yml up -d cassandra
   # Wait for it to join the cluster
   ```

3. **Verify cluster status** (from any node):
   ```bash
   docker compose -f docker-compose.prod.yml exec cassandra nodetool status
   # Should show both nodes as UN (Up/Normal) in their respective DCs
   ```

4. **Switch the keyspace to NetworkTopologyStrategy** (from any node):
   ```bash
   docker compose -f docker-compose.prod.yml exec cassandra cqlsh -e "
     ALTER KEYSPACE sesamefs
     WITH replication = {
       'class': 'NetworkTopologyStrategy',
       'dc-usa': 1,
       'dc-eu': 1
     };
   "
   ```
   > **This is required.** By default, the keyspace uses `SimpleStrategy`
   > (single DC). Without this change, data will NOT replicate across DCs.
   > Only list DCs that actually exist. When adding a new region later,
   > run `ALTER KEYSPACE` again adding the new DC.

5. **Run repair to sync existing data** (on each node):
   ```bash
   docker compose -f docker-compose.prod.yml exec cassandra nodetool repair sesamefs
   ```
   > This forces Cassandra to replicate all existing data to the new DC.
   > Skip this only if both nodes started with empty data.

6. **Start SesameFS + OnlyOffice** on all nodes:
   ```bash
   docker compose -f docker-compose.prod.yml up -d
   ```

### Step M5 — Verify

```bash
# From VPS USA
curl https://us.files.sesamedisk.com/ping       # pong
curl https://us.files.sesamedisk.com/ready       # {"database":"ok","storage":"ok"}

# From VPS EU
curl https://eu.files.sesamedisk.com/ping        # pong
curl https://eu.files.sesamedisk.com/ready       # {"database":"ok","storage":"ok"}

# Cassandra cluster health (from any node)
docker compose -f docker-compose.prod.yml exec cassandra nodetool status
```

### Cassandra tips for multi-DC

- **Consistency:** `LOCAL_QUORUM` reads/writes stay within the local DC
  (no cross-DC latency for normal operations). Replication happens async.
- **Replication strategy:** Must be `NetworkTopologyStrategy` — see Step M4.4.
  Without it, data stays in a single DC and does NOT replicate.
- **Seeds:** Use 2 seeds per DC max. If every node is a seed, gossip degrades.
- **Adding a region:** Deploy a new VPS with its `.env`, add its IP to
  `CASSANDRA_SEEDS` on existing nodes, restart Cassandra, run `nodetool status`.

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
`config.prod.yaml → auth.dev_tokens`.

**Permanent fix:** See [docs/TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) for options
(Personal Access Tokens, OIDC Device Flow).

---

### Other limitations

- **OIDC JWT signature verification** is incomplete — the app validates issuer,
  nonce, and expiry but not the cryptographic signature of the ID token.
  Risk is low in authorization code flow (tokens come server-to-server),
  but this should be patched before high-security deployments.
- **Rate limiting** is implemented via two nginx zones: API calls (100r/s, burst 200) and file transfers
  (`/seafhttp/`, `/d/`, `/u/d/`) at a separate 20r/s zone (burst 40) to prevent large uploads/downloads
  from starving API traffic. For stricter application-layer protection add a WAF or API gateway.
- **Single Cassandra node** (single-region default) — suitable for testing
  and early production. For HA, deploy multi-region (see above).
- **No Cassandra backup** configured — set up snapshots before storing
  important data.

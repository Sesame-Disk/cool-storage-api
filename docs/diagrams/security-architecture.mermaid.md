# SesameFS Security Architecture — Summary

> All diagrams use Mermaid syntax. Render on GitHub, VS Code, or [mermaid.live](https://mermaid.live).
> This page is the quick-reference. For detailed diagrams see the individual pages linked in the [diagram index](#diagram-index) below.

### How to read colors (all diagrams)

| Color | Meaning |
|-------|---------|
| **Red** | Critical vulnerability — must fix before production |
| **Orange** | Security gap — should fix soon |
| **Yellow** | Medium concern — defense-in-depth needed |
| **Green** | Security control working correctly |
| **Blue** | Encryption / cryptographic module |
| **Gray** | Infrastructure (no finding) |

---

## Diagram Index

| Diagram | What it shows | Link |
|---------|--------------|------|
| Architecture Overview | High-level system components and data flow | [architecture-overview.md](./architecture-overview.md) |
| Security Layer | Auth decision flow, OIDC login sequence, header status | [security-layer.md](./security-layer.md) |
| Authentication Layer | Token lifecycle, session invalidation, role mapping, rate limits | [auth-layer.md](./auth-layer.md) |
| Storage & Encryption | Upload/download pipelines, key management, compromise impact | [storage-layer.md](./storage-layer.md) |
| Full Architecture | Complete system map with every finding annotated | [full-architecture.md](./full-architecture.md) |

---

## 1. System Overview

**Key issues:** OnlyOffice callback (red) has zero auth. S3 (orange) has no server-side encryption. Share links (yellow) serve SVG inline.

```mermaid
flowchart TD
    Client["Clients"] -->|HTTPS| Nginx["Nginx<br/>TLS + headers"]
    Nginx -->|Static| FE["React :3000"]
    Nginx -->|API| Auth["Auth MW"]
    Nginx -->|/onlyoffice| OO["OnlyOffice CB<br/>NO AUTH"]

    Auth --> API["REST API v2"]
    Auth --> Sync["Seafile Sync"]
    Auth -.-> OIDC["OIDC Client"]
    Auth -.-> Sess["Session Store"]

    API --> BS["Block Store"]
    Sync --> BS
    Sync --> Crypto["AES-256-CBC"]
    BS -->|"No SSE"| S3[("S3")]
    API --> Cass[("Cassandra")]
    OIDC --> IdP["OIDC Provider"]
    OO -->|"SSRF"| OOSrv["OnlyOffice :8088"]

    style OO fill:#dc3545,color:#fff
    style S3 fill:#e67700,color:#fff
```

---

## 2. Security Controls Status

**How to read:** Green boxes are findings fixed since the v1 assessment. Red boxes are critical issues still open. Yellow boxes are medium issues still open.

```mermaid
flowchart LR
    subgraph Fixed["Fixed Since v1"]
        F1["JWT v5.3.1"]
        F2["Audience validation"]
        F3["Role escalation blocked"]
        F4["Cookie constant-time"]
        F5["State flood capped"]
        F6["3 security headers"]
    end

    subgraph Open["Still Open"]
        O1["OnlyOffice: no auth"]
        O2["OnlyOffice: SSRF"]
        O3["SVG served inline"]
        O4["CORS wildcard in prod"]
        O5["No CSP header"]
        O6["ZIP no size limits"]
        O7["Share-link oracle"]
        O8["/metrics at app level"]
    end

    style F1 fill:#28a745,color:#fff
    style F2 fill:#28a745,color:#fff
    style F3 fill:#28a745,color:#fff
    style F4 fill:#28a745,color:#fff
    style F5 fill:#28a745,color:#fff
    style F6 fill:#28a745,color:#fff
    style O1 fill:#dc3545,color:#fff
    style O2 fill:#dc3545,color:#fff
    style O3 fill:#dc3545,color:#fff
    style O4 fill:#ffc107,color:#000
    style O5 fill:#ffc107,color:#000
    style O6 fill:#ffc107,color:#000
    style O7 fill:#ffc107,color:#000
    style O8 fill:#ffc107,color:#000
```

---

## 3. Encryption Architecture

**Key issue:** The Seafile-compatible path (yellow) uses PBKDF2 with 1,000 iterations. The web/API path (green) uses Argon2id, which is strong.

```mermaid
flowchart TD
    PW["Password"] -->|Web| Argon["Argon2id<br/>64MB, 3 iter"]
    PW -->|Seafile| PBKDF["PBKDF2<br/>1000 iter"]

    Argon --> Verify1["Verify magic"]
    PBKDF --> Verify2["Verify magic"]
    Verify1 --> Decrypt["Decrypt random_key"]
    Verify2 --> Decrypt
    Decrypt --> FK["File Key 256-bit"]
    FK --> EncBlock["AES-256-CBC<br/>per block"]
    EncBlock --> S3["S3 storage"]

    style PBKDF fill:#ffc107,color:#000
    style Argon fill:#28a745,color:#fff
```

---

## 4. Attack Surface

**How to read:** These are all endpoints reachable without any credentials. Arrows show the risk each one carries.

```mermaid
flowchart TD
    subgraph Unauth["No Auth Needed"]
        A1["/onlyoffice/editor-callback"]
        A2["/d/:token share viewer"]
        A3["/ping /health /ready"]
        A4["/bootstrap /server-info"]
        A5["/auth/oidc/config"]
        A6["/metrics"]
        A7["DELETE /auth/session"]
    end

    subgraph Risk["Risk Level"]
        A1 -->|"SSRF + file write"| Critical["CRITICAL"]
        A2 -->|"Inline SVG XSS"| Critical
        A5 -->|"Leaks client_id"| Low["LOW"]
        A6 -->|"Route map"| Medium["MEDIUM"]
        A7 -->|"CSRF logout"| Medium
    end

    style Critical fill:#dc3545,color:#fff
    style Medium fill:#ffc107,color:#000
    style Low fill:#6c757d,color:#fff
```

---

## 5. Local vs Production Differences

| Check | Local :8082 | Prod sfs.nihaoshares.com |
|-------|-------------|--------------------------|
| OnlyOffice CB | HTTP 200, no auth | HTTP 200, no auth |
| CORS | `*` (dev mode) | `*` (config.prod.yaml) |
| /metrics | HTTP 200 exposed | HTTP 403 (nginx) |
| /ready | JSON with db/storage | Caught by nginx |
| OIDC config | `enabled: false` | Leaks client_id, issuer |
| Security headers | 3/5 from app | 5/7 (nginx adds 2) |
| Auth rate limit | 10/120 through | 26/120 through |
| Share-link oracle | 404 confirmed | 404 confirmed |
| CSRF logout | HTTP 200 | HTTP 200 |

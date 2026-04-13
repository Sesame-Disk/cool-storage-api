# SesameFS Full Architecture

> How to read: This is the complete system map. Every component, connection, and security finding is shown. Red nodes are critical vulnerabilities, orange are gaps, yellow are medium concerns, green are working controls, blue are encryption modules.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Red** | Critical vulnerability — must fix before production |
| **Orange** | Security gap — should fix soon |
| **Yellow** | Medium concern — defense-in-depth needed |
| **Green** | Security control working correctly |
| **Blue** | Encryption / cryptographic module |
| **Gray** | Infrastructure (no finding) |

---

## System Diagram

**Key issues visible:**
- The OnlyOffice callback (red, thick border) has zero authentication and triggers SSRF.
- S3 (orange) has no server-side encryption on Put calls.
- CORS (yellow) is configured as wildcard `*` even in production.
- ZIP download (yellow) has no file count or size limits.
- /metrics (yellow) is exposed unauthenticated at the app level.

```mermaid
flowchart TD
    Client["Clients<br/>Browser / Seafile / API"]
    Client -->|HTTPS| Nginx["Nginx<br/>TLS, X-Frame-Options,<br/>HSTS preload, /metrics=403"]

    Nginx -->|"/"| FE["React Frontend :3000"]
    Nginx -->|"/api /seafhttp"| MW
    Nginx -->|"/onlyoffice"| OOCb

    subgraph Go["SesameFS Go API :8080"]
        MW["Middleware Stack"]
        MW --> SecH["Security Headers<br/>nosniff, HSTS, Referrer"]
        SecH --> CORS["CORS<br/>origins=* in prod"]
        CORS --> RL["Rate Limiter<br/>per-IP bucket"]
        RL --> Auth["Auth Middleware"]

        Auth --> AuthRoutes
        subgraph AuthRoutes["Auth Required"]
            Lib["Libraries"]
            Files["Files"]
            Shares["Share Mgmt"]
            Admin["Admin"]
            Search["Search"]
            Blocks["Block API"]
        end

        subgraph NoAuth["No Auth"]
            OOCb["OnlyOffice CB<br/>ZERO AUTH"]
            SView["Share Viewer<br/>inline SVG"]
            Info["/ping /health<br/>/bootstrap"]
            OIDCcfg["/oidc/config<br/>leaks client_id"]
            Met["/metrics"]
            Logout["DELETE /session<br/>no CSRF"]
        end

        subgraph OIDC["OIDC"]
            OClient["Client<br/>PKCE, aud check"]
            States["State store<br/>TTL 10m, cap 10k"]
            Roles["Role mapper<br/>superadmin blocked"]
        end

        subgraph Sess["Sessions"]
            Cache["Memory cache"]
            SessDB["Cassandra store<br/>SHA-256 tokens"]
        end

        subgraph Store["Storage"]
            CDC["FastCDC<br/>2-256MB"]
            BS["Block Store<br/>SHA-256 keys"]
            Zip["ZIP download<br/>NO limits"]
        end

        subgraph Enc["Encryption"]
            A2["Argon2id"]
            PB["PBKDF2 1000i"]
            AES["AES-256-CBC"]
        end

        GC["GC<br/>grace period"]
    end

    Auth -.-> Cache
    Auth -.-> OClient
    OClient -->|"code exchange"| IdP["OIDC Provider"]
    Cache -.-> SessDB

    Files --> BS
    Blocks --> BS
    SView --> BS
    BS --> CDC
    BS -->|"No SSE"| S3[("S3 / MinIO")]

    OOCb -->|"SSRF"| OOSrv["OnlyOffice :8088"]

    Lib --> Cass[("Cassandra 5.0")]
    Files --> Cass
    Shares --> Cass
    Admin --> Cass
    SessDB -.-> Cass
    GC --> S3
    GC --> Cass

    style OOCb fill:#dc3545,color:#fff,stroke-width:3px
    style S3 fill:#e67700,color:#fff
    style SView fill:#ffc107,color:#000
    style CORS fill:#ffc107,color:#000
    style Zip fill:#ffc107,color:#000
    style Logout fill:#ffc107,color:#000
    style Met fill:#ffc107,color:#000
    style PB fill:#ffc107,color:#000
    style SecH fill:#28a745,color:#fff
    style Auth fill:#28a745,color:#fff
    style OClient fill:#28a745,color:#fff
    style Roles fill:#28a745,color:#fff
    style A2 fill:#17a2b8,color:#fff
    style AES fill:#17a2b8,color:#fff
    style Nginx fill:#6c757d,color:#fff
```

---

## OnlyOffice Attack Chain

**How to read:** Follow the arrows to see what an unauthenticated attacker can do. Red nodes are steps the attacker controls. The only gate is whether a valid `doc_key` exists in Cassandra — if it does, the full SSRF + file write chain executes.

```mermaid
flowchart TD
    A["Attacker<br/>no credentials"]
    A -->|POST| EP["/onlyoffice/editor-callback<br/>NO auth, NO JWT check"]
    EP --> Parse["Parse JSON body"]
    Parse --> Key["Lookup doc_key<br/>in Cassandra"]
    Key -->|Not found| Safe["Exit early<br/>no damage"]
    Key -->|Found| User["userID = req.Users[0]<br/>attacker controlled"]
    User --> Perm["Permission check<br/>uses attacker's userID"]
    Perm --> Fetch["http.Get(req.url)<br/>no IP filter<br/>no size limit<br/>no redirect limit"]
    Fetch --> Write["Write fetched bytes<br/>to library file"]

    style EP fill:#dc3545,color:#fff,stroke-width:3px
    style Fetch fill:#dc3545,color:#fff
    style Write fill:#dc3545,color:#fff
```

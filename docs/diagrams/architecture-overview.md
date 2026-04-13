# SesameFS Architecture Overview

```mermaid
flowchart TB
    subgraph External["External"]
        Internet["fa:fa-globe Internet / Clients<br/>Browsers, Seafile Desktop/Mobile, API"]
        IdP["fa:fa-key OIDC Identity Provider<br/>accounts.sesamedisk.com"]
    end

    subgraph Edge["Edge Layer"]
        Nginx["fa:fa-shield-alt Nginx Reverse Proxy<br/>TLS termination, rate limiting<br/>Security headers: X-Frame-Options, X-Robots-Tag<br/>/metrics blocked (403)"]
        Frontend["fa:fa-desktop React Frontend :3000<br/>React 17 SPA, Bootstrap 4.6<br/>Served via nginx:alpine"]
    end

    subgraph SesameFS["SesameFS API Server (Go 1.25, Gin) :8080"]
        direction TB

        subgraph Middleware["Middleware Chain"]
            SecurityHeaders["Security Headers MW<br/>HSTS, nosniff, Referrer-Policy<br/>CSP: MISSING"]
            RateLimiter["Rate Limiter<br/>Per-IP token bucket<br/>Auth endpoints only"]
            AuthMW["Auth Middleware<br/>Token extraction (Header/Cookie)<br/>Session, API Key, Repo Token"]
        end

        subgraph Auth["Authentication & Sessions"]
            OIDC["OIDC Client<br/>Audience validation (FIXED)<br/>Role mapping (superadmin blocked)<br/>State TTL + cap (FIXED)<br/>PKCE support"]
            Sessions["Session Store<br/>In-memory cache + Cassandra<br/>SHA-256 hashed tokens<br/>Node-local invalidation"]
        end

        subgraph ProtectedRoutes["Protected Routes (auth required)"]
            APIv2["API v2 Handlers<br/>Libraries, Files, Shares<br/>Admin, Search, Tags<br/>Groups, Departments"]
            SeafHTTP["Seafile HTTP Protocol<br/>Chunked upload/download<br/>Sync protocol<br/>ZIP dir download (NO size limits)"]
        end

        subgraph PublicRoutes["Public Routes (NO auth)"]
            ShareLink["Share Link Handler<br/>Content-Disposition: inline (XSS risk)<br/>Constant-time cookie (FIXED)<br/>Token enumeration oracle (OPEN)"]
            OnlyOffice["OnlyOffice Handler<br/>editor-callback: ZERO AUTH<br/>No JWT verification<br/>SSRF via http.Get(url)"]
            InfoEndpoints["Info Endpoints<br/>/ping, /health, /ready<br/>/bootstrap, /server-info<br/>/auth/oidc/config"]
            Metrics["Prometheus /metrics<br/>Unauth at app level<br/>Blocked by nginx in prod"]
        end

        subgraph Crypto["Encryption"]
            CryptoMod["Dual-Mode Encryption<br/>Strong: Argon2id (3 iter, 64MB)<br/>Compat: PBKDF2 (1000 iter)<br/>AES-256-CBC per block"]
        end

        subgraph StorageLogic["Storage Logic"]
            Chunker["FastCDC Chunker<br/>Adaptive 2-256MB<br/>SHA-256 block IDs"]
            BlockStore["Block Store<br/>Content-addressed (SHA-256)<br/>Two-level sharding<br/>Deduplication"]
            GC["Garbage Collector<br/>Grace period deletion<br/>Queue-based, persistent"]
        end
    end

    subgraph Infrastructure["Infrastructure"]
        OOServer["fa:fa-file-word OnlyOffice Server :8088<br/>Document editing<br/>JWT-signed callbacks"]
    end

    subgraph DataLayer["Data Layer"]
        S3[("fa:fa-database S3 / MinIO<br/>File block storage<br/>Multi-region buckets<br/>NO ServerSideEncryption")]
        Cassandra[("fa:fa-database Cassandra 5.0<br/>Users, sessions, libraries<br/>fs_objects, permissions<br/>Parameterized queries")]
    end

    Internet -->|"HTTPS"| Nginx
    Nginx -->|"Static assets /"| Frontend
    Nginx -->|"/api*, /seafhttp"| SecurityHeaders
    Nginx -->|"/onlyoffice"| OnlyOffice

    SecurityHeaders --> RateLimiter
    RateLimiter --> AuthMW
    AuthMW -->|"Session lookup"| Sessions
    AuthMW -->|"OIDC validation"| OIDC
    AuthMW --> ProtectedRoutes

    OIDC -->|"Code exchange"| IdP
    Sessions -->|"Persistence"| Cassandra

    APIv2 -->|"Parameterized CQL"| Cassandra
    APIv2 --> BlockStore
    SeafHTTP --> BlockStore
    SeafHTTP -->|"Encrypt/Decrypt"| CryptoMod

    ShareLink --> BlockStore
    OnlyOffice -->|"SSRF: http.Get(url)"| OOServer

    BlockStore --> Chunker
    BlockStore -->|"Put/Get (no SSE)"| S3

    GC -->|"Delete orphans"| S3
    GC -->|"Ref count queries"| Cassandra

    style OnlyOffice fill:#dc3545,color:#fff,stroke:#a71d2a,stroke-width:3px
    style S3 fill:#e67700,color:#fff,stroke:#cc6600,stroke-width:2px
    style ShareLink fill:#ffc107,color:#000,stroke:#d4a106,stroke-width:2px
    style Metrics fill:#ffc107,color:#000,stroke:#d4a106,stroke-width:2px
    style SecurityHeaders fill:#28a745,color:#fff,stroke:#1e7e34
    style AuthMW fill:#28a745,color:#fff,stroke:#1e7e34
    style OIDC fill:#28a745,color:#fff,stroke:#1e7e34
    style Sessions fill:#28a745,color:#fff,stroke:#1e7e34
    style CryptoMod fill:#17a2b8,color:#fff,stroke:#138496
```

### Legend

| Color | Meaning |
|-------|---------|
| Red | Critical vulnerability (must fix) |
| Orange | Security gap (should fix) |
| Yellow | Medium concern |
| Green | Security control working correctly |
| Blue | Encryption / crypto module |

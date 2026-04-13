# SesameFS Architecture Overview

> How to read: Colored nodes indicate security findings. Follow the arrows to trace how data flows between components. The "Unauthenticated" box shows routes an attacker can reach without any credentials.

**Key issues visible here:**
- The OnlyOffice callback (red) has zero authentication and can reach the OnlyOffice server via SSRF.
- S3 (orange) stores blocks without server-side encryption.
- Share links (yellow) serve SVG files inline, enabling stored XSS.

```mermaid
flowchart TD
    Internet["Clients<br/>Browser, Seafile, API"]
    Internet -->|HTTPS| Nginx

    Nginx["Nginx<br/>TLS, headers, /metrics block"]
    Nginx -->|Static| Frontend["React Frontend<br/>:3000"]
    Nginx -->|API| SesameFS

    subgraph SesameFS["SesameFS API :8080"]
        direction TD
        MW["Middleware<br/>Headers + CORS + Auth"]
        MW --> Protected
        MW --> Public

        subgraph Protected["Authenticated"]
            API["REST API v2<br/>Libraries, Files,<br/>Shares, Admin"]
            Sync["Seafile Sync<br/>Upload, Download"]
        end

        subgraph Public["Unauthenticated"]
            Share["Share Links<br/>inline SVG risk"]
            OO["OnlyOffice CB<br/>NO AUTH"]
            Info["/ping /health<br/>/bootstrap"]
        end
    end

    SesameFS --> Storage
    subgraph Storage["Storage"]
        Chunk["FastCDC Chunker"]
        Blocks["Block Store<br/>SHA-256 addressed"]
        Crypto["Encryption<br/>Argon2id + AES-256"]
    end

    Blocks -->|"No SSE"| S3[("S3 / MinIO")]
    SesameFS --> Cass[("Cassandra")]
    SesameFS -->|OIDC| IdP["Identity Provider"]
    OO -->|"SSRF"| OOSrv["OnlyOffice :8088"]

    style OO fill:#dc3545,color:#fff
    style S3 fill:#e67700,color:#fff
    style Share fill:#ffc107,color:#000
```

### How to read colors

| Color | Meaning |
|-------|---------|
| **Red** | Critical vulnerability — must fix before production |
| **Orange** | Security gap — should fix soon |
| **Yellow** | Medium concern — defense-in-depth improvement needed |
| **Green** | Security control working correctly |
| **Blue** | Encryption / cryptographic module |
| **Gray** | Infrastructure component (no finding) |
| Default | Normal component (no finding) |

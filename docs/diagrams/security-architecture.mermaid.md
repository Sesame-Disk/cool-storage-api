# SesameFS Security Architecture Diagrams

## 1. System Overview

```mermaid
flowchart TB
    subgraph External
        Internet["Internet / Clients<br/>(Browsers, Seafile Desktop, API)"]
        IdP["OIDC Identity Provider<br/>(accounts.sesamedisk.com)"]
    end

    subgraph Edge["Edge Layer"]
        Nginx["Nginx Reverse Proxy<br/>TLS, rate limiting<br/>X-Frame-Options, X-Robots-Tag<br/>/metrics blocked"]
    end

    subgraph App["Application Layer (Go :8080)"]
        direction TB
        AuthMW["Auth Middleware<br/>Token/session validation<br/>Security headers (HSTS, nosniff)"]
        RateLimiter["Rate Limiter<br/>Per-IP token bucket<br/>Auth endpoints only"]
        
        subgraph Protected["Protected Routes"]
            APIv2["API v2 Handlers<br/>Libraries, Files, Shares<br/>Admin, Search, Tags"]
            SeafHTTP["Seafile HTTP Protocol<br/>Chunked upload/download<br/>ZIP dir download"]
        end
        
        subgraph Public["Public Routes"]
            ShareLink["Share Link Handler<br/>inline SVG = XSS risk"]
            OnlyOffice["OnlyOffice Handler<br/>UNAUTHENTICATED<br/>SSRF via http.Get"]
        end
        
        subgraph Security["Security Modules"]
            OIDC["OIDC Client<br/>Audience validation<br/>Role mapping<br/>PKCE support"]
            Sessions["Session Store<br/>SHA-256 hashed tokens<br/>In-memory cache"]
            Crypto["Encryption Module<br/>Argon2id + PBKDF2<br/>AES-256-CBC"]
        end
    end
    
    subgraph Storage["Storage Layer"]
        BlockStore["Block Store<br/>SHA-256 addressed<br/>Two-level sharding"]
        Chunker["FastCDC Chunker<br/>2-256MB adaptive"]
        GC["Garbage Collector<br/>Grace period deletion"]
    end
    
    subgraph Data["Data Layer"]
        S3[("S3 / MinIO<br/>File blocks<br/>NO ServerSideEncryption")]
        Cassandra[("Cassandra<br/>Metadata, sessions<br/>Parameterized queries")]
    end
    
    subgraph DocEdit["Document Editing"]
        OOServer["OnlyOffice Server<br/>:8088"]
    end

    Internet -->|HTTPS| Nginx
    Nginx -->|"Static assets"| Frontend["React Frontend :3000"]
    Nginx -->|"/api*, /seafhttp"| AuthMW
    Nginx -->|"/onlyoffice"| OnlyOffice
    
    AuthMW --> RateLimiter
    AuthMW --> APIv2
    AuthMW --> SeafHTTP
    AuthMW -.->|"Session lookup"| Sessions
    AuthMW -.->|"OIDC validation"| OIDC
    
    APIv2 --> BlockStore
    APIv2 --> Cassandra
    SeafHTTP --> BlockStore
    SeafHTTP -->|"Encrypt/decrypt"| Crypto
    
    ShareLink --> BlockStore
    OnlyOffice -->|"SSRF: http.Get(url)"| OOServer
    
    BlockStore --> Chunker
    BlockStore --> S3
    
    Sessions --> Cassandra
    OIDC -->|"Code exchange"| IdP
    
    GC --> S3
    GC --> Cassandra

    style OnlyOffice fill:#ff4444,color:#fff,stroke:#cc0000
    style S3 fill:#ff8800,color:#fff,stroke:#cc6600
    style ShareLink fill:#ffaa00,color:#000,stroke:#cc8800
```

## 2. Security Layer Detail

```mermaid
flowchart LR
    subgraph Request["Incoming Request"]
        R["HTTP Request"]
    end
    
    subgraph Auth["Authentication Chain"]
        direction TB
        ExtractToken["Extract Token<br/>(Header / Cookie)"]
        DevMode{"Dev Mode?"}
        CheckSession["Validate Session<br/>(SHA-256 hash lookup)"]
        CheckAPIKey["Validate API Key<br/>(Normalized timing)"]
        CheckRepoToken["Validate Repo Token<br/>(Account status check)"]
        RateLimit["Rate Limiter<br/>(Per-IP bucket)"]
    end
    
    subgraph Headers["Security Headers"]
        HSTS["HSTS: max-age=31536000"]
        Nosniff["X-Content-Type-Options: nosniff"]
        Referrer["Referrer-Policy: strict-origin"]
        CSP["CSP: MISSING"]
        XFO["X-Frame-Options: MISSING at app<br/>(nginx adds SAMEORIGIN)"]
    end
    
    subgraph Bypass["NO AUTH PATHS"]
        OOCallback["/onlyoffice/editor-callback<br/>CRITICAL: Zero auth"]
        ShareView["/d/:token<br/>Public share links"]
        HealthPing["/ping, /health, /ready"]
        Bootstrap["/api/v2.1/bootstrap"]
        AuthToken["/api2/auth-token<br/>(rate-limited)"]
        Metrics["/metrics<br/>(unauth at app)"]
    end
    
    R --> ExtractToken
    ExtractToken --> DevMode
    DevMode -->|Yes| DevBypass["Dev Token Match<br/>(plaintext ==)"]
    DevMode -->|No| CheckSession
    CheckSession -->|Miss| CheckAPIKey
    CheckAPIKey -->|Miss| CheckRepoToken
    
    R -->|"No middleware"| OOCallback
    R -->|"No middleware"| ShareView
    R -->|"No middleware"| HealthPing
    
    style OOCallback fill:#ff4444,color:#fff
    style CSP fill:#ff8800,color:#fff
    style XFO fill:#ffaa00,color:#000
    style Metrics fill:#ffaa00,color:#000
```

## 3. Storage & Encryption Architecture

```mermaid
flowchart TB
    subgraph Upload["Upload Flow"]
        Client["Client"] -->|"File bytes"| Chunker["FastCDC Chunker<br/>Adaptive 2-256MB"]
        Chunker -->|"Chunks"| SHA["SHA-256 Hash<br/>(Block ID)"]
        SHA -->|"Check exists"| Dedup{"Block exists?"}
        Dedup -->|"Yes"| Skip["Skip upload<br/>(dedup)"]
        Dedup -->|"No"| Encrypt{"Library encrypted?"}
        Encrypt -->|"Yes"| AES["AES-256-CBC<br/>Encrypt block"]
        Encrypt -->|"No"| Store
        AES --> Store["S3 Put<br/>blocks/xx/yy/hash<br/>NO ServerSideEncryption"]
    end
    
    subgraph KeyMgmt["Key Management"]
        Password["User Password"]
        Password -->|"Web/API"| Argon2["Argon2id<br/>3 iter, 64MB, 4 threads"]
        Password -->|"Seafile client"| PBKDF2["PBKDF2-SHA256<br/>1000 iter (weak)"]
        Argon2 --> DerivedKey["Derived Key + IV"]
        PBKDF2 --> DerivedKeyWeak["Derived Key + IV"]
        DerivedKey --> DecryptFK["Decrypt random_key<br/>(per-library)"]
        DerivedKeyWeak --> DecryptFKW["Decrypt random_key<br/>(compat)"]
        DecryptFK --> FileKey["File Key (AES-256)"]
        DecryptFKW --> FileKey
        FileKey --> AES
    end
    
    subgraph Download["Download Flow"]
        Request["Download Request"] --> Auth["Auth + Permission Check"]
        Auth --> GetBlocks["Get Block IDs<br/>from Cassandra"]
        GetBlocks --> S3Get["S3 Get<br/>blocks/xx/yy/hash"]
        S3Get --> DecryptQ{"Encrypted?"}
        DecryptQ -->|"Yes"| Decrypt["AES-256-CBC Decrypt"]
        DecryptQ -->|"No"| Stream["Stream to client"]
        Decrypt --> Stream
    end
    
    subgraph Compromise["If S3 Compromised"]
        S3Data["S3 Block Data"]
        S3Data -->|"Unencrypted libs"| Exposed["DATA EXPOSED"]
        S3Data -->|"Encrypted libs"| Protected["Protected by AES-256<br/>Key NOT in S3"]
    end
    
    style Store fill:#ff8800,color:#fff
    style PBKDF2 fill:#ffaa00,color:#000
    style Exposed fill:#ff4444,color:#fff
    style Protected fill:#44bb44,color:#fff
```

## 4. OnlyOffice Attack Chain (V2-C1 + C-1)

```mermaid
flowchart LR
    Attacker["Attacker<br/>(No credentials needed)"]
    
    Attacker -->|"POST /onlyoffice/editor-callback<br/>status=2, url=http://169.254.169.254/..."| Endpoint["/onlyoffice/editor-callback<br/>NO AUTH MIDDLEWARE<br/>NO JWT VERIFICATION"]
    
    Endpoint -->|"1. Parse JSON body"| Parse["json.Unmarshal<br/>(no signature check)"]
    Parse -->|"2. Lookup doc_key"| DocKey["getDocKeyMapping<br/>(if key not found: exit)"]
    DocKey -->|"3. Get user from body"| UserID["userID = req.Users[0]<br/>(attacker controlled!)"]
    UserID -->|"4. Permission check"| Perm["HasLibraryAccess<br/>(uses attacker's userID)"]
    Perm -->|"5. Fetch attacker URL"| SSRF["http.Get(internalURL)<br/>No IP validation<br/>No body size limit<br/>No redirect policy"]
    SSRF -->|"6. Write to library"| Write["saveEditedDocument<br/>(overwrites target file)"]
    
    subgraph Impact["Impact"]
        direction TB
        I1["EC2 metadata theft<br/>(AWS credentials)"]
        I2["Internal service probing<br/>(Cassandra, other services)"]
        I3["Arbitrary file write<br/>(if valid doc_key known)"]
    end
    
    SSRF --> I1
    SSRF --> I2
    Write --> I3
    
    style Endpoint fill:#ff4444,color:#fff
    style SSRF fill:#ff4444,color:#fff
    style I1 fill:#ff4444,color:#fff
    style I2 fill:#ff4444,color:#fff
    style I3 fill:#ff4444,color:#fff
```

## 5. CORS Configuration Issue (M-2)

```mermaid
flowchart LR
    subgraph Attacker["Attacker's Page (evil.example)"]
        JS["fetch('https://sfs.nihaoshares.com/api/v2.1/bootstrap')"]
    end
    
    subgraph Server["SesameFS Production"]
        CORS["CORS Middleware<br/>config.prod.yaml:<br/>allowed_origins: ['*']"]
        CORS -->|"Access-Control-Allow-Origin: *<br/>Access-Control-Allow-Credentials: true"| Response["Response with full body"]
    end
    
    JS -->|"Origin: https://evil.example"| CORS
    Response -->|"Cross-origin read succeeds"| JS
    
    subgraph Readable["Endpoints readable cross-origin"]
        B["/api/v2.1/bootstrap<br/>(feature flags, config)"]
        S["/api2/server-info<br/>(version, features)"]
        O["/api/v2.1/auth/oidc/config<br/>(client_id, issuer)"]
        SL["/api/v2.1/share-links/:token/*<br/>(enumeration)"]
    end
    
    style CORS fill:#ff8800,color:#fff
```

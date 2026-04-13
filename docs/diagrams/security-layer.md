# SesameFS Security Layer

## Auth Flow

```mermaid
flowchart TD
    Req["Request"] --> Token{"Has token?"}

    Token -->|No| PubRoute{"Public route?"}
    PubRoute -->|Yes| Serve["Serve unauthenticated"]
    PubRoute -->|No| R401["401"]

    Token -->|Yes| Dev{"Dev mode?"}
    Dev -->|Yes| DevMatch{"Match?"}
    DevMatch -->|Yes| OK["Authenticated"]
    DevMatch -->|No| Session

    Dev -->|No| Session["Session check<br/>SHA-256 in cache"]
    Session --> Hit{"Cache hit?"}
    Hit -->|Yes + valid| OK
    Hit -->|No| DB["Cassandra lookup"]
    DB --> DBOk{"Found?"}
    DBOk -->|Yes| OK
    DBOk -->|No| APIKey["API Key check"]
    APIKey --> KeyOk{"Valid?"}
    KeyOk -->|Yes| Status{"Active?"}
    Status -->|Yes| OK
    Status -->|No| R403["403"]
    KeyOk -->|No| R401

    OK --> Perm["Permission check<br/>R / RW / flags"]
    Perm --> Handler["Route handler"]

    style OK fill:#28a745,color:#fff
    style R401 fill:#dc3545,color:#fff
    style R403 fill:#dc3545,color:#fff
    style Serve fill:#ffc107,color:#000
```

## OIDC Login

```mermaid
sequenceDiagram
    participant B as Browser
    participant S as SesameFS
    participant I as OIDC Provider

    B->>S: GET /auth/oidc/login
    S->>S: state + nonce + PKCE
    S-->>B: 302 -> IdP

    B->>I: Authorize
    I-->>B: 302 -> /callback?code=X

    B->>S: GET /callback?code=X
    S->>I: POST /token (code, secret, verifier)
    I-->>S: id_token

    S->>S: Verify signature (RSA/ECDSA)
    S->>S: Check issuer
    S->>S: Check audience (FIXED)
    S->>S: Check nonce
    S->>S: Map role (superadmin blocked)
    S->>S: Create session
    S-->>B: Set cookie + redirect
```

## Headers Status

| Header | App Level | Nginx (prod) |
|--------|-----------|-------------|
| X-Content-Type-Options: nosniff | Present | Present |
| Referrer-Policy | Present | Present |
| HSTS | Present | Present (preload) |
| X-Frame-Options | **Missing** | SAMEORIGIN |
| Content-Security-Policy | **Missing** | **Missing** |
| Permissions-Policy | **Missing** | **Missing** |
| X-Robots-Tag | **Missing** | Present |

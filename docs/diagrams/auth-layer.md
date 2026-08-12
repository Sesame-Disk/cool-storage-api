# SesameFS Authentication Layer

> How to read: Each section below is a separate stage of the auth lifecycle. Green = secure outcome, red = rejection, yellow = a gap or concern that needs attention. Diamonds are decision points.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Red** | Auth rejection or critical finding |
| **Yellow** | Security gap (e.g. cookie without httpOnly, node-local invalidation) |
| **Green** | Secure outcome or working control |

---

## Token Creation

**Fixed (`ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`):** The `sesamefs_auth` cookie is now set with
`httpOnly=true`, so JavaScript (and any XSS) can no longer read it. No code in this repository
was found reading its value from JS; the desktop-client SSO flow gets its token via polling,
not by reading this cookie.

```mermaid
flowchart TD
    Login["OIDC Login or<br/>/api2/auth-token"]
    Login --> GenToken["Generate crypto/rand token"]
    GenToken --> HashToken["SHA-256 hash"]
    HashToken --> StoreDB["Store hash in Cassandra<br/>user_id, org_id, expiry"]
    StoreDB --> SetCookie["Set cookie<br/>sesamefs_auth = email@token<br/>httpOnly = true"]
    SetCookie --> Done["Return token to client"]

    style SetCookie fill:#28a745,color:#fff
```

---

## Token Validation

**How it works:** Each request tries session lookup first, then API key, then repo token. API keys use normalized timing to prevent oracle attacks. Account status is enforced on every auth path.

```mermaid
flowchart TD
    Req["Incoming request"] --> Extract["Extract token<br/>Header or Cookie"]
    Extract --> Hash["SHA-256 hash"]
    Hash --> Cache{"In cache?"}

    Cache -->|Yes| TTL{"Expired?"}
    TTL -->|No| OK["Authenticated"]
    TTL -->|Yes| DB

    Cache -->|No| DB["Cassandra lookup"]
    DB --> Found{"Valid?"}
    Found -->|Yes| CacheIt["Add to cache"]
    CacheIt --> OK
    Found -->|No| TryKey["Try API Key auth"]

    TryKey --> Normalize["Normalize format<br/>malformed -> dummy"]
    Normalize --> HashKey["SHA-256 hash"]
    HashKey --> LookupKey["Cassandra lookup"]
    LookupKey --> KeyOK{"Found?"}

    KeyOK -->|Yes| Status{"Account active?"}
    Status -->|Yes| OK
    Status -->|No| Reject403["403 Forbidden"]
    KeyOK -->|No| TryRepo["Try Repo Token"]
    TryRepo --> RepoOK{"Valid?"}
    RepoOK -->|Yes| OK
    RepoOK -->|No| Reject401["401 Unauthorized"]

    style OK fill:#28a745,color:#fff
    style Reject401 fill:#dc3545,color:#fff
    style Reject403 fill:#dc3545,color:#fff
```

---

## Session Invalidation

**Key issue:** Invalidation only clears the current node's cache. In a multi-node deployment, other nodes will keep serving the revoked session until their cache TTL expires (finding M-7).

```mermaid
flowchart TD
    Trigger["User deactivated<br/>or org suspended"]
    Trigger --> Kill["InvalidateUserSessions()"]
    Kill --> ClearLocal["Clear in-memory cache<br/>current node only"]
    Kill --> DeleteDB["Delete from Cassandra"]
    ClearLocal --> Gap["Other nodes keep<br/>cached session until<br/>TTL expires"]

    style Gap fill:#ffc107,color:#000
```

---

## OIDC Role Mapping

**Key fix since v1:** `superadmin` / `super_admin` / `platform_admin` claims from OIDC are now explicitly blocked and downgraded. Superadmin can only be set via a manual script in the database.

```mermaid
flowchart TD
    Claims["OIDC token claims"]
    Claims --> HasRole{"Role claim<br/>present?"}

    HasRole -->|No| Keep["Keep existing DB role"]

    HasRole -->|Yes| Map["mapOIDCRole()"]
    Map --> Super{"superadmin?"}

    Super -->|Yes| Block["BLOCKED<br/>Use DefaultRole"]
    Super -->|No| Normalize["Map to local role<br/>owner / admin / user<br/>readonly / guest"]

    Normalize --> Existing{"Existing user?"}
    Existing -->|No| SetNew["Set role on new user"]
    Existing -->|Yes| CheckDB["Get current DB role"]

    CheckDB --> DBSuper{"DB = superadmin?"}
    DBSuper -->|Yes| Preserve["Preserve<br/>script-bootstrapped"]
    DBSuper -->|No| Sync["Update role from OIDC"]

    Block --> Keep

    style Block fill:#28a745,color:#fff,stroke-width:3px
    style Preserve fill:#28a745,color:#fff
```

---

## Rate Limiting Coverage

**Key issue:** Rate limiting is only applied to auth endpoints. Upload, download, share-link enumeration, and the OnlyOffice callback have no rate limiting at all.

```mermaid
flowchart TD
    subgraph Protected["Rate Limited"]
        direction LR
        A1["/api2/auth-token"]
        A2["/oauth/callback"]
        A3["/auth/oidc/*"]
    end

    subgraph Unprotected["NOT Rate Limited"]
        direction LR
        B1["/share-links/:token/*"]
        B2["/seafhttp/upload"]
        B3["/seafhttp/download"]
        B4["/onlyoffice/callback"]
    end

    style A1 fill:#28a745,color:#fff
    style A2 fill:#28a745,color:#fff
    style A3 fill:#28a745,color:#fff
    style B1 fill:#dc3545,color:#fff
    style B2 fill:#ffc107,color:#000
    style B3 fill:#ffc107,color:#000
    style B4 fill:#dc3545,color:#fff
```

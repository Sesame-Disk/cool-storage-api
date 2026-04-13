C4Context
    title  - Domain: auth

    Container(auth_middleware, "Auth Middleware", "", "Token/session validation, dev-mode bypass, API key auth, repo-token auth. Security headers middleware (HSTS, nosniff, Referrer-Policy)")
    Container(oidc, "OIDC Client", "", "OpenID Connect auth flow. Audience validation, role mapping (superadmin blocked), state management with TTL+cap, PKCE support")
    Container(session_store, "Session Store", "", "In-memory cache + Cassandra persistence. SHA-256 hashed tokens. Node-local invalidation (M-7)")
    Container(idp, "OIDC Identity Provider", "", "External IdP (e.g., accounts.sesamedisk.com). Provides authentication, role claims, group claims")

    Rel(sesamefs, auth_middleware, "Every request", "")
    Rel(auth_middleware, oidc, "Session validation", "")
    Rel(auth_middleware, session_store, "Token lookup", "")
    Rel(auth_middleware, rate_limiter, "Per-IP check", "")
    Rel(session_store, cassandra, "Session persistence", "")
    Rel(oidc, idp, "OIDC flow (code exchange)", "")

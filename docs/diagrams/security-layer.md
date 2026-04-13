C4Context
    title  - Domain: security

    Container(nginx, "Nginx Reverse Proxy", "", "TLS termination, rate limiting, security headers (X-Frame-Options, X-Robots-Tag), /metrics blocking, static file serving")
    Container(auth_middleware, "Auth Middleware", "", "Token/session validation, dev-mode bypass, API key auth, repo-token auth. Security headers middleware (HSTS, nosniff, Referrer-Policy)")
    Container(oidc, "OIDC Client", "", "OpenID Connect auth flow. Audience validation, role mapping (superadmin blocked), state management with TTL+cap, PKCE support")
    Container(onlyoffice, "OnlyOffice Handler", "", "CRITICAL: editor-callback is UNAUTHENTICATED (V2-C1). No JWT verification. SSRF via http.Get on controlled URL (C-1)")
    Container(sharelink, "Share Link Handler", "", "Public share links. Content-Disposition: inline for SVG (C-2 XSS). Constant-time cookie comparison (H-6 fixed). Token enumeration oracle (H-5)")
    Container(rate_limiter, "Rate Limiter", "", "Per-IP token bucket. Applied to auth endpoints. NOT applied to upload/download or share-link enumeration paths")

    Rel(internet, nginx, "HTTPS", "")
    Rel(nginx, frontend, "Static assets", "")
    Rel(nginx, sesamefs, "/api*, /seafhttp, /onlyoffice", "")
    Rel(sesamefs, auth_middleware, "Every request", "")
    Rel(auth_middleware, oidc, "Session validation", "")
    Rel(auth_middleware, session_store, "Token lookup", "")
    Rel(auth_middleware, rate_limiter, "Per-IP check", "")
    Rel(sesamefs, onlyoffice, "NO AUTH (V2-C1)", "")
    Rel(sesamefs, sharelink, "Public share routes", "")
    Rel(onlyoffice, onlyoffice_server, "SSRF: http.Get(url) (C-1)", "")
    Rel(oidc, idp, "OIDC flow (code exchange)", "")

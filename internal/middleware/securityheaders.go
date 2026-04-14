package middleware

import "github.com/gin-gonic/gin"

// defaultCSP is a restrictive Content-Security-Policy for API/JSON responses.
// Handlers that serve HTML (share links, OnlyOffice, file viewer) override this
// via SetCSP() before writing the response.
//
// frame-ancestors 'self': allows same-origin iframes (PDF viewer, file preview
// loaded inside the SPA). External sites cannot embed any response.
const defaultCSP = "default-src 'none'; frame-ancestors 'self'"

// defaultPermissionsPolicy disables browser capabilities SesameFS does not use.
const defaultPermissionsPolicy = "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"

// SetCSP allows a handler to override the default CSP for routes that serve HTML.
// Call this before c.HTML() or c.Data() — the value replaces the default header.
func SetCSP(c *gin.Context, policy string) {
	c.Header("Content-Security-Policy", policy)
}

// SecurityHeaders emits baseline HTTP security headers on every response.
//
// CSP: The default policy is maximally restrictive ("default-src 'none'"),
// suitable for API endpoints that return JSON. Routes serving HTML content
// (share links, file viewer, OnlyOffice) must call SetCSP() to relax the
// policy for their specific needs.
//
// Framing: frame-ancestors 'self' is set by default — same-origin iframes are
// allowed (PDF viewer, file preview in the SPA), but external sites cannot embed
// any response. Sensitive HTML routes (login success) override to 'none'.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Content-Security-Policy", defaultCSP)
		h.Set("Permissions-Policy", defaultPermissionsPolicy)
		c.Next()
	}
}

package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders emits baseline HTTP security headers on every response.
//
// Framing policy is intentionally omitted here: SesameFS uses same-origin
// iframes for internal previews and also exposes public share surfaces that may
// be embedded externally. A single global X-Frame-Options or frame-ancestors
// policy would either break legitimate previews/embeds or be too weak for
// sensitive HTML routes. That policy must be applied closer to the specific
// handlers or at the edge.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}

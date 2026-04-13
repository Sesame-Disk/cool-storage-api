package middleware

import (
	"net/http"
	"net/netip"

	"github.com/gin-gonic/gin"
)

// InternalOnly restricts a route to loopback, private, or link-local clients.
// This is intended for operational endpoints such as readiness and metrics.
func InternalOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isInternalClientIP(c.ClientIP()) {
			c.Next()
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}

func isInternalClientIP(raw string) bool {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}

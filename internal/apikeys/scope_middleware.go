package apikeys

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireScope returns a Gin middleware that enforces API key scope.
//
// If the request was authenticated via a session (no "api_key_scope" in context),
// the middleware passes through — sessions have full access.
//
// If the request was authenticated via an API key, the middleware checks that
// the key's scope grants access to the required level.
func RequireScope(required string) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, exists := c.Get("api_key_scope")
		if !exists {
			// Not an API key auth — session or other auth method. Allow through.
			c.Next()
			return
		}

		scopeStr, _ := scope.(string)
		if !ScopeAllows(scopeStr, required) {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "insufficient api key scope",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

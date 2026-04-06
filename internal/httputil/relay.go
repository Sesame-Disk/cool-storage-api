// Package httputil provides shared HTTP helpers used by both the api and api/v2
// packages.  Keeping them here avoids circular imports (api imports api/v2).
package httputil

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func hostnameFromServerURL(serverURL string) string {
	host := strings.TrimSpace(serverURL)
	if host == "" {
		return ""
	}
	if idx := strings.Index(host, "://"); idx != -1 {
		host = host[idx+3:]
	}
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return NormalizeHostname(host)
}

// GetRoutingHostname returns the hostname that should drive request-scoped routing
// decisions such as region-aware storage selection.
//
// Precedence (highest to lowest):
//  1. X-Forwarded-Host header — preserves the original public hostname through proxies
//  2. c.Request.Host — direct client host when no forwarding proxy is present
//  3. SERVER_URL env var — last-resort fallback only when the request carries no host context
func GetRoutingHostname(c *gin.Context) string {
	if c != nil {
		if fwdHost := c.GetHeader("X-Forwarded-Host"); fwdHost != "" {
			return NormalizeHostname(fwdHost)
		}
		if host := NormalizeHostname(c.Request.Host); host != "" {
			return host
		}
	}
	return hostnameFromServerURL(os.Getenv("SERVER_URL"))
}

// GetEffectiveHostname returns the real external hostname for relay_id / relay_addr.
// Precedence (highest to lowest):
//  1. SERVER_URL env var — explicitly configured by the admin
//  2. X-Forwarded-Host header — set by nginx/traefik when proxying
//  3. c.Request.Host — last resort (works for direct connections)
func GetEffectiveHostname(c *gin.Context) string {
	if host := hostnameFromServerURL(os.Getenv("SERVER_URL")); host != "" {
		return host
	}
	if fwdHost := c.GetHeader("X-Forwarded-Host"); fwdHost != "" {
		return NormalizeHostname(fwdHost)
	}
	return NormalizeHostname(c.Request.Host)
}

// GetRelayPortFromRequest extracts the port from the request context.
// If no explicit port, returns the default for the detected scheme (443/80).
func GetRelayPortFromRequest(c *gin.Context) string {
	// If SERVER_URL is set, extract port from it
	if serverURL := os.Getenv("SERVER_URL"); serverURL != "" {
		serverURL = strings.TrimSuffix(serverURL, "/")
		after := serverURL
		if idx := strings.Index(after, "://"); idx != -1 {
			after = after[idx+3:]
		}
		if idx := strings.LastIndex(after, ":"); idx != -1 {
			return after[idx+1:]
		}
		if strings.HasPrefix(serverURL, "https") {
			return "443"
		}
		return "80"
	}

	// Extract from Host header (e.g., "localhost:3000" → "3000")
	host := c.Request.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		return host[idx+1:]
	}

	// No explicit port — use scheme default
	if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" {
		return "443"
	}
	if c.Request.TLS != nil {
		return "443"
	}
	return "80"
}

// GetBaseURLFromRequest derives the server base URL from the incoming request.
// Respects SERVER_URL env var, X-Forwarded-Proto/Host headers, and TLS state.
func GetBaseURLFromRequest(c *gin.Context) string {
	if serverURL := os.Getenv("SERVER_URL"); serverURL != "" {
		return strings.TrimSuffix(serverURL, "/")
	}
	scheme := "https"
	host := GetEffectiveHostname(c)
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if c.Request.TLS == nil && (strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")) {
		scheme = "http"
	}
	return scheme + "://" + host
}

// NormalizeHostname normalises a hostname for comparison: lowercase, strip port,
// strip trailing FQDN dot.
func NormalizeHostname(hostname string) string {
	hostname = strings.ToLower(hostname)
	if idx := strings.LastIndex(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}
	hostname = strings.TrimSuffix(hostname, ".")
	return hostname
}

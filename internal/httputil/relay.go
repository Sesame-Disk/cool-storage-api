// Package httputil provides shared HTTP helpers used by both the api and api/v2
// packages.  Keeping them here avoids circular imports (api imports api/v2).
package httputil

import (
	"net/url"
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

func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx != -1 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func getBrowserHost(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if fwdHost := firstForwardedValue(c.GetHeader("X-Forwarded-Host")); fwdHost != "" {
		return fwdHost
	}
	return strings.TrimSpace(c.Request.Host)
}

func getExplicitPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		if strings.Contains(host[idx+1:], "]") {
			return ""
		}
		return host[idx+1:]
	}
	return ""
}

func getBrowserScheme(c *gin.Context, host string) string {
	if c == nil {
		return "http"
	}
	if proto := firstForwardedValue(c.GetHeader("X-Forwarded-Proto")); proto != "" {
		return proto
	}
	if c.Request.TLS != nil {
		return "https"
	}
	host = NormalizeHostname(host)
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		return "http"
	}
	return "http"
}

// GetBrowserURL derives the browser-facing base URL for absolute links returned by the API.
// If configuredURL is provided it takes priority, except that a stale http URL is upgraded to
// https when the current request is clearly https for the same host.
func GetBrowserURL(c *gin.Context, configuredURL string) string {
	configuredURL = strings.TrimSuffix(strings.TrimSpace(configuredURL), "/")
	host := getBrowserHost(c)
	scheme := getBrowserScheme(c, host)

	if configuredURL != "" {
		if scheme == "https" && host != "" {
			configured, err := url.Parse(configuredURL)
			if err == nil && strings.EqualFold(configured.Scheme, "http") {
				requestURL := &url.URL{Host: host}
				if strings.EqualFold(configured.Hostname(), requestURL.Hostname()) {
					configuredPort := configured.Port()
					requestPort := requestURL.Port()
					if configuredPort == "" || configuredPort == requestPort {
						return "https://" + host
					}
				}
			}
		}
		return configuredURL
	}

	if host != "" {
		return scheme + "://" + host
	}

	return "http://localhost:8080"
}

// GetRoutingHostname returns the hostname that should drive request-scoped routing
// decisions such as region-aware storage selection.
//
// Precedence (highest to lowest):
//  1. X-Forwarded-Host header — preserves the original public hostname through proxies
//  2. c.Request.Host — direct client host when no forwarding proxy is present
//  3. configuredURL — last-resort fallback only when the request carries no host context
func GetRoutingHostname(c *gin.Context, configuredURL string) string {
	if c != nil {
		if fwdHost := firstForwardedValue(c.GetHeader("X-Forwarded-Host")); fwdHost != "" {
			return NormalizeHostname(fwdHost)
		}
		if host := NormalizeHostname(c.Request.Host); host != "" {
			return host
		}
	}
	return hostnameFromServerURL(configuredURL)
}

// GetEffectiveHostname returns the real external hostname for relay_id / relay_addr.
// Precedence (highest to lowest):
//  1. configuredURL — explicitly configured by the admin
//  2. X-Forwarded-Host header — set by nginx/traefik when proxying
//  3. c.Request.Host — last resort (works for direct connections)
func GetEffectiveHostname(c *gin.Context, configuredURL string) string {
	if host := hostnameFromServerURL(configuredURL); host != "" {
		return host
	}
	if fwdHost := firstForwardedValue(c.GetHeader("X-Forwarded-Host")); fwdHost != "" {
		return NormalizeHostname(fwdHost)
	}
	return NormalizeHostname(c.Request.Host)
}

// GetRelayPortFromRequest extracts the port from the request context.
// If no explicit port, returns the default for the detected scheme (443/80).
func GetRelayPortFromRequest(c *gin.Context, configuredURL string) string {
	if serverURL := strings.TrimSpace(configuredURL); serverURL != "" {
		serverURL = strings.TrimSuffix(strings.TrimSpace(serverURL), "/")
		if parsed, err := url.Parse(serverURL); err == nil {
			if port := parsed.Port(); port != "" {
				return port
			}
			if strings.EqualFold(parsed.Scheme, "https") {
				return "443"
			}
			return "80"
		}
		if strings.HasPrefix(serverURL, "https") {
			return "443"
		}
		return "80"
	}

	// Preserve a forwarded public port before falling back to the internal host.
	if host := firstForwardedValue(c.GetHeader("X-Forwarded-Host")); host != "" {
		if port := getExplicitPort(host); port != "" {
			return port
		}
	}

	// Extract from Host header (e.g., "localhost:3000" → "3000")
	if port := getExplicitPort(c.Request.Host); port != "" {
		return port
	}

	// No explicit port — use scheme default
	if proto := firstForwardedValue(c.GetHeader("X-Forwarded-Proto")); proto == "https" {
		return "443"
	}
	if c.Request.TLS != nil {
		return "443"
	}
	return "80"
}

// GetBaseURLFromRequest derives the server base URL from the incoming request.
// Respects configuredURL, X-Forwarded-Proto/Host headers, and TLS state.
func GetBaseURLFromRequest(c *gin.Context, configuredURL string) string {
	return GetBrowserURL(c, configuredURL)
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

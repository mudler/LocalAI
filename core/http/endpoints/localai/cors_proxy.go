package localai

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

var privateNetworks []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	} {
		_, network, _ := net.ParseCIDR(cidr)
		privateNetworks = append(privateNetworks, network)
	}
}

func isPrivateIP(ip net.IP) bool {
	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// CORSProxyEndpoint proxies HTTP requests to external MCP servers,
// solving CORS issues for browser-based MCP connections.
// The target URL is passed as a query parameter: /api/cors-proxy?url=https://...
func CORSProxyEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		targetURL := c.QueryParam("url")
		if targetURL == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing 'url' query parameter"})
		}

		parsed, err := url.Parse(targetURL)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid target URL"})
		}

		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "only http and https schemes are supported"})
		}

		ips, err := net.LookupIP(parsed.Hostname())
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot resolve hostname"})
		}
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "requests to private networks are not allowed"})
			}
		}

		// Pin the connection to the validated IP to prevent DNS rebinding (TOCTOU)
		validIP := ips[0]
		port := parsed.Port()
		if port == "" {
			if parsed.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(
					ctx, network, net.JoinHostPort(validIP.String(), port),
				)
			},
		}
		client := &http.Client{Transport: transport, Timeout: 10 * time.Minute}

		xlog.Debug("CORS proxy request", "method", c.Request().Method, "target", targetURL)

		proxyReq, err := http.NewRequestWithContext(
			c.Request().Context(),
			c.Request().Method,
			targetURL,
			c.Request().Body,
		)
		if err != nil {
			return fmt.Errorf("failed to create proxy request: %w", err)
		}

		// Copy headers from the original request, excluding hop-by-hop headers
		skipHeaders := map[string]bool{
			"Host": true, "Connection": true, "Keep-Alive": true,
			"Transfer-Encoding": true, "Upgrade": true, "Origin": true,
			"Referer":       true,
			"Authorization": true, "Cookie": true,
			"X-Api-Key": true, "Proxy-Authorization": true,
		}
		for key, values := range c.Request().Header {
			if skipHeaders[key] {
				continue
			}
			for _, v := range values {
				proxyReq.Header.Add(key, v)
			}
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			xlog.Error("CORS proxy request failed", "error", err, "target", targetURL)
			return c.JSON(http.StatusBadGateway, map[string]string{"error": "proxy request failed: " + err.Error()})
		}
		defer resp.Body.Close()

		// Copy response headers
		for key, values := range resp.Header {
			lower := strings.ToLower(key)
			// Skip CORS headers — we'll set our own
			if strings.HasPrefix(lower, "access-control-") {
				continue
			}
			for _, v := range values {
				c.Response().Header().Add(key, v)
			}
		}

		// Set CORS headers to allow browser access
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Response().Header().Set("Access-Control-Allow-Headers", "*")
		c.Response().Header().Set("Access-Control-Expose-Headers", "*")

		c.Response().WriteHeader(resp.StatusCode)

		// Stream the response body with a size limit
		const maxProxyResponseSize = 100 << 20 // 100 MB
		_, err = io.Copy(c.Response().Writer, io.LimitReader(resp.Body, maxProxyResponseSize))
		return err
	}
}

// CORSProxyOptionsEndpoint handles CORS preflight requests for the proxy.
func CORSProxyOptionsEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("Access-Control-Allow-Origin", "*")
		c.Response().Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Response().Header().Set("Access-Control-Allow-Headers", "*")
		c.Response().Header().Set("Access-Control-Max-Age", "86400")
		return c.NoContent(http.StatusNoContent)
	}
}

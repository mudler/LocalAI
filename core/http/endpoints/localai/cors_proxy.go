package localai

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

var corsProxyClient = &http.Client{
	Timeout: 10 * time.Minute,
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
			"Referer": true,
		}
		for key, values := range c.Request().Header {
			if skipHeaders[key] {
				continue
			}
			for _, v := range values {
				proxyReq.Header.Add(key, v)
			}
		}

		resp, err := corsProxyClient.Do(proxyReq)
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

		// Stream the response body
		_, err = io.Copy(c.Response().Writer, resp.Body)
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

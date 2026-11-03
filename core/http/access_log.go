package http

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/clientip"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/xlog"
)

// maxLoggedUserAgent caps the user agent in the access log so a client
// cannot flood the log with one header.
const maxLoggedUserAgent = 80

// maxLoggedRequestID caps the request id for the same reason.
const maxLoggedRequestID = 64

// accessLog logs one line per request. When logClient is true the line also
// carries the client address (the TCP peer, or the forwarded client when the
// peer is a trusted proxy) and the user agent, so an unauthorized caller can
// be identified.
func accessLog(logClient bool, clients *clientip.Resolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			req := c.Request()
			res := c.Response()
			err := next(c)

			// Echo's central HTTPErrorHandler runs *after* this middleware
			// returns, so res.Status still reads the default 200 here when a
			// handler returned an error without writing a response. Mirror
			// echo.DefaultHTTPErrorHandler's status derivation so the access
			// log reflects the status the client actually receives — without
			// this, every silent handler error logs as 200.
			status := res.Status
			if err != nil && !res.Committed {
				status = http.StatusInternalServerError
				var he *echo.HTTPError
				if errors.As(err, &he) {
					status = he.Code
				}
			}

			// Fix for #7989: Reduce log verbosity of Web UI polling, resources API, and health checks
			// These paths are logged at DEBUG level (hidden by default) instead of INFO.
			isQuietPath := false
			for _, path := range quietPaths {
				if req.URL.Path == path {
					isQuietPath = true
					break
				}
			}

			// URL.Path never holds the query string, so keys passed as
			// ?api_key=... stay out of the log.
			attrs := []any{"method", req.Method, "path", req.URL.Path, "status", status}
			if logClient {
				attrs = append(attrs, "duration", time.Since(start).String(), "remote", clients.Resolve(req), "user_agent", sanitizeLogValue(req.UserAgent(), maxLoggedUserAgent))
				if id := requestID(req, res); id != "" {
					attrs = append(attrs, "request_id", id)
				}
			}

			if isQuietPath && status == 200 {
				xlog.Debug("HTTP request", attrs...)
			} else {
				xlog.Info("HTTP request", attrs...)
			}
			return err
		}
	}
}

// requestID returns the diagnostic id the server set on the response, or the
// X-Request-Id the caller sent, or an empty string.
func requestID(req *http.Request, res *echo.Response) string {
	id := res.Header().Get(diagnostics.HeaderDiagnosticID)
	if id == "" {
		id = req.Header.Get(echo.HeaderXRequestID)
	}
	return sanitizeLogValue(id, maxLoggedRequestID)
}

// sanitizeLogValue replaces control characters with a space and truncates
// the value to max characters.
func sanitizeLogValue(ua string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range ua {
		if n == max {
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

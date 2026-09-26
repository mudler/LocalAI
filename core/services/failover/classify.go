package failover

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// cloud-proxy translate mode reports upstream failures as plain text, so the
// status is only visible in the message.
var upstreamStatusRe = regexp.MustCompile(`upstream (\d{3})`)

// Errors that the next target would reject in the same way.
var requestErrorMarkers = []string{
	"exceeds the available context size",
	"is larger than the max context size",
	"maximum context length",
}

// IsRetryable reports whether a failed attempt should move to the next
// target. status is the HTTP status a handler wrote, or 0 when it returned err
// without writing.
func IsRetryable(err error, status int) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if status != 0 {
		return retryableStatus(status)
	}
	if err == nil {
		return false
	}
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return retryableStatus(he.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if st, ok := grpcstatus.FromError(err); ok {
		switch st.Code() {
		// ResourceExhausted (a rate-limited upstream, what localai-proxy
		// returns for a 429, or a backend out of memory) is retried
		// elsewhere and trips the target: a gap would skip a chronically
		// exhausted target forever without moving traffic off it.
		case codes.Unavailable, codes.Internal, codes.DeadlineExceeded, codes.Unknown, codes.ResourceExhausted:
			return !isRequestError(st.Message())
		default:
			return false
		}
	}
	msg := err.Error()
	if m := upstreamStatusRe.FindStringSubmatch(msg); m != nil {
		code, _ := strconv.Atoi(m[1])
		return retryableStatus(code)
	}
	// Anything else is usually a dial or load failure of this target.
	return !isRequestError(msg)
}

// IsCapabilityGap reports a target that cannot serve this kind of request at
// all (gRPC Unimplemented, anywhere in the error chain). The next target may
// serve it, and this target is not broken: the failure carries no signal
// about its health, so callers must skip it without tripping.
func IsCapabilityGap(err error) bool {
	if err == nil {
		return false
	}
	st, ok := grpcstatus.FromError(err)
	return ok && st.Code() == codes.Unimplemented
}

func retryableStatus(code int) bool {
	return code >= 500 && code != http.StatusNotImplemented
}

func isRequestError(msg string) bool {
	for _, m := range requestErrorMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

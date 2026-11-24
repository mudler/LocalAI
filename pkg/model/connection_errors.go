package model

import (
	"errors"
	"strings"
	"syscall"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isConnectionError returns true if the error indicates the remote endpoint is
// unreachable (connection refused, reset, gRPC Unavailable). Returns false for
// timeouts and deadline exceeded — those may indicate a busy server, not a dead one.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// gRPC Unavailable = server not reachable (covers connection refused, DNS, TLS errors)
	if s, ok := status.FromError(err); ok && s.Code() == codes.Unavailable {
		return true
	}

	// Syscall-level connection errors
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// Fallback string matching for wrapped errors that lose the typed error
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host")
}

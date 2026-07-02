// Package grpcerrors defines well-known error signals shared between backends
// (which produce them) and the router (which consumes them). Go error types do
// not survive the gRPC boundary, so these conditions are carried as gRPC status
// codes and detected via the code rather than by matching the error message.
package grpcerrors

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ModelNotLoaded returns the canonical error a backend returns when it has no
// model loaded for the request. It carries codes.FailedPrecondition so callers
// can detect it across the gRPC boundary without matching the message string.
func ModelNotLoaded(backend string) error {
	return status.Errorf(codes.FailedPrecondition, "%s: model not loaded", backend)
}

// IsModelNotLoaded reports whether err signals that the backend has no model
// loaded. It prefers the typed gRPC status code (FailedPrecondition) and falls
// back to the message for backends that have not yet adopted ModelNotLoaded.
//
// Acting on a false positive is harmless: the only consequence upstream is that
// the model is reloaded, which is idempotent.
func IsModelNotLoaded(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.FailedPrecondition {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "model not loaded")
}

// LiveTranscriptionUnsupported returns the canonical error a backend returns
// when it (or the loaded model) cannot serve the bidirectional
// AudioTranscriptionLive RPC. It carries codes.Unimplemented deliberately:
// that is also what gRPC itself returns for backends whose stubs predate the
// RPC, so callers get one uniform "degrade to non-live transcription" signal.
// (codes.FailedPrecondition is not used here — IsModelNotLoaded claims it.)
func LiveTranscriptionUnsupported(backend, reason string) error {
	return status.Errorf(codes.Unimplemented, "%s: live transcription unsupported: %s", backend, reason)
}

// IsLiveTranscriptionUnsupported reports whether err signals that live
// transcription is not available for this backend/model. It prefers the typed
// gRPC status code (Unimplemented) and falls back to the message for paths
// that lose the status (e.g. errors wrapped across non-gRPC boundaries).
func IsLiveTranscriptionUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.Unimplemented {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unimplemented")
}

// IsUnimplemented reports whether err is a gRPC Unimplemented status — the
// signal a backend gives for an RPC it does not implement. The generated
// UnimplementedBackendServer stub returns exactly this for any RPC a backend
// (e.g. a Python or external backend) has not overridden, so callers can treat
// an optional RPC as a no-op rather than a failure. Prefers the typed status
// code and falls back to the message for paths that lose the status (e.g. errors
// wrapped across non-gRPC boundaries).
func IsUnimplemented(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.Unimplemented {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unimplemented")
}

// StreamTranscriptionUnsupported returns the canonical error a backend returns
// when it (or the loaded model) cannot serve the server-streaming
// AudioTranscriptionStream RPC. It carries codes.Unimplemented like the live
// signal, but its intent is the opposite: it is meant to be SURFACED to the
// caller, not silently degraded. A backend must not decode the audio offline
// and emit it as a single "delta" + final to fake a stream — a client that
// asked for streaming has to learn the model cannot stream (qualitatively
// identical output would otherwise hide a missing, possibly required,
// capability). Callers wanting a plain transcript use the unary
// AudioTranscription / non-streaming endpoint instead.
func StreamTranscriptionUnsupported(backend, reason string) error {
	return status.Errorf(codes.Unimplemented, "%s: streaming transcription unsupported: %s", backend, reason)
}

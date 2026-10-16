package backend

import (
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// staleDecisionMessage is returned in place of the raw backend error when a
// backend answers a decision request as a plain Score call. Constant text on
// purpose: it never echoes backend or request content.
const staleDecisionMessage = "the backend is older than this server and has no native decision support; " +
	"upgrade the %q backend (see /backends/upgrades), then retry"

// A decision request is a Score call with question_type "systemone". A backend
// that knows the field never returns the two errors below for it: it either
// runs the decision or answers UNIMPLEMENTED. A backend built before native
// decisions ignores the unknown field and runs plain scoring, which fails with
// one of these messages. So they identify an old backend, not a bad model
// configuration, and adding "score" to known_usecases is not the fix.
var staleDecisionSignatures = []string{
	"Score was not enabled when the model was loaded",
	"candidates must be non-empty",
}

// staleDecisionBackendError reports whether err, returned for a decision
// request, comes from a backend that predates native decisions.
func staleDecisionBackendError(err error) bool {
	if err == nil {
		return false
	}
	msg := status.Convert(err).Message()
	for _, sig := range staleDecisionSignatures {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// handleStaleDecisionBackend turns the old-backend failure into a plain
// error and flags the backend in the upgrade check. Other errors pass through.
func handleStaleDecisionBackend(backendName string, err error) error {
	if !staleDecisionBackendError(err) {
		return err
	}
	gallery.MarkBackendStale(backendName, "backend does not support native decisions (older than this server)")
	return status.Errorf(codes.Unimplemented, staleDecisionMessage, backendName)
}

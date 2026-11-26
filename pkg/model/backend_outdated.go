package model

import (
	"errors"
	"fmt"
	"strings"
)

// BackendOutdatedError says that a backend refused a model because the backend
// build is too old for it (for example a llama.cpp build that predates the
// model's architecture). A newer build of the same backend can load it.
type BackendOutdatedError struct {
	// Backend is the name of the backend that failed.
	Backend string
	Cause   error
}

func (e *BackendOutdatedError) Error() string {
	return fmt.Sprintf("backend %q is too old for this model: %v", e.Backend, e.Cause)
}

func (e *BackendOutdatedError) Unwrap() error { return e.Cause }

// backendTooOldMarkers are the phrases a backend uses when it does not know
// what the model file asks for. They are matched in lower case.
var backendTooOldMarkers = []string{
	"unknown model architecture",
}

// BackendTooOldText says whether the error text of a failed load means that the
// backend is older than the model.
func BackendTooOldText(msg string) bool {
	lower := strings.ToLower(msg)
	for _, m := range backendTooOldMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// outdatedBackend wraps err in a BackendOutdatedError when its text says the
// backend is too old. Any other error is returned as it is.
func outdatedBackend(backend string, err error) error {
	if err == nil || !BackendTooOldText(err.Error()) {
		return err
	}
	return &BackendOutdatedError{Backend: backend, Cause: err}
}

// OutdatedBackendOf returns the backend named by a BackendOutdatedError in the
// chain of err, or "" when there is none.
func OutdatedBackendOf(err error) string {
	var oe *BackendOutdatedError
	if errors.As(err, &oe) {
		return oe.Backend
	}
	return ""
}

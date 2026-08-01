package chat

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/xlog"
)

// ModelChooser asks the user to pick one of models. It is nil when the session
// is not interactive.
type ModelChooser func(models []string) (string, error)

// ModelRequest is everything model resolution needs.
type ModelRequest struct {
	Flag       string       // --model
	Configured string       // model recorded in the agent config
	Available  []string     // models the server advertises
	StateDir   string       // where an interactive choice is persisted
	Choose     ModelChooser // nil means non-interactive
}

// ResolveModel picks the model for this invocation. A flag or a configured
// value wins outright and is not persisted; only an interactive choice is
// written back, so the prompt appears at most once.
//
// Available is used exactly as the server gave it. LocalAI advertises stray
// files it finds in the models directory alongside real models, but real model
// IDs contain dots too (lfm2.5-8b-a1b), so any client-side "looks like a
// filename" heuristic would eventually hide a model the user has. Deciding
// which advertised IDs are real belongs to the endpoint, not to a guess here.
func ResolveModel(req ModelRequest) (string, error) {
	if req.Flag != "" {
		return req.Flag, nil
	}
	if req.Configured != "" {
		return req.Configured, nil
	}

	// The server's /v1/models ordering is not stable between calls, so sort
	// before showing or listing: the same number must mean the same model on
	// the next run. Sort a copy; the caller's slice is not ours to reorder.
	available := append([]string(nil), req.Available...)
	sort.Strings(available)

	switch len(available) {
	case 0:
		return "", errors.New("the LocalAI server has no models installed. Install one with 'local-ai models install <name>', then run 'local-ai chat' again")
	case 1:
		return available[0], nil
	}

	if req.Choose == nil {
		return "", fmt.Errorf(
			"several models are available; pick one with --model. Available: %s",
			strings.Join(available, ", "),
		)
	}

	chosen, err := req.Choose(available)
	if err != nil {
		return "", err
	}
	if req.StateDir != "" {
		if err := PersistModel(req.StateDir, chosen); err != nil {
			// A failure to remember the choice must not block the session: the
			// user picked a model, so honour it and warn.
			xlog.Warn("could not save the model choice", "error", err, "model", chosen)
		}
	}
	return chosen, nil
}

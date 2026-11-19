package schema

// ModelUpgradeRequest is the optional body of POST /api/models/upgrade/{name}.
type ModelUpgradeRequest struct {
	// ConfigOnly applies the new model config and prompt templates and
	// downloads nothing. It fails when the update needs new files.
	ConfigOnly bool `json:"config_only,omitempty"`
	// Choices give, for each key a person edited that the update also
	// changes (the conflicts of GET /api/models/updates), "mine" to keep the
	// edit or "gallery" to take the new value, by dotted key. The upgrade is
	// refused while a conflict has no choice.
	Choices map[string]string `json:"choices,omitempty"`
}

// ModelUpgradeResponse is the answer of the upgrade and retry routes: the
// operation that runs the upgrade.
type ModelUpgradeResponse struct {
	ID        string `json:"id"`
	JobID     string `json:"jobID"`
	StatusURL string `json:"status_url"`
	Message   string `json:"message,omitempty"`
}

// ModelUpgradeError is the answer of a refused upgrade request. Keys lists
// the edited keys that need a choice, for a 409.
type ModelUpgradeError struct {
	Error string   `json:"error"`
	Keys  []string `json:"keys,omitempty"`
}

// ModelHoldRequest is the body of PUT /api/models/{name}/hold.
type ModelHoldRequest struct {
	Hold bool `json:"hold"`
}

// ModelHoldResponse is the answer of PUT /api/models/{name}/hold.
type ModelHoldResponse struct {
	Name string `json:"name"`
	Hold bool   `json:"hold"`
}

// ModelRemoveOldResponse is the answer of POST /api/models/{name}/remove-old.
type ModelRemoveOldResponse struct {
	Name string `json:"name"`
	// Removed are the old files that were deleted, relative to the models
	// directory.
	Removed []string `json:"removed"`
}

// RemovedRouteResponse is the answer of a route that was removed: 410 Gone
// with the route to use instead.
type RemovedRouteResponse struct {
	Error       string `json:"error"`
	Replacement string `json:"replacement"`
}

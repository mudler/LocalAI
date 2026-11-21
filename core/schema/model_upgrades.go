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
// the edited keys that need a choice, for a 409. Candidates lists the gallery
// entries an adoption can take ("gallery@entry"), for a refused adoption.
type ModelUpgradeError struct {
	Error      string   `json:"error"`
	Keys       []string `json:"keys,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

// ModelAdoptRequest is the optional body of POST /api/models/{name}/adopt.
// Without it the entry is found by the model's names, and only when exactly
// one gallery lists it.
type ModelAdoptRequest struct {
	// Gallery names the gallery the model came from.
	Gallery string `json:"gallery,omitempty"`
	// Entry names the gallery entry, when it differs from the model's name.
	Entry string `json:"entry,omitempty"`
}

// ModelAdoptResponse is the answer of POST /api/models/{name}/adopt.
type ModelAdoptResponse struct {
	Name    string `json:"name"`
	Gallery string `json:"gallery"`
	Entry   string `json:"entry"`
	// FilesMatch is true when the model has the files the entry lists now;
	// false means the next check offers an update.
	FilesMatch bool `json:"filesMatch"`
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
	// Kept are the old files that stayed, listed for a later removal. Set
	// when only some files could be removed.
	Kept []string `json:"kept,omitempty"`
	// Errors say why files stayed. Set when only some files could be removed.
	Errors []string `json:"errors,omitempty"`
}

// RemovedRouteResponse is the answer of a route that was removed: 410 Gone
// with the route to use instead.
type RemovedRouteResponse struct {
	Error       string `json:"error"`
	Replacement string `json:"replacement"`
}

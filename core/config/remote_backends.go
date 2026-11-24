package config

import (
	"sort"

	"github.com/mudler/LocalAI/pkg/model"
)

// RemoteBackend describes a backend that serves a model from a remote
// upstream instead of running it in this instance. It is the one place that
// answers "is this backend remote, and what follows from it". Code that
// belongs to a single remote backend (for example its wire-format modes)
// stays with that backend and does not read this registry.
type RemoteBackend struct {
	// Name is the backend name as written in a model config.
	Name string
	// PIIDefault is true when the PII filter is on by default for models on
	// this backend. An explicit pii.enabled in the model config still wins.
	PIIDefault bool
}

var remoteBackends = map[string]RemoteBackend{
	model.CloudProxyBackend:   {Name: model.CloudProxyBackend, PIIDefault: true},
	model.LocalAIProxyBackend: {Name: model.LocalAIProxyBackend, PIIDefault: false},
}

// RemoteBackendFor returns the registry entry for a backend name.
func RemoteBackendFor(name string) (RemoteBackend, bool) {
	b, ok := remoteBackends[name]
	return b, ok
}

// IsRemoteBackend reports whether the backend name is a remote backend.
func IsRemoteBackend(name string) bool {
	_, ok := remoteBackends[name]
	return ok
}

// PIIDefaultBackends returns the names, sorted, of the remote backends that
// have the PII filter on by default.
func PIIDefaultBackends() []string {
	out := []string{}
	for _, b := range remoteBackends {
		if b.PIIDefault {
			out = append(out, b.Name)
		}
	}
	sort.Strings(out)
	return out
}

// PIIDefaultFor reports whether the PII filter is on by default for models
// on the named backend.
func PIIDefaultFor(name string) bool {
	b, ok := remoteBackends[name]
	return ok && b.PIIDefault
}

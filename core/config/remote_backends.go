package config

import (
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/model"
)

// UpstreamURLShape says what a remote backend expects in proxy.upstream_url.
type UpstreamURLShape string

const (
	// UpstreamURLServerRoot means the URL is the root of the upstream server;
	// the backend appends the endpoint path itself.
	UpstreamURLServerRoot UpstreamURLShape = "server_root"
	// UpstreamURLFullEndpoint means the URL is the complete endpoint the
	// request body is posted to.
	UpstreamURLFullEndpoint UpstreamURLShape = "full_endpoint"
)

// RemoteBackend describes a backend that serves a model from a remote
// upstream instead of running it in this instance. It is the one place that
// answers "is this backend remote, and what follows from it". Code that
// belongs to a single remote backend (for example its wire-format modes)
// stays with that backend and does not read this registry.
type RemoteBackend struct {
	// Name is the backend name as written in a model config.
	Name string
	// Label is the human name shown in UIs.
	Label string
	// PIIDefault is true when the PII filter is on by default for models on
	// this backend. An explicit pii.enabled in the model config still wins.
	PIIDefault bool
	// KnownUsecasesRequired is true when the model config must declare
	// known_usecases, because LocalAI cannot guess what a remote model does.
	KnownUsecasesRequired bool
	// UpstreamURLShape says what proxy.upstream_url holds for this backend.
	UpstreamURLShape UpstreamURLShape
	// WarmApplies is true when the failover `warm` option has an effect on a
	// target served by this backend. A remote upstream is not loaded by this
	// instance, so it is false for every entry today.
	WarmApplies bool
}

var remoteBackends = map[string]RemoteBackend{
	model.CloudProxyBackend: {
		Name:                  model.CloudProxyBackend,
		Label:                 "Cloud proxy",
		PIIDefault:            true,
		KnownUsecasesRequired: false,
		UpstreamURLShape:      UpstreamURLFullEndpoint,
		WarmApplies:           false,
	},
	model.LocalAIProxyBackend: {
		Name:                  model.LocalAIProxyBackend,
		Label:                 "LocalAI proxy",
		PIIDefault:            false,
		KnownUsecasesRequired: true,
		UpstreamURLShape:      UpstreamURLServerRoot,
		WarmApplies:           false,
	},
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

// RemoteBackends returns a copy of the registry entries, sorted by name.
func RemoteBackends() []RemoteBackend {
	out := make([]RemoteBackend, 0, len(remoteBackends))
	for _, b := range remoteBackends {
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b RemoteBackend) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// PIIDefaultBackends returns the names, sorted, of the remote backends that
// have the PII filter on by default.
func PIIDefaultBackends() []string {
	out := []string{}
	for _, b := range RemoteBackends() {
		if b.PIIDefault {
			out = append(out, b.Name)
		}
	}
	return out
}

// PIIDefaultFor reports whether the PII filter is on by default for models
// on the named backend.
func PIIDefaultFor(name string) bool {
	b, ok := remoteBackends[name]
	return ok && b.PIIDefault
}

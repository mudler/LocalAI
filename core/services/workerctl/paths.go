// Package workerctl names the HTTP control plane a worker serves to the
// frontends that manage it.
//
// It is a leaf on the standard library alone, and deliberately so: the worker
// registers these paths and the frontend calls them, so both sides must agree
// on the literals without either importing the other's package.
package workerctl

import "encoding/json"

// Prefix is the one path prefix the worker mounts its whole control plane
// under. Everything the frontend may command a worker to do lives below it,
// which is what lets the worker put the control plane behind a single
// authentication check instead of one per verb.
const Prefix = "/v1/control/"

// The control verbs. Each replaces one NATS subject; the request and reply
// bodies are the messaging DTOs those subjects already carried, unchanged, so
// a worker still reachable over NATS and one reachable over the tunnel answer
// with the same bytes.
const (
	PathBackendInstall = "/v1/control/backend/install"
	PathBackendUpgrade = "/v1/control/backend/upgrade"
	PathBackendList    = "/v1/control/backend/list"
	PathBackendStop    = "/v1/control/backend/stop"
	PathBackendDelete  = "/v1/control/backend/delete"
	PathModelStop      = "/v1/control/model/stop"
	PathModelUnload    = "/v1/control/model/unload"
	PathModelDelete    = "/v1/control/model/delete"
	PathModelsRunning  = "/v1/control/models/running"
	PathNodeStop       = "/v1/control/node/stop"

	// The file-staging verbs. They are only ever served by a worker whose
	// deployment configured an object store, because without one there is
	// nothing for them to move a file to or from; a worker without one mounts
	// them not at all, and the catch-all under Prefix answers for them. That is
	// the same 404 an older build gives, which is what the frontend already
	// reads as "this worker does not serve that verb" rather than as absence.
	PathFilesEnsure  = "/v1/control/files/ensure"
	PathFilesStage   = "/v1/control/files/stage"
	PathFilesTemp    = "/v1/control/files/temp"
	PathFilesListDir = "/v1/control/files/listdir"
)

// The verbs an AGENT worker serves.
//
// They live in the same package, under the same prefix and behind the same
// bearer check as the backend worker's, because a worker is a worker to the
// frontend: one control client, one path table, one set of failure meanings.
// What differs is which of them a given worker MOUNTS, which is why the two
// sets are named separately below.
const (
	PathMCPToolExecute = "/v1/control/mcp/tools/execute"
	PathMCPDiscovery   = "/v1/control/mcp/discovery"
	PathAgentExecute   = "/v1/control/agent/execute"
	PathAgentCancel    = "/v1/control/agent/cancel"
	PathMCPCIRun       = "/v1/control/mcp/ci/run"
)

// BackendPaths returns every control verb a BACKEND worker serves.
//
// It exists so a spec can assert a property of the whole set rather than of a
// list it re-types, which would go stale the moment a verb is added.
func BackendPaths() []string {
	return []string{
		PathBackendInstall,
		PathBackendUpgrade,
		PathBackendList,
		PathBackendStop,
		PathBackendDelete,
		PathModelStop,
		PathModelUnload,
		PathModelDelete,
		PathModelsRunning,
		PathNodeStop,
		PathFilesEnsure,
		PathFilesStage,
		PathFilesTemp,
		PathFilesListDir,
	}
}

// AgentPaths returns every control verb an AGENT worker serves.
//
// PathBackendStop is in BOTH sets and that is the point rather than an
// oversight: one path, two implementations, one caller. A backend worker kills
// the process and recycles its port; an agent worker drops the MCP sessions it
// cached for that backend. The frontend issues the same RPC to either and does
// not branch on the node's type to pick a carrier.
//
// PathAgentCancel is named here with nothing mounting it yet. The frontend
// therefore gets the catch-all's 404, which it already reads as "this worker
// does not serve that verb" rather than as absence, and the path is fixed now
// so the two sides cannot disagree about it later.
func AgentPaths() []string {
	return []string{
		PathMCPToolExecute,
		PathMCPDiscovery,
		PathAgentExecute,
		PathAgentCancel,
		PathMCPCIRun,
		PathBackendStop,
	}
}

// AllPaths returns every control verb's path, each once.
//
// It is the union of the two sets above and is what package-wide properties
// (every path under the prefix, no two verbs sharing a path) are asserted
// over. A spec about what a PARTICULAR worker mounts must use BackendPaths or
// AgentPaths instead: asserting mounting over the union would require every
// worker to serve every verb, which is the opposite of what the split is for.
func AllPaths() []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(BackendPaths())+len(AgentPaths()))
	for _, p := range append(BackendPaths(), AgentPaths()...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Envelope is one line of a streaming control response.
//
// Exactly one of the two is set. Zero or more Progress lines are followed by
// exactly ONE Reply line, and the Reply line is the last thing on the body.
// That ordering is the contract: it is what lets the frontend stop reading, and
// it is what replaces the subscribe-before-request dance the NATS carrier
// needed, since progress and reply now share one response and nothing can
// arrive before the caller is listening.
//
// Progress carrying the reply's own bytes is also why the 8000-byte
// notification cap that bounded the NATS progress subject has no analogue here:
// a line is written into the response body the caller is already reading.
type Envelope struct {
	Progress json.RawMessage `json:"progress,omitempty"`
	Reply    json.RawMessage `json:"reply,omitempty"`
}

// ContentTypeStream is the media type of a streaming control response.
const ContentTypeStream = "application/x-ndjson"

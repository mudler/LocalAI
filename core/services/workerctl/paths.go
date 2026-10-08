package workerctl

import (
	"encoding/json"
	"strings"
)

// Prefix is the path prefix under which a worker serves its control verbs over
// HTTP. Everything the frontend may command a worker to do lives below it, so
// the worker can put the whole control plane behind one authentication check.
const Prefix = "/v1/control/"

// The control verbs. The name of a verb is the same on every carrier: NATS maps
// it onto a per-node subject, and the HTTP control plane maps it onto a path
// with PathOf. The request and reply bodies are the payloads of this package,
// so a worker that is reached over NATS and one that is reached over a tunnel
// answer with the same bytes.
const (
	VerbBackendInstall = "backend.install"
	VerbBackendUpgrade = "backend.upgrade"
	VerbBackendStop    = "backend.stop"
	VerbBackendDelete  = "backend.delete"
	VerbBackendList    = "backend.list"
	VerbModelsRunning  = "models.running"
	VerbModelUnload    = "model.unload"
	VerbModelStop      = "model.stop"
	VerbModelDelete    = "model.delete"
	VerbModelOp        = "model.op"
	VerbNodeStop       = "node.stop"
	VerbFilesEnsure    = "files.ensure"
	VerbFilesStage     = "files.stage"
	VerbFilesTemp      = "files.temp"
	VerbFilesListDir   = "files.listdir"
	VerbFilesRelease   = "files.release"
)

// AllVerbs returns every control verb once. A spec that asserts a property of
// the whole set reads it from here, so the list cannot go stale when a verb is
// added.
func AllVerbs() []string {
	return []string{
		VerbBackendInstall, VerbBackendUpgrade, VerbBackendStop, VerbBackendDelete,
		VerbBackendList, VerbModelsRunning, VerbModelUnload, VerbModelStop,
		VerbModelDelete, VerbModelOp, VerbNodeStop,
		VerbFilesEnsure, VerbFilesStage, VerbFilesTemp, VerbFilesListDir, VerbFilesRelease,
	}
}

// PathOf returns the HTTP path of a verb: "backend.install" is
// "/v1/control/backend/install". One function, used by the worker that mounts
// the verb and by the frontend that calls it, so the two cannot disagree.
func PathOf(verb string) string {
	return Prefix + strings.ReplaceAll(verb, ".", "/")
}

// Envelope is one line of a streaming control response.
//
// Exactly one of Progress and Reply is set. Zero or more Progress lines are
// followed by exactly one Reply line, and the Reply line is the last thing in
// the body. A caller that reads to the end of the body without a Reply line
// learned nothing about the work: the link broke.
type Envelope struct {
	Progress json.RawMessage `json:"progress,omitempty"`
	Reply    json.RawMessage `json:"reply,omitempty"`
}

// ContentTypeStream is the media type of a streaming control response.
const ContentTypeStream = "application/x-ndjson"

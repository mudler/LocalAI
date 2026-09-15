package workerctl

import (
	"io"
	"net/http"
	"unicode/utf8"
)

// The rules every worker control verb enforces on a request, and the one answer
// every worker gives for a path it does not serve.
//
// They live here, beside the path table both sides read, because there are now
// TWO kinds of worker mounting verbs under Prefix: a backend worker and an
// agent worker. Phase 3 found the same rule written at three sites in one file
// and pinned at one, which is how a verb ends up reading an unbounded body or
// answering a 500 where the frontend expects a 404. A second worker package
// with its own copy of these rules would be that finding again, across a
// package boundary where it is harder to see.

// MaxRequestBytes bounds a control request body.
//
// The largest real body is BackendInstallRequest.BackendGalleries, a serialized
// gallery list of a few hundred kilobytes. Eight megabytes is therefore not a
// size the protocol needs: it is a defence against a body that never ends,
// arriving on a boundary a worker now serves.
const MaxRequestBytes = 8 << 20

// MaxEchoedPathBytes bounds how much of an unknown control path the 404 body
// repeats back. The path is caller-controlled and the answer exists to be read
// in a log line, so a caller cannot make a worker echo a request-sized string
// into one.
const MaxEchoedPathBytes = 128

// ReadRequestBody enforces the two things every control verb requires of a
// request: that it is a POST, and that its body is bounded. It writes the
// refusal itself and reports false when it did.
//
// A GET is refused rather than served because a control verb is a command, and
// a liveness probe, a link prefetch or a browser address bar must not be able
// to stop a node.
func ReadRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "control verbs are POST only", http.StatusMethodNotAllowed)
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		// A body a worker could not READ is not an answer about any backend, so
		// it must not look like one: 400 is what the frontend maps onto "the
		// request was rejected", never onto "that model is gone".
		http.Error(w, "reading the control request body: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// WriteUnknownPath answers a path under Prefix that this worker mounts no
// handler for.
//
// The body says what happened, because a bare 404 through a tunnel is
// indistinguishable from a proxy fault, and the frontend reads this exact
// status as "this worker does not serve that verb"
// (nodes.ErrWorkerControlUnsupported) rather than as the worker being absent.
func WriteUnknownPath(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "unknown worker control path "+truncate(r.URL.Path, MaxEchoedPathBytes), http.StatusNotFound)
}

// truncate bounds a caller-controlled string that is about to be echoed.
//
// It cuts on a rune boundary. A byte-wise cut can split a multi-byte rune, and
// the half rune then travels as a replacement character through every log and
// UI that reads it; phase 2 shipped exactly that defect on a refusal reason and
// pinned the rule afterwards. utf8.RuneStart is the same predicate the cluster
// package uses for it, so the two are one rule rather than two hand-rolled
// copies that can drift.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

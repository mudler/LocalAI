package workerctl

import (
	"io"
	"net/http"
	"unicode/utf8"
)

// The rules every control verb enforces on a request, and the one answer every
// worker gives for a path under Prefix that it does not serve. They live beside
// the path table so that two kinds of worker, which mount verbs under the same
// prefix, cannot end up with two copies that drift.

// MaxRequestBytes bounds a control request body. The largest real body is
// BackendInstallRequest.BackendGalleries, a serialized gallery list of a few
// hundred kilobytes. Eight megabytes is therefore not a size the protocol
// needs: it is a defence against a body that never ends.
const MaxRequestBytes = 8 << 20

// MaxEchoedPathBytes bounds how much of an unknown control path the 404 body
// repeats. The path is chosen by the caller and the answer exists to be read in
// a log line.
const MaxEchoedPathBytes = 128

// ReadRequestBody enforces what every control verb requires of a request: that
// it is a POST, and that its body is bounded. It writes the refusal itself and
// reports false when it did.
//
// A GET is refused because a control verb is a command, and a liveness probe or
// a link prefetch must not be able to stop a node.
func ReadRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "control verbs are POST only", http.StatusMethodNotAllowed)
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		// A body that the worker could not read is not an answer about any
		// backend. 400 is what the frontend reads as "the request was
		// rejected".
		http.Error(w, "reading the control request body: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// WriteUnknownPath answers a path under Prefix that this worker serves no verb
// on. The body says what happened, because a bare 404 through a tunnel cannot be
// told from a fault of a proxy. The frontend reads this status as "this worker
// does not serve that verb", which is a statement about the worker's version and
// not about any backend.
func WriteUnknownPath(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "unknown worker control path "+truncate(r.URL.Path, MaxEchoedPathBytes), http.StatusNotFound)
}

// truncate bounds a string that a caller chose and that is about to be echoed.
// It cuts on a rune boundary, because a cut inside a multi-byte rune travels as
// a replacement character through every log that reads it.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

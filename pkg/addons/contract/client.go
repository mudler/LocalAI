// SPDX-License-Identifier: MIT

package contract

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// A paired client is a program or box that the owner paired with an instance. An
// add-on can serve it: the add-on declares routes under capabilities.client
// (feature level 7; a tail needs 8), and the host forwards a request of a paired client to one
// of them over the loopback connection of the add-on. The host checks the
// credential of the client, the route, the method, the size and the time; the
// add-on only sees a ClientRequest with the identity of the client, never its
// credential.

const (
	// MaxClientRoutes bounds capabilities.client.routes.
	MaxClientRoutes = 32
	// MaxClientPathBytes and MaxClientPathSegments bound a route path.
	MaxClientPathBytes    = 128
	MaxClientPathSegments = 6
	// MaxClientTailPathBytes and MaxClientTailPathSegments bound the path of a
	// request that matches a route with a wildcard tail ({name...}). A tail
	// carries a path of the add-on's own (a file below a folder), which needs
	// more room than the fixed routes. A declared route path keeps the limits
	// above, and so does a request for a route without a tail.
	MaxClientTailPathBytes    = 256
	MaxClientTailPathSegments = 12
	// DefaultClientBodyBytes is the request body a route takes unless it says
	// otherwise; MaxClientBodyBytes is the most a route may ask for.
	DefaultClientBodyBytes = 64 << 10
	MaxClientBodyBytes     = 1 << 20
	// DefaultClientTimeoutSeconds and MaxClientTimeoutSeconds bound one call of
	// a route that does not stream.
	DefaultClientTimeoutSeconds = 10
	MaxClientTimeoutSeconds     = 60
	// MaxClientQuery bounds the query a request carries: the number of
	// parameters and the length of a name and of a value.
	MaxClientQuery      = 16
	MaxClientQueryName  = 64
	MaxClientQueryValue = 512
	// MaxClientStreamSeconds is how long the host keeps one stream open before
	// it ends it. The client reconnects with its Last-Event-ID.
	MaxClientStreamSeconds = 3600
	// MaxClientEventIDBytes bounds the Last-Event-ID header handed to a route.
	MaxClientEventIDBytes = 128
)

// ClientCapability is capabilities.client.
type ClientCapability struct {
	Routes []ClientRoute `json:"routes"`
}

// ClientRoute is one route that a client may call. The host serves it at
// /api/client/addons/<addon id><path>.
type ClientRoute struct {
	// ID names the route in the ClientRequest.
	ID string `json:"id"`
	// Method is GET, POST, PUT, PATCH or DELETE.
	Method string `json:"method"`
	// Path starts with a slash. A segment is lower case letters, digits, dash
	// or underscore, or {name} for one variable segment. The last segment may
	// be {name...}, which matches one or more segments and gives the add-on
	// the matched part as one slash-joined parameter. The declared path has no
	// dot, no percent escape and no empty segment. A tail value is checked
	// segment by segment by the host (no dot segment, no escape), but the
	// add-on must still validate it before it uses it as a path.
	Path string `json:"path"`
	// Stream makes the route a server-sent event stream. It must be a GET.
	Stream bool `json:"stream,omitempty"`
	// MaxBodyBytes is the largest request body; 0 means DefaultClientBodyBytes.
	MaxBodyBytes int64 `json:"maxBodyBytes,omitempty"`
	// TimeoutSeconds is the time one call may run; 0 means
	// DefaultClientTimeoutSeconds. A stream has no such limit.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

var (
	clientRouteID  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	clientSegment  = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	clientVariable = regexp.MustCompile(`^\{[a-z][a-z0-9_]{0,31}\}$`)
	clientTail     = regexp.MustCompile(`^\{[a-z][a-z0-9_]{0,31}\.\.\.\}$`)
	// clientParamValue is what a variable segment may hold when a request
	// arrives. It has no slash, no percent and no dot-only value.
	clientParamValue = regexp.MustCompile(`^[A-Za-z0-9_.~:-]{1,128}$`)
)

// ClientMethodAllowed reports whether the method is one a route may use.
func ClientMethodAllowed(m string) bool {
	switch m {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// BodyLimit is the request body limit of the route.
func (r ClientRoute) BodyLimit() int64 {
	if r.MaxBodyBytes > 0 {
		return r.MaxBodyBytes
	}
	return DefaultClientBodyBytes
}

// Timeout is the time one call of the route may run.
func (r ClientRoute) Timeout() time.Duration {
	if r.TimeoutSeconds > 0 {
		return time.Duration(r.TimeoutSeconds) * time.Second
	}
	return DefaultClientTimeoutSeconds * time.Second
}

func validateClient(d *ClientCapability, service bool) error {
	if d == nil {
		return nil
	}
	if !service {
		return fmt.Errorf("client routes need the service capability")
	}
	if len(d.Routes) == 0 || len(d.Routes) > MaxClientRoutes {
		return fmt.Errorf("invalid client routes")
	}
	ids := map[string]bool{}
	shapes := map[string]bool{}
	for _, r := range d.Routes {
		if !clientRouteID.MatchString(r.ID) || ids[r.ID] {
			return fmt.Errorf("invalid client route id")
		}
		ids[r.ID] = true
		if !ClientMethodAllowed(r.Method) {
			return fmt.Errorf("invalid client route method")
		}
		if r.Stream && r.Method != http.MethodGet {
			return fmt.Errorf("a client stream route must be a GET")
		}
		segs, err := splitClientPath(r.Path)
		if err != nil {
			return err
		}
		// Two routes whose paths differ only in variable names would match the
		// same request.
		shape := make([]string, len(segs))
		for i, s := range segs {
			shape[i] = s
			switch {
			case clientVariable.MatchString(s):
				shape[i] = "{}"
			case clientTail.MatchString(s):
				shape[i] = "{...}"
			}
		}
		key := r.Method + " /" + strings.Join(shape, "/")
		if shapes[key] {
			return fmt.Errorf("two client routes match the same requests")
		}
		shapes[key] = true
		if r.MaxBodyBytes < 0 || r.MaxBodyBytes > MaxClientBodyBytes || (r.Method == http.MethodGet || r.Method == http.MethodDelete) && r.MaxBodyBytes != 0 {
			return fmt.Errorf("invalid client route body limit")
		}
		if r.TimeoutSeconds < 0 || r.TimeoutSeconds > MaxClientTimeoutSeconds || r.Stream && r.TimeoutSeconds != 0 {
			return fmt.Errorf("invalid client route timeout")
		}
	}
	return nil
}

// splitClientPath validates a route path and returns its segments. "/" has none.
func splitClientPath(p string) ([]string, error) {
	if p == "/" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") || len(p) > MaxClientPathBytes {
		return nil, fmt.Errorf("invalid client route path")
	}
	segs := strings.Split(p[1:], "/")
	if len(segs) > MaxClientPathSegments {
		return nil, fmt.Errorf("invalid client route path")
	}
	names := map[string]bool{}
	for i, s := range segs {
		switch {
		case clientSegment.MatchString(s):
		case clientVariable.MatchString(s), clientTail.MatchString(s) && i == len(segs)-1:
			name := strings.TrimSuffix(s[1:len(s)-1], "...")
			if names[name] {
				return nil, fmt.Errorf("invalid client route path")
			}
			names[name] = true
		default:
			return nil, fmt.Errorf("invalid client route path")
		}
	}
	return segs, nil
}

// hasClientTail reports whether the last segment of a route is a wildcard tail.
func hasClientTail(segs []string) bool {
	return len(segs) > 0 && clientTail.MatchString(segs[len(segs)-1])
}

// MatchClientRoute finds the declared route for a request. path is the part
// after the add-on id and must be exactly what the client sent: a path with a
// percent escape, a dot segment, an empty segment, a backslash or a byte
// outside printable ASCII matches nothing. A trailing slash is not ignored.
// The result is false when no route matches the path; methodMismatch is true
// when a route matches the path with another method.
//
// A route without a tail wins over a route with one, whatever their order in
// the manifest. Among routes with a tail, the one with the longer fixed part
// wins. A request for a route without a tail keeps MaxClientPathBytes and
// MaxClientPathSegments; only a route with a tail takes the larger
// MaxClientTailPath* limits.
func MatchClientRoute(routes []ClientRoute, method, path string) (route ClientRoute, params map[string]string, found, methodMismatch bool) {
	if path == "" || path[0] != '/' || len(path) > MaxClientTailPathBytes {
		return
	}
	var segs []string
	if path != "/" {
		segs = strings.Split(path[1:], "/")
	}
	if len(segs) > MaxClientTailPathSegments {
		return
	}
	fixedOK := len(path) <= MaxClientPathBytes && len(segs) <= MaxClientPathSegments
	bestLen := -1
	for _, r := range routes {
		rsegs, err := splitClientPath(r.Path)
		if err != nil {
			continue
		}
		var p map[string]string
		var ok bool
		tail := hasClientTail(rsegs)
		switch {
		case tail:
			p, ok = matchClientTail(rsegs, segs)
		case fixedOK && len(rsegs) == len(segs):
			p, ok = matchClientFixed(rsegs, segs)
		}
		if !ok {
			continue
		}
		if r.Method != method {
			methodMismatch = true
			continue
		}
		if !tail {
			return r, p, true, false
		}
		if len(rsegs) > bestLen {
			route, params, bestLen = r, p, len(rsegs)
		}
	}
	if bestLen >= 0 {
		return route, params, true, false
	}
	return
}

// clientParamOK is the rule for the value of a variable segment, and for each
// part of a tail.
func clientParamOK(s string) bool {
	return clientParamValue.MatchString(s) && s != "." && s != ".."
}

// matchClientFixed matches the segments of a request against a route of the
// same length, without a tail.
func matchClientFixed(rsegs, segs []string) (map[string]string, bool) {
	p := map[string]string{}
	for i, s := range rsegs {
		if clientVariable.MatchString(s) {
			if !clientParamOK(segs[i]) {
				return nil, false
			}
			p[s[1:len(s)-1]] = segs[i]
		} else if s != segs[i] {
			return nil, false
		}
	}
	return p, true
}

// matchClientTail matches a route whose last segment is {name...}. The tail
// takes one or more segments, each of which follows the rule of a variable
// segment, and its parameter is the segments joined with a slash.
func matchClientTail(rsegs, segs []string) (map[string]string, bool) {
	fixed := rsegs[:len(rsegs)-1]
	if len(segs) <= len(fixed) {
		return nil, false
	}
	p, ok := matchClientFixed(fixed, segs[:len(fixed)])
	if !ok {
		return nil, false
	}
	rest := segs[len(fixed):]
	for _, s := range rest {
		if !clientParamOK(s) {
			return nil, false
		}
	}
	last := rsegs[len(rsegs)-1]
	p[strings.TrimSuffix(last[1:len(last)-1], "...")] = strings.Join(rest, "/")
	return p, true
}

// ClientIdentity says which paired client made a request. It is the id and the
// label of the client and nothing that works as a credential.
type ClientIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ClientCredentialPrefix starts the credential id of the actor of a client
// call: "client:" and the id of the client key. The host puts the actor into
// its own record of the live call; host operations read the client from there,
// never from an argument.
const ClientCredentialPrefix = "client:"

// ClientRequest is what the host sends to the client route of an add-on.
type ClientRequest struct {
	// OperationID names the call. The add-on passes it with every host
	// operation it makes while it serves the call (see hostclient.Do); the host
	// finds the client in its own record of the call.
	OperationID string            `json:"operationId,omitempty"`
	RouteID     string            `json:"routeId"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Params      map[string]string `json:"params,omitempty"`
	Query       map[string]string `json:"query,omitempty"`
	// LastEventID is the Last-Event-ID header of a stream request.
	LastEventID string `json:"lastEventId,omitempty"`
	// ContentType is application/json when a body is present.
	ContentType string         `json:"contentType,omitempty"`
	Body        []byte         `json:"body,omitempty"`
	Client      ClientIdentity `json:"client"`
}

// ClientResponse is the answer of a route that does not stream. The host
// passes the status, a content type from a short list and the body; it never
// passes a header of the add-on.
type ClientResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// ClientStatusAllowed reports whether the host passes the status to a client.
// Redirects, authentication challenges and 5xx are the host's own words.
func ClientStatusAllowed(s int) bool {
	switch s {
	case 200, 201, 202, 204, 400, 404, 409, 410, 413, 422, 429:
		return true
	}
	return false
}

// ClientContentTypes are the content types of a client response.
var ClientContentTypes = []string{"application/json", "text/plain; charset=utf-8"}

// Validate checks a response against the limits of the host.
func (r ClientResponse) Validate(l Limits) error {
	if !ClientStatusAllowed(r.Status) || int64(len(r.Body)) > l.PayloadBytes {
		return fmt.Errorf("invalid client response")
	}
	if len(r.Body) > 0 {
		ok := false
		for _, c := range ClientContentTypes {
			ok = ok || r.ContentType == c
		}
		if !ok || r.Status == 204 {
			return fmt.Errorf("invalid client response")
		}
	}
	return nil
}

// ValidateClientQuery checks the query of a client request against the bounds.
func ValidateClientQuery(q map[string]string) error {
	if len(q) > MaxClientQuery {
		return fmt.Errorf("too many query parameters")
	}
	for k, v := range q {
		if len(k) > MaxClientQueryName || len(v) > MaxClientQueryValue || !printable(k) || !printable(v) {
			return fmt.Errorf("invalid query parameter")
		}
	}
	return nil
}

func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ValidEventID reports whether a Last-Event-ID is acceptable: printable ASCII
// without a line break, up to MaxClientEventIDBytes.
func ValidEventID(id string) bool { return len(id) <= MaxClientEventIDBytes && printable(id) }

// ClientRuntime is a runtime that serves the client routes it declared. The
// add-on implements it; the host's client implements it by calling the add-on.
type ClientRuntime interface {
	// ClientCall answers a route that does not stream.
	ClientCall(ctx context.Context, req ClientRequest) (ClientResponse, error)
	// ClientStream opens a route that streams. The body is text/event-stream
	// and ends when the add-on closes it or ctx is done.
	ClientStream(ctx context.Context, req ClientRequest) (io.ReadCloser, error)
}

// ClientCaller is the host's side: it calls a client route on behalf of the
// actor of a client. The runtime client records the live call under the actor,
// so the host operations of the add-on during the call run as that client and
// nothing else.
type ClientCaller interface {
	ClientCallAs(ctx context.Context, actor Actor, req ClientRequest) (ClientResponse, error)
	ClientStreamAs(ctx context.Context, actor Actor, req ClientRequest) (io.ReadCloser, error)
}

// ClientRouteSource gives the host the declared routes of a running add-on.
type ClientRouteSource interface {
	ClientRoutes() []ClientRoute
}

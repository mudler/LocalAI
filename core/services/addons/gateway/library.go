// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The operations of a library call. Each one is a carrier verb with the action
// "library-<op>". The first four sources verbs and the two backend verbs are
// reads; every other verb is a mutation, so a reply lost on the way is an
// unknown outcome with its operation ID.
const (
	LibraryList            = "list"
	LibraryAdd             = "add"
	LibraryUpdate          = "update"
	LibraryEnable          = "enable"
	LibraryDisable         = "disable"
	LibraryRemove          = "remove"
	LibraryBackendResolve  = "backend-resolve"
	LibraryBackendReleases = "backend-releases"
)

// maxLibraryRefs bounds the backends of one backend-releases call.
const maxLibraryRefs = 256

// LibraryRef names one library backend in a backend-releases call.
type LibraryRef struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
}

// LibraryCall asks the owner for a library operation: a change of the library
// sources, or the owner's half of a backend install. Only the fields of the
// operation are set.
type LibraryCall struct {
	Op string `json:"op"`
	// ID names the source of update, enable, disable and remove.
	ID string `json:"id,omitempty"`
	// Kind, URL, Name, Format and Mirrors describe the source of add; update
	// takes Mirrors.
	Kind    string   `json:"kind,omitempty"`
	URL     string   `json:"url,omitempty"`
	Name    string   `json:"name,omitempty"`
	Format  string   `json:"format,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
	// Known is the list version the caller holds (list only).
	Known string `json:"known,omitempty"`
	// SourceID, Backend and Version name the entry of backend-resolve.
	// Capability is the capability the installing host reports, which picks the
	// backend of a meta backend.
	SourceID   string `json:"sourceId,omitempty"`
	Backend    string `json:"backend,omitempty"`
	Version    string `json:"version,omitempty"`
	Capability string `json:"capability,omitempty"`
	// Refs are the backends of backend-releases, at most 256.
	Refs []LibraryRef `json:"refs,omitempty"`
}

func (c LibraryCall) wireAction() string { return "library-" + c.Op }

func (c LibraryCall) valid() bool {
	switch c.Op {
	case LibraryList, LibraryAdd, LibraryUpdate, LibraryEnable, LibraryDisable, LibraryRemove, LibraryBackendResolve:
		return len(c.Refs) == 0
	case LibraryBackendReleases:
		return len(c.Refs) <= maxLibraryRefs
	}
	return false
}

// libraryRead are the library actions that change nothing.
var libraryRead = map[string]bool{"library-" + LibraryList: true, "library-" + LibraryBackendResolve: true, "library-" + LibraryBackendReleases: true}

// The error types of a LibraryError. The frontend maps each to the sentinel the
// owner's own routes would have seen, so both answer with the same status.
const (
	LibraryNotFound   = "not_found"
	LibraryExists     = "exists"
	LibraryDuplicate  = "duplicate"
	LibraryInvalid    = "invalid"
	LibraryBadRequest = "bad_request"
	LibraryInternal   = "internal"
)

// LibraryError is a refusal the owner decided. It travels in the reply envelope
// with status 200, because typedError would map several of these to other
// statuses than the routes answer.
type LibraryError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *LibraryError) Error() string { return e.Message }

// libraryReply is the reply of a library call: data or error.
type libraryReply struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error *LibraryError   `json:"error,omitempty"`
}

// LibraryAdmin is the owner's side of a library call. core/library imports this
// package through core/config, so the application implements it. Library runs
// after the verifier checked the signature, the audience and the actor, and it
// must check the administrator right of the actor again. Snapshot is the
// peer-authenticated read of the gallery-relevant source list and has no actor.
type LibraryAdmin interface {
	Library(ctx context.Context, actor contract.Actor, call LibraryCall) (json.RawMessage, error)
	Snapshot(ctx context.Context, known string) (json.RawMessage, error)
}

// UnavailableError is an unavailable answer that says why. It matches
// ErrUnavailable.
type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return e.Reason }
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// UnavailableReason returns the message for the 503 of an unavailable error:
// its own reason when it has one, else the unreachable owner.
func UnavailableReason(e error) string {
	var u *UnavailableError
	if errors.As(e, &u) {
		return u.Reason
	}
	var unknown *OutcomeUnknown
	if errors.As(e, &unknown) {
		return "the add-on owner did not confirm the change (operation " + unknown.OperationID + "); read the list again"
	}
	return ReasonOwnerUnreached
}

// Reasons of the library calls that the owner cannot serve.
const (
	ReasonLibraryUnsupported = "the add-on owner does not support library operations; upgrade the owner"
	ReasonPlatformMismatch   = "this instance and the add-on owner run on different operating systems or architectures"
)

// Capabilities is the answer of GET /addons/capabilities, a separate call from
// the audience, whose reply an older frontend decodes strictly. Protocol 1 is an
// owner that answers 404.
type Capabilities struct {
	Protocol int      `json:"protocol"`
	Calls    []string `json:"calls"`
	// OS and Arch are the platform the owner resolves libraries for.
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

func (c Capabilities) Supports(call string) bool {
	for _, s := range c.Calls {
		if s == call {
			return true
		}
	}
	return false
}

func (g *Gateway) capabilities() Capabilities {
	c := Capabilities{Protocol: 2, OS: g.OS, Arch: g.Arch}
	if c.OS == "" {
		c.OS = runtime.GOOS
	}
	if c.Arch == "" {
		c.Arch = runtime.GOARCH
	}
	if g.Libraries != nil {
		c.Calls = append(c.Calls, "library")
	}
	return c
}

// libraryLocal runs a library call on the owner and writes the reply envelope.
func (g *Gateway) libraryLocal(ctx context.Context, w http.ResponseWriter, actor contract.Actor, call LibraryCall) {
	if g.Libraries == nil {
		typedError(w, ErrUnavailable)
		return
	}
	raw, e := g.Libraries.Library(ctx, actor, call)
	var le *LibraryError
	switch {
	case e == nil:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(libraryReply{Data: raw})
	case errors.As(e, &le):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(libraryReply{Error: le})
	default:
		typedError(w, e)
	}
}

// capsCache keeps the owner's capabilities for one owner audience.
type capsCache struct {
	mu       sync.Mutex
	audience string
	caps     Capabilities
}

// Capabilities asks the owner what it serves. The answer is kept for the
// owner's current audience, so it is asked again after the owner restarts. An
// owner that answers 404 is protocol 1.
func (c *Carrier) Capabilities(ctx context.Context) (Capabilities, error) {
	audience, e := c.audience(ctx)
	if e != nil {
		return Capabilities{}, e
	}
	c.caps.mu.Lock()
	defer c.caps.mu.Unlock()
	if c.caps.audience == audience {
		return c.caps.caps, nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", c.config.OwnerAddress+"/addons/capabilities", nil)
	if e != nil {
		return Capabilities{}, ErrUnavailable
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return Capabilities{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	var caps Capabilities
	switch {
	case resp.StatusCode == http.StatusNotFound:
		caps = Capabilities{Protocol: 1}
	case resp.StatusCode != http.StatusOK:
		return Capabilities{}, ErrUnavailable
	default:
		if decode(io.LimitReader(resp.Body, 4096), &caps) != nil || caps.Protocol < 1 {
			return Capabilities{}, ErrUnavailable
		}
	}
	c.caps.audience, c.caps.caps = audience, caps
	return caps, nil
}

// Library forwards one library call to the owner and returns the data of its
// reply. A refusal the owner decided is a *LibraryError. The caller checks
// Capabilities first.
func (c *Carrier) Library(ctx context.Context, a contract.Actor, call LibraryCall) (json.RawMessage, error) {
	if !call.valid() {
		return nil, ErrForbidden
	}
	id, e := newResourceOperationID()
	if e != nil {
		return nil, ErrUnavailable
	}
	resp, e := c.send(ctx, a, wireRequest{Request: contract.Request{OperationID: id, Action: call.wireAction()}, Library: &call})
	if e != nil {
		return nil, e
	}
	defer func() { _ = resp.Body.Close() }()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	var reply libraryReply
	if e != nil || len(raw) > 2<<20 || decode(bytes.NewReader(raw), &reply) != nil {
		if mutation(call.wireAction()) {
			return nil, &OutcomeUnknown{OperationID: id}
		}
		return nil, ErrUnavailable
	}
	if reply.Error != nil {
		return nil, reply.Error
	}
	return reply.Data, nil
}

// LibrarySnapshot asks the owner for the source list that decides the
// galleries, as the owner's peer-authenticated call: it carries no actor, so a
// frontend can poll it without a signed-in user. known is the version the
// caller holds; an unchanged list is answered without sources.
func (c *Carrier) LibrarySnapshot(ctx context.Context, known string) (json.RawMessage, error) {
	if _, e := c.audience(ctx); e != nil {
		return nil, e
	}
	u := c.config.OwnerAddress + "/addons/library/snapshot"
	if known != "" {
		u += "?known=" + url.QueryEscape(known)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, ErrUnavailable
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil || len(raw) > 2<<20 {
		return nil, ErrUnavailable
	}
	return raw, nil
}

// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Pins bind deployment identities to exact leaf certificates, in addition to CA
// verification. No proxy, redirect, environment discovery, or owner failover.
type CarrierConfig struct {
	Limits                            RelayLimits
	InstanceID, OwnerID, OwnerAddress string
	Certificate                       tls.Certificate
	Roots                             *x509.CertPool
	Pins                              map[string][32]byte
	SigningKey                        ed25519.PrivateKey
	VerificationKeys                  map[string]ed25519.PublicKey
	// ReadTimeout bounds a quick read of the owner (the apps list, the host
	// grants overview, the library list). Zero means DefaultReadTimeout.
	ReadTimeout time.Duration
}
type Carrier struct {
	config CarrierConfig
	client *http.Client
	long   *http.Client // for action calls, which have no overall timeout
	caps   capsCache
}
type OutcomeUnknown struct {
	OperationID string `json:"operationId"`
}

func (e *OutcomeUnknown) Error() string { return "addon mutation outcome unknown" }
func (e *OutcomeUnknown) Unwrap() error { return ErrUnavailable }

// validSigningKey reports whether the signing key fits the role. A frontend
// signs every request it forwards and needs a full key. The owner never signs
// outbound requests, so it needs none; a key it has must still be well formed.
func validSigningKey(c CarrierConfig) bool {
	if len(c.SigningKey) == 0 {
		return c.InstanceID == c.OwnerID
	}
	return len(c.SigningKey) == ed25519.PrivateKeySize
}

func NewCarrier(c CarrierConfig) (*Carrier, error) {
	limits, e := c.Limits.resolved()
	if e != nil {
		return nil, e
	}
	c.Limits = limits
	u, e := url.Parse(c.OwnerAddress)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || c.InstanceID == "" || c.OwnerID == "" || c.Roots == nil || len(c.Certificate.Certificate) == 0 || !validSigningKey(c) {
		return nil, ErrForbidden
	}
	if _, ok := c.Pins[c.OwnerID]; !ok {
		return nil, ErrForbidden
	}
	if _, ok := c.Pins[c.InstanceID]; !ok {
		return nil, ErrForbidden
	}
	pins := make(map[string][32]byte)
	for k, v := range c.Pins {
		pins[k] = v
	}
	c.Pins = pins
	c.SigningKey = append(ed25519.PrivateKey(nil), c.SigningKey...)
	t := httpclient.HardenedTransport()
	t.Proxy = nil
	t.DisableKeepAlives = true
	t.MaxResponseHeaderBytes = 16 << 10
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: c.Roots, Certificates: []tls.Certificate{c.Certificate}, VerifyConnection: func(s tls.ConnectionState) error {
		if len(s.VerifiedChains) == 0 || len(s.PeerCertificates) == 0 || sha256.Sum256(s.PeerCertificates[0].Raw) != pins[c.OwnerID] {
			return ErrForbidden
		}
		return nil
	}}
	h := httpclient.New(httpclient.WithTransport(t))
	h.Timeout = 65 * time.Second
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrForbidden }
	// An action call can run for minutes: its client has no overall timeout and
	// its context carries the bound.
	long := httpclient.New(httpclient.WithTransport(t))
	long.CheckRedirect = h.CheckRedirect
	return &Carrier{config: c, client: h, long: long}, nil
}
func (c *Carrier) ServerTLS() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: c.config.Roots, Certificates: []tls.Certificate{c.config.Certificate}}
}
func (c *Carrier) peer(r *http.Request) (PeerIdentity, error) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return PeerIdentity{}, ErrForbidden
	}
	pin := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	id := ""
	for k, v := range c.config.Pins {
		if v == pin {
			if id != "" {
				return PeerIdentity{}, ErrForbidden
			}
			id = k
		}
	}
	if id == "" {
		return PeerIdentity{}, ErrForbidden
	}
	return PeerIdentity{ID: id, Verified: true}, nil
}

type wireRequest struct {
	ExecutionBinding *executionBindingCall   `json:"executionBinding,omitempty"`
	Observation      *ExecutionStatusRequest `json:"observation,omitempty"`
	Request          contract.Request        `json:"request"`
	Relay            *RelayRequest           `json:"relay,omitempty"`
	// Proposal marks a call for the owner's proposal broker. Such a call is
	// never a typed dispatch.
	Proposal *ProposalCall `json:"proposal,omitempty"`
	// Artifact marks a read-only lookup in the owner's artifact store.
	Artifact *artifactCall `json:"artifact,omitempty"`
	// Apps marks a read of the apps list.
	Apps *appsCall `json:"apps,omitempty"`
	// Resource marks a UI-only resource management call: listing, creation,
	// grants, rename, deletion and conversation selections. Such a call is
	// never a typed dispatch.
	Resource *ResourceCall `json:"resource,omitempty"`
	// Library marks a library call: a change of the library sources, or the
	// owner's half of a backend install. Such a call is never a typed dispatch.
	Library *LibraryCall `json:"library,omitempty"`
	// HostGrants marks a host access call (grants, deny rules, blanket
	// approval). Such a call is never a typed dispatch.
	HostGrants *HostGrantsCall `json:"hostGrants,omitempty"`
	// OAuth marks an OAuth login or client call. Such a call is never a
	// typed dispatch.
	OAuth *OAuthCall `json:"oauth,omitempty"`
}

// exclusive reports whether exactly one routing payload is set. Each branch of
// OwnerHandler serves one payload and refuses a request that also carries
// another, since the owner classifies by the call and not by what the peer
// claims.
func (w wireRequest) exclusive() bool {
	set := 0
	for _, present := range []bool{w.ExecutionBinding != nil, w.Observation != nil, w.Relay != nil, w.Proposal != nil, w.Artifact != nil, w.Apps != nil, w.Resource != nil, w.Library != nil, w.HostGrants != nil, w.OAuth != nil} {
		if present {
			set++
		}
	}
	return set == 1
}

func decode(r io.Reader, dst any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		return e
	}
	var more any
	if d.Decode(&more) != io.EOF {
		return ErrForbidden
	}
	return nil
}

// OwnerHandler creates its verifier internally so a caller cannot reuse an old
// audience across restarts. Audience bootstrap itself requires pinned mTLS.
func (c *Carrier) OwnerHandler(g *Gateway) (http.Handler, error) {
	if c.config.InstanceID != c.config.OwnerID || g == nil {
		return nil, ErrForbidden
	}
	v, e := NewForwardVerifier(VerifierConfig{Keys: c.config.VerificationKeys, Revalidate: g.Revalidate, Account: g.revalidateAccount})
	if e != nil {
		return nil, e
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, e := c.peer(r)
		if e != nil {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if g.serveConnectors(w, r) {
			return
		}
		if r.Method == "GET" && r.URL.Path == "/addons/audience" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Audience string `json:"audience"`
			}{v.Audience()})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/addons/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(g.capabilities())
			return
		}
		if r.Method == "GET" && r.URL.Path == "/addons/host-settings" {
			// Only pinned host peers reach this read-only internal route.
			if g.Forward != nil {
				typedError(w, ErrUnavailable)
				return
			}
			s, err := g.HostSettingsInternalSnapshot(r.Context())
			if err != nil {
				typedError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(hostSettingsEnvelope{Data: &s})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/addons/models-probe" {
			var p modelsprobe.Probe
			ok := false
			if g.ModelsProbe != nil {
				p, ok = g.ModelsProbe()
			}
			if !ok {
				typedError(w, ErrUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/addons/library/snapshot" {
			if g.Libraries == nil {
				typedError(w, ErrUnavailable)
				return
			}
			raw, e := g.Libraries.Snapshot(r.Context(), r.URL.Query().Get("known"))
			if e != nil {
				typedError(w, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/addons/library/system" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			var call LibraryCall
			if decode(r.Body, &call) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			g.librarySystemLocal(r.Context(), w, call)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/addons/forward" {
			http.Error(w, "not found", 404)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
		var m ForwardedRequest
		if decode(r.Body, &m) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		actor, e := v.Verify(r.Context(), peer, m)
		if e != nil {
			http.Error(w, "forbidden", StatusFor(e))
			return
		}
		// The chat the frontend bound the call to rides with the request
		// context, so the add-on's host operations act with the chat's
		// access. The signature covers it.
		if b, ok := m.Binding(); ok {
			r = r.WithContext(hostops.WithBinding(r.Context(), b))
		}
		var wire wireRequest
		if decode(bytes.NewReader(m.Body), &wire) != nil || wire.Request.OperationID != m.OperationID || (wire.Relay != nil && wire.Relay.Request.OperationID != m.OperationID) {
			http.Error(w, "invalid binding", 400)
			return
		}
		if wire.ExecutionBinding != nil {
			// New binding messages use exact canonical keys, including the
			// routing envelope. Older ordinary messages keep their decoder.
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(m.Body, &fields)
			if observationObject(m.Body, "request", "executionBinding") != nil || observationObject(fields["request"], "action", "operationId", "addonId", "resourceId", "generation", "arguments") != nil {
				http.Error(w, "invalid execution binding keys", 400)
				return
			}
			if !wire.exclusive() || wire.Request.Action != executionBindingAction || wire.Request.AddonID != "" || wire.Request.ResourceID != "" || wire.Request.Generation != 0 || (len(wire.Request.Arguments) != 0 && string(wire.Request.Arguments) != "null") {
				http.Error(w, "invalid execution binding", 400)
				return
			}
			out, err := g.BindingForActor(r.Context(), actor, wire.ExecutionBinding.Claim)
			if err != nil {
				typedError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(executionBindingReply{Version: 1, Binding: out})
			return
		}
		// Reserved actions cannot fall through to ordinary tool dispatch.
		if wire.Request.Action == executionBindingAction {
			http.Error(w, "missing execution binding", 400)
			return
		}
		if wire.Observation != nil {
			if !wire.exclusive() || wire.Request.Action != executionStatusAction || wire.Request.AddonID != "" || wire.Request.ResourceID != "" || wire.Request.Generation != 0 || (len(wire.Request.Arguments) != 0 && string(wire.Request.Arguments) != "null") {
				http.Error(w, "invalid observation binding", 400)
				return
			}
			out, err := g.ExecutionStatusRead(r.Context(), actor, *wire.Observation)
			if err != nil {
				typedError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if wire.HostGrants != nil {
			call := *wire.HostGrants
			// The owner classifies by the call, not by the action the peer wrote.
			if !wire.exclusive() || !call.valid() || wire.Request.Action != call.wireAction() {
				http.Error(w, "invalid binding", 400)
				return
			}
			// The human mark is the signed field of this request, honored for
			// a host access call only.
			hctx := r.Context()
			if m.HumanSurface {
				hctx = WithHumanSurface(hctx)
			}
			g.hostGrantsLocalReply(hctx, w, actor, call)
			return
		}
		if wire.OAuth != nil {
			call := *wire.OAuth
			// The owner classifies by the call, not by the action the peer wrote.
			if !wire.exclusive() || !call.valid() || wire.Request.Action != call.wireAction() {
				http.Error(w, "invalid binding", 400)
				return
			}
			g.oauthLocalReply(r.Context(), w, actor, call)
			return
		}
		if wire.Library != nil {
			call := *wire.Library
			// The owner classifies by the call, not by the action the peer wrote.
			if !wire.exclusive() || !call.valid() || call.System || wire.Request.Action != call.wireAction() {
				http.Error(w, "invalid binding", 400)
				return
			}
			g.libraryLocal(r.Context(), w, actor, call)
			return
		}
		if wire.Resource != nil {
			call := *wire.Resource
			if !wire.exclusive() || wire.Request.Action != call.wireAction() {
				http.Error(w, "invalid binding", 400)
				return
			}
			raw, e := g.resourceLocal(r.Context(), actor, wire.Request, call)
			if e != nil {
				resourceFailure(w, call, wire.Request.OperationID, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		if wire.Apps != nil {
			if !wire.exclusive() || wire.Request.Action != appsListAction {
				http.Error(w, "invalid binding", 400)
				return
			}
			list, e := g.appsLocal(r.Context(), actor)
			if e != nil {
				typedError(w, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(newAppsWire(list))
			return
		}
		if wire.Artifact != nil {
			call := *wire.Artifact
			if !wire.exclusive() || call.Op != artifactStatus || call.ID == "" || wire.Request.OperationID != call.ID {
				http.Error(w, "invalid binding", 400)
				return
			}
			wire.Request.Action = "artifact-" + call.Op
			info, e := g.artifactStatusLocal(r.Context(), actor, call.ID)
			if e != nil {
				typedError(w, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(info)
			return
		}
		if wire.Proposal != nil {
			if !wire.exclusive() {
				http.Error(w, "invalid binding", 400)
				return
			}
			call := *wire.Proposal
			// The owner classifies by the call, not by the action the peer wrote.
			wire.Request.Action = call.wireAction(wire.Request)
			if call.Op == proposalPending {
				list, e := g.pendingLocal(r.Context(), actor, wire.Request, call)
				if e != nil {
					proposalFailure(w, call, wire.Request.OperationID, e)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(list)
				return
			}
			if call.Op == proposalList {
				list, e := g.listLocal(r.Context(), actor, wire.Request, call)
				if e != nil {
					proposalFailure(w, call, wire.Request.OperationID, e)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(list)
				return
			}
			p, e := g.proposalLocal(r.Context(), actor, wire.Request, call)
			if e != nil {
				proposalFailure(w, call, wire.Request.OperationID, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if wire.Relay != nil {
			tw := &trackedWriter{ResponseWriter: w}
			if e = g.Relay(r.Context(), actor, *wire.Relay, tw); e != nil {
				// Once a byte is committed the status cannot change: abort so the
				// receiving frontend sees a truncated stream, never a clean end.
				if tw.committed {
					panic(http.ErrAbortHandler)
				}
				// Before the first byte the denial is a typed status that the
				// receiving frontend decodes back, so every frontend reports the
				// same error for the same owner decision.
				for _, h := range []string{"Content-Type", "Content-Disposition", "Content-Length"} {
					w.Header().Del(h)
				}
				typedError(w, e)
			}
			return
		}
		call := g.Dispatch
		if wire.Request.Action == "act" {
			// The owner runs an action call it was forwarded, for the actor
			// the signature binds; Dispatch refuses the action.
			call = g.Act
			// The server's write timeout is sized for short calls. An action
			// may run up to ActTimeout, and its answer would be cut off on a
			// connection that already expired, which the frontend would then
			// report as an unknown outcome.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(contract.ActTimeout + time.Minute))
		}
		result, e := call(r.Context(), actor, wire.Request)
		if e != nil {
			typedError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}), nil
}

// denials are authorization, policy and request-level rejections.
var denials = []error{contract.ErrRefused, ErrForbidden, recovery.ErrForbidden, addons.ErrForbidden, addons.ErrStaleGeneration, catalog.ErrForbidden, addons.ErrConflict, addons.ErrOperationNotFound, addons.ErrOperationFailed, addons.ErrStaleInstalled, addons.ErrCandidateChanged, addons.ErrTransitionConflict, addons.ErrIncompatible, addons.ErrIntegrity, addons.ErrCheckFailed, catalog.ErrNotFound, catalog.ErrConflict, catalog.ErrShadowed, catalog.ErrInvalid, packages.ErrPackage}

// unavailable are operational failures. They say the owner could not serve the
// request, not that it ran nothing: the owner can return them after it recorded
// or applied a mutation. They never prove that a mutation did not execute.
var unavailable = []error{ErrUnavailable, addonruntime.ErrRuntime, addons.ErrNotOwner, addons.ErrRecoveryRequired, addons.ErrOwnerBusy, io.ErrUnexpectedEOF, catalog.ErrUnavailable, catalog.ErrCredential, catalog.ErrRefresh}

func matches(e error, set []error) bool {
	for _, c := range set {
		if errors.Is(e, c) {
			return true
		}
	}
	return false
}

// StatusFor is the one mapping from a gateway error to an HTTP status. The owner
// uses it for typed responses and the HTTP endpoints use it for local errors, so
// the same decision has the same status from every frontend. Only an explicit
// denial is 403. An error that wraps both a denial and an unavailable cause is
// 503: the check for unavailability comes first, so the answer fails closed. Every other error, including an unclassified one, is
// unavailable: an unknown failure is never reported as success or as a policy
// decision.
func StatusFor(e error) int {
	var limited *contract.ErrRateLimited
	if errors.As(e, &limited) {
		return http.StatusTooManyRequests
	}
	if e != nil && !matches(e, unavailable) && matches(e, denials) {
		return http.StatusForbidden
	}
	return http.StatusServiceUnavailable
}

// Reasons that WriteUnavailable puts in the body, so a client can tell a
// disabled feature from an absent owner.
const (
	ReasonAddonsOff       = "add-ons are turned off on this instance"
	ReasonOwnerUnreached  = "the add-on owner is not reachable"
	ReasonLibraryOnOwner  = "library sources are managed on the owner"
	ReasonModelsNotShared = "the models path of this instance is not the shared models storage of the add-on owner; model exports are blocked"
	unavailableErrorType  = "server_error"
	unavailableErrorCode  = "unavailable"
)

// unavailableBody has the shape of the LocalAI error envelope
// (schema.ErrorResponse), which the rest of the API writes.
type unavailableBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// WriteUnavailable writes the response of an add-on request that no owner can
// serve: status 503, the status StatusFor gives ErrUnavailable, and the LocalAI
// error envelope with code "unavailable". The message says why. The add-on
// feature gate uses it so a disabled add-on side and an unreachable owner share
// one status and one shape, and differ only in the message.
func WriteUnavailable(w http.ResponseWriter, reason string) {
	var b unavailableBody
	b.Error.Message, b.Error.Type, b.Error.Code = reason, unavailableErrorType, unavailableErrorCode
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(StatusFor(ErrUnavailable))
	_ = json.NewEncoder(w).Encode(b)
}

// typedError writes the owner's typed failure response.
func typedError(w http.ResponseWriter, e error) {
	if e = Refused(e); errors.Is(e, ErrInvalidRequest) {
		// The add-on refused the request as invalid. A forwarding frontend
		// decodes the body back to the same typed error.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(resourceWire{Type: wireInvalid, refusalWire: refusalWireOf(e)})
		return
	}
	var limited *contract.ErrRateLimited
	if errors.As(e, &limited) {
		// The service behind the add-on asks the caller to wait. Nothing
		// failed for good, so the status is not a denial.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", strconv.Itoa(limited.Seconds()))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(sdkWire{Type: wireRateLimited, RetryAfter: limited.Seconds()})
		return
	}
	var refusal2 *CredentialRefusal
	if errors.As(e, &refusal2) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(sdkWire{Type: refusal2.Code, Allowed: refusal2.Allowed})
		return
	}
	var requirements *addons.HostRequirementsRefusal
	if errors.As(e, &requirements) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(sdkWire{Type: "host_requirements", HostOperations: &requirements.Summary})
		return
	}
	var refusal *contract.SDKRefusal
	if errors.As(e, &refusal) {
		// The administrator is told why the add-on cannot be installed. Only
		// the three numbers cross the wire, never the error text.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(sdkWire{Type: wireSDK, Min: refusal.Min, Max: refusal.Max, Host: refusal.Host})
		return
	}
	if t := denialType(e); t != "" {
		// Typed apart from a plain denial, so a forwarding frontend tells the
		// model "no permission" or "no such resource" like the owner does.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(sdkWire{Type: t})
		return
	}
	http.Error(w, "addon request failed", StatusFor(e))
}

// Wire types of a 403 that says which denial it is.
const (
	wirePackage      = "package_unverified"
	wireNotPermitted = "not_permitted"
	wireUnreachable  = "resource_unreachable"
	wireRateLimited  = "rate_limited"
)

// denialType is the wire type of a denial that callers tell apart, or "".
func denialType(e error) string {
	switch {
	case errors.Is(e, ErrUnavailable):
		return ""
	case errors.Is(e, packages.ErrPackage) && errors.Is(e, addons.ErrOperationFailed):
		return wirePackage
	case operationCause(e) != "":
		return operationCause(e)
	case errors.Is(e, ErrNotPermitted):
		return wireNotPermitted
	case errors.Is(e, ErrResourceUnreachable):
		return wireUnreachable
	}
	return ""
}

// operationCause is the code of the typed cause of a failed operation, or "".
// The codes are the wire types, so a forwarding frontend gives the same
// answer as the owner.
func operationCause(e error) string {
	var c *addons.FailureCause
	if errors.As(e, &c) && IsOperationCause(c.Code) {
		return c.Code
	}
	return ""
}

// IsOperationCause reports whether code is one of the causes of a failed
// operation that cross the wire.
func IsOperationCause(code string) bool {
	switch code {
	case addons.CauseLibraryUnavailable, addons.CauseLibraryConflict, addons.CauseReleaseNotFound, addons.CausePackageFetch, addons.CauseInstallStorage,
		addons.CauseFileMissing, addons.CauseVersionNotNewer, addons.CauseDowngradeRefused, addons.CauseInstalledIncompatible, addons.CauseNeedsNewerHost:
		return true
	}
	return false
}

// wireSDK types the owner's refusal of an add-on built for another SDK range.
const wireSDK = "sdk_incompatible"

type sdkWire struct {
	HostOperations *hostops.RequestSummary `json:"hostOperations,omitempty"`
	Type           string                  `json:"type"`
	Min            uint64                  `json:"min"`
	Max            uint64                  `json:"max"`
	Host           uint64                  `json:"host"`
	// Allowed and RetryAfter belong to the types resource_not_allowed and
	// rate_limited.
	Allowed    []string `json:"allowed,omitempty"`
	RetryAfter int      `json:"retryAfter,omitempty"`
}

// SDKRefusalError is the error of a failed operation whose cause is an SDK
// refusal: it matches addons.ErrOperationFailed and carries the typed cause.
func SDKRefusalError(r *contract.SDKRefusal) error {
	return fmt.Errorf("%w: %w", addons.ErrOperationFailed, r)
}

// trackedWriter records whether any response byte or status was committed.
type trackedWriter struct {
	http.ResponseWriter
	committed bool
}

func (t *trackedWriter) WriteHeader(code int) { t.committed = true; t.ResponseWriter.WriteHeader(code) }
func (t *trackedWriter) Write(p []byte) (int, error) {
	t.committed = true
	return t.ResponseWriter.Write(p)
}
func (t *trackedWriter) FlushError() error {
	t.committed = true
	return http.NewResponseController(t.ResponseWriter).Flush()
}
func (t *trackedWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// relayBudget is the frontend byte bound for one relay kind.
func (c *Carrier) relayBudget(kind string) (int64, error) {
	l := c.config.Limits
	switch kind {
	case "file":
		return l.Bytes, nil
	case "progress", "status":
		return l.ProgressBytes, nil
	case "asset", "icon":
		return l.AssetBytes, nil
	case "json":
		return min(l.Bytes, 2<<20), nil
	}
	return 0, ErrForbidden
}

// audience asks the owner for the audience its envelopes must name.
func (c *Carrier) audience(ctx context.Context) (string, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", c.config.OwnerAddress+"/addons/audience", nil)
	if e != nil {
		return "", ErrUnavailable
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return "", ErrUnavailable
	}
	var bootstrap struct {
		Audience string `json:"audience"`
	}
	e = decode(io.LimitReader(resp.Body, 4096), &bootstrap)
	_ = resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || len(bootstrap.Audience) != 64 {
		return "", ErrUnavailable
	}
	return bootstrap.Audience, nil
}

// ModelsProbe asks the owner for the probe it wrote into its models path. An
// owner that has none, or that does not know the call, gives ErrUnavailable.
func (c *Carrier) ModelsProbe(ctx context.Context) (modelsprobe.Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, ReachableTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", c.config.OwnerAddress+"/addons/models-probe", nil)
	if e != nil {
		return modelsprobe.Probe{}, ErrUnavailable
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return modelsprobe.Probe{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	var p modelsprobe.Probe
	if resp.StatusCode != http.StatusOK || decode(io.LimitReader(resp.Body, 4096), &p) != nil || !p.Valid() {
		return modelsprobe.Probe{}, ErrUnavailable
	}
	return p, nil
}

// ReachableTimeout bounds one reachability probe of the owner.
const ReachableTimeout = 2 * time.Second

// Reachable reports whether the owner answered the audience call. It signs
// nothing and changes nothing, and it has its own timeout.
func (c *Carrier) Reachable(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ReachableTimeout)
	defer cancel()
	_, e := c.audience(ctx)
	return e
}

func (c *Carrier) send(ctx context.Context, a contract.Actor, wire wireRequest) (*http.Response, error) {
	if quickRead(wire.Request.Action) && wire.Relay == nil {
		// A page waits for these reads, so they have a short deadline of their
		// own: an owner that is busy answers "busy" in seconds, not after the
		// 65 s of the client.
		d := c.config.ReadTimeout
		if d <= 0 {
			d = DefaultReadTimeout
		}
		rctx, cancel := context.WithTimeout(ctx, d)
		resp, e := c.sendCtx(rctx, a, wire)
		if e != nil {
			cancel()
			if rctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return nil, &OwnerTimeout{After: d}
			}
			return nil, e
		}
		resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
	return c.sendCtx(ctx, a, wire)
}

func (c *Carrier) sendCtx(ctx context.Context, a contract.Actor, wire wireRequest) (*http.Response, error) {
	audience, e := c.audience(ctx)
	if e != nil {
		return nil, e
	}
	body, e := json.Marshal(wire)
	if e != nil || len(body) > 2<<20 {
		return nil, ErrForbidden
	}
	var nonce [32]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return nil, ErrUnavailable
	}
	if len(c.config.SigningKey) != ed25519.PrivateKeySize {
		// Only an owner can lack a key, and an owner never forwards.
		return nil, ErrForbidden
	}
	m := ForwardedRequest{PeerID: c.config.InstanceID, Audience: audience, Actor: a, OperationID: wire.Request.OperationID, Nonce: hex.EncodeToString(nonce[:]), ExpiresAt: time.Now().Add(30 * time.Second), Body: body}
	if b, ok := hostops.BindingOf(ctx); ok {
		m.Chat = &b
	}
	// Only a host access call carries the mark, whatever the context holds.
	m.HumanSurface = wire.HostGrants != nil && HumanSurfaceOf(ctx)
	m.Sign(c.config.SigningKey)
	b, _ := json.Marshal(m)
	req, e := http.NewRequestWithContext(ctx, "POST", c.config.OwnerAddress+"/addons/forward", bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.client
	if wire.Request.Action == "act" {
		client = c.long
	}
	resp, e := client.Do(req)
	if e != nil {
		if wire.Proposal != nil {
			return nil, uncertainProposal(*wire.Proposal, wire.Request.OperationID)
		}
		if wire.Relay == nil && mutation(wire.Request.Action) {
			return nil, &OutcomeUnknown{OperationID: wire.Request.OperationID}
		}
		return nil, ErrUnavailable
	}
	if resp.StatusCode != 200 {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == 403 {
			if wire.Proposal != nil {
				// A denial of a proposal call may name the blocking operation.
				return nil, proposalDenied(resp.Body, wire.Request.OperationID)
			}
			if wire.Resource != nil {
				// The owner types its denials, so a quota or a block that the
				// owner decided is the same answer from every frontend.
				return nil, resourceReplyError(resp.Body, resp.StatusCode, wire.Request.OperationID)
			}
			var b sdkWire
			if decode(io.LimitReader(resp.Body, 32768), &b) == nil {
				switch b.Type {
				case "host_requirements":
					if b.HostOperations != nil {
						return nil, fmt.Errorf("%w: %w", addons.ErrOperationFailed, &addons.HostRequirementsRefusal{Summary: *b.HostOperations})
					}
					return nil, ErrForbidden
				case wireSDK:
					return nil, SDKRefusalError(&contract.SDKRefusal{Min: b.Min, Max: b.Max, Host: b.Host})
				case wirePackage:
					return nil, fmt.Errorf("%w: %w", addons.ErrOperationFailed, packages.ErrPackage)
				case addons.CauseLibraryUnavailable, addons.CauseLibraryConflict, addons.CauseReleaseNotFound, addons.CausePackageFetch, addons.CauseInstallStorage,
					addons.CauseFileMissing, addons.CauseVersionNotNewer, addons.CauseDowngradeRefused, addons.CauseInstalledIncompatible, addons.CauseNeedsNewerHost:
					return nil, fmt.Errorf("%w: %w", addons.ErrOperationFailed, &addons.FailureCause{Code: b.Type})
				case wireNotPermitted:
					return nil, ErrNotPermitted
				case wireUnreachable:
					return nil, ErrResourceUnreachable
				}
				if refusalCode(b.Type) {
					return nil, &CredentialRefusal{Code: b.Type, Allowed: b.Allowed}
				}
			}
			return nil, ErrForbidden
		}
		if wire.Proposal != nil {
			// The owner types its failure of a proposal call, so the answer
			// is the owner's state and not a guess of this frontend.
			return nil, proposalReplyError(resp.Body, *wire.Proposal, wire.Request.OperationID)
		}
		if wire.Resource != nil {
			return nil, resourceReplyErrorFor(resp.Body, resp.StatusCode, wire.Request)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			var b sdkWire
			if decode(io.LimitReader(resp.Body, 32768), &b) == nil && b.Type == wireRateLimited {
				return nil, &contract.ErrRateLimited{RetryAfter: b.RetryAfter}
			}
		}
		if resp.StatusCode == http.StatusBadRequest {
			// The owner's typed refusal is a decision, not an outage: nothing
			// was applied and the same call is refused again.
			var b resourceWire
			if decode(io.LimitReader(resp.Body, 32768), &b) == nil && b.Type == wireInvalid {
				return nil, b.refusalWire.err()
			}
		}
		// Any other status, including a typed 503, leaves a mutation uncertain:
		// the owner may have recorded or applied it before it failed.
		if wire.Relay == nil && mutation(wire.Request.Action) {
			return nil, &OutcomeUnknown{OperationID: wire.Request.OperationID}
		}
		return nil, ErrUnavailable
	}
	return resp, nil
}

// DefaultReadTimeout is how long a frontend waits for a quick read of the
// owner before it reports the owner as busy.
const DefaultReadTimeout = 8 * time.Second

// OwnerTimeout means the owner did not answer a read within the read
// deadline. The owner is up but busy (an upgrade holds it, say); the page
// shows the last list it has and asks again. It is an unavailable error.
type OwnerTimeout struct{ After time.Duration }

func (e *OwnerTimeout) Error() string {
	return "the add-on owner is busy and did not answer in " + e.After.String()
}
func (e *OwnerTimeout) Unwrap() error { return ErrUnavailable }

// cancelBody ends the read deadline when the body is closed, so a slow body
// is bound too.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// quickRead are the reads a page waits for. They answer from the owner's
// snapshot and never wait for a lifecycle operation.
func quickRead(action string) bool {
	return quickReads[action]
}

// quickReads names them one by one. A read that fetches from a library or
// stages a package (the host grants preview, the update check, a catalog
// search) can take long by its nature and keeps the client's own deadline.
var quickReads = map[string]bool{
	appsListAction:                 true,
	"hostgrants-" + HostGrantsRead: true,
	"library-" + LibraryList:       true,
	"addons-installed":             true,
	"catalog-sources":              true,
	ActionUpdateStatus:             true,
	ActionRecoveryStatus:           true,
}

// mutationOutcome applies the rule every frontend shares: a mutation that fails
// as unavailable is reported as an unknown outcome with its original operation
// identifier, because the owner can fail after it recorded or applied the work.
// Reads, denials and errors that are already unknown pass through unchanged.
func mutationOutcome(r contract.Request, e error) error {
	var unknown *OutcomeUnknown
	if e == nil || errors.As(e, &unknown) || errors.Is(e, ErrNotSent) || !mutation(r.Action) || StatusFor(e) != http.StatusServiceUnavailable {
		return e
	}
	return &OutcomeUnknown{OperationID: r.OperationID}
}

func init() {
	// The management reads of the typed dispatch change nothing, so a lost
	// answer is plainly unavailable. The update reads fetch a library and
	// apply nothing.
	markRead("catalog-search", "catalog-details", "catalog-sources", "addons-installed", ActionRecoveryStatus, ActionUpdateStatus, ActionUpdateCheck)
	// The resource management calls that have no operation with an outcome to
	// report: the reads and the two idempotent selection calls.
	markRead("resource-list", "resource-grants", "resource-users", "resource-status", "resource-selection-get", "resource-selection-resolve", "resource-selection-put", "resource-selection-clear",
		"resource-share-list", "resource-share-grants", "resource-share-users", "resource-share-views", "resource-share-watch", "resource-share-release")
}

// mutation fails closed: only the actions marked as reads are exempt, so an
// action added later is reported as an unknown outcome until it is classified.
func mutation(action string) bool { return !readActions[action] }

// Act forwards an action call to the owner and waits for its answer, up to the
// act timeout. A lost answer is an unknown outcome: the tool may have run.
func (c *Carrier) Act(ctx context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, contract.ActTimeout+time.Minute)
	defer cancel()
	return c.Dispatch(ctx, a, r)
}
func (c *Carrier) Dispatch(ctx context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	resp, e := c.send(ctx, a, wireRequest{Request: r})
	if e != nil {
		return contract.Result{}, e
	}
	defer func() { _ = resp.Body.Close() }()
	var result contract.Result
	e = decode(io.LimitReader(resp.Body, 2<<20), &result)
	if e != nil {
		if mutation(r.Action) {
			return contract.Result{}, &OutcomeUnknown{OperationID: r.OperationID}
		}
		return contract.Result{}, ErrUnavailable
	}
	return result, nil
}
func (c *Carrier) Relay(ctx context.Context, a contract.Actor, r RelayRequest, w http.ResponseWriter) error {
	budget, e := c.relayBudget(r.Kind)
	if e != nil {
		return e
	}
	resp, e := c.send(ctx, a, wireRequest{Request: r.Request, Relay: &r})
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	// Owner headers are copied only once the first byte is about to be committed
	// or the stream ended cleanly, so a failed first read leaves no attachment
	// headers on the error response.
	published := false
	publish := func() {
		if published {
			return
		}
		published = true
		for _, h := range []string{"Content-Type", "Content-Disposition", "Content-Length", "Content-Security-Policy", "Cache-Control", "X-Content-Type-Options"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
	}
	var total int64
	buffer := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > budget {
				return ErrUnavailable
			}
			publish()
			if _, e = w.Write(buffer[:n]); e != nil {
				return e
			}
			if r.Kind == "progress" {
				if e = http.NewResponseController(w).Flush(); e != nil {
					return e
				}
			}
		}
		if err == io.EOF {
			// An end of stream that races a canceled or expired context is not
			// proof that the owner finished: never report it as complete.
			if ctx.Err() == nil {
				publish()
			}
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// artifactStatus forwards a read-only artifact lookup to the owner.
func (c *Carrier) artifactStatus(ctx context.Context, a contract.Actor, id string) (Artifact, error) {
	request := contract.Request{OperationID: id, Action: "artifact-" + artifactStatus}
	resp, e := c.send(ctx, a, wireRequest{Request: request, Artifact: &artifactCall{Op: artifactStatus, ID: id}})
	if e != nil {
		return Artifact{}, e
	}
	defer func() { _ = resp.Body.Close() }()
	var art Artifact
	if e = decode(io.LimitReader(resp.Body, 1<<20), &art); e != nil {
		return Artifact{}, ErrUnavailable
	}
	return art, nil
}

// HostSettingsSnapshot reads owner policy for trusted host consumers over pinned
// mTLS. This route has no mutation counterpart and carries no caller flags.
func (c *Carrier) HostSettingsSnapshot(ctx context.Context) (HostSettingsPolicy, error) {
	ctx, cancel := context.WithTimeout(ctx, ReachableTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.config.OwnerAddress+"/addons/host-settings", nil)
	if err != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	return DecodeHostSettingsResult(contract.Result{Payload: raw})
}

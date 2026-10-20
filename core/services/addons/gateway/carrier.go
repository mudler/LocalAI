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
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
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
}
type Carrier struct {
	config CarrierConfig
	client *http.Client
}
type OutcomeUnknown struct {
	OperationID string `json:"operationId"`
}

func (e *OutcomeUnknown) Error() string { return "addon mutation outcome unknown" }
func (e *OutcomeUnknown) Unwrap() error { return ErrUnavailable }
func NewCarrier(c CarrierConfig) (*Carrier, error) {
	limits, e := c.Limits.resolved()
	if e != nil {
		return nil, e
	}
	c.Limits = limits
	u, e := url.Parse(c.OwnerAddress)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || c.InstanceID == "" || c.OwnerID == "" || c.Roots == nil || len(c.Certificate.Certificate) == 0 || len(c.SigningKey) != ed25519.PrivateKeySize {
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
	return &Carrier{config: c, client: h}, nil
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
	Request contract.Request `json:"request"`
	Relay   *RelayRequest    `json:"relay,omitempty"`
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
	v, e := NewForwardVerifier(VerifierConfig{Keys: c.config.VerificationKeys, Revalidate: g.Revalidate})
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
		if r.Method == "GET" && r.URL.Path == "/addons/audience" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Audience string `json:"audience"`
			}{v.Audience()})
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
		var wire wireRequest
		if decode(bytes.NewReader(m.Body), &wire) != nil || wire.Request.OperationID != m.OperationID || (wire.Relay != nil && wire.Relay.Request.OperationID != m.OperationID) {
			http.Error(w, "invalid binding", 400)
			return
		}
		if wire.Resource != nil {
			call := *wire.Resource
			if wire.Relay != nil || wire.Proposal != nil || wire.Artifact != nil || wire.Apps != nil || wire.Request.Action != call.wireAction() {
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
			if wire.Relay != nil || wire.Proposal != nil || wire.Artifact != nil || wire.Request.Action != appsListAction {
				http.Error(w, "invalid binding", 400)
				return
			}
			list, e := g.appsLocal(r.Context(), actor)
			if e != nil {
				typedError(w, e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		if wire.Artifact != nil {
			call := *wire.Artifact
			if wire.Relay != nil || wire.Proposal != nil || call.Op != artifactStatus || call.ID == "" || wire.Request.OperationID != call.ID {
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
			if wire.Relay != nil {
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
		result, e := g.Dispatch(r.Context(), actor, wire.Request)
		if e != nil {
			typedError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}), nil
}

// denials are authorization, policy and request-level rejections.
var denials = []error{contract.ErrRefused, ErrForbidden, recovery.ErrForbidden, addons.ErrForbidden, addons.ErrStaleGeneration, catalog.ErrForbidden, addons.ErrConflict, addons.ErrOperationNotFound, addons.ErrOperationFailed, addons.ErrStaleInstalled, addons.ErrCandidateChanged, addons.ErrTransitionConflict, addons.ErrIncompatible, addons.ErrIntegrity, addons.ErrCheckFailed, catalog.ErrNotFound, catalog.ErrInvalid, packages.ErrPackage}

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
	if e != nil && !matches(e, unavailable) && matches(e, denials) {
		return http.StatusForbidden
	}
	return http.StatusServiceUnavailable
}

// typedError writes the owner's typed failure response.
func typedError(w http.ResponseWriter, e error) {
	if e = Refused(e); errors.Is(e, ErrInvalidRequest) {
		// The add-on refused the request as invalid. A forwarding frontend
		// decodes the body back to the same typed error.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(resourceWire{Type: wireInvalid})
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
	http.Error(w, "addon request failed", StatusFor(e))
}

// wireSDK types the owner's refusal of an add-on built for another SDK range.
const wireSDK = "sdk_incompatible"

type sdkWire struct {
	Type string `json:"type"`
	Min  uint64 `json:"min"`
	Max  uint64 `json:"max"`
	Host uint64 `json:"host"`
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
func (c *Carrier) send(ctx context.Context, a contract.Actor, wire wireRequest) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", c.config.OwnerAddress+"/addons/audience", nil)
	if e != nil {
		return nil, ErrUnavailable
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return nil, ErrUnavailable
	}
	var bootstrap struct {
		Audience string `json:"audience"`
	}
	e = decode(io.LimitReader(resp.Body, 4096), &bootstrap)
	_ = resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || len(bootstrap.Audience) != 64 {
		return nil, ErrUnavailable
	}
	body, e := json.Marshal(wire)
	if e != nil || len(body) > 2<<20 {
		return nil, ErrForbidden
	}
	var nonce [32]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return nil, ErrUnavailable
	}
	m := ForwardedRequest{PeerID: c.config.InstanceID, Audience: bootstrap.Audience, Actor: a, OperationID: wire.Request.OperationID, Nonce: hex.EncodeToString(nonce[:]), ExpiresAt: time.Now().Add(30 * time.Second), Body: body}
	m.Sign(c.config.SigningKey)
	b, _ := json.Marshal(m)
	req, e = http.NewRequestWithContext(ctx, "POST", c.config.OwnerAddress+"/addons/forward", bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e = c.client.Do(req)
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
			if decode(io.LimitReader(resp.Body, 4096), &b) == nil && b.Type == wireSDK {
				return nil, SDKRefusalError(&contract.SDKRefusal{Min: b.Min, Max: b.Max, Host: b.Host})
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
		if resp.StatusCode == http.StatusBadRequest {
			// The owner's typed refusal is a decision, not an outage: nothing
			// was applied and the same call is refused again.
			var b resourceWire
			if decode(io.LimitReader(resp.Body, 4096), &b) == nil && b.Type == wireInvalid {
				return nil, ErrInvalidRequest
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

// mutationOutcome applies the rule every frontend shares: a mutation that fails
// as unavailable is reported as an unknown outcome with its original operation
// identifier, because the owner can fail after it recorded or applied the work.
// Reads, denials and errors that are already unknown pass through unchanged.
func mutationOutcome(r contract.Request, e error) error {
	var unknown *OutcomeUnknown
	if e == nil || errors.As(e, &unknown) || !mutation(r.Action) || StatusFor(e) != http.StatusServiceUnavailable {
		return e
	}
	return &OutcomeUnknown{OperationID: r.OperationID}
}

// readManagement are the management reads of the typed dispatch. They change
// nothing, so a lost answer is plainly unavailable.
var readManagement = map[string]bool{"catalog-search": true, "catalog-details": true, "catalog-sources": true, "addons-installed": true, ActionRecoveryStatus: true,
	// The update reads change no state: the check fetches a library and applies nothing.
	ActionUpdateStatus: true, ActionUpdateCheck: true}

// resourceRead are the resource management calls that have no operation with
// an outcome to report: the reads and the two idempotent selection calls.
var resourceRead = map[string]bool{"resource-list": true, "resource-grants": true, "resource-users": true, "resource-status": true, "resource-selection-get": true, "resource-selection-resolve": true, "resource-selection-put": true, "resource-selection-clear": true}

// mutation fails closed: only the known read actions are exempt, so an action
// added later is reported as an unknown outcome until it is classified.
func mutation(action string) bool {
	return action != "read" && action != "operation-status" && action != "proposal-get" && action != "proposal-reconcile" && action != "proposal-list" && action != "artifact-status" && action != appsListAction && !readManagement[action] && !resourceRead[action]
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

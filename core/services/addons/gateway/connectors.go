// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ConnectorSource must fence discovery and execution against runtime ownership.
type ConnectorSource interface {
	ConnectorKinds(context.Context) ([]addons.ConnectorKind, error)
	Connector(context.Context, string, contract.ConnectorRequest) (contract.ConnectorResponse, error)
}
type ConnectorAuthority interface {
	AuthorizeConnector(context.Context, connectors.ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorRequest, error)
}
type connectorCall struct {
	Scope   connectors.ConnectorCallScope `json:"scope"`
	Request contract.ConnectorRequest     `json:"request"`
}

// connectorWireLimit bounds the body of one connector call between hosts: the
// scope and the request, which holds text and no file bytes (those travel by
// handle). This route is pinned and peer-authenticated. Do not raise global
// limits.
const connectorWireLimit = 1 << 20

// connectorTimeout bounds one connector round trip. It is set once per hop: by
// the forwarding carrier on a frontend, and by the owner when it runs the call,
// since the owner must bound the work whichever door the call came through.
const connectorTimeout = 45 * time.Second

func (g *Gateway) connectorSource() ConnectorSource {
	if g.Connectors != nil {
		return g.Connectors
	}
	if g.Manager != nil {
		return g.Manager
	}
	return nil
}
func (g *Gateway) ConnectorKinds(ctx context.Context) ([]connectors.Kind, error) {
	if g.Forward != nil {
		var out []connectors.Kind
		err := g.Forward.connectorRequest(ctx, "GET", "/addons/connectors/kinds", nil, &out, connectorTimeout)
		return out, err
	}
	return g.localConnectorKinds(ctx)
}
func (g *Gateway) localConnectorKinds(ctx context.Context) ([]connectors.Kind, error) {
	source := g.connectorSource()
	if source == nil {
		return nil, ErrUnavailable
	}
	kinds, err := source.ConnectorKinds(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	out := make([]connectors.Kind, len(kinds))
	for i, k := range kinds {
		out[i] = connectors.Kind{AddonID: k.AddonID, AuthKind: k.AuthKind, ConnectorKind: k.ConnectorKind, Translations: k.Translations}
	}
	return out, nil
}
func (g *Gateway) ConnectorCall(ctx context.Context, s connectors.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if g.Forward != nil {
		if s.Purpose == connectors.ConnectorPurposeBinding {
			req.Token = ""
		}
		var out contract.ConnectorResponse
		err := g.Forward.connectorRequest(ctx, "POST", "/addons/connectors/call", connectorCall{s, req}, &out, req.CallTimeout())
		return out, err
	}
	return g.localConnectorCall(ctx, s, req)
}
func (g *Gateway) localConnectorCall(ctx context.Context, s connectors.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	timeout := connectorTimeout
	if req.Transfer != nil {
		timeout = req.CallTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	source := g.connectorSource()
	if source == nil || g.ConnectorAuthority == nil {
		return contract.ConnectorResponse{}, ErrUnavailable
	}
	kinds, err := source.ConnectorKinds(ctx)
	if err != nil {
		return contract.ConnectorResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	i := slices.IndexFunc(kinds, func(k addons.ConnectorKind) bool { return k.AddonID == s.AddonID && k.ID == s.Kind })
	if i < 0 {
		return contract.ConnectorResponse{}, ErrForbidden
	}
	if req.Op == contract.ConnectorOpPoll && kinds[i].Delivery != contract.DeliveryPull {
		return contract.ConnectorResponse{}, ErrForbidden
	}
	req, err = g.ConnectorAuthority.AuthorizeConnector(ctx, s, req)
	if err != nil {
		// An authority service that has not started is an outage, not a denial.
		// Return only gateway sentinels so authority details never reach the carrier.
		if errors.Is(err, ErrUnavailable) {
			return contract.ConnectorResponse{}, ErrUnavailable
		}
		return contract.ConnectorResponse{}, ErrForbidden
	}
	moves := req.Op == contract.ConnectorOpMedia || req.Op == contract.ConnectorOpSendFile
	if moves != (req.Transfer != nil) || (moves && kinds[i].Files == nil) {
		// File bytes only travel by handle, and only to a kind that declared
		// the files ability.
		return contract.ConnectorResponse{}, ErrForbidden
	}
	end, err := g.beginTransfer(s, req)
	if err != nil {
		return contract.ConnectorResponse{}, err
	}
	out, err := source.Connector(ctx, s.AddonID, req)
	if end != nil {
		out = finishTransfer(end, out)
	}
	if err != nil {
		// A definite refusal of the add-on keeps its type; anything else could
		// not tell, so the caller treats it as an outage, with the cause kept.
		if refused := Refused(err); !errors.Is(refused, ErrUnavailable) && errors.Is(refused, ErrForbidden) {
			return contract.ConnectorResponse{}, refused
		}
		return contract.ConnectorResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return out, nil
}
func (c *Carrier) connectorRequest(ctx context.Context, method, path string, body any, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil || len(raw) > connectorWireLimit {
			return ErrForbidden
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.config.OwnerAddress+path, bytes.NewReader(raw))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	// A call that moves a file runs longer than the client's own timeout; the
	// context carries the bound.
	client := c.client
	if timeout > connectorTimeout {
		client = c.long
	}
	resp, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusBadRequest:
		// The owner relays the add-on's definite refusal; see typedError.
		return ErrInvalidRequest
	}
	if resp.StatusCode != http.StatusOK || decodeConnector(resp.Body, out) != nil {
		return ErrUnavailable
	}
	return nil
}

// Called only after the carrier has authenticated its pinned peer.
func (g *Gateway) serveConnectors(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == "GET" && r.URL.Path == "/addons/connectors/kinds" {
		kinds, err := g.localConnectorKinds(r.Context())
		if err != nil {
			typedError(w, err)
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kinds)
		return true
	}
	if r.Method == "POST" && r.URL.Path == "/addons/connectors/call" {
		var call connectorCall
		if decodeConnector(r.Body, &call) != nil {
			typedError(w, ErrForbidden)
			return true
		}
		if call.Request.Transfer != nil {
			// The server's write timeout is sized for short calls.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(contract.ConnectorTransferTimeout + time.Minute))
		}
		out, err := g.localConnectorCall(r.Context(), call.Scope, call.Request)
		if err != nil {
			typedError(w, err)
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return true
	}
	return false
}

// Read one extra byte so a valid JSON prefix followed by oversized whitespace
// cannot turn truncation into a successful EOF.
func decodeConnector(r io.Reader, out any) error {
	raw, err := io.ReadAll(io.LimitReader(r, connectorWireLimit+1))
	if err != nil || len(raw) > connectorWireLimit {
		return ErrForbidden
	}
	return decode(bytes.NewReader(raw), out)
}

// beginTransfer registers the handle of a call that carries one, on the
// process that runs the add-on. The frontend made the handle and checked the
// chat's access; this process binds it to the add-on of the call, so no other
// add-on can use it, and to the content or the bound the scope names.
func (g *Gateway) beginTransfer(s connectors.ConnectorCallScope, req contract.ConnectorRequest) (func() hostops.TransferOutcome, error) {
	if req.Transfer == nil {
		return nil, nil
	}
	grant := hostops.TransferGrant{Handle: req.Transfer.Handle, AddonID: s.AddonID, BindingID: s.BindingID}
	switch req.Op {
	case contract.ConnectorOpMedia:
		grant.Direction, grant.MaxBytes = hostops.TransferIn, req.Transfer.MaxBytes
	case contract.ConnectorOpSendFile:
		if s.Content == nil {
			return nil, ErrForbidden
		}
		grant.Direction, grant.SHA, grant.Size = hostops.TransferOut, s.Content.SHA, s.Content.Size
	default:
		return nil, ErrForbidden
	}
	if g.Transfers == nil {
		return nil, ErrUnavailable
	}
	end, err := g.Transfers.Begin(grant)
	if err != nil {
		if errors.Is(err, hostops.ErrBadRequest) || errors.Is(err, hostops.ErrConflict) {
			return nil, ErrForbidden
		}
		return nil, ErrUnavailable
	}
	return end, nil
}

// finishTransfer ends the handle after the add-on answered. For an inbound
// file it writes the size and the hash from the host's own record into the
// answer; an add-on that did not finish the file gets no Received.
func finishTransfer(end func() hostops.TransferOutcome, out contract.ConnectorResponse) contract.ConnectorResponse {
	o := end()
	if out.Received != nil {
		if o.Direction != hostops.TransferIn || o.Result != hostops.TransferDone || o.SHA == "" {
			out.Received = nil
		} else {
			out.Received.Size, out.Received.SHA256 = o.Bytes, o.SHA
		}
	}
	return out
}

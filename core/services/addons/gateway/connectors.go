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

// The file payload is base64 encoded only once; no signed envelope is needed on
// this pinned, peer-authenticated host-only route. Do not raise global limits.
const connectorWireLimit = 2 << 20

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
		err := g.Forward.connectorRequest(ctx, "GET", "/addons/connectors/kinds", nil, &out)
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
		out[i] = connectors.Kind{AddonID: k.AddonID, ConnectorKind: k.ConnectorKind}
	}
	return out, nil
}
func (g *Gateway) ConnectorCall(ctx context.Context, s connectors.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if g.Forward != nil {
		if s.Purpose == connectors.ConnectorPurposeBinding {
			req.Token = ""
		}
		var out contract.ConnectorResponse
		err := g.Forward.connectorRequest(ctx, "POST", "/addons/connectors/call", connectorCall{s, req}, &out)
		return out, err
	}
	return g.localConnectorCall(ctx, s, req)
}
func (g *Gateway) localConnectorCall(ctx context.Context, s connectors.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, connectorTimeout)
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
	out, err := source.Connector(ctx, s.AddonID, req)
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
func (c *Carrier) connectorRequest(ctx context.Context, method, path string, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, connectorTimeout)
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
	resp, err := c.client.Do(req)
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

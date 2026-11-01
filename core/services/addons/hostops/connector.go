// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ConnectorInbox takes the messages that connector add-ons push. The chats
// implement it. addonID is the add-on the broker authenticated, never a value
// of the arguments; the implementation finds the owner, the agent and the
// allowlist in the stored binding.
type ConnectorInbox interface {
	Ingest(ctx context.Context, addonID string, in contract.ConnectorInbound) error
}

// RegisterConnector registers connector.inbound, the one operation that needs no
// live request of a user (see ScopeInbound).
func RegisterConnector(b *Broker, inbox ConnectorInbox) error {
	if inbox == nil {
		return errors.New("connector.inbound needs an inbox")
	}
	return b.Register(Spec{Op: contract.ConnectorInboundOp, Scope: ScopeInbound, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		var in contract.ConnectorInbound
		d := json.NewDecoder(bytes.NewReader(c.Arguments))
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil || in.BindingID == "" {
			return nil, ErrBadRequest
		}
		switch err := inbox.Ingest(ctx, c.AddonID, in); {
		case err == nil:
			return json.RawMessage(`{}`), nil
		case errors.Is(err, connectors.ErrForbidden):
			return nil, ErrForbidden
		case errors.Is(err, connectors.ErrNotFound):
			return nil, ErrNotFound
		case errors.Is(err, connectors.ErrInvalid):
			return nil, ErrBadRequest
		default:
			return nil, err
		}
	}})
}

package studiotest

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// BotTransport is a studio.ConnectorTransport that serves the real Telegram
// add-on runtime in the process, over a Bot API that a test fakes. The tests of
// the chats therefore exercise the same transport code that the add-on runs.
type BotTransport struct {
	rt transport.Runtime
	// Running is false to play an add-on that is not installed.
	Running bool
	// Addon is the add-on ID the transport answers for.
	Addon string
}

// NewBotTransport returns a running Telegram add-on over api.
func NewBotTransport(api *transport.API) *BotTransport {
	return &BotTransport{rt: transport.Runtime{API: api}, Running: true, Addon: "telegram"}
}

// Kinds lists the Telegram kind while the add-on runs.
func (b *BotTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	if !b.Running {
		return nil, nil
	}
	return []studio.ConnectorKind{{AddonID: b.Addon, ConnectorKind: contract.ConnectorKind{ID: transport.Kind, Title: "Telegram", Delivery: contract.DeliveryPull}}}, nil
}

// Call runs the connector route of the add-on.
func (b *BotTransport) Call(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if !b.Running || addonID != b.Addon {
		return contract.ConnectorResponse{}, errors.New("the add-on is not running")
	}
	return b.rt.Connector(ctx, req)
}

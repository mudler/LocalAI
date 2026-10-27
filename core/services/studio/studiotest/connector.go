package studiotest

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// BotTransport is a studio.ConnectorTransport that serves the real Telegram
// add-on runtime in the process, over a Bot API that a test fakes. The tests of
// the chats therefore exercise the same transport code that the add-on runs.
type BotTransport struct {
	rt      transport.Runtime
	running atomic.Bool
	// Addon is the add-on ID the transport answers for.
	Addon string
}

// SetRunning plays an add-on that is installed (true) or missing (false). The
// loops of the manager read it while a test changes it, hence the atomic.
func (b *BotTransport) SetRunning(on bool) { b.running.Store(on) }

// NewBotTransport returns a running Telegram add-on over api.
func NewBotTransport(api *transport.API) *BotTransport {
	b := &BotTransport{rt: transport.Runtime{API: api}, Addon: "telegram"}
	b.running.Store(true)
	return b
}

// Kinds lists the Telegram kind while the add-on runs.
func (b *BotTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	if !b.running.Load() {
		return nil, nil
	}
	return []studio.ConnectorKind{{AddonID: b.Addon, ConnectorKind: contract.ConnectorKind{ID: transport.Kind, Title: "Telegram", Delivery: contract.DeliveryPull}}}, nil
}

// Call runs the connector route of the add-on.
func (b *BotTransport) Call(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if !b.running.Load() || addonID != b.Addon {
		return contract.ConnectorResponse{}, errors.New("the add-on is not running")
	}
	return b.rt.Connector(ctx, req)
}

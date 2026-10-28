// Package studiobot serves connector add-on runtimes (Telegram, Slack) to the
// connector manager of the chats in a test, over platform APIs that the test
// fakes. It lives outside core because the core does not depend on an add-on.
package studiobot

import (
	"context"
	"errors"
	"sync/atomic"

	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Addon is one connector add-on that a BotTransport serves: its ID, the kind
// it declares in its manifest, and its real runtime.
type Addon struct {
	ID      string
	Kind    contract.ConnectorKind
	Runtime contract.ConnectorRuntime
}

// Telegram is the Telegram add-on over api.
func Telegram(api *transport.API) Addon {
	return Addon{ID: "telegram", Kind: contract.ConnectorKind{ID: transport.Kind, Title: "Telegram", Delivery: contract.DeliveryPull}, Runtime: transport.Runtime{API: api}}
}

// Slack is the Slack add-on over api. The caller closes the runtime.
func Slack(rt *slack.Runtime) Addon {
	return Addon{ID: "slack", Kind: contract.ConnectorKind{ID: slack.Kind, Title: "Slack", Delivery: contract.DeliveryPull}, Runtime: rt}
}

// BotTransport is a studio.ConnectorTransport that serves the real connector
// add-on runtimes in the process, over APIs that a test fakes. The tests of
// the chats therefore exercise the same transport code that the add-ons run.
type BotTransport struct {
	addons  []Addon
	running atomic.Bool
	// Addon is the ID of the first add-on, for the tests that serve one.
	Addon string
}

// SetRunning plays add-ons that are installed (true) or missing (false). The
// loops of the manager read it while a test changes it, hence the atomic.
func (b *BotTransport) SetRunning(on bool) { b.running.Store(on) }

// NewBotTransport returns a running Telegram add-on over api.
func NewBotTransport(api *transport.API) *BotTransport {
	return NewTransport(Telegram(api))
}

// NewTransport returns running add-ons.
func NewTransport(addons ...Addon) *BotTransport {
	b := &BotTransport{addons: addons}
	if len(addons) > 0 {
		b.Addon = addons[0].ID
	}
	b.running.Store(true)
	return b
}

// Kinds lists the kinds of the add-ons while they run.
func (b *BotTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	if !b.running.Load() {
		return nil, nil
	}
	out := make([]studio.ConnectorKind, len(b.addons))
	for i, a := range b.addons {
		out[i] = studio.ConnectorKind{AddonID: a.ID, ConnectorKind: a.Kind}
	}
	return out, nil
}

// Call runs the connector route of an add-on.
func (b *BotTransport) Call(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if b.running.Load() {
		for _, a := range b.addons {
			if a.ID == addonID {
				return a.Runtime.Connector(ctx, req)
			}
		}
	}
	return contract.ConnectorResponse{}, errors.New("the add-on is not running")
}

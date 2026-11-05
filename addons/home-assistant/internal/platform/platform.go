// SPDX-License-Identifier: MIT

// Package platform is the connector route of the Home Assistant add-on. A
// binding of kind home-assistant holds the access token and the address of one
// instance as its resource list; the tools read both from the host. The
// connector carries no messages. The host's Test call asks "me" to check the
// token, and this route cannot: the connector request carries the token and not
// the address, so the address is checked by the page, which calls get-config
// for each listed instance.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "home-assistant"

// ErrNoMessages is what the ops that send answer.
var ErrNoMessages = errors.New("this add-on does not carry messages")

// Platform is Home Assistant as a connector.Platform.
type Platform struct{}

var _ connector.Platform = Platform{}

func (Platform) Kind() string  { return Kind }
func (Platform) MaxRunes() int { return 4000 }

// Poll returns no update. It waits for the time the host allows so the loop
// of an enabled binding does not spin.
func (Platform) Poll(ctx context.Context, _ string, _ int64, wait int) ([]contract.ConnectorMessage, error) {
	if wait <= 0 {
		return nil, nil
	}
	t := time.NewTimer(time.Duration(wait) * time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
		return nil, nil
	}
}

func (Platform) Send(context.Context, contract.ConnectorRequest) (string, error) {
	return "", ErrNoMessages
}
func (Platform) Edit(context.Context, contract.ConnectorRequest) error   { return ErrNoMessages }
func (Platform) Typing(context.Context, contract.ConnectorRequest) error { return nil }

// Me accepts a token that is present. It cannot reach the instance, because
// the request has no address, so it never says a token works; the page does
// that, for each address.
func (Platform) Me(_ context.Context, token string) (connector.Bot, error) {
	if token == "" {
		return connector.Bot{}, connector.ErrUnauthorized
	}
	return connector.Bot{ID: "home-assistant", Username: "access token"}, nil
}

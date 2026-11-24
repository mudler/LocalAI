// SPDX-License-Identifier: MIT

// Package platform is the connector route of the PiKVM add-on. A
// binding of kind pikvm holds the credential (user:password) and the address
// of one device as its resource list; the tools read both from the host. The
// connector carries no messages. The host's Test call asks "me" to check the
// credential, and this route can only check its shape: the connector request
// carries the credential and not the address, so the page checks the device
// itself, with get-info, for each listed address.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "pikvm"

// ErrNoMessages is what the ops that send answer.
var ErrNoMessages = errors.New("this add-on does not carry messages")

// Platform is PiKVM as a connector.Platform.
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

// Me accepts a credential written user:password. It cannot reach the device,
// because the request has no address, so it never says a login works; the page
// does that, for each address.
func (Platform) Me(_ context.Context, token string) (connector.Bot, error) {
	if _, _, err := kvmd.SplitCredential(token); err != nil {
		return connector.Bot{}, connector.ErrUnauthorized
	}
	return connector.Bot{ID: "pikvm", Username: "device user"}, nil
}

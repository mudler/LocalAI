// SPDX-License-Identifier: MIT

// Package platform is the connector route of the SSH add-on. A binding of kind
// ssh holds the credential (a private key or user:password) and the address of
// one machine as its resource list; the tools read both from the host. The
// connector carries no messages. The host's Test call asks "me" to check the
// credential, and this route can only check its shape: the connector request
// carries the credential and not the address, so the page checks the machine
// itself, with get-info.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/ssh/internal/credential"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "ssh"

// ErrNoMessages is what the ops that send answer.
var ErrNoMessages = errors.New("this add-on does not carry messages")

// Platform is SSH as a connector.Platform.
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

// Me accepts a credential that Parse reads. It cannot reach the machine,
// because the request has no address, so it never says a login works; the
// page does that.
func (Platform) Me(_ context.Context, token string) (connector.Bot, error) {
	if _, err := credential.Parse(token); err != nil {
		return connector.Bot{}, connector.ErrUnauthorized
	}
	return connector.Bot{ID: "ssh", Username: "remote user"}, nil
}

// SPDX-License-Identifier: MIT

// Package platform is the connector route of the Jellyfin add-on. In this
// version it carries no messages: a binding of kind jellyfin holds the API key
// and the address of the server that the tools use. The host's Test call asks
// "me", which carries the key but not the address of the server, so it cannot
// reach the server; the settings page checks the server with the check tool
// instead. Here "me" only refuses a key that cannot be one.
package platform

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "jellyfin"

// ErrNoMessages is what the ops that send answer.
var ErrNoMessages = errors.New("this add-on does not carry messages")

// Platform is Jellyfin as a connector.Platform.
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

// Me accepts a key that is plausible and refuses one that is not. It makes no
// request: it is never given the address of a server.
func (Platform) Me(_ context.Context, token string) (connector.Bot, error) {
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\"\\ \t\r\n") {
		return connector.Bot{}, connector.ErrUnauthorized
	}
	return connector.Bot{ID: "jellyfin", Username: "Jellyfin API key"}, nil
}

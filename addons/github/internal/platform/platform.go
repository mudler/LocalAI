// SPDX-License-Identifier: MIT

// Package platform is the connector route of the GitHub add-on. In this
// version it carries no messages: a binding of kind github holds the token and
// the repository list that the read tools use, and the host's Test call asks
// "me" to check the token. Listening for mentions comes later, so poll waits
// and returns nothing, and the ops that send fail with a plain reason.
package platform

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "github"

// ErrNoMessages is what the ops that send answer until the connector exists.
var ErrNoMessages = errors.New("this add-on does not carry messages yet")

// Platform is GitHub as a connector.Platform.
type Platform struct{ API *ghapi.Client }

var _ connector.Platform = Platform{}

func (Platform) Kind() string  { return Kind }
func (Platform) MaxRunes() int { return 65000 }

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

// Me returns the login of the token.
func (p Platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	var me struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if _, err := p.API.GetJSON(ctx, token, "/user", nil, &me); err != nil {
		return connector.Bot{}, mapErr(err)
	}
	return connector.Bot{ID: strconv.FormatInt(me.ID, 10), Username: me.Login}, nil
}

func mapErr(err error) error {
	var limited *contract.ErrRateLimited
	switch {
	case errors.Is(err, ghapi.ErrUnauthorized):
		return connector.ErrUnauthorized
	case errors.As(err, &limited):
		return &connector.RetryAfter{Seconds: limited.Seconds()}
	}
	return errors.New(ghapi.Describe(err))
}

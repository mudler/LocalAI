// SPDX-License-Identifier: MIT

// Package platform is the connector route of the Gmail add-on. It
// carries no messages: a binding of kind gmail holds the login and the label
// list that the tools use, and the host's Test call and the end of a login ask
// "me" to check the access token. Listening comes never, so poll waits and
// returns nothing, and the ops that send fail with a plain reason.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/addons/gmail/internal/gmailapi"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "gmail"

// ErrNoMessages is what the ops that send answer.
var ErrNoMessages = errors.New("this add-on does not carry messages")

// Platform is Gmail as a connector.Platform.
type Platform struct{ API *gmailapi.Client }

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

// Me returns the account of the access token: its e-mail address names the
// connection on the Connections page.
func (p Platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	var profile struct {
		Email string `json:"emailAddress"`
	}
	if err := p.API.GetJSON(ctx, token, "/gmail/v1/users/me/profile", nil, &profile); err != nil {
		return connector.Bot{}, mapErr(err)
	}
	return connector.Bot{ID: profile.Email, Username: profile.Email}, nil
}

func mapErr(err error) error {
	var limited *contract.ErrRateLimited
	var server *gmailapi.ServerError
	switch {
	case errors.Is(err, gmailapi.ErrUnauthorized):
		return connector.ErrUnauthorized
	case errors.As(err, &limited):
		return &connector.RetryAfter{Seconds: limited.Seconds()}
	case errors.As(err, &server):
		return &connector.RetryAfter{Seconds: server.RetryAfter}
	}
	return errors.New(gmailapi.Describe(err))
}

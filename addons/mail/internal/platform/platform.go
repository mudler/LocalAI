// SPDX-License-Identifier: MIT

// Package platform is the connector route of the Mail add-on. A binding of
// kind mail carries no messages: it holds the sealed connection (servers,
// login, password) that the tools use, and the host's check on save and its
// Test call ask "me", which logs in to both servers.
package platform

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/imapc"
	"github.com/mudler/LocalAI/addons/mail/internal/smtpc"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "mail"

// ErrNoMessages is what the ops that send answer: a mail binding carries no chat messages.
var ErrNoMessages = errors.New("this add-on does not carry chat messages")

// Platform is the mailbox as a connector.Platform.
type Platform struct{ Dialer *dial.Dialer }

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

// Me logs in to the IMAP server and to the SMTP server and returns the
// address. Either login failing is a refusal of the credential.
func (p Platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	a, err := account.Parse(token)
	if err != nil {
		return connector.Bot{}, connector.ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	s, err := imapc.Open(ctx, p.Dialer, a)
	if err != nil {
		return connector.Bot{}, mapErr(err)
	}
	s.Close()
	if err := smtpc.Check(ctx, p.Dialer, a); err != nil {
		return connector.Bot{}, mapErr(err)
	}
	return connector.Bot{ID: a.Address, Username: a.Address}, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, imapc.ErrAuth), errors.Is(err, smtpc.ErrAuth):
		return connector.ErrUnauthorized
	}
	return err
}

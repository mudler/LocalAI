// SPDX-License-Identifier: MIT

// Package connector is the part that every connector add-on shares: the
// dispatch of the ops of the route "/connector", the typed errors of the
// contract, the wait that a platform asks for, and the cutting of long texts.
// An add-on implements Platform with the API of its platform and serves the
// route with Serve.
package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Errors that a Platform returns. Serve maps them to the codes of the contract.
var (
	// ErrUnauthorized means the platform refused the token.
	ErrUnauthorized = errors.New("the platform refused the token")
	// ErrConflict means another process reads the updates of the same bot.
	ErrConflict = errors.New("another process reads the updates of this bot")
)

// RetryAfter means the platform asks the caller to wait.
type RetryAfter struct{ Seconds int }

func (r *RetryAfter) Error() string {
	return fmt.Sprintf("the platform asks to wait %d s", r.Seconds)
}

// Bot is the identity of the bot of a token.
type Bot struct {
	ID       string
	Username string
}

// Platform is the API of one messaging platform. The host passes the token in
// every request; a Platform keeps none.
type Platform interface {
	// Kind is the kind of binding that the platform serves.
	Kind() string
	// MaxRunes is the longest text of one message.
	MaxRunes() int
	// Poll returns the updates from offset on, waiting up to wait seconds.
	Poll(ctx context.Context, token string, offset int64, wait int) ([]contract.ConnectorMessage, error)
	// Send sends req.Text, which fits one message, to req.Chat and req.Thread
	// and returns the ID of the message.
	Send(ctx context.Context, req contract.ConnectorRequest) (string, error)
	// Edit replaces the text of the message req.MessageID with req.Text.
	Edit(ctx context.Context, req contract.ConnectorRequest) error
	// Typing shows that the bot works in req.Chat and req.Thread.
	Typing(ctx context.Context, req contract.ConnectorRequest) error
	// Me returns the bot of the token, and so checks the token.
	Me(ctx context.Context, token string) (Bot, error)
}

// Formatter is a Platform that writes Markdown in its own markup. Serve
// formats a text before it cuts it.
type Formatter interface {
	Format(markdown string) string
}

// MediaReader is a Platform that reads the files of messages for the op media.
type MediaReader interface {
	Media(ctx context.Context, token, ref string) (contract.ConnectorFile, error)
}

// Splitter is a Platform that cuts its texts itself; others use Split.
type Splitter interface {
	Split(text string) []string
}

// Serve answers one call of the host. A failure of the platform is an answer
// with a typed error, not an error of the route: the host decides what it
// means. A request for another kind, without a token or with an unknown op is
// refused.
func Serve(ctx context.Context, p Platform, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if req.Kind != p.Kind() || req.Token == "" {
		return contract.ConnectorResponse{}, fmt.Errorf("%w: unknown kind or no token", contract.ErrRefused)
	}
	var out contract.ConnectorResponse
	var err error
	switch req.Op {
	case contract.ConnectorOpPoll:
		out, err = poll(ctx, p, req)
	case contract.ConnectorOpSend:
		out.MessageID, err = send(ctx, p, req)
	case contract.ConnectorOpEdit:
		parts := parts(p, req.Text)
		if len(parts) == 0 {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no text", contract.ErrRefused)
		}
		// An edit replaces one message: the text keeps what fits.
		req.Text = parts[0]
		err = Retry(ctx, func() error { return p.Edit(ctx, req) })
		out.MessageID = req.MessageID
	case contract.ConnectorOpTyping:
		err = p.Typing(ctx, req)
	case contract.ConnectorOpMe:
		var bot Bot
		if bot, err = p.Me(ctx, req.Token); err == nil {
			out.BotID, out.BotUsername = bot.ID, bot.Username
		}
	case contract.ConnectorOpMedia:
		m, ok := p.(MediaReader)
		if !ok || req.Media == "" {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no media", contract.ErrRefused)
		}
		var f contract.ConnectorFile
		if f, err = m.Media(ctx, req.Token, req.Media); err == nil {
			out.File = &f
		}
	default:
		return contract.ConnectorResponse{}, fmt.Errorf("%w: unknown op", contract.ErrRefused)
	}
	if err != nil {
		return contract.ConnectorResponse{Error: Typed(err)}, nil
	}
	return out, nil
}

func poll(ctx context.Context, p Platform, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	wait := min(max(req.TimeoutSeconds, 0), contract.MaxPollSeconds)
	ups, err := p.Poll(ctx, req.Token, req.Offset, wait)
	if err != nil {
		return contract.ConnectorResponse{}, err
	}
	next := req.Offset
	for _, u := range ups {
		next = max(next, u.Cursor+1)
	}
	return contract.ConnectorResponse{Updates: ups, NextOffset: next}, nil
}

// parts formats a text for the platform and cuts it to its limit.
func parts(p Platform, text string) []string {
	if f, ok := p.(Formatter); ok {
		text = f.Format(text)
	}
	if s, ok := p.(Splitter); ok {
		return s.Split(text)
	}
	return Split(text, p.MaxRunes())
}

// send sends the parts of a text in order and returns the ID of the last one.
func send(ctx context.Context, p Platform, req contract.ConnectorRequest) (string, error) {
	last := ""
	for _, part := range parts(p, req.Text) {
		r := req
		r.Text = part
		err := Retry(ctx, func() error {
			id, err := p.Send(ctx, r)
			if err == nil {
				last = id
			}
			return err
		})
		if err != nil {
			return "", err
		}
	}
	return last, nil
}

// Retry runs call, and runs it once more after the wait the platform asks
// for. A second wait is the caller's: the error goes back.
func Retry(ctx context.Context, call func() error) error {
	err := call()
	var ra *RetryAfter
	if !errors.As(err, &ra) {
		return err
	}
	timer := time.NewTimer(time.Duration(ra.Seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return call()
}

// Typed maps an error of a Platform to the typed error of the contract.
func Typed(err error) *contract.ConnectorError {
	var ra *RetryAfter
	switch {
	case errors.Is(err, ErrUnauthorized):
		return &contract.ConnectorError{Code: contract.ConnectorUnauthorized, Message: err.Error()}
	case errors.Is(err, ErrConflict):
		return &contract.ConnectorError{Code: contract.ConnectorConflict, Message: err.Error()}
	case errors.As(err, &ra):
		return &contract.ConnectorError{Code: contract.ConnectorRateLimited, Message: err.Error(), RetryAfter: ra.Seconds}
	}
	return &contract.ConnectorError{Code: contract.ConnectorFailed, Message: err.Error()}
}

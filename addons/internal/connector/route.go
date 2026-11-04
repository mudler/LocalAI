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
	"io"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Errors that a Platform returns. Serve maps them to the codes of the contract.
var (
	// ErrUnauthorized means the platform refused the token.
	ErrUnauthorized = errors.New("the platform refused the token")
	// ErrConflict means another process reads the updates of the same bot.
	ErrConflict = errors.New("another process reads the updates of this bot")
	// ErrTooLarge means a file of a message is larger than the bound of the
	// handle (contract.ConnectorTransfer.MaxBytes) or than the platform lets a
	// bot download.
	ErrTooLarge = errors.New("the file is too large")
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

// RichSender is a Platform that sends an answer in its own markup and falls
// back to plain text itself when the platform refuses the markup. The op send
// uses SendRich and EditRich for each part of req.Text, which is Markdown; the
// op edit, which shows a preview, still uses Edit.
type RichSender interface {
	SendRich(ctx context.Context, req contract.ConnectorRequest) (string, error)
	EditRich(ctx context.Context, req contract.ConnectorRequest) error
}

// Drafter is a Platform that shows a draft for the op draft: a preview of an
// answer that is not a message yet. req.DraftID is not zero.
type Drafter interface {
	Draft(ctx context.Context, req contract.ConnectorRequest) error
}

// HistoryReader is a Platform that reads the earlier messages of a thread for
// the op history. It returns the messages oldest first, the newest of those
// before req.Before, at most req.Limit of them, and whether older messages
// exist. Serve checks the request and bounds the answer.
type HistoryReader interface {
	History(ctx context.Context, req contract.ConnectorRequest) ([]contract.ConnectorHistoryMessage, bool, error)
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
	return ServeWith(ctx, p, nil, req)
}

// ServeWith is Serve for an add-on that has the host operation channel, so it
// can move file bytes by handle (see ConnectorTransfer). The ops media and
// send_file need a handle and a Platform that serves it; any other request for
// them is refused.
func ServeWith(ctx context.Context, p Platform, host Host, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
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
		// An edit replaces one message: the text keeps what fits. It is not
		// retried after a wait: an edit shows progress, and the host would
		// rather skip one than hold the turn's answer behind it.
		req.Text = parts[0]
		err = p.Edit(ctx, req)
		out.MessageID = req.MessageID
	case contract.ConnectorOpDraft:
		d, ok := p.(Drafter)
		parts := parts(p, req.Text)
		if !ok || req.DraftID == 0 || len(parts) == 0 {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no draft, no draft ID or no text", contract.ErrRefused)
		}
		// Like an edit, a draft is a preview: it is not retried after a wait.
		req.Text = parts[0]
		err = d.Draft(ctx, req)
	case contract.ConnectorOpTyping:
		err = p.Typing(ctx, req)
	case contract.ConnectorOpMe:
		var bot Bot
		if bot, err = p.Me(ctx, req.Token); err == nil {
			out.BotID, out.BotUsername = bot.ID, bot.Username
		}
	case contract.ConnectorOpSendFile:
		ss, ok := p.(StreamSender)
		if !ok || req.Transfer == nil || req.Transfer.Handle == "" || req.Transfer.Size <= 0 || req.Transfer.Name != contract.SafeFileName(req.Transfer.Name) {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no file, or one this platform cannot send", contract.ErrRefused)
		}
		err = Retry(ctx, func() error {
			id, err := ss.SendStream(ctx, req, Get(ctx, host, req.Transfer))
			out.MessageID = id
			return err
		})
	case contract.ConnectorOpMedia:
		if req.Transfer == nil {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no handle", contract.ErrRefused)
		}
		out.Received, err = receive(ctx, p, host, req)
	case contract.ConnectorOpHistory:
		h, ok := p.(HistoryReader)
		if !ok || req.Chat == "" || req.Thread == "" {
			return contract.ConnectorResponse{}, fmt.Errorf("%w: no thread, or a platform that cannot read one", contract.ErrRefused)
		}
		if req.Limit < 1 || req.Limit > contract.MaxHistoryMessages {
			req.Limit = contract.DefaultHistoryMessages
		}
		// Not retried after a wait: the history is context, and the host
		// would rather answer without it than hold the answer behind it.
		var msgs []contract.ConnectorHistoryMessage
		var more bool
		if msgs, more, err = h.History(ctx, req); err == nil {
			if len(msgs) > req.Limit {
				msgs, more = msgs[len(msgs)-req.Limit:], true
			}
			out.Messages, out.Truncated = msgs, more
		}
	default:
		return contract.ConnectorResponse{}, fmt.Errorf("%w: unknown op", contract.ErrRefused)
	}
	if errors.Is(err, contract.ErrRefused) {
		return contract.ConnectorResponse{}, err
	}
	if err != nil {
		return contract.ConnectorResponse{Error: Typed(err)}, nil
	}
	return out, nil
}

// receive streams the file of a reference to the host by the handle.
func receive(ctx context.Context, p Platform, host Host, req contract.ConnectorRequest) (*contract.ConnectorReceived, error) {
	sr, ok := p.(StreamReader)
	if !ok || req.Media == "" || req.Transfer.Handle == "" || req.Transfer.MaxBytes <= 0 {
		return nil, fmt.Errorf("%w: no media, or a platform that cannot stream it", contract.ErrRefused)
	}
	m, err := sr.MediaStream(ctx, req.Token, req.Media, req.Transfer.MaxBytes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = m.Body.Close() }()
	if m.Size > req.Transfer.MaxBytes {
		return nil, ErrTooLarge
	}
	// One byte past the bound shows a file that is larger than it said.
	if _, err := Put(ctx, host, req.Transfer, io.LimitReader(m.Body, req.Transfer.MaxBytes+1)); err != nil {
		return nil, err
	}
	return &contract.ConnectorReceived{Name: contract.SafeFileName(m.Name), MIME: m.MIME}, nil
}

type optionsKey struct{}

// OptionsFrom returns the options in effect that the host sent with the op
// poll (contract.ConnectorRequest.Options), for a Platform whose Poll reads
// them. It is nil when there are none.
func OptionsFrom(ctx context.Context) map[string]string {
	o, _ := ctx.Value(optionsKey{}).(map[string]string)
	return o
}

func poll(ctx context.Context, p Platform, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	ctx = context.WithValue(ctx, optionsKey{}, req.Options)
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
// When the request names a message, the first part replaces it; a message
// that cannot be edited (deleted, too old) gets the part as a new message.
func send(ctx context.Context, p Platform, req contract.ConnectorRequest) (string, error) {
	sendPart, editPart := p.Send, p.Edit
	if r, ok := p.(RichSender); ok {
		sendPart, editPart = r.SendRich, r.EditRich
	}
	last := ""
	replace := req.MessageID
	req.MessageID = ""
	for _, part := range parts(p, req.Text) {
		r := req
		r.Text = part
		if replace != "" {
			e := r
			e.MessageID = replace
			replace = ""
			if err := Retry(ctx, func() error { return editPart(ctx, e) }); err == nil {
				last = e.MessageID
				continue
			} else if ctx.Err() != nil {
				return "", ctx.Err()
			}
		}
		err := Retry(ctx, func() error {
			id, err := sendPart(ctx, r)
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
	case errors.Is(err, ErrTooLarge):
		return &contract.ConnectorError{Code: contract.ConnectorTooLarge, Message: err.Error()}
	case errors.As(err, &ra):
		return &contract.ConnectorError{Code: contract.ConnectorRateLimited, Message: err.Error(), RetryAfter: ra.Seconds}
	}
	return &contract.ConnectorError{Code: contract.ConnectorFailed, Message: err.Error()}
}

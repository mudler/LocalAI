package transport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/internal/idle"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// MaxMessageRunes is the longest text Telegram takes in one message.
const MaxMessageRunes = 4096

// Kind is the one kind of binding this add-on serves.
const Kind = "telegram"

// Runtime is the add-on runtime: the connector route over the Bot API. It
// declares no tool, so every other operation is refused as in any add-on that
// has only a UI.
type Runtime struct {
	idle.Runtime
	API *API
}

var _ contract.ConnectorRuntime = Runtime{}

// Connector serves one call of the host. A failure of Telegram is an answer
// with a typed error, not an error of the route: the host decides what it means.
func (r Runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if req.Kind != Kind || req.Token == "" {
		return contract.ConnectorResponse{}, fmt.Errorf("%w: unknown kind or no token", contract.ErrRefused)
	}
	api := r.API
	if api == nil {
		api = &API{}
	}
	var out contract.ConnectorResponse
	var err error
	switch req.Op {
	case contract.ConnectorOpPoll:
		out, err = r.poll(ctx, api, req)
	case contract.ConnectorOpSend:
		var chat int64
		if chat, err = strconv.ParseInt(req.Chat, 10, 64); err == nil {
			out.MessageID, err = r.send(ctx, api, req.Token, chat, req.Text)
		}
	case contract.ConnectorOpEdit:
		var chat, msg int64
		if chat, err = strconv.ParseInt(req.Chat, 10, 64); err == nil {
			if msg, err = strconv.ParseInt(req.MessageID, 10, 64); err == nil {
				err = api.Edit(ctx, req.Token, chat, msg, req.Text)
			}
		}
	case contract.ConnectorOpTyping:
		var chat int64
		if chat, err = strconv.ParseInt(req.Chat, 10, 64); err == nil {
			err = api.Typing(ctx, req.Token, chat)
		}
	case contract.ConnectorOpMe:
		var bot Bot
		if bot, err = api.Me(ctx, req.Token); err == nil {
			out.BotID, out.BotUsername = strconv.FormatInt(bot.ID, 10), bot.Username
		}
	default:
		return contract.ConnectorResponse{}, fmt.Errorf("%w: unknown op", contract.ErrRefused)
	}
	if err != nil {
		return contract.ConnectorResponse{Error: typed(err)}, nil
	}
	return out, nil
}

func (r Runtime) poll(ctx context.Context, api *API, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	wait := min(max(req.TimeoutSeconds, 0), contract.MaxPollSeconds)
	ups, err := api.Updates(ctx, req.Token, req.Offset, wait)
	if err != nil {
		return contract.ConnectorResponse{}, err
	}
	next := req.Offset
	for _, u := range ups {
		next = max(next, u.Cursor+1)
	}
	return contract.ConnectorResponse{Updates: ups, NextOffset: next}, nil
}

// send cuts the text to the limit of Telegram and sends the parts in order. A
// wait that Telegram asks for is kept once before the part is given up.
func (r Runtime) send(ctx context.Context, api *API, token string, chat int64, text string) (string, error) {
	last := int64(0)
	for _, part := range Split(text, MaxMessageRunes) {
		id, err := api.Send(ctx, token, chat, part)
		var ra *RetryAfter
		if errors.As(err, &ra) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(ra.Seconds) * time.Second):
			}
			id, err = api.Send(ctx, token, chat, part)
		}
		if err != nil {
			return "", err
		}
		last = id
	}
	return strconv.FormatInt(last, 10), nil
}

// typed maps an error of the Bot API to the typed error of the contract.
func typed(err error) *contract.ConnectorError {
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

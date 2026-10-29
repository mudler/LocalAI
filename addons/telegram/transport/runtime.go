package transport

import (
	"context"
	"strconv"

	"github.com/mudler/LocalAI/addons/internal/connector"
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

// Connector serves one call of the host through the shared route of the
// connector add-ons.
func (r Runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	api := r.API
	if api == nil {
		api = &API{}
	}
	return connector.Serve(ctx, platform{api}, req)
}

// platform is the Bot API as a connector.Platform. Telegram names chats and
// messages with numbers; the contract carries them as text.
type platform struct{ api *API }

func (platform) Kind() string  { return Kind }
func (platform) MaxRunes() int { return MaxMessageRunes }

func (p platform) Poll(ctx context.Context, token string, offset int64, wait int) ([]contract.ConnectorMessage, error) {
	return p.api.Updates(ctx, token, offset, wait)
}

var _ connector.FileSender = platform{}

// where reads the chat and the topic of a request. Telegram names them with
// numbers; a request without a topic has thread 0.
func where(req contract.ConnectorRequest) (chat, thread int64, err error) {
	if chat, err = strconv.ParseInt(req.Chat, 10, 64); err != nil {
		return 0, 0, err
	}
	if req.Thread != "" {
		if thread, err = strconv.ParseInt(req.Thread, 10, 64); err != nil {
			return 0, 0, err
		}
	}
	return chat, thread, nil
}

func (p platform) Send(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	chat, thread, err := where(req)
	if err != nil {
		return "", err
	}
	id, err := p.api.Send(ctx, req.Token, chat, thread, req.Text)
	return strconv.FormatInt(id, 10), err
}

func (p platform) SendFile(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	chat, thread, err := where(req)
	if err != nil {
		return "", err
	}
	id, err := p.api.SendFile(ctx, req.Token, chat, thread, req.File.Name, req.File.MIME, req.File.Data)
	return strconv.FormatInt(id, 10), err
}

func (p platform) Edit(ctx context.Context, req contract.ConnectorRequest) error {
	chat, err := strconv.ParseInt(req.Chat, 10, 64)
	if err != nil {
		return err
	}
	msg, err := strconv.ParseInt(req.MessageID, 10, 64)
	if err != nil {
		return err
	}
	return p.api.Edit(ctx, req.Token, chat, msg, req.Text)
}

func (p platform) Typing(ctx context.Context, req contract.ConnectorRequest) error {
	chat, thread, err := where(req)
	if err != nil {
		return err
	}
	return p.api.Typing(ctx, req.Token, chat, thread)
}

func (p platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	bot, err := p.api.Me(ctx, token)
	if err != nil {
		return connector.Bot{}, err
	}
	return connector.Bot{ID: strconv.FormatInt(bot.ID, 10), Username: bot.Username}, nil
}

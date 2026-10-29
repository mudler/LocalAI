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

var (
	_ connector.FileSender  = platform{}
	_ connector.MediaReader = platform{}
	_ connector.RichSender  = platform{}
	_ connector.Drafter     = platform{}
	_ connector.Splitter    = platform{}
)

// Split cuts an answer, which is Markdown, without breaking a code block. A
// part is at most MaxMessageRunes long as Markdown, and Telegram counts the
// text after it parsed the markup, which is never longer.
func (platform) Split(text string) []string { return connector.SplitFenced(text, MaxMessageRunes) }

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
	return messageRef(id), err
}

// messageRef is the ID of a message as the contract carries it: "" when
// Telegram named none, so the host never edits a message that is not there.
func messageRef(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func (p platform) SendFile(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	chat, thread, err := where(req)
	if err != nil {
		return "", err
	}
	id, err := p.api.SendFile(ctx, req.Token, chat, thread, req.File.Name, req.File.MIME, req.File.Data)
	return messageRef(id), err
}

// SendRich sends one part of an answer as MarkdownV2, or as plain text when
// Telegram refuses the markup.
func (p platform) SendRich(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	chat, thread, err := where(req)
	if err != nil {
		return "", err
	}
	id, err := p.api.SendMarkdown(ctx, req.Token, chat, thread, req.Text)
	return messageRef(id), err
}

// edited reads the chat and the message of an edit.
func edited(req contract.ConnectorRequest) (chat, msg int64, err error) {
	if chat, err = strconv.ParseInt(req.Chat, 10, 64); err != nil {
		return 0, 0, err
	}
	if msg, err = strconv.ParseInt(req.MessageID, 10, 64); err != nil {
		return 0, 0, err
	}
	return chat, msg, nil
}

// Edit shows a preview: plain text, since the answer is not whole yet and
// its markup may be open.
func (p platform) Edit(ctx context.Context, req contract.ConnectorRequest) error {
	chat, msg, err := edited(req)
	if err != nil {
		return err
	}
	return p.api.Edit(ctx, req.Token, chat, msg, req.Text)
}

// EditRich replaces the working message with the first part of an answer.
func (p platform) EditRich(ctx context.Context, req contract.ConnectorRequest) error {
	chat, msg, err := edited(req)
	if err != nil {
		return err
	}
	return p.api.EditMarkdown(ctx, req.Token, chat, msg, req.Text)
}

// Draft shows a preview as a draft, which only a private chat has; Telegram
// refuses it elsewhere, and the host then edits a working message instead.
func (p platform) Draft(ctx context.Context, req contract.ConnectorRequest) error {
	chat, thread, err := where(req)
	if err != nil {
		return err
	}
	return p.api.Draft(ctx, req.Token, chat, thread, req.DraftID, req.Text)
}

func (p platform) Typing(ctx context.Context, req contract.ConnectorRequest) error {
	chat, thread, err := where(req)
	if err != nil {
		return err
	}
	return p.api.Typing(ctx, req.Token, chat, thread)
}

// Media reads a photo, a voice message, an audio file, a video note or a
// document of a message, by the reference that Updates made.
func (p platform) Media(ctx context.Context, token, ref string) (contract.ConnectorFile, error) {
	return p.api.Media(ctx, token, ref)
}

func (p platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	bot, err := p.api.Me(ctx, token)
	if err != nil {
		return connector.Bot{}, err
	}
	return connector.Bot{ID: strconv.FormatInt(bot.ID, 10), Username: bot.Username}, nil
}

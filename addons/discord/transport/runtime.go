// SPDX-License-Identifier: MIT
package transport

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/internal/idle"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "discord"

// MaxMessageRunes is the longest text of one message that Discord takes.
const MaxMessageRunes = 2000

// Runtime is the add-on runtime: the connector route over the REST API and
// the Gateway. It declares no tool, so every other operation is refused as in
// any add-on that has only a UI.
type Runtime struct {
	idle.Runtime
	API     *API
	Gateway *Gateway
	// Host is the host operation channel, with which a file moves by handle.
	// Nil: the add-on cannot move files.
	Host connector.Host

	mu  sync.Mutex
	dms map[string]dmChannel
}

type dmChannel struct {
	id string
	at time.Time
}

// New returns a runtime over api.
func New(api *API) *Runtime {
	return &Runtime{API: api, Gateway: &Gateway{API: api}, dms: map[string]dmChannel{}}
}

var _ contract.ConnectorRuntime = (*Runtime)(nil)

// Connector serves one call of the host through the shared route of the
// connector add-ons.
func (r *Runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return connector.ServeWith(ctx, platform{r}, r.Host, req)
}

// Close closes the Gateway links.
func (r *Runtime) Close() { r.Gateway.Close() }

// platform is Discord as a connector.Platform. The secret of a binding is the
// bot token.
type platform struct{ r *Runtime }

var (
	_ connector.Formatter     = platform{}
	_ connector.Splitter      = platform{}
	_ connector.HistoryReader = platform{}
	_ connector.StreamReader  = platform{}
	_ connector.StreamSender  = platform{}
)

func (platform) Kind() string                  { return Kind }
func (platform) MaxRunes() int                 { return MaxMessageRunes }
func (platform) Format(markdown string) string { return Format(markdown) }
func (platform) Split(text string) []string    { return connector.SplitFenced(text, MaxMessageRunes) }

func (p platform) Poll(ctx context.Context, token string, offset int64, wait int) ([]contract.ConnectorMessage, error) {
	return p.r.Gateway.Poll(ctx, token, offset, wait)
}

// userPrefix marks a chat that is a person: the add-on opens the direct
// conversation with them. It is for the notifications of a binding, whose
// setting holds one ID that can be a channel or a person.
const userPrefix = "user:"

// channel is where a request writes: the thread when it has one, else the
// chat. A chat of the form user:<ID> is the direct conversation with a person.
func (r *Runtime) channel(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	if req.Thread != "" {
		return req.Thread, nil
	}
	user, ok := strings.CutPrefix(req.Chat, userPrefix)
	if !ok {
		return req.Chat, nil
	}
	key := keyOf(req.Token) + user
	r.mu.Lock()
	d, hit := r.dms[key]
	r.mu.Unlock()
	if hit && time.Since(d.at) < time.Hour {
		return d.id, nil
	}
	id, err := r.API.DMChannel(ctx, req.Token, user)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if len(r.dms) > maxThreads {
		clear(r.dms)
	}
	r.dms[key] = dmChannel{id: id, at: time.Now()}
	r.mu.Unlock()
	return id, nil
}

func (p platform) Send(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	ch, err := p.r.channel(ctx, req)
	if err != nil {
		return "", err
	}
	return p.r.API.PostMessage(ctx, req.Token, ch, req.Text)
}

func (p platform) Edit(ctx context.Context, req contract.ConnectorRequest) error {
	ch, err := p.r.channel(ctx, req)
	if err != nil {
		return err
	}
	return p.r.API.EditMessage(ctx, req.Token, ch, req.MessageID, req.Text)
}

// Delete removes the preview that an answer could not replace.
func (p platform) Delete(ctx context.Context, req contract.ConnectorRequest) error {
	ch, err := p.r.channel(ctx, req)
	if err != nil {
		return err
	}
	return p.r.API.DeleteMessage(ctx, req.Token, ch, req.MessageID)
}

// Typing does nothing. The typing indicator of Discord lasts ten seconds and
// its documentation discourages it for bots; the host shows its own working
// message and edits it.
func (platform) Typing(context.Context, contract.ConnectorRequest) error { return nil }

// Me checks the token and names the bot. It also says when the Message
// Content intent is off, because the bot then reads no text in servers.
func (p platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	u, err := p.r.API.Me(ctx, token)
	if err != nil {
		return connector.Bot{}, err
	}
	name := u.Username
	// The application is the account: a second token of the same application
	// is the same bot. The bot user is the fallback when it cannot be read.
	account := u.ID
	if app, ok := p.r.API.Application(ctx, token); ok {
		if app.ID != "" {
			account = app.ID
		}
		if app.Flags&(flagMessageContent|flagMessageContentLimited) == 0 {
			name += " (warning: the Message Content intent is off, so the bot cannot read messages in servers; turn it on in the developer portal)"
		}
	}
	return connector.Bot{ID: u.ID, Username: name, AccountID: account}, nil
}

// MediaStream streams a file of a message to the host. The reference holds
// the channel, message and file IDs; the message is read again for the current
// signed URL of the file. The size is checked first, so a file over the bound
// is refused before any byte moves.
func (p platform) MediaStream(ctx context.Context, token, ref string, bound int64) (connector.MediaStream, error) {
	channel, message, attachment, ok := splitRef(ref)
	if !ok {
		return connector.MediaStream{}, errors.New("discord: not a file reference")
	}
	m, err := p.r.API.Message(ctx, token, channel, message)
	if err != nil {
		return connector.MediaStream{}, err
	}
	for _, a := range m.Attachments {
		if a.ID != attachment {
			continue
		}
		if a.Size > min(bound, MaxFileBytes) {
			return connector.MediaStream{}, ErrTooLarge
		}
		body, err := p.r.API.DownloadStream(ctx, a.URL)
		if err != nil {
			return connector.MediaStream{}, err
		}
		name := a.Filename
		if name == "" {
			name = a.ID
		}
		return connector.MediaStream{Name: name, MIME: a.ContentType, Size: a.Size, Body: body}, nil
	}
	return connector.MediaStream{}, errors.New("discord: the message has no such file any more")
}

// SendStream uploads a file that the host hands over by handle.
func (p platform) SendStream(ctx context.Context, req contract.ConnectorRequest, body io.Reader) (string, error) {
	ch, err := p.r.channel(ctx, req)
	if err != nil {
		return "", err
	}
	return p.r.API.UploadStream(ctx, req.Token, ch, req.Transfer.Name, req.Transfer.Size, body)
}

// History returns the earlier messages of a thread: the newest req.Limit
// before req.Before, oldest first. A message of the bot is marked Own.
// Messages of other bots and of Discord (joins, pins) are left out, as the
// bot does not read them live either. The file bytes are never read, only
// their names. Discord returns at most 100 messages for one call.
func (p platform) History(ctx context.Context, req contract.ConnectorRequest) ([]contract.ConnectorHistoryMessage, bool, error) {
	want := min(req.Limit+1, 100)
	raw, err := p.r.API.Before(ctx, req.Token, req.Thread, req.Before, want)
	if err != nil {
		return nil, false, err
	}
	more := len(raw) >= want
	self := p.r.Gateway.botID(ctx, req.Token)
	out := make([]contract.ConnectorHistoryMessage, 0, len(raw))
	// Discord returns the newest first.
	for i := len(raw) - 1; i >= 0; i-- {
		m := raw[i]
		own := self != "" && m.Author.ID == self
		if !own && (m.Author.Bot || m.WebhookID != "" || (m.Type != 0 && m.Type != 19)) {
			continue
		}
		h := contract.ConnectorHistoryMessage{
			MessageID: m.ID, SenderID: m.Author.ID, SenderName: m.Author.Display(),
			Text: Plain(m.Content, m.Mentions, self), TS: m.Timestamp.UTC(), Own: own,
		}
		for _, a := range m.Attachments {
			h.Media++
			if len(h.MediaNames) < contract.MaxHistoryMedia && a.Filename != "" {
				h.MediaNames = append(h.MediaNames, a.Filename)
			}
		}
		out = append(out, h)
	}
	if len(out) > req.Limit {
		out, more = out[len(out)-req.Limit:], true
	}
	return out, more, nil
}

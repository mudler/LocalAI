// SPDX-License-Identifier: MIT
package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/internal/idle"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the one kind of binding this add-on serves.
const Kind = "slack"

// MaxMessageRunes is the longest text of one message. Slack takes more, but
// it asks that the text of a message stays under 4,000 characters.
const MaxMessageRunes = 4000

// Working is the message that shows that the bot works. The answer replaces
// it, so the conversation shows one message per answer.
const Working = "_Working…_"

// placeholderLife is how long a "working" message waits for its answer.
const placeholderLife = 15 * time.Minute

// Runtime is the add-on runtime: the connector route over the Web API and
// Socket Mode. It declares no tool, so every other operation is refused as in
// any add-on that has only a UI.
type Runtime struct {
	idle.Runtime
	API    *API
	Socket *Socket

	mu      sync.Mutex
	working map[string]placeholder
}

type placeholder struct {
	ts string
	at time.Time
}

// New returns a runtime over api.
func New(api *API) *Runtime {
	return &Runtime{API: api, Socket: &Socket{API: api}, working: map[string]placeholder{}}
}

var _ contract.ConnectorRuntime = (*Runtime)(nil)

// Connector serves one call of the host through the shared route of the
// connector add-ons.
func (r *Runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return connector.Serve(ctx, platform{r}, req)
}

// Close closes the Socket Mode links.
func (r *Runtime) Close() { r.Socket.Close() }

// platform is Slack as a connector.Platform. The secret of a binding holds
// the bot token and the app-level token (see ParseTokens).
type platform struct{ r *Runtime }

var (
	_ connector.Formatter   = platform{}
	_ connector.Splitter    = platform{}
	_ connector.MediaReader = platform{}
	_ connector.FileSender  = platform{}
)

func (platform) Kind() string                  { return Kind }
func (platform) MaxRunes() int                 { return MaxMessageRunes }
func (platform) Format(markdown string) string { return Mrkdwn(markdown) }
func (platform) Split(text string) []string    { return connector.SplitFenced(text, MaxMessageRunes) }

func (p platform) Poll(ctx context.Context, token string, offset int64, wait int) ([]contract.ConnectorMessage, error) {
	return p.r.Socket.Poll(ctx, ParseTokens(token), offset, wait)
}

func bot(token string) (string, error) {
	t := ParseTokens(token)
	if t.Bot == "" {
		return "", missing(t)
	}
	return t.Bot, nil
}

// Send posts one part. The first part of an answer replaces the "working"
// message of its conversation, when there is one.
func (p platform) Send(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	b, err := bot(req.Token)
	if err != nil {
		return "", err
	}
	if ts, ok := p.r.take(req); ok {
		if err := p.r.API.Update(ctx, b, req.Chat, ts, req.Text); err == nil {
			return ts, nil
		}
		// The message was deleted, or is too old to edit: post a new one.
	}
	return p.r.API.PostMessage(ctx, b, req.Chat, req.Thread, req.Text)
}

func (p platform) Edit(ctx context.Context, req contract.ConnectorRequest) error {
	b, err := bot(req.Token)
	if err != nil {
		return err
	}
	return p.r.API.Update(ctx, b, req.Chat, req.MessageID, req.Text)
}

// Typing posts the "working" message of a conversation once. Slack has no
// typing indicator for a bot; the answer replaces this message. When the
// host shows its own working message (req.MessageID), there is nothing to do.
func (p platform) Typing(ctx context.Context, req contract.ConnectorRequest) error {
	b, err := bot(req.Token)
	if err != nil {
		return err
	}
	if req.MessageID != "" {
		return nil
	}
	key := p.r.mark(req)
	p.r.mu.Lock()
	ph, ok := p.r.working[key]
	p.r.mu.Unlock()
	if ok && time.Since(ph.at) < placeholderLife {
		return nil
	}
	ts, err := p.r.API.PostMessage(ctx, b, req.Chat, req.Thread, Working)
	if err != nil {
		return err
	}
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	for k, old := range p.r.working {
		if time.Since(old.at) > placeholderLife {
			delete(p.r.working, k)
		}
	}
	p.r.working[key] = placeholder{ts: ts, at: time.Now()}
	return nil
}

// SendFile uploads a file into the conversation and returns the file's ID.
func (p platform) SendFile(ctx context.Context, req contract.ConnectorRequest) (string, error) {
	b, err := bot(req.Token)
	if err != nil {
		return "", err
	}
	return p.r.API.Upload(ctx, b, req.Chat, req.Thread, req.File.Name, req.File.Data)
}

// Me checks both tokens: the bot token names the bot, and the app-level token
// must open a Socket Mode link (the ticket it gets is not used).
func (p platform) Me(ctx context.Context, token string) (connector.Bot, error) {
	t := ParseTokens(token)
	if t.Bot == "" || t.App == "" {
		return connector.Bot{}, missing(t)
	}
	id, err := p.r.API.AuthTest(ctx, t.Bot)
	if err != nil {
		return connector.Bot{}, err
	}
	if _, err := p.r.API.OpenConnection(ctx, t.App); err != nil {
		return connector.Bot{}, fmt.Errorf("slack: the app-level token: %w", err)
	}
	name := id.User
	if id.Team != "" {
		name += " (" + id.Team + ")"
	}
	return connector.Bot{ID: id.UserID, Username: name}, nil
}

var fileID = regexp.MustCompile(`^F[A-Z0-9]{1,31}$`)

// Media reads a file of a message, by its Slack ID.
func (p platform) Media(ctx context.Context, token, ref string) (contract.ConnectorFile, error) {
	b, err := bot(token)
	if err != nil {
		return contract.ConnectorFile{}, err
	}
	if !fileID.MatchString(ref) {
		return contract.ConnectorFile{}, errors.New("slack: not a file ID")
	}
	f, err := p.r.API.FileInfo(ctx, b, ref)
	if err != nil {
		return contract.ConnectorFile{}, err
	}
	if f.Size > contract.MaxConnectorFileBytes {
		return contract.ConnectorFile{}, ErrTooLarge
	}
	data, err := p.r.API.Download(ctx, b, f.Download, contract.MaxConnectorFileBytes)
	if err != nil {
		return contract.ConnectorFile{}, err
	}
	name := f.Name
	if name == "" {
		name = f.ID
	}
	return contract.ConnectorFile{Name: name, MIME: f.Mimetype, Data: data}, nil
}

// mark is the key of the "working" message of a conversation. It holds a hash
// of the token, never the token.
func (r *Runtime) mark(req contract.ConnectorRequest) string {
	sum := sha256.Sum256([]byte(req.Token + "\x00" + req.Chat + "\x00" + req.Thread))
	return hex.EncodeToString(sum[:])
}

// take removes the "working" message of a conversation and returns it.
func (r *Runtime) take(req contract.ConnectorRequest) (string, bool) {
	key := r.mark(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	ph, ok := r.working[key]
	delete(r.working, key)
	return ph.ts, ok && time.Since(ph.at) < placeholderLife
}

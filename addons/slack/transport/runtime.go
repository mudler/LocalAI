// SPDX-License-Identifier: MIT
package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
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
	// Host is the host operation channel, with which a file moves by handle.
	// Nil: the add-on keeps the inline path.
	Host connector.Host

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
	return connector.ServeWith(ctx, platform{r}, r.Host, req)
}

// Close closes the Socket Mode links.
func (r *Runtime) Close() { r.Socket.Close() }

// platform is Slack as a connector.Platform. The secret of a binding holds
// the bot token and the app-level token (see ParseTokens).
type platform struct{ r *Runtime }

var (
	_ connector.Deleter       = platform{}
	_ connector.Formatter     = platform{}
	_ connector.Splitter      = platform{}
	_ connector.HistoryReader = platform{}
	_ connector.StreamReader  = platform{}
	_ connector.StreamSender  = platform{}
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

// Delete removes the preview that an answer could not replace.
func (p platform) Delete(ctx context.Context, req contract.ConnectorRequest) error {
	b, err := bot(req.Token)
	if err != nil {
		return err
	}
	return p.r.API.Delete(ctx, b, req.Chat, req.MessageID)
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
	// The bot user and the workspace together name the app: every token pair of
	// the same app installed in the same workspace gives the same pair.
	account := id.UserID
	if id.TeamID != "" {
		account = id.TeamID + "/" + id.UserID
	}
	return connector.Bot{ID: id.UserID, Username: name, AccountID: account}, nil
}

var fileID = regexp.MustCompile(`^F[A-Z0-9]{1,31}$`)

// MediaStream streams a file of a message, by its Slack ID, to the host. The
// platform names the size first, so a file over the bound is refused before
// any byte moves.
func (p platform) MediaStream(ctx context.Context, token, ref string, max int64) (connector.MediaStream, error) {
	b, err := bot(token)
	if err != nil {
		return connector.MediaStream{}, err
	}
	if !fileID.MatchString(ref) {
		return connector.MediaStream{}, errors.New("slack: not a file ID")
	}
	f, err := p.r.API.FileInfo(ctx, b, ref)
	if err != nil {
		return connector.MediaStream{}, err
	}
	if f.Size > max {
		return connector.MediaStream{}, ErrTooLarge
	}
	body, err := p.r.API.DownloadStream(ctx, b, f.Download)
	if err != nil {
		return connector.MediaStream{}, err
	}
	name := f.Name
	if name == "" {
		name = f.ID
	}
	return connector.MediaStream{Name: name, MIME: f.Mimetype, Size: f.Size, Body: body}, nil
}

// SendStream uploads a file that the host hands over by handle.
func (p platform) SendStream(ctx context.Context, req contract.ConnectorRequest, body io.Reader) (string, error) {
	b, err := bot(req.Token)
	if err != nil {
		return "", err
	}
	return p.r.API.UploadStream(ctx, b, req.Chat, req.Thread, req.Transfer.Name, req.Transfer.Size, body)
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

// Limits of the history of a thread.
const (
	// repliesPage is the size of one page of conversations.replies.
	repliesPage = 200
	// repliesPages is how many pages one history reads. Slack returns a thread
	// oldest first, so the newest messages are at its end: a thread longer
	// than repliesPages pages is refused, not shown from its middle.
	repliesPages = 5
	// historyNames bounds the people whose names one history looks up.
	historyNames = 30
)

// errThreadTooLong means the thread has more messages than one history reads.
var errThreadTooLong = errors.New("slack: the thread is too long to read")

// History returns the earlier messages of a thread: the newest req.Limit
// before req.Before, oldest first, with the names of their authors. A message
// of the bot is marked Own. Messages that no person or app wrote (joins,
// deletions) are left out. The file bytes are never read, only their names.
func (p platform) History(ctx context.Context, req contract.ConnectorRequest) ([]contract.ConnectorHistoryMessage, bool, error) {
	b, err := bot(req.Token)
	if err != nil {
		return nil, false, err
	}
	var all []Reply
	cursor := ""
	for page := 0; ; page++ {
		if page == repliesPages {
			return nil, false, errThreadTooLong
		}
		got, next, err := p.r.API.Replies(ctx, b, req.Chat, req.Thread, req.Before, cursor, repliesPage)
		if err != nil {
			return nil, false, err
		}
		all = append(all, got...)
		if next == "" {
			break
		}
		cursor = next
	}
	all = slices.DeleteFunc(all, func(m Reply) bool {
		return m.TS == req.Before || (m.Subtype != "" && !personal[m.Subtype] && m.Subtype != "bot_message")
	})
	more := len(all) > req.Limit
	if more {
		all = all[len(all)-req.Limit:]
	}
	self := p.r.Socket.botUser(ctx, req.Token)
	users := p.r.Socket.namer(ParseTokens(req.Token))
	out := make([]contract.ConnectorHistoryMessage, 0, len(all))
	for _, m := range all {
		h := contract.ConnectorHistoryMessage{MessageID: m.TS, SenderID: m.User, Text: Plain(m.Text, ""), Own: self != "" && m.User == self}
		if sec, err := strconv.ParseFloat(m.TS, 64); err == nil {
			h.TS = time.UnixMicro(int64(sec * 1e6)).UTC()
		}
		switch {
		case m.User != "":
			if u, ok := users(ctx, m.User); ok {
				h.SenderName, h.SenderUsername = u.Display(), u.Name
			}
		case m.BotID != "":
			h.SenderID, h.SenderName = m.BotID, m.Username
		}
		for _, f := range m.Files {
			h.Media++
			if len(h.MediaNames) < contract.MaxHistoryMedia && f.Name != "" {
				h.MediaNames = append(h.MediaNames, f.Name)
			}
		}
		out = append(out, h)
	}
	return out, more, nil
}

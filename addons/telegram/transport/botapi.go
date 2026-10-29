// Package transport speaks the Telegram Bot API for the Telegram add-on: long
// polling, sending, typing and who the bot is. It holds no state; the host
// passes the bot token on every call.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// DefaultBase is the Bot API. Only a test sets another base URL.
const DefaultBase = "https://api.telegram.org"

// Errors of the Bot API. They are the shared errors of the connector
// add-ons, so the route types them.
var (
	// ErrUnauthorized means Telegram refused the token.
	ErrUnauthorized = connector.ErrUnauthorized
	// ErrConflict means another process polls the same bot.
	ErrConflict = connector.ErrConflict
)

// RetryAfter means Telegram asks the caller to wait.
type RetryAfter = connector.RetryAfter

// Bot is the identity of a bot.
type Bot struct {
	ID       int64
	Username string
}

// API speaks the Bot API. The token goes in the URL path, as the API
// wants, and is taken out of every error.
type API struct {
	// BaseURL is DefaultBase when empty.
	BaseURL string
	// Client sends the requests. Nil: a client of pkg/httpclient with a
	// timeout.
	Client *http.Client

	// names caches the username of each bot, by the bot's ID, to find a
	// mention in a group. It holds no token.
	mu    sync.Mutex
	names map[int64]string
}

func (t *API) client(extra time.Duration) *http.Client {
	if t.Client != nil {
		return t.Client
	}
	// pkg/httpclient refuses redirects, so a Bot API answer cannot send the
	// token to another host.
	return httpclient.NewWithTimeout(extra + 15*time.Second)
}

type apiError struct {
	Code        int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// callError is a refusal of the Bot API that has no typed error. Its
// description never holds the token.
type callError struct {
	Method      string
	Code        int
	Description string
}

func (e *callError) Error() string {
	return fmt.Sprintf("telegram: Telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// redactedError is an error whose text named the token. It does not wrap the
// original, so no caller can print the token from the chain.
type redactedError struct{ msg string }

func (e *redactedError) Error() string { return e.msg }

// redact takes the token, raw and as it goes in the URL, out of the text of
// an error. The Bot API puts the token in the path, and an error of the
// network or a description of Telegram can echo it.
func redact(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := err.Error()
	clean := strings.NewReplacer(token, "<token>", url.PathEscape(token), "<token>").Replace(msg)
	if clean == msg {
		return err
	}
	return &redactedError{msg: clean}
}

// call posts a method and decodes the result into out.
func (t *API) call(ctx context.Context, token, method string, params map[string]any, wait time.Duration, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram: could not encode a Telegram %s request", method)
	}
	return t.post(ctx, token, method, "application/json", body, wait, out)
}

// post sends a body of the given type to a method and decodes the result
// into out. No error it returns holds the token.
func (t *API) post(ctx context.Context, token, method, contentType string, body []byte, wait time.Duration, out any) error {
	return redact(t.do(ctx, token, method, contentType, body, wait, out), token)
}

func (t *API) do(ctx context.Context, token, method, contentType string, body []byte, wait time.Duration, out any) error {
	base := t.BaseURL
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/bot"+url.PathEscape(token)+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: could not build a Telegram request")
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := t.client(wait).Do(req)
	if err != nil {
		// The error of net/http names the URL, which has the token.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("telegram: Telegram %s failed: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		apiError
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("telegram: Telegram %s answered %d with something that is not JSON", method, resp.StatusCode)
	}
	if !env.OK {
		code := env.Code
		if code == 0 {
			code = resp.StatusCode
		}
		switch {
		case code == http.StatusUnauthorized:
			return ErrUnauthorized
		case code == http.StatusConflict:
			return ErrConflict
		case code == http.StatusTooManyRequests:
			return &RetryAfter{Seconds: max(env.Parameters.RetryAfter, 1)}
		}
		desc := strings.NewReplacer(token, "<token>", url.PathEscape(token), "<token>").Replace(env.Description)
		return &callError{Method: method, Code: code, Description: desc}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram: could not read the result of Telegram %s", method)
		}
	}
	return nil
}

// refusedBecause tells whether Telegram refused a request as a bad one, for
// a reason whose description holds what.
func refusedBecause(err error, what string) bool {
	var ce *callError
	return errors.As(err, &ce) && ce.Code == http.StatusBadRequest && strings.Contains(ce.Description, what)
}

// Me returns the bot of a token.
func (t *API) Me(ctx context.Context, token string) (Bot, error) {
	var r struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := t.call(ctx, token, "getMe", map[string]any{}, 0, &r); err != nil {
		return Bot{}, err
	}
	return Bot{ID: r.ID, Username: r.Username}, nil
}

type tgMessage struct {
	ID     int64 `json:"message_id"`
	Thread int64 `json:"message_thread_id"`
	// Topic tells that Thread is a topic of a forum. Without it, Thread is
	// only the start of a reply chain.
	Topic bool `json:"is_topic_message"`
	Chat  struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Text     string     `json:"text"`
	Entities []tgEntity `json:"entities"`
	// A message with a file has a caption, not a text.
	Caption         string     `json:"caption"`
	CaptionEntities []tgEntity `json:"caption_entities"`
	// MediaGroup names the album that the message is one item of.
	MediaGroup string    `json:"media_group_id"`
	Photo      []tgPhoto `json:"photo"`
	Voice      *tgFile   `json:"voice"`
	Audio      *tgFile   `json:"audio"`
	VideoNote  *tgFile   `json:"video_note"`
	Document   *tgFile   `json:"document"`
	ReplyTo    *struct {
		ID   int64 `json:"message_id"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
	} `json:"reply_to_message"`
}

type tgEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// albumPolls is how many more times Updates asks for updates while the last
// update it has is an item of an album: Telegram answers a long poll as soon
// as the first item of an album is there.
const albumPolls = 3

// update is a message of getUpdates with the album it is an item of.
type update struct {
	contract.ConnectorMessage
	group string
}

// Updates long-polls for updates from offset on, waiting up to timeoutSeconds.
// Each update comes back as a normalized message with its update ID as the
// cursor. A kind that is not a plain message (an edited message, a channel
// post) has Skip set, so the host can move its offset past it. The items of
// an album that follow each other become one message with all their files
// (see mergeAlbums).
func (t *API) Updates(ctx context.Context, token string, offset int64, timeoutSeconds int) ([]contract.ConnectorMessage, error) {
	ups, err := t.updates(ctx, token, offset, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	for i := 0; i < albumPolls && len(ups) > 0 && ups[len(ups)-1].group != ""; i++ {
		// The rest of an album may be on its way: wait a moment and ask
		// again, from the same offset. A higher offset would confirm the
		// updates to Telegram before the host has handled them. A failure
		// only ends the wait; the next poll reads the rest.
		if !sleep(ctx, albumWait) {
			break
		}
		more, err := t.updates(ctx, token, offset, 0)
		if err != nil || len(more) < len(ups) {
			break
		}
		ups = more
	}
	return mergeAlbums(ups), nil
}

func (t *API) updates(ctx context.Context, token string, offset int64, timeoutSeconds int) ([]update, error) {
	var raw []struct {
		ID      int64           `json:"update_id"`
		Message json.RawMessage `json:"message"`
	}
	params := map[string]any{"offset": offset, "timeout": timeoutSeconds, "allowed_updates": []string{"message"}}
	if err := t.call(ctx, token, "getUpdates", params, time.Duration(timeoutSeconds)*time.Second, &raw); err != nil {
		return nil, err
	}
	out := make([]update, 0, len(raw))
	for _, r := range raw {
		u := contract.ConnectorMessage{Cursor: r.ID}
		var m tgMessage
		if len(r.Message) == 0 || string(r.Message) == "null" || json.Unmarshal(r.Message, &m) != nil {
			u.Skip = true
			out = append(out, update{ConnectorMessage: u})
			continue
		}
		if m.Text == "" {
			// The caption of a file is its text, with its own entities.
			m.Text, m.Entities = m.Caption, m.CaptionEntities
		}
		u.Chat, u.Direct = strconv.FormatInt(m.Chat.ID, 10), m.Chat.Type == "private"
		u.SenderID = strconv.FormatInt(m.From.ID, 10)
		u.SenderName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		u.SenderUsername, u.Text = m.From.Username, m.Text
		u.Media = mediaRefs(m)
		u.Unsupported = m.Text == "" && len(u.Media) == 0
		if m.ID != 0 {
			u.MessageID = strconv.FormatInt(m.ID, 10)
		}
		if m.Topic && m.Thread != 0 {
			u.Thread = strconv.FormatInt(m.Thread, 10)
		}
		if m.ReplyTo != nil && m.ReplyTo.ID != 0 {
			u.ReplyTo = strconv.FormatInt(m.ReplyTo.ID, 10)
		}
		if !u.Direct {
			u.Mention, u.Text = t.addressed(ctx, token, m)
		}
		out = append(out, update{ConnectorMessage: u, group: m.MediaGroup})
	}
	return out, nil
}

// botID is the ID of the bot of a token: the part before the colon.
func botID(token string) int64 {
	head, _, _ := strings.Cut(token, ":")
	id, _ := strconv.ParseInt(head, 10, 64)
	return id
}

// addressed tells whether a message in a group is for the bot: it answers a
// message of the bot, or names the bot. The name is taken out of the text.
func (t *API) addressed(ctx context.Context, token string, m tgMessage) (bool, string) {
	id := botID(token)
	if m.ReplyTo != nil && id != 0 && m.ReplyTo.From.ID == id {
		return true, m.Text
	}
	if !strings.Contains(m.Text, "@") {
		return false, m.Text
	}
	name := t.botName(ctx, token, id)
	if name == "" {
		return false, m.Text
	}
	// Entity offsets count UTF-16 units.
	units := utf16.Encode([]rune(m.Text))
	for _, e := range m.Entities {
		if e.Type != "mention" || e.Offset < 0 || e.Length < 1 || e.Offset+e.Length > len(units) {
			continue
		}
		at := string(utf16.Decode(units[e.Offset : e.Offset+e.Length]))
		if strings.EqualFold(at, "@"+name) {
			rest := append(append([]uint16{}, units[:e.Offset]...), units[e.Offset+e.Length:]...)
			return true, strings.Join(strings.Fields(string(utf16.Decode(rest))), " ")
		}
	}
	return false, m.Text
}

// botName is the username of the bot of a token, read once with getMe. It
// is "" when getMe fails; the next message asks again.
func (t *API) botName(ctx context.Context, token string, id int64) string {
	t.mu.Lock()
	name, ok := t.names[id]
	t.mu.Unlock()
	if ok {
		return name
	}
	bot, err := t.Me(ctx, token)
	if err != nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.names == nil {
		t.names = map[int64]string{}
	}
	t.names[id] = bot.Username
	return bot.Username
}

// ParseMode of an answer: Telegram's dialect of Markdown.
const markdownV2 = "MarkdownV2"

// textParams is the body of sendMessage and editMessageText. The preview of
// a link is off: an answer that cites sources would otherwise show a card
// for the first one, and a preview would change as the text grows.
func textParams(chatID int64, text, parseMode string) map[string]any {
	p := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]any{"is_disabled": true}}
	if parseMode != "" {
		p["parse_mode"] = parseMode
	}
	return p
}

// Send sends plain text and returns the ID of the message. The caller splits
// the text to the limit of Telegram. A thread other than 0 is the topic of a
// forum.
func (t *API) Send(ctx context.Context, token string, chatID, thread int64, text string) (int64, error) {
	return t.send(ctx, token, chatID, thread, text, "")
}

// SendMarkdown sends Markdown as MarkdownV2 and returns the ID of the
// message. A model writes the Markdown, so Telegram may still refuse what it
// becomes; the message then goes as plain text. The caller splits the text
// to the limit of Telegram.
func (t *API) SendMarkdown(ctx context.Context, token string, chatID, thread int64, md string) (int64, error) {
	id, err := t.send(ctx, token, chatID, thread, MarkdownV2(md), markdownV2)
	if refusedBecause(err, "can't parse entities") {
		return t.send(ctx, token, chatID, thread, PlainText(md), "")
	}
	return id, err
}

func (t *API) send(ctx context.Context, token string, chatID, thread int64, text, parseMode string) (int64, error) {
	params := textParams(chatID, text, parseMode)
	if thread != 0 {
		params["message_thread_id"] = thread
	}
	var raw json.RawMessage
	if err := t.call(ctx, token, "sendMessage", params, 0, &raw); err != nil {
		return 0, err
	}
	return messageID(raw), nil
}

// Draft shows text as a draft in a private chat: a message that the bot is
// still writing, which the next message of the bot replaces. The same draftID
// across calls animates the change. A thread other than 0 is the topic.
func (t *API) Draft(ctx context.Context, token string, chatID, thread, draftID int64, text string) error {
	if draftID == 0 {
		return errors.New("telegram: a draft needs a draft ID other than 0")
	}
	params := map[string]any{"chat_id": chatID, "draft_id": draftID, "text": text}
	if thread != 0 {
		params["message_thread_id"] = thread
	}
	return t.call(ctx, token, "sendMessageDraft", params, 0, nil)
}

// messageID reads the ID of a sent message. The ID only serves a later edit,
// so an answer without one is not a failure.
func messageID(raw json.RawMessage) int64 {
	var r struct {
		ID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.ID
}

// photoTypes are the pictures that Telegram shows as photos. Anything else,
// and a GIF (which Telegram would turn into a video), goes as a document.
var photoTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// SendFile uploads a file to a chat and returns the ID of its message. A
// picture goes with sendPhoto, anything else with sendDocument. A thread
// other than 0 is the topic of a forum.
func (t *API) SendFile(ctx context.Context, token string, chatID, thread int64, name, mimeType string, data []byte) (int64, error) {
	method, field := "sendDocument", "document"
	if photoTypes[mimeType] {
		method, field = "sendPhoto", "photo"
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if thread != 0 {
		_ = w.WriteField("message_thread_id", strconv.FormatInt(thread, 10))
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": name}))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	h.Set("Content-Type", mimeType)
	part, err := w.CreatePart(h)
	if err != nil {
		return 0, errors.New("telegram: could not build the upload")
	}
	_, _ = part.Write(data)
	if err := w.Close(); err != nil {
		return 0, errors.New("telegram: could not build the upload")
	}
	var raw json.RawMessage
	if err := t.post(ctx, token, method, w.FormDataContentType(), body.Bytes(), 30*time.Second, &raw); err != nil {
		return 0, err
	}
	return messageID(raw), nil
}

// Edit replaces the text of a message the bot sent with plain text.
func (t *API) Edit(ctx context.Context, token string, chatID, messageID int64, text string) error {
	return t.edit(ctx, token, chatID, messageID, text, "")
}

// EditMarkdown replaces the text of a message the bot sent with Markdown, as
// MarkdownV2, and with plain text when Telegram refuses that.
func (t *API) EditMarkdown(ctx context.Context, token string, chatID, messageID int64, md string) error {
	err := t.edit(ctx, token, chatID, messageID, MarkdownV2(md), markdownV2)
	if refusedBecause(err, "can't parse entities") {
		return t.edit(ctx, token, chatID, messageID, PlainText(md), "")
	}
	return err
}

func (t *API) edit(ctx context.Context, token string, chatID, messageID int64, text, parseMode string) error {
	params := textParams(chatID, text, parseMode)
	params["message_id"] = messageID
	err := t.call(ctx, token, "editMessageText", params, 0, nil)
	if refusedBecause(err, "message is not modified") {
		// The message shows this text already: an answer that equals its
		// preview must not go again as a new message.
		return nil
	}
	return err
}

// Typing shows "typing" in a chat, or in a topic of it, for a few seconds.
func (t *API) Typing(ctx context.Context, token string, chatID, thread int64) error {
	params := map[string]any{"chat_id": chatID, "action": "typing"}
	if thread != 0 {
		params["message_thread_id"] = thread
	}
	return t.call(ctx, token, "sendChatAction", params, 0, nil)
}

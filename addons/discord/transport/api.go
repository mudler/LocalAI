// SPDX-License-Identifier: MIT

// Package transport speaks Discord for the Discord add-on: the REST API for
// sending, editing, files, history and who the bot is, and the Gateway (see
// gateway.go) for the events that reach the bot. The host passes the bot token
// on every call; the add-on keeps it only in memory, for the Gateway link of a
// binding.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// DefaultBase is the REST API. Only a test sets another base URL.
const DefaultBase = "https://discord.com/api/v10"

// DefaultCDNHosts are the hosts that serve the files of messages. A download
// goes to one of them only, and never carries the bot token.
var DefaultCDNHosts = []string{"cdn.discordapp.com", "media.discordapp.net"}

// MaxFileBytes is the largest file the add-on moves in either direction. The
// limit of Discord depends on the server and the person; this one is safe for
// a bot on every tier.
const MaxFileBytes = 10 << 20

// ErrTooLarge means a file is larger than the add-on hands over.
var ErrTooLarge = connector.ErrTooLarge

// userAgent is required by Discord for a bot that calls the REST API.
const userAgent = "DiscordBot (https://localai.io, 0.1)"

// API speaks the REST API. The token goes in the Authorization header, never
// in a URL, and no error repeats it.
type API struct {
	// BaseURL is DefaultBase when empty.
	BaseURL string
	// CDNHosts is DefaultCDNHosts when empty.
	CDNHosts []string
	// Client sends the requests. Nil: a client of pkg/httpclient with a
	// timeout, which refuses redirects.
	Client *http.Client
}

func (a *API) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return httpclient.NewWithTimeout(30 * time.Second)
}

func (a *API) base() string {
	if a.BaseURL == "" {
		return DefaultBase
	}
	return strings.TrimSuffix(a.BaseURL, "/")
}

func (a *API) cdnHosts() []string {
	if len(a.CDNHosts) > 0 {
		return a.CDNHosts
	}
	return DefaultCDNHosts
}

// do sends one request and decodes a JSON answer into out. 429 becomes a
// *connector.RetryAfter, 401 an ErrUnauthorized. The body of an error answer
// is read for its message only, which Discord keeps free of the token.
func (a *API) do(ctx context.Context, token, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, body)
	if err != nil {
		return errors.New("discord: could not build a request")
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("discord: %s %s failed: %w", method, route(path), err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("discord: %s: could not read the answer", route(path))
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &connector.RetryAfter{Seconds: waitSeconds(resp.Header.Get("Retry-After"), raw)}
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("discord: the bot token was refused: %w", connector.ErrUnauthorized)
	case resp.StatusCode >= 300:
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := strings.ReplaceAll(e.Message, token, "<token>")
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("discord: %s %s: HTTP %d, code %d: %s", method, route(path), resp.StatusCode, e.Code, msg)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("discord: %s: unexpected answer", route(path))
		}
	}
	return nil
}

// route is a path without the IDs in it, for error messages and logs.
func route(path string) string {
	parts := strings.Split(strings.SplitN(path, "?", 2)[0], "/")
	for i, p := range parts {
		if p != "" && strings.Trim(p, "0123456789") == "" {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// waitSeconds reads the wait of a 429: the header, else retry_after in the
// body (seconds, with a fraction). It rounds up, and is at least one second
// and at most five minutes.
func waitSeconds(header string, body []byte) int {
	if f, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && f > 0 {
		return min(int(math.Ceil(f)), 300)
	}
	var b struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if json.Unmarshal(body, &b) == nil && b.RetryAfter > 0 {
		return min(int(math.Ceil(b.RetryAfter)), 300)
	}
	return 1
}

// User is a Discord user, as the REST API and the Gateway name one.
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

// Display is the name people see: the display name, else the username.
func (u User) Display() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// Me returns the bot of a token.
func (a *API) Me(ctx context.Context, token string) (User, error) {
	var u User
	err := a.do(ctx, token, http.MethodGet, "/users/@me", nil, "", &u)
	if err == nil && u.ID == "" {
		err = errors.New("discord: users/@me named no bot")
	}
	return u, err
}

// Application flags that say the Message Content intent is on. The first is
// for a bot of 100 servers or more that Discord verified, the second for a
// smaller one.
const (
	flagMessageContent        = 1 << 18
	flagMessageContentLimited = 1 << 19
)

// MessageContentOn says whether the application of the token has the Message
// Content intent. Without it the Gateway hands over no text from servers. A
// failed call gives ok false: the answer is not known.
func (a *API) MessageContentOn(ctx context.Context, token string) (on, ok bool) {
	app, ok := a.Application(ctx, token)
	return app.Flags&(flagMessageContent|flagMessageContentLimited) != 0, ok
}

// Application is the part of the application of a bot token that the add-on
// reads. ID names the application, which is the same for every token of it.
type Application struct {
	ID    string `json:"id"`
	Flags int64  `json:"flags"`
}

// Application reads the application of the token. A failed call gives ok
// false: the answer is not known.
func (a *API) Application(ctx context.Context, token string) (app Application, ok bool) {
	if a.do(ctx, token, http.MethodGet, "/applications/@me", nil, "", &app) != nil {
		return Application{}, false
	}
	return app, true
}

// Channel is the part of a channel the add-on reads.
type Channel struct {
	ID       string `json:"id"`
	Type     int    `json:"type"`
	ParentID string `json:"parent_id"`
	GuildID  string `json:"guild_id"`
}

// The channel types of threads.
const (
	typeAnnouncementThread = 10
	typePublicThread       = 11
	typePrivateThread      = 12
)

// IsThread says whether a channel type is a thread.
func IsThread(t int) bool {
	return t == typeAnnouncementThread || t == typePublicThread || t == typePrivateThread
}

// Channel reads one channel.
func (a *API) Channel(ctx context.Context, token, id string) (Channel, error) {
	var c Channel
	if !isSnowflake(id) {
		return c, errors.New("discord: not a channel ID")
	}
	err := a.do(ctx, token, http.MethodGet, "/channels/"+id, nil, "", &c)
	return c, err
}

// isSnowflake says whether s is a Discord ID: only digits, and a plausible
// length. An ID goes into a URL path, so nothing else is let through.
func isSnowflake(s string) bool {
	if len(s) < 5 || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// allowedMentions switches off every ping. A message that Discord would
// otherwise parse for @everyone, @here and mentions of people and roles pings
// no one: the text still shows the mention.
var allowedMentions = map[string]any{"parse": []string{}}

type messageBody struct {
	Content         string         `json:"content"`
	AllowedMentions map[string]any `json:"allowed_mentions"`
}

type sentMessage struct {
	ID string `json:"id"`
}

// PostMessage sends text to a channel or a thread and returns the message ID.
func (a *API) PostMessage(ctx context.Context, token, channel, text string) (string, error) {
	if !isSnowflake(channel) {
		return "", errors.New("discord: not a channel ID")
	}
	raw, _ := json.Marshal(messageBody{Content: text, AllowedMentions: allowedMentions})
	var m sentMessage
	if err := a.do(ctx, token, http.MethodPost, "/channels/"+channel+"/messages", strings.NewReader(string(raw)), "application/json", &m); err != nil {
		return "", err
	}
	return m.ID, nil
}

// EditMessage replaces the text of a message of the bot.
func (a *API) EditMessage(ctx context.Context, token, channel, id, text string) error {
	if !isSnowflake(channel) || !isSnowflake(id) {
		return errors.New("discord: not a channel or message ID")
	}
	raw, _ := json.Marshal(messageBody{Content: text, AllowedMentions: allowedMentions})
	return a.do(ctx, token, http.MethodPatch, "/channels/"+channel+"/messages/"+id, strings.NewReader(string(raw)), "application/json", nil)
}

// DeleteMessage removes a message the bot sent.
func (a *API) DeleteMessage(ctx context.Context, token, channel, id string) error {
	if !isSnowflake(channel) || !isSnowflake(id) {
		return errors.New("discord: not a channel or message ID")
	}
	return a.do(ctx, token, http.MethodDelete, "/channels/"+channel+"/messages/"+id, nil, "", nil)
}

// DMChannel opens (or returns) the direct conversation with a user.
func (a *API) DMChannel(ctx context.Context, token, user string) (string, error) {
	if !isSnowflake(user) {
		return "", errors.New("discord: not a user ID")
	}
	raw, _ := json.Marshal(map[string]string{"recipient_id": user})
	var c Channel
	if err := a.do(ctx, token, http.MethodPost, "/users/@me/channels", strings.NewReader(string(raw)), "application/json", &c); err != nil {
		return "", err
	}
	return c.ID, nil
}

// Attachment is a file of a message.
type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
}

// Message is the part of a message the add-on reads.
type Message struct {
	ID          string       `json:"id"`
	ChannelID   string       `json:"channel_id"`
	Type        int          `json:"type"`
	Content     string       `json:"content"`
	Timestamp   time.Time    `json:"timestamp"`
	WebhookID   string       `json:"webhook_id"`
	Author      User         `json:"author"`
	Mentions    []User       `json:"mentions"`
	Attachments []Attachment `json:"attachments"`
}

// Message reads one message. It names the current, signed URLs of its files.
func (a *API) Message(ctx context.Context, token, channel, id string) (Message, error) {
	var m Message
	if !isSnowflake(channel) || !isSnowflake(id) {
		return m, errors.New("discord: not a channel or message ID")
	}
	err := a.do(ctx, token, http.MethodGet, "/channels/"+channel+"/messages/"+id, nil, "", &m)
	return m, err
}

// Before reads up to limit messages (1 to 100) of a channel or thread sent
// before the message id, newest first.
func (a *API) Before(ctx context.Context, token, channel, before string, limit int) ([]Message, error) {
	if !isSnowflake(channel) || (before != "" && !isSnowflake(before)) {
		return nil, errors.New("discord: not a channel or message ID")
	}
	q := url.Values{"limit": {strconv.Itoa(min(max(limit, 1), 100))}}
	if before != "" {
		q.Set("before", before)
	}
	var out []Message
	err := a.do(ctx, token, http.MethodGet, "/channels/"+channel+"/messages?"+q.Encode(), nil, "", &out)
	return out, err
}

// DownloadStream opens a file of a message. The URL must be on a host of the
// Discord CDN over https, and the request carries no token.
func (a *API) DownloadStream(ctx context.Context, raw string) (io.ReadCloser, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || !a.cdnAllowed(u) {
		return nil, errors.New("discord: the file is not on the Discord CDN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("discord: could not build a download")
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("discord: the download failed")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("discord: the download answered HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (a *API) cdnAllowed(u *url.URL) bool {
	if u.Scheme != "https" && a.CDNHosts == nil {
		return false
	}
	for _, h := range a.cdnHosts() {
		if strings.EqualFold(u.Host, h) {
			return true
		}
	}
	return false
}

// UploadStream sends a file to a channel or thread from a stream of size
// bytes, in a message with no text.
func (a *API) UploadStream(ctx context.Context, token, channel, name string, size int64, body io.Reader) (string, error) {
	if !isSnowflake(channel) {
		return "", errors.New("discord: not a channel ID")
	}
	if size > MaxFileBytes {
		return "", ErrTooLarge
	}
	name = contract.SafeFileName(name)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		payload, _ := json.Marshal(map[string]any{
			"allowed_mentions": allowedMentions,
			"attachments":      []map[string]any{{"id": 0, "filename": name}},
		})
		err := func() error {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="payload_json"`)
			h.Set("Content-Type", "application/json")
			w, err := mw.CreatePart(h)
			if err != nil {
				return err
			}
			if _, err := w.Write(payload); err != nil {
				return err
			}
			h = textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[0]"; filename=%q`, name))
			h.Set("Content-Type", "application/octet-stream")
			w, err = mw.CreatePart(h)
			if err != nil {
				return err
			}
			// One byte past the size shows a file that is larger than it said.
			n, err := io.Copy(w, io.LimitReader(body, size+1))
			if err != nil {
				return err
			}
			if n != size {
				return fmt.Errorf("discord: the file has %d bytes, not the %d it said", n, size)
			}
			return mw.Close()
		}()
		_ = pw.CloseWithError(err)
	}()
	var m sentMessage
	if err := a.do(ctx, token, http.MethodPost, "/channels/"+channel+"/messages", pr, mw.FormDataContentType(), &m); err != nil {
		_ = pr.CloseWithError(err)
		return "", err
	}
	return m.ID, nil
}

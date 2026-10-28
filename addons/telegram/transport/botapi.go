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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// DefaultBase is the Bot API. Only a test sets another base URL.
const DefaultBase = "https://api.telegram.org"

// Errors of the Bot API.
var (
	// ErrUnauthorized means Telegram refused the token.
	ErrUnauthorized = errors.New("telegram: the Telegram token was refused")
	// ErrConflict means another process polls the same bot.
	ErrConflict = errors.New("telegram: another process reads the updates of this bot")
)

// RetryAfter means Telegram asks the caller to wait.
type RetryAfter struct{ Seconds int }

func (r *RetryAfter) Error() string {
	return fmt.Sprintf("telegram: Telegram asks to wait %d s", r.Seconds)
}

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

// call posts a method and decodes the result into out.
func (t *API) call(ctx context.Context, token, method string, params map[string]any, wait time.Duration, out any) error {
	base := t.BaseURL
	if base == "" {
		base = DefaultBase
	}
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/bot"+url.PathEscape(token)+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: could not build a Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
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
		return fmt.Errorf("telegram: Telegram %s: %d %s", method, code, strings.ReplaceAll(env.Description, token, "<token>"))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
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
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Text string `json:"text"`
}

// Updates long-polls for updates from offset on, waiting up to timeoutSeconds.
// Each update comes back as a normalized message with its update ID as the
// cursor. A kind that is not a plain message (an edited message, a channel
// post) has Skip set, so the host can move its offset past it.
func (t *API) Updates(ctx context.Context, token string, offset int64, timeoutSeconds int) ([]contract.ConnectorMessage, error) {
	var raw []struct {
		ID      int64           `json:"update_id"`
		Message json.RawMessage `json:"message"`
	}
	params := map[string]any{"offset": offset, "timeout": timeoutSeconds, "allowed_updates": []string{"message"}}
	if err := t.call(ctx, token, "getUpdates", params, time.Duration(timeoutSeconds)*time.Second, &raw); err != nil {
		return nil, err
	}
	out := make([]contract.ConnectorMessage, 0, len(raw))
	for _, r := range raw {
		u := contract.ConnectorMessage{Cursor: r.ID}
		var m tgMessage
		if len(r.Message) == 0 || string(r.Message) == "null" || json.Unmarshal(r.Message, &m) != nil {
			u.Skip = true
			out = append(out, u)
			continue
		}
		u.Chat, u.Direct = strconv.FormatInt(m.Chat.ID, 10), m.Chat.Type == "private"
		u.SenderID = strconv.FormatInt(m.From.ID, 10)
		u.SenderName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		u.SenderUsername, u.Text = m.From.Username, m.Text
		u.Unsupported = m.Text == ""
		out = append(out, u)
	}
	return out, nil
}

// Send sends plain text and returns the ID of the message. The caller splits
// the text to the limit of Telegram. A model writes the text, so no parse mode
// is set: a stray character must not make Telegram refuse the message.
func (t *API) Send(ctx context.Context, token string, chatID int64, text string) (int64, error) {
	var raw json.RawMessage
	if err := t.call(ctx, token, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, 0, &raw); err != nil {
		return 0, err
	}
	// The ID only serves a later edit, so an answer without one is not a failure.
	var r struct {
		ID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.ID, nil
}

// Edit replaces the text of a message the bot sent.
func (t *API) Edit(ctx context.Context, token string, chatID, messageID int64, text string) error {
	return t.call(ctx, token, "editMessageText", map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}, 0, nil)
}

// Typing shows "typing" in a chat for a few seconds.
func (t *API) Typing(ctx context.Context, token string, chatID int64) error {
	return t.call(ctx, token, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, 0, nil)
}

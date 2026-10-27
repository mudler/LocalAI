package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTelegramBase is the Bot API. Only a test sets another base URL.
const DefaultTelegramBase = "https://api.telegram.org"

// Errors of the Bot API.
var (
	// ErrUnauthorized means Telegram refused the token.
	ErrUnauthorized = errors.New("connectors: the Telegram token was refused")
	// ErrConflict means another process polls the same bot.
	ErrConflict = errors.New("connectors: another process reads the updates of this bot")
)

// RetryAfter means Telegram asks the caller to wait.
type RetryAfter struct{ Seconds int }

func (r *RetryAfter) Error() string {
	return fmt.Sprintf("connectors: Telegram asks to wait %d s", r.Seconds)
}

// Bot is the identity of a bot.
type Bot struct {
	ID       int64
	Username string
}

// Update is one update of a bot. A kind that is not a plain message (an edited
// message, a channel post) has Skip set, so the caller can move its offset past it.
type Update struct {
	ID           int64
	ChatID       int64
	ChatType     string
	FromID       int64
	FromName     string
	FromUsername string
	Text         string
	// Unsupported is a message with no text: a photo, a voice note, a file.
	Unsupported bool
	Skip        bool
}

// Telegram speaks the Bot API. The token goes in the URL path, as the API
// wants, and is taken out of every error.
type Telegram struct {
	// BaseURL is DefaultTelegramBase when empty.
	BaseURL string
	// Client sends the requests. Nil: a client with a timeout that does not
	// follow redirects.
	Client *http.Client
}

func (t *Telegram) client(extra time.Duration) *http.Client {
	if t.Client != nil {
		return t.Client
	}
	return &http.Client{Timeout: extra + 15*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type apiError struct {
	Code        int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call posts a method and decodes the result into out.
func (t *Telegram) call(ctx context.Context, token, method string, params map[string]any, wait time.Duration, out any) error {
	base := t.BaseURL
	if base == "" {
		base = DefaultTelegramBase
	}
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/bot"+url.PathEscape(token)+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("connectors: could not build a Telegram request")
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
		return fmt.Errorf("connectors: Telegram %s failed: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		apiError
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("connectors: Telegram %s answered %d with something that is not JSON", method, resp.StatusCode)
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
		return fmt.Errorf("connectors: Telegram %s: %d %s", method, code, strings.ReplaceAll(env.Description, token, "<token>"))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Me returns the bot of a token.
func (t *Telegram) Me(ctx context.Context, token string) (Bot, error) {
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
func (t *Telegram) Updates(ctx context.Context, token string, offset int64, timeoutSeconds int) ([]Update, error) {
	var raw []struct {
		ID      int64           `json:"update_id"`
		Message json.RawMessage `json:"message"`
	}
	params := map[string]any{"offset": offset, "timeout": timeoutSeconds, "allowed_updates": []string{"message"}}
	if err := t.call(ctx, token, "getUpdates", params, time.Duration(timeoutSeconds)*time.Second, &raw); err != nil {
		return nil, err
	}
	out := make([]Update, 0, len(raw))
	for _, r := range raw {
		u := Update{ID: r.ID}
		if len(r.Message) == 0 || string(r.Message) == "null" {
			u.Skip = true
			out = append(out, u)
			continue
		}
		var m tgMessage
		if err := json.Unmarshal(r.Message, &m); err != nil {
			u.Skip = true
			out = append(out, u)
			continue
		}
		u.ChatID, u.ChatType, u.FromID = m.Chat.ID, m.Chat.Type, m.From.ID
		u.FromName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		u.FromUsername, u.Text = m.From.Username, m.Text
		u.Unsupported = m.Text == ""
		out = append(out, u)
	}
	return out, nil
}

// Send sends plain text. The caller splits it to the limit of Telegram.
func (t *Telegram) Send(ctx context.Context, token string, chatID int64, text string) error {
	return t.call(ctx, token, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, 0, nil)
}

// Typing shows "typing" in a chat for a few seconds.
func (t *Telegram) Typing(ctx context.Context, token string, chatID int64) error {
	return t.call(ctx, token, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, 0, nil)
}

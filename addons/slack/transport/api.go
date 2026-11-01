// SPDX-License-Identifier: MIT

// Package transport speaks Slack for the Slack add-on: the Web API for
// sending, editing, files and who the bot is, and Socket Mode for the events
// that reach the bot. The host passes the tokens on every call; the add-on
// keeps them only in memory, for the Socket Mode link of a binding.
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

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// DefaultBase is the Web API. Only a test sets another base URL.
const DefaultBase = "https://slack.com/api"

// DefaultFilesHost is the host of the files of messages. A download sends the
// bot token, so it goes to this host only.
const DefaultFilesHost = "files.slack.com"

// errors of Slack that mean the token is not good, or no longer good.
var refused = map[string]bool{
	"invalid_auth": true, "not_authed": true, "account_inactive": true,
	"token_revoked": true, "token_expired": true, "not_allowed_token_type": true,
}

// API speaks the Web API. The token goes in the Authorization header, never
// in a URL, and no error repeats it.
type API struct {
	// BaseURL is DefaultBase when empty.
	BaseURL string
	// FilesHost is DefaultFilesHost when empty.
	FilesHost string
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

// call posts a method of the Web API as a form and decodes the answer into
// out. Slack answers HTTP 429 with Retry-After when a method is called too
// often; that becomes a *connector.RetryAfter.
func (a *API) call(ctx context.Context, token, method string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("slack: could not build a Slack request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("slack: %s failed: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests {
		return &connector.RetryAfter{Seconds: retryAfter(resp.Header.Get("Retry-After"))}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("slack: %s: could not read the answer", method)
	}
	var env struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("slack: %s answered %d with something that is not JSON", method, resp.StatusCode)
	}
	if !env.OK {
		switch {
		case refused[env.Error]:
			return fmt.Errorf("slack: %s: %s: %w", method, env.Error, connector.ErrUnauthorized)
		case env.Error == "ratelimited":
			return &connector.RetryAfter{Seconds: retryAfter(resp.Header.Get("Retry-After"))}
		}
		// The error of Slack is a fixed code, such as channel_not_found.
		return fmt.Errorf("slack: %s: %s", method, strings.ReplaceAll(env.Error, token, "<token>"))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("slack: %s: unexpected answer", method)
		}
	}
	return nil
}

func retryAfter(h string) int {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, 300)
}

// Identity is who a bot token belongs to.
type Identity struct {
	UserID string `json:"user_id"`
	User   string `json:"user"`
	BotID  string `json:"bot_id"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
}

// AuthTest returns the bot of a bot token.
func (a *API) AuthTest(ctx context.Context, bot string) (Identity, error) {
	var id Identity
	err := a.call(ctx, bot, "auth.test", url.Values{}, &id)
	return id, err
}

// OpenConnection asks for the address of a Socket Mode link with an
// app-level token. The address holds a ticket for one link, not the token.
func (a *API) OpenConnection(ctx context.Context, app string) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if err := a.call(ctx, app, "apps.connections.open", url.Values{}, &r); err != nil {
		return "", err
	}
	u, err := url.Parse(r.URL)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return "", errors.New("slack: apps.connections.open gave no Socket Mode address")
	}
	return r.URL, nil
}

// PostMessage sends a message in mrkdwn to a channel, in a thread when
// thread is set, and returns its timestamp, which is its ID.
func (a *API) PostMessage(ctx context.Context, bot, channel, thread, text string) (string, error) {
	form := url.Values{"channel": {channel}, "text": {text}}
	if thread != "" {
		form.Set("thread_ts", thread)
	}
	var r struct {
		TS string `json:"ts"`
	}
	err := a.call(ctx, bot, "chat.postMessage", form, &r)
	return r.TS, err
}

// Update replaces the text of a message that the bot sent.
func (a *API) Update(ctx context.Context, bot, channel, ts, text string) error {
	return a.call(ctx, bot, "chat.update", url.Values{"channel": {channel}, "ts": {ts}, "text": {text}}, nil)
}

// User is a person of the workspace.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Profile struct {
		RealName    string `json:"real_name"`
		DisplayName string `json:"display_name"`
	} `json:"profile"`
}

// Display is the name that people see.
func (u User) Display() string {
	if n := strings.TrimSpace(u.Profile.DisplayName); n != "" {
		return n
	}
	if n := strings.TrimSpace(u.Profile.RealName); n != "" {
		return n
	}
	return u.Name
}

// UserInfo returns a person of the workspace. It needs the scope users:read.
func (a *API) UserInfo(ctx context.Context, bot, id string) (User, error) {
	var r struct {
		User User `json:"user"`
	}
	err := a.call(ctx, bot, "users.info", url.Values{"user": {id}}, &r)
	return r.User, err
}

// File is a file that a message carries.
type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Mimetype string `json:"mimetype"`
	Size     int64  `json:"size"`
	Download string `json:"url_private_download"`
}

// FileInfo returns a file. It needs the scope files:read.
func (a *API) FileInfo(ctx context.Context, bot, id string) (File, error) {
	var r struct {
		File File `json:"file"`
	}
	err := a.call(ctx, bot, "files.info", url.Values{"file": {id}}, &r)
	return r.File, err
}

// ErrTooLarge means a file is larger than the add-on hands over. It is the
// shared error of the connector add-ons, so the host tells the sender.
var ErrTooLarge = connector.ErrTooLarge

// onFilesHost parses an address of a file and checks that it is on the files
// host of Slack, over https or over the scheme of the base URL that a test
// sets.
func (a *API) onFilesHost(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	host := a.FilesHost
	if host == "" {
		host = DefaultFilesHost
	}
	scheme := "https"
	if b, berr := url.Parse(a.base()); berr == nil && a.BaseURL != "" {
		scheme = b.Scheme
	}
	if err != nil || u.Host != host || (u.Scheme != "https" && u.Scheme != scheme) || u.User != nil {
		return nil, errors.New("slack: the file is not on the files host of Slack")
	}
	return u, nil
}

// Upload shares a file in a channel, in a thread when thread is set, and
// returns the ID of the file. It takes the three steps of Slack's external
// upload: an upload address, the bytes, and the completion that posts the
// file. It needs the scope files:write.
func (a *API) Upload(ctx context.Context, bot, channel, thread, name string, data []byte) (string, error) {
	var r struct {
		URL string `json:"upload_url"`
		ID  string `json:"file_id"`
	}
	if err := a.call(ctx, bot, "files.getUploadURLExternal", url.Values{"filename": {name}, "length": {strconv.Itoa(len(data))}}, &r); err != nil {
		return "", err
	}
	// The address is signed for this one upload, so the bot token does not
	// go with the bytes.
	u, err := a.onFilesHost(r.URL)
	if err != nil || r.ID == "" {
		return "", errors.New("slack: the upload address is not on the files host of Slack")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return "", errors.New("slack: could not build an upload request")
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := a.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("slack: the file could not be uploaded")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", &connector.RetryAfter{Seconds: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("slack: the upload answered %d", resp.StatusCode)
	}
	files, _ := json.Marshal([]map[string]string{{"id": r.ID, "title": name}})
	form := url.Values{"files": {string(files)}, "channel_id": {channel}}
	if thread != "" {
		form.Set("thread_ts", thread)
	}
	if err := a.call(ctx, bot, "files.completeUploadExternal", form, nil); err != nil {
		return "", err
	}
	return r.ID, nil
}

// Download reads a file of Slack, at most limit bytes. The bot token goes
// with the request, so the address must be on the files host (see
// onFilesHost).
func (a *API) Download(ctx context.Context, bot, address string, limit int64) ([]byte, error) {
	u, err := a.onFilesHost(address)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("slack: could not build a download request")
	}
	req.Header.Set("Authorization", "Bearer "+bot)
	resp, err := a.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("slack: the file could not be downloaded")
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &connector.RetryAfter{Seconds: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("slack: the download answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("slack: the file could not be read")
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<!DOCTYPE html")) {
		// Slack answers a download without the scope files:read with its
		// sign-in page, not with an error.
		return nil, errors.New("slack: the download needs the scope files:read")
	}
	return data, nil
}

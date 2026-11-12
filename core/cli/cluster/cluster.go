// SPDX-License-Identifier: MIT

// Package cluster is the "local-ai cluster" command. It calls the admin API of
// a running distributed frontend to read or change the carrier of the cluster
// and to read or store the cluster settings.
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Command is the "local-ai cluster" command group.
type Command struct {
	Carrier  CarrierCommand  `cmd:"" help:"Read the carrier of a distributed cluster, or change it"`
	Settings SettingsCommand `cmd:"" help:"Read or store the settings of a distributed cluster"`
}

// Remote says which server a command talks to.
type Remote struct {
	Endpoint string        `default:"http://127.0.0.1:8080" env:"LOCALAI_ENDPOINT" help:"URL of a LocalAI frontend of the cluster."`
	APIKey   string        `name:"api-key" env:"LOCALAI_API_KEY,API_KEY" help:"API key of an admin user."`
	Timeout  time.Duration `default:"2m" help:"Timeout for each request. A dry run asks every replica, so it can take a few seconds."`
	JSON     bool          `name:"json" help:"Write the answer of the server as JSON."`
}

// apiError is an answer that is not a success.
type apiError struct {
	Status int
	Msg    string
	// Body is the decoded body, for the callers that read more than the message.
	Body []byte
}

func (e *apiError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("the server refused the request (HTTP %d): use --api-key with the key of an admin user", e.Status)
	case http.StatusServiceUnavailable:
		return "the server is not running in distributed mode, or the cluster is not ready: " + e.Msg
	}
	if e.Msg == "" {
		return fmt.Sprintf("HTTP status %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Msg, e.Status)
}

// endpointURL joins the base URL and the path of an API route.
func endpointURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(base, "#") {
		return "", errors.New("endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	return u.String(), nil
}

// do sends one request and decodes a JSON answer into out. It returns the status
// code and the raw body as well. A status outside 2xx is an *apiError.
func (r *Remote) do(ctx context.Context, client *http.Client, method, path string, in, out any) (int, []byte, error) {
	target, err := endpointURL(r.Endpoint, path)
	if err != nil {
		return 0, nil, err
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, errors.New("cannot create the request")
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		// A transport error can echo the URL and the credentials of a proxy.
		return 0, nil, errors.New("cannot reach the server at the endpoint")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, errors.New("cannot read the answer")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return resp.StatusCode, raw, &apiError{Status: resp.StatusCode, Msg: e.Error, Body: raw}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("the server answered with a body that is not valid: %w", err)
		}
	}
	return resp.StatusCode, raw, nil
}

func (r *Remote) client() *http.Client {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return httpclient.NewWithTimeout(timeout)
}

// printJSON writes raw, indented.
func printJSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		_, err = w.Write(raw)
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

package mcpconns

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/mcptransport"
)

// maxTools bounds what one server may offer.
const maxTools = 200

var mcpClient = mcp.NewClient(&mcp.Implementation{Name: "LocalAI", Version: "v1.0.0"}, nil)

// conn is an open session with the tools it listed.
type conn struct {
	cs     *mcp.ClientSession
	cancel context.CancelFunc
	tools  []*mcp.Tool
}

func (c *conn) close() {
	if c == nil {
		return
	}
	_ = c.cs.Close()
	c.cancel()
}

// headerRT adds the connector's headers to a request, but only for the host the
// connector names. A redirect to another host never gets the credentials.
type headerRT struct {
	base    http.RoundTripper
	origin  *url.URL
	headers map[string]string
}

func (h *headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.EqualFold(r.URL.Scheme, h.origin.Scheme) || !strings.EqualFold(r.URL.Host, h.origin.Host) {
		return h.base.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// httpClientFor builds the client of a remote connector. Unless the
// administrator allows private addresses, the transport resolves the host
// itself and refuses to dial an address that is not public, so a name whose
// answer changes between the check and the connection cannot reach the
// internal network, and every redirect hop is checked the same way.
func (s *Service) httpClientFor(c Connector, sec Secrets) (*http.Client, error) {
	origin, err := url.Parse(c.URL)
	if err != nil {
		return nil, bad("the URL is not valid")
	}
	headers := map[string]string{}
	if sec.Token != "" {
		headers["Authorization"] = "Bearer " + sec.Token
	}
	for k, v := range sec.Headers {
		headers[k] = v
	}
	if s.o.AllowPrivate() {
		rt := &headerRT{base: httpclient.HardenedTransport(), origin: origin, headers: headers}
		return httpclient.New(httpclient.WithTransport(rt)), nil
	}
	rt := &headerRT{base: httpclient.New(httpclient.WithExternalOnly()).Transport, origin: origin, headers: headers}
	return httpclient.New(httpclient.WithTransport(rt), httpclient.WithExternalOnly(), httpclient.WithFollowRedirects()), nil
}

// open connects to a connector and lists its tools.
func (s *Service) openConn(c Connector, sec Secrets) (*conn, error) {
	ctx, cancel := context.WithCancel(context.Background())
	var (
		cs  *mcp.ClientSession
		err error
	)
	switch c.Transport {
	case TransportHTTP:
		if err := CheckURL(c.URL, !s.o.AllowPrivate()); err != nil {
			cancel()
			return nil, err
		}
		var hc *http.Client
		if hc, err = s.httpClientFor(c, sec); err != nil {
			cancel()
			return nil, err
		}
		cs, err = mcptransport.Connect(ctx, mcpClient, c.URL, hc, s.o.ConnectTimeout)
	case TransportStdio:
		// No shell: the command and its arguments go to the kernel as they are.
		cmd := exec.CommandContext(ctx, c.Command, c.Args...) // #nosec G204 -- an administrator's own connector
		cmd.Env = StdioEnv(sec.Env, os.Environ())
		cs, err = connectCommand(ctx, cancel, cmd, s.o.ConnectTimeout)
	default:
		err = bad("unknown transport")
	}
	if err != nil {
		cancel()
		return nil, err
	}
	lctx, lcancel := context.WithTimeout(ctx, s.o.ConnectTimeout)
	defer lcancel()
	var tools []*mcp.Tool
	cursor := ""
	for {
		res, lerr := cs.ListTools(lctx, &mcp.ListToolsParams{Cursor: cursor})
		if lerr != nil {
			_ = cs.Close()
			cancel()
			return nil, lerr
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" || len(tools) >= maxTools {
			break
		}
		cursor = res.NextCursor
	}
	if len(tools) > maxTools {
		tools = tools[:maxTools]
	}
	return &conn{cs: cs, cancel: cancel, tools: tools}, nil
}

func connectCommand(ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, timeout time.Duration) (*mcp.ClientSession, error) {
	type result struct {
		cs  *mcp.ClientSession
		err error
	}
	done := make(chan result, 1)
	go func() {
		cs, err := mcpClient.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		done <- result{cs, err}
	}()
	select {
	case r := <-done:
		return r.cs, r.err
	case <-time.After(timeout):
		// Cancelling the context kills the process.
		cancel()
		return nil, fmt.Errorf("the command did not answer within %s", timeout)
	}
}

// stdioKept are the variables of the server's own environment that a local MCP
// command gets. Everything else (API keys, cloud credentials) stays out.
var stdioKept = map[string]bool{"PATH": true, "HOME": true, "LANG": true, "LC_ALL": true, "TMPDIR": true, "TZ": true}

// StdioEnv builds the environment of a local MCP command: a few neutral
// variables of the parent, then the connector's own.
func StdioEnv(own map[string]string, parent []string) []string {
	var out []string
	for _, kv := range parent {
		if k, _, ok := strings.Cut(kv, "="); ok && stdioKept[k] {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(own))
	for k := range own {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+own[k])
	}
	return out
}

// redact removes secrets, and the query of the address (it may hold a key),
// from the text of an error before anyone reads it.
func redact(msg string, c Connector, sec Secrets) string {
	if u, err := url.Parse(c.URL); err == nil && u.RawQuery != "" {
		clean := *u
		clean.RawQuery = ""
		msg = strings.ReplaceAll(msg, c.URL, clean.String())
		msg = strings.ReplaceAll(msg, u.RawQuery, "")
	}
	secrets := []string{sec.Token}
	for _, v := range sec.Headers {
		secrets = append(secrets, v)
	}
	for _, v := range sec.Env {
		secrets = append(secrets, v)
	}
	for _, v := range secrets {
		if len(v) >= 3 {
			msg = strings.ReplaceAll(msg, v, "***")
		}
	}
	return msg
}

// SPDX-License-Identifier: MIT

// Package client serves the routes of a paired client that reach an
// add-on. The client authenticates with a client key; this package forwards a
// request to a route the add-on declared in its manifest, over the loopback
// connection the owner keeps to the add-on.
package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// Host is what the proxy needs of the add-on owner. *addons.Manager is one.
type Host interface {
	ClientRoutes(ctx context.Context, addonID string) ([]contract.ClientRoute, error)
	ClientCall(ctx context.Context, addonID string, actor contract.Actor, req contract.ClientRequest) (contract.ClientResponse, error)
	ClientStream(ctx context.Context, addonID string, actor contract.Actor, req contract.ClientRequest) (io.ReadCloser, error)
}

const (
	// PathPrefix is where the routes of an add-on appear: PathPrefix + id + path.
	PathPrefix = "/api/client/addons/"

	// MaxCallsPerClient is the number of calls of one client that run at once,
	// and MaxStreamsPerClient the number of its open streams. A new stream of a
	// client at the limit replaces nothing: it is refused, so a client that
	// lost its connection retries once the old stream is gone.
	MaxCallsPerClient   = 8
	MaxStreamsPerClient = 2
	// MaxCallsPerMinute is the rate limit of one client, streams included.
	MaxCallsPerMinute = 240

	// HeartbeatInterval is how often the proxy writes a comment line into a
	// stream that is quiet, so a proxy or the client does not give up on it.
	HeartbeatInterval = 15 * time.Second
)

// Proxy forwards client requests. Its limits are per client and live in memory
// of this process: they are a guard against a runaway client, not an account.
type Proxy struct {
	host func() Host
	// stillPaired, when set, says whether a client key still stands. The proxy
	// asks it at each heartbeat of a stream and ends the stream of a key that
	// was revoked, expired or paused.
	stillPaired func(ownerID, keyID string) bool
	heartbeat   time.Duration
	mu          sync.Mutex
	clients     map[string]*clientUse
	// ticks counts the heartbeat ticks of all streams, for the tests.
	ticks atomic.Int64
}

type clientUse struct {
	calls, streams int
	window         time.Time
	count          int
}

// NewProxy returns a proxy. host is read per request so that a manager that
// starts late is found; it may return nil, which answers 503. stillPaired may
// be nil.
func NewProxy(host func() Host, stillPaired func(ownerID, keyID string) bool) *Proxy {
	return &Proxy{host: host, stillPaired: stillPaired, heartbeat: HeartbeatInterval, clients: map[string]*clientUse{}}
}

type errorBody struct {
	Error string `json:"error"`
}

func fail(c echo.Context, status int, msg string) error {
	return c.JSON(status, errorBody{Error: msg})
}

// admit counts one call of a client and returns the function that ends it.
func (p *Proxy) admit(id string, stream bool) (release func(), status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.clients[id]
	if u == nil {
		u = &clientUse{}
		p.clients[id] = u
	}
	now := time.Now()
	if now.Sub(u.window) >= time.Minute {
		u.window, u.count = now, 0
	}
	if u.count >= MaxCallsPerMinute {
		return nil, http.StatusTooManyRequests
	}
	if stream && u.streams >= MaxStreamsPerClient || !stream && u.calls >= MaxCallsPerClient {
		return nil, http.StatusTooManyRequests
	}
	u.count++
	if stream {
		u.streams++
	} else {
		u.calls++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if stream {
				u.streams--
			} else {
				u.calls--
			}
			if u.calls == 0 && u.streams == 0 && time.Since(u.window) >= time.Minute {
				delete(p.clients, id)
			}
		})
	}, 0
}

// Handler is the catch-all handler of /api/client/addons/:addon/*. The route
// is in auth.RouteFeatureRegistry with auth.FeatureClientAddons; the handler
// still insists on a client key, because the feature check lets an
// administrator through.
func (p *Proxy) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		key := auth.ClientKey(c)
		if key == nil {
			return fail(c, http.StatusForbidden, "client credential required")
		}
		req := c.Request()
		addonID := c.Param("addon")
		// The key reaches only the add-ons of its list. This comes first and
		// answers the same 403 whether or not the add-on exists, so the list of
		// the installed add-ons does not leak.
		if !key.AllowsAddon(addonID) {
			return fail(c, http.StatusForbidden, "this credential cannot use this route")
		}
		if !contract.ValidAddonID(addonID) {
			return fail(c, http.StatusNotFound, "not found")
		}
		// The path is matched exactly as it was sent. Any escape is refused
		// instead of decoded, so no spelling of a dot or a slash gets past the
		// route table.
		if strings.Contains(req.URL.EscapedPath(), "%") || strings.ContainsRune(req.URL.Path, '\\') {
			return fail(c, http.StatusBadRequest, "invalid path")
		}
		rest, ok := strings.CutPrefix(req.URL.Path, PathPrefix+addonID)
		if !ok {
			return fail(c, http.StatusNotFound, "not found")
		}
		host := p.host()
		if host == nil {
			return fail(c, http.StatusServiceUnavailable, "add-ons are not available")
		}
		routes, err := host.ClientRoutes(req.Context(), addonID)
		if err != nil {
			return fail(c, http.StatusServiceUnavailable, "add-on unavailable")
		}
		// Only a declared route is forwarded. An add-on that declared none looks
		// the same as an add-on that does not exist.
		route, params, found, mismatch := contract.MatchClientRoute(routes, req.Method, rest)
		switch {
		case mismatch:
			return fail(c, http.StatusMethodNotAllowed, "method not allowed")
		case !found:
			return fail(c, http.StatusNotFound, "not found")
		}

		// The actor is what the host records for the call: the owner who paired
		// the client and the client key. Host operations during the call run as
		// this client and as no other principal.
		actor := contract.Actor{UserID: key.UserID, CredentialID: contract.ClientCredentialPrefix + key.ID, Mode: "named-user"}
		creq := contract.ClientRequest{
			RouteID: route.ID, Method: route.Method, Path: rest, Params: params,
			Client: contract.ClientIdentity{ID: key.ID, Name: printableName(key.Name)},
		}
		if q := req.URL.Query(); len(q) > 0 {
			creq.Query = make(map[string]string, len(q))
			for k, v := range q {
				creq.Query[k] = v[0]
			}
			if contract.ValidateClientQuery(creq.Query) != nil {
				return fail(c, http.StatusBadRequest, "invalid query")
			}
		}
		if id := req.Header.Get("Last-Event-ID"); id != "" {
			if !route.Stream || !contract.ValidEventID(id) {
				return fail(c, http.StatusBadRequest, "invalid Last-Event-ID")
			}
			creq.LastEventID = id
		}

		release, status := p.admit(key.ID, route.Stream)
		if status != 0 {
			c.Response().Header().Set("Retry-After", "5")
			return fail(c, status, "too many requests")
		}
		defer release()

		if route.Stream {
			return p.stream(c, host, addonID, actor, creq)
		}
		body, status, msg := readBody(c, route)
		if status != 0 {
			return fail(c, status, msg)
		}
		if len(body) > 0 {
			creq.Body, creq.ContentType = body, "application/json"
		}
		ctx, cancel := context.WithTimeout(req.Context(), route.Timeout())
		defer cancel()
		out, err := host.ClientCall(ctx, addonID, actor, creq)
		if err != nil {
			xlog.Debug("client call failed", "addon", addonID, "route", route.ID, "client", key.ID, "error", err)
			return mapError(c, err)
		}
		// The client of the add-on checks this too; the proxy does not trust
		// that a Host is always a client.
		if out.Validate(contract.DefaultLimits()) != nil {
			return fail(c, http.StatusBadGateway, "add-on error")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		if out.Status == http.StatusNoContent || len(out.Body) == 0 {
			return c.NoContent(out.Status)
		}
		return c.Blob(out.Status, out.ContentType, out.Body)
	}
}

// readBody reads the body of a request within the limit of the route. A body
// must be JSON, and a route that takes none refuses one.
func readBody(c echo.Context, route contract.ClientRoute) ([]byte, int, string) {
	req := c.Request()
	if route.Method == http.MethodGet || route.Method == http.MethodDelete {
		if req.ContentLength > 0 || len(req.TransferEncoding) > 0 {
			return nil, http.StatusBadRequest, "this route takes no body"
		}
		return nil, 0, ""
	}
	limit := route.BodyLimit()
	if req.ContentLength > limit {
		return nil, http.StatusRequestEntityTooLarge, "body too large"
	}
	b, err := io.ReadAll(http.MaxBytesReader(c.Response(), req.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, http.StatusRequestEntityTooLarge, "body too large"
		}
		return nil, http.StatusBadRequest, "could not read the body"
	}
	if len(b) == 0 {
		return nil, 0, ""
	}
	if mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return nil, http.StatusUnsupportedMediaType, "the body must be application/json"
	}
	return b, 0, ""
}

func mapError(c echo.Context, err error) error {
	var refusal *addonruntime.Refusal
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fail(c, http.StatusGatewayTimeout, "the add-on did not answer in time")
	case errors.Is(err, context.Canceled):
		return fail(c, http.StatusServiceUnavailable, "cancelled")
	case errors.As(err, &refusal):
		return fail(c, http.StatusBadRequest, "the add-on refused the request")
	case errors.Is(err, addonruntime.ErrConflict):
		return fail(c, http.StatusConflict, "conflict")
	case errors.Is(err, addonruntime.ErrDenied):
		return fail(c, http.StatusForbidden, "denied")
	case errors.Is(err, addonruntime.ErrNotSent):
		return fail(c, http.StatusServiceUnavailable, "add-on unavailable")
	default:
		return fail(c, http.StatusBadGateway, "add-on error")
	}
}

// printableName keeps the printable characters of a client name, of any script,
// and cuts the result at 64 bytes on a character boundary. A control character
// or an invalid byte is dropped.
func printableName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != utf8.RuneError && unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return utils.TruncateUTF8(b.String(), 64)
}

// stream forwards a server-sent event stream. It writes the headers and
// flushes them at once, flushes after every chunk, writes a comment line when
// the stream is quiet, and closes the upstream stream when the client goes
// away, so a dropped connection costs the add-on nothing.
func (p *Proxy) stream(c echo.Context, host Host, addonID string, actor contract.Actor, creq contract.ClientRequest) error {
	req := c.Request()
	up, err := host.ClientStream(req.Context(), addonID, actor, creq)
	if err != nil {
		return mapError(c, err)
	}
	defer func() { _ = up.Close() }()

	w := c.Response()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	// The server has no write timeout, but a proxy in front of it might; say
	// so for this response in case the server was started with one.
	_ = rc.SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil
	}

	type chunk struct {
		b   []byte
		err error
	}
	chunks := make(chan chunk)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buf := make([]byte, 4<<10)
		for {
			n, err := up.Read(buf)
			var b []byte
			if n > 0 {
				b = append(b, buf[:n]...)
			}
			select {
			case chunks <- chunk{b, err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	tick := time.NewTicker(p.heartbeat)
	defer tick.Stop()
	// The revocation check has a ticker of its own that data never resets: an
	// add-on that sends a chunk every few seconds must not keep a revoked
	// client connected.
	revoke := time.NewTicker(p.heartbeat)
	defer revoke.Stop()
	// A comment line goes only between events, never into the middle of one.
	var tail []byte
	boundary := func() bool {
		return len(tail) == 0 || bytes.HasSuffix(tail, []byte("\n\n")) || bytes.HasSuffix(tail, []byte("\r\n\r\n"))
	}
	for {
		select {
		case <-req.Context().Done():
			return nil
		case <-revoke.C:
			if p.stillPaired != nil && !p.stillPaired(actor.UserID, strings.TrimPrefix(actor.CredentialID, contract.ClientCredentialPrefix)) {
				return nil
			}
		case <-tick.C:
			p.ticks.Add(1)
			if !boundary() {
				continue
			}
			if _, err := w.Write([]byte(": hb\n\n")); err != nil {
				return nil
			}
			if rc.Flush() != nil {
				return nil
			}
		case ch := <-chunks:
			if len(ch.b) > 0 {
				if _, err := w.Write(ch.b); err != nil {
					return nil
				}
				if rc.Flush() != nil {
					return nil
				}
				tail = append(tail, ch.b...)
				if len(tail) > 4 {
					tail = tail[len(tail)-4:]
				}
				tick.Reset(p.heartbeat)
			}
			if ch.err != nil {
				return nil
			}
		}
	}
}

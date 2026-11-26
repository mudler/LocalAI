package pairclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// RoutesPath is the route of the server that gives a paired device the
// current routes to it. The server serves it over P2P only.
const RoutesPath = "/api/p2p/routes"

// RoutesInterval is how often a device that stays connected asks for the
// routes again. Relay reservations last an hour and move rarely, so a few
// small requests an hour are enough to follow them.
const RoutesInterval = 10 * time.Minute

// routesTimeout bounds one request for the routes.
const routesTimeout = 30 * time.Second

// The most entries of each list a device keeps, as in an invite link.
const (
	maxRouteAddrs  = 4
	maxRouteRelays = 3
	maxRouteHints  = 2
)

// Routes are the routes a server gives a paired device: its direct
// addresses, the relays that carry data and the public relays for hole
// punch coordination. Relays and hints end in /p2p/<relay peer ID>.
type Routes struct {
	Server string   `json:"server"`
	Addrs  []string `json:"addrs"`
	Relays []string `json:"relays"`
	Hints  []string `json:"hints"`
}

// Equal reports whether r and o have the same lists in the same order.
func (r Routes) Equal(o Routes) bool {
	return r.Server == o.Server && slices.Equal(r.Addrs, o.Addrs) && slices.Equal(r.Relays, o.Relays) && slices.Equal(r.Hints, o.Hints)
}

// FetchRoutes asks the server for its current routes over c, an HTTP client
// whose connections go to the server over P2P (p2pnet.Host.HTTPClient). It
// refuses an answer that names another server, and drops entries that do
// not parse and entries beyond the counts of an invite link, so a device
// stores only what it could dial.
func FetchRoutes(ctx context.Context, c *http.Client, server p2pnet.ID) (Routes, error) {
	ctx, cancel := context.WithTimeout(ctx, routesTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server.String()+RoutesPath, nil)
	if err != nil {
		return Routes{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return Routes{}, fmt.Errorf("routes: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Routes{}, fmt.Errorf("routes: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Routes{}, fmt.Errorf("routes: the server answered %d", resp.StatusCode)
	}
	var r Routes
	if err := json.Unmarshal(body, &r); err != nil {
		return Routes{}, fmt.Errorf("routes: %w", err)
	}
	if r.Server != server.String() {
		return Routes{}, fmt.Errorf("routes: %w", ErrWrongServer)
	}
	return Routes{
		Server: r.Server,
		Addrs:  keep(r.Addrs, maxRouteAddrs, func(s string) bool { _, err := p2pnet.ParseMultiaddr(s); return err == nil }),
		Relays: keep(r.Relays, maxRouteRelays, relayAddr(server)),
		Hints:  keep(r.Hints, maxRouteHints, relayAddr(server)),
	}, nil
}

// relayAddr accepts an address of a relay that is not the server itself.
func relayAddr(server p2pnet.ID) func(string) bool {
	return func(s string) bool {
		ai, err := p2pnet.ParseAddrInfo(s)
		return err == nil && ai.ID != server && len(ai.Addrs) > 0
	}
}

// keep returns the entries of in that pass ok, without repeats, at most
// limit of them. It never returns nil, so an empty list stays an empty JSON
// array.
func keep(in []string, limit int, ok func(string) bool) []string {
	out := []string{}
	for _, s := range in {
		if len(out) >= limit {
			break
		}
		if ok(s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// RouteWatcher asks the server for its routes when a connection to it opens
// and then every interval while it stays connected, and hands each answer to
// a callback. A forwarder tells it when the connection is up or down.
type RouteWatcher struct {
	fetch    func(context.Context) (Routes, error)
	onRoutes func(Routes)
	onError  func(error)
	interval time.Duration

	mu sync.Mutex
	up bool

	kick   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// WatchRoutes starts a RouteWatcher. fetch asks the server (FetchRoutes),
// onRoutes gets every answer, onError (may be nil) every failure. An interval
// of zero means RoutesInterval. Stop ends it.
func WatchRoutes(ctx context.Context, fetch func(context.Context) (Routes, error), interval time.Duration, onRoutes func(Routes), onError func(error)) *RouteWatcher {
	if interval <= 0 {
		interval = RoutesInterval
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &RouteWatcher{
		fetch: fetch, onRoutes: onRoutes, onError: onError, interval: interval,
		kick: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{}),
	}
	go w.run(ctx)
	return w
}

// Connected tells the watcher whether the connection to the server is up.
// A change to up asks for the routes now; it returns at once.
func (w *RouteWatcher) Connected(up bool) {
	w.mu.Lock()
	was := w.up
	w.up = up
	w.mu.Unlock()
	if up && !was {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// Stop ends the watcher and waits for a request under way. It is safe to
// call more than once.
func (w *RouteWatcher) Stop() {
	w.cancel()
	<-w.done
}

func (w *RouteWatcher) isUp() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.up
}

func (w *RouteWatcher) run(ctx context.Context) {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		case <-t.C:
			if !w.isUp() {
				continue
			}
		}
		r, err := w.fetch(ctx)
		switch {
		case err == nil:
			w.onRoutes(r)
		case ctx.Err() != nil || errors.Is(err, context.Canceled):
			return
		case w.onError != nil:
			w.onError(err)
		}
	}
}

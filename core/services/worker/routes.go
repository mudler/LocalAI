package worker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// Route is one way the operator gave a worker to reach the cluster. A worker
// holds every route it was given, at the same time, and reports which of them
// it holds. The names are the ones of the route columns of a node, which
// nodes.RoutesOf reads.
type Route string

const (
	// RouteNATS is a NATS connection with the control verbs served on the
	// subjects of the node. The frontends dial the address of the worker for the
	// data.
	RouteNATS Route = workerregistry.RouteNATS
	// RouteTunnel is the tunnel that the worker holds open to a frontend, over a
	// websocket or, for a worker that joined with an invite, over P2P. The
	// worker needs no address and never talks to NATS on it.
	RouteTunnel Route = workerregistry.RouteTunnel
)

// routeOrder is the order in which a worker attaches its routes and lists them:
// NATS first.
var routeOrder = [...]Route{RouteNATS, RouteTunnel}

// ParseRoutes reads the routes that an operator named, keeps each once, and
// puts them in the order a worker attaches them. A name that is not a route is
// an error.
func ParseRoutes(names []string) ([]Route, error) {
	var out []Route
	for _, n := range names {
		r := Route(strings.ToLower(strings.TrimSpace(n)))
		if r == "" {
			continue
		}
		if r != RouteNATS && r != RouteTunnel {
			return nil, fmt.Errorf("unknown worker route %q: use %q or %q", n, RouteNATS, RouteTunnel)
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Route) int { return slices.Index(routeOrder[:], a) - slices.Index(routeOrder[:], b) })
	return out, nil
}

// Timings of a worker that holds its routes.
const (
	// routeBackoffMin and routeBackoffMax bound the wait between two attempts to
	// attach a route that failed.
	routeBackoffMin = time.Second
	routeBackoffMax = 30 * time.Second
	// routeRecheck is how often a held route is checked for an attachment that
	// ended for good.
	routeRecheck = time.Second
	// routeConnectWait bounds the wait for a new attachment to connect before the
	// worker reports it.
	routeConnectWait = time.Minute
)

// Attachment is a worker's hold on one route: a NATS connection with the verbs
// served on it, or a tunnel with the control plane mounted behind it.
type Attachment interface {
	// Connected reports whether the route can be used now. It is false while the
	// attachment connects and while it connects again.
	Connected() bool
	// Close ends the hold. The process of a backend is not touched.
	Close() error
}

// failable is implemented by an attachment that can end by itself for good, for
// example when its credential cannot be renewed. The holder then attaches the
// route again.
type failable interface{ Failed() bool }

// tunnelReporter is implemented by a tunnel attachment. It says how the tunnel
// travels, which the worker reports with its routes.
type tunnelReporter interface{ TunnelRoute() nodes.TunnelRoute }

// Handover is what the frontend gave a worker for one route.
type Handover struct {
	Route Route
	// Creds holds the credential of the route. It keeps the credential fresh.
	Creds *workerregistry.CredentialManager
	// The address of NATS, its CA as PEM, and whether the server asks for a
	// client certificate. Set for NATS only.
	NATSURL       string
	NATSCAPEM     string
	NATSClientTLS bool
}

func handoverOf(r Route, mgr *workerregistry.CredentialManager, res *workerregistry.RegisterResponse) Handover {
	h := Handover{Route: r, Creds: mgr}
	if r == RouteNATS && res != nil {
		h.NATSURL, h.NATSCAPEM, h.NATSClientTLS = res.NatsURL, res.NatsCA, res.NatsClientTLS
	}
	return h
}

// Attacher connects a worker to one route. It returns once the attachment
// exists. It does not wait for the route to answer.
type Attacher func(ctx context.Context, h Handover) (Attachment, error)

// ErrNoCredentials is returned by an attacher that was not given what it needs.
var ErrNoCredentials = errors.New("the frontend did not hand over a credential for this route")

// RouteHolderOptions configures a RouteHolder.
type RouteHolderOptions struct {
	// Routes are the routes the operator gave the worker, in the order of
	// ParseRoutes. Each needs an entry in Attachers.
	Routes []Route
	// Attachers connect each route.
	Attachers map[Route]Attacher
	// Credentials returns the manager that registers the worker for one route
	// and keeps its credential. It must ask the frontend for that route only.
	Credentials func(r Route) *workerregistry.CredentialManager
	// Heartbeat sends a heartbeat.
	Heartbeat func(ctx context.Context, body map[string]any) error
	// Body returns the usual content of a heartbeat. May be nil.
	Body func() map[string]any
	// Interval is the interval of the heartbeat. Zero is ten seconds.
	Interval time.Duration
}

// RouteHolder holds every route the operator gave a worker and reports them.
//
// Each route is attached on its own: a route that cannot be attached, or whose
// attachment ends for good, is tried again in the background with a wait that
// grows from one to thirty seconds, and it never holds back the other routes.
// The heartbeat reports the routes the worker was given, the ones it holds and
// is connected on, and why the others are not held. That report is the only
// source for the frontends of how the worker can be reached (nodes.RoutesOf).
type RouteHolder struct {
	o RouteHolderOptions

	mu       sync.Mutex
	attached map[Route]Attachment
	errs     map[Route]string

	beat sync.Mutex
	wg   sync.WaitGroup
}

// NewRouteHolder returns a holder. Run starts it.
func NewRouteHolder(o RouteHolderOptions) *RouteHolder {
	if o.Interval <= 0 {
		o.Interval = 10 * time.Second
	}
	h := &RouteHolder{o: o, attached: map[Route]Attachment{}, errs: map[Route]string{}}
	for _, r := range o.Routes {
		h.errs[r] = "not attached yet"
	}
	return h
}

// Routes returns the routes the worker was given.
func (h *RouteHolder) Routes() []Route { return slices.Clone(h.o.Routes) }

// Held returns the routes that are attached and connected now, NATS first.
func (h *RouteHolder) Held() []Route {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connectedLocked()
}

func (h *RouteHolder) connectedLocked() []Route {
	var out []Route
	for _, r := range routeOrder {
		if a := h.attached[r]; a != nil && a.Connected() {
			out = append(out, r)
		}
	}
	return out
}

// Ready is nil while at least one route is connected. A worker whose routes are
// all down still sends its heartbeat to the frontend, and the registry would
// show a node that looks healthy and cannot be reached.
func (h *RouteHolder) Ready() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.connectedLocked()) > 0 {
		return nil
	}
	return fmt.Errorf("no route of this worker is connected (%s): the frontend cannot reach this worker", h.reasonsLocked())
}

// Report is the route part of a heartbeat: the routes the worker was given
// (routes), the ones it holds and is connected on (attached), why each of the
// others is not (route_error), and how the tunnel travels when the worker holds
// one.
func (h *RouteHolder) Report() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := func(rs []Route) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, string(r))
		}
		return out
	}
	held := h.connectedLocked()
	rep := map[string]any{
		"routes":      names(h.o.Routes),
		"attached":    names(held),
		"route_error": h.reasonsLocked(),
	}
	if a := h.attached[RouteTunnel]; a != nil {
		if tr, ok := a.(tunnelReporter); ok {
			t := tr.TunnelRoute()
			rep["tunnel_transport"] = t.Transport
			if t.Path != "" {
				rep["tunnel_path"] = t.Path
				rep["tunnel_limited"] = t.Limited
			}
		}
	}
	return rep
}

// reasonsLocked says, for each route the worker was given and is not connected
// on, why.
func (h *RouteHolder) reasonsLocked() string {
	var out []string
	for _, r := range h.o.Routes {
		a := h.attached[r]
		switch {
		case a != nil && a.Connected():
			continue
		case a != nil:
			out = append(out, string(r)+": connecting")
		case h.errs[r] != "":
			out = append(out, string(r)+": "+h.errs[r])
		default:
			out = append(out, string(r)+": not attached")
		}
	}
	return strings.Join(out, "; ")
}

// Run attaches every route, keeps them attached, and sends the heartbeats until
// ctx ends. It returns when every loop has ended. The attachments are not closed
// by Run: Close closes them.
func (h *RouteHolder) Run(ctx context.Context) {
	for _, r := range h.o.Routes {
		h.wg.Add(1)
		go func() { defer h.wg.Done(); h.hold(ctx, r) }()
	}
	h.wg.Add(1)
	go func() { defer h.wg.Done(); h.heartbeats(ctx) }()
	h.wg.Wait()
}

// Close closes every attachment.
func (h *RouteHolder) Close() {
	h.mu.Lock()
	held := h.attached
	h.attached = map[Route]Attachment{}
	h.mu.Unlock()
	for r, a := range held {
		if err := a.Close(); err != nil {
			xlog.Warn("Closing a route of the worker failed", "route", r, "error", err)
		}
	}
}

// hold attaches one route and attaches it again whenever its attachment ends
// for good, until ctx ends.
func (h *RouteHolder) hold(ctx context.Context, r Route) {
	tries := 0
	for ctx.Err() == nil {
		a, err := h.attach(ctx, r)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			tries++
			wait := min(routeBackoffMin<<min(tries-1, 5), routeBackoffMax)
			h.setErr(r, err.Error())
			xlog.Warn("The worker could not attach a route; it keeps its other routes and tries again", "route", r, "retry_in", wait, "error", err)
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}
		tries = 0
		h.mu.Lock()
		h.attached[r] = a
		delete(h.errs, r)
		h.mu.Unlock()
		xlog.Info("The worker attached a route", "route", r)
		// The frontends route by what the worker reports, so tell them once the
		// route answers, and not at the next tick.
		go h.reportWhenConnected(ctx, a)
		if !h.watch(ctx, a) {
			return
		}
		xlog.Warn("A route of the worker ended and will be attached again", "route", r)
		h.mu.Lock()
		if h.attached[r] == a {
			delete(h.attached, r)
		}
		h.errs[r] = "the attachment ended; attaching again"
		h.mu.Unlock()
		_ = a.Close()
	}
}

// attach gets the credential of a route and attaches it.
func (h *RouteHolder) attach(ctx context.Context, r Route) (Attachment, error) {
	attach := h.o.Attachers[r]
	if attach == nil {
		return nil, fmt.Errorf("this worker has no way to attach the %s route", r)
	}
	h.setErr(r, "getting the credential")
	mgr := h.o.Credentials(r)
	res, err := mgr.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting the credential of the %s route: %w", r, err)
	}
	h.setErr(r, "attaching")
	a, err := attach(ctx, handoverOf(r, mgr, res))
	if err != nil {
		return nil, fmt.Errorf("attaching the %s route: %w", r, err)
	}
	return a, nil
}

// watch waits until the attachment ends for good, and returns false when ctx
// ends first.
func (h *RouteHolder) watch(ctx context.Context, a Attachment) bool {
	fa, ok := a.(failable)
	if !ok {
		<-ctx.Done()
		return false
	}
	for {
		if fa.Failed() {
			return true
		}
		if !sleepCtx(ctx, routeRecheck) {
			return false
		}
	}
}

func (h *RouteHolder) setErr(r Route, reason string) {
	h.mu.Lock()
	h.errs[r] = reason
	h.mu.Unlock()
}

func (h *RouteHolder) heartbeats(ctx context.Context) {
	wait := time.Duration(0) // the first heartbeat is at once
	for {
		if !sleepCtx(ctx, wait) {
			return
		}
		h.sendBeat(ctx)
		wait = h.o.Interval
	}
}

// sendBeat sends one heartbeat with the report. It is skipped when no route is
// connected, as the worker has always done: the registry must not show a worker
// healthy that nobody can reach.
func (h *RouteHolder) sendBeat(ctx context.Context) {
	h.beat.Lock()
	defer h.beat.Unlock()
	if err := h.Ready(); err != nil {
		xlog.Warn("Skipping heartbeat: " + err.Error())
		return
	}
	body := map[string]any{}
	if h.o.Body != nil {
		body = h.o.Body()
	}
	maps.Copy(body, h.Report())
	if h.o.Heartbeat == nil {
		return
	}
	if err := h.o.Heartbeat(ctx, body); err != nil {
		xlog.Warn("Heartbeat failed", "error", err)
	}
}

// reportWhenConnected sends a heartbeat as soon as a new attachment answers.
func (h *RouteHolder) reportWhenConnected(ctx context.Context, a Attachment) {
	deadline := time.Now().Add(routeConnectWait)
	for !a.Connected() {
		if time.Now().After(deadline) || !sleepCtx(ctx, 50*time.Millisecond) {
			return
		}
	}
	h.sendBeat(ctx)
}

// sleepCtx waits d, and returns false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

package carrier

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// RouteSource returns the usable routes of one node (nodes.RoutesOf over the row
// of the node).
type RouteSource func(ctx context.Context, nodeID string) ([]nodes.Route, error)

// routeKinds is the order in which the kinds of route are tried when a node has
// no sticky route: direct, then the tunnel.
var routeKinds = [...]nodes.RouteKind{nodes.RouteDirect, nodes.RouteTunnel}

// routesTTL is how long an answer of the RouteSource is reused. The routing of a
// request must not cost a database read when a burst of requests goes to one
// worker. A worker that changed how it attaches is seen after at most this time.
const routesTTL = time.Second

// RouteTable holds the worker route of each kind that this frontend has, and
// picks the routes of one node from the routes that the node has.
//
// A call to a node goes to the route that last worked for it (its sticky route),
// else to the direct route, else to the tunnel, among the routes that the node
// has and this frontend holds. It moves to the next route only when the failure
// proves that the worker received nothing (notStarted): never after a timeout, a
// broken stream or an answer of the worker. A node keeps its sticky route because
// NATS runs the verbs of a worker one at a time and HTTP runs them in parallel,
// so the verbs of one worker must not alternate between the two.
//
// The table is per replica: the sticky routes and the cache are hints, and a
// replica that has none of them picks the same routes from the rows.
type RouteTable struct {
	of RouteSource

	// slots holds the route of each kind.
	slots [len(routeKinds)]atomic.Pointer[WorkerRoute]

	mu     sync.Mutex
	cache  map[string]cachedRoutes
	sticky map[string]nodes.RouteKind
}

type cachedRoutes struct {
	at     time.Time
	routes []nodes.Route
}

// NewRouteTable returns a table with no routes. of may be nil: every call then
// tries every route that the table holds, in the default order.
func NewRouteTable(of RouteSource) *RouteTable {
	return &RouteTable{of: of, cache: map[string]cachedRoutes{}, sticky: map[string]nodes.RouteKind{}}
}

func kindIndex(k nodes.RouteKind) int {
	for i, kind := range routeKinds {
		if kind == k {
			return i
		}
	}
	return -1
}

// Pin makes r the route of its kind for as long as the table lives. A frontend
// pins the tunnel route on every deployment, and the direct route when it has
// a NATS server.
func (t *RouteTable) Pin(r *WorkerRoute) {
	if r == nil {
		return
	}
	if i := kindIndex(r.Kind); i >= 0 {
		t.slots[i].Store(r)
	}
}

// Route returns the route of kind k that the table holds, or nil.
func (t *RouteTable) Route(k nodes.RouteKind) *WorkerRoute {
	if i := kindIndex(k); i >= 0 {
		return t.slots[i].Load()
	}
	return nil
}

// held returns the routes that the table holds, in the default order.
func (t *RouteTable) held() []*WorkerRoute {
	out := make([]*WorkerRoute, 0, len(routeKinds))
	for i := range routeKinds {
		if r := t.slots[i].Load(); r != nil {
			out = append(out, r)
		}
	}
	return out
}

// first returns the first route that the table holds, or nil.
func (t *RouteTable) first() *WorkerRoute {
	for i := range routeKinds {
		if r := t.slots[i].Load(); r != nil {
			return r
		}
	}
	return nil
}

// routesOf returns the routes of a node, from the cache when it is fresh and
// fresh is false. ok is false when they are not known.
func (t *RouteTable) routesOf(ctx context.Context, nodeID string, fresh bool) (routes []nodes.Route, ok bool) {
	if t.of == nil {
		return nil, false
	}
	if !fresh {
		t.mu.Lock()
		hit, cached := t.cache[nodeID]
		t.mu.Unlock()
		if cached && time.Since(hit.at) < routesTTL {
			return hit.routes, true
		}
	}
	routes, err := t.of(ctx, nodeID)
	if err != nil {
		return nil, false
	}
	t.mu.Lock()
	t.cache[nodeID] = cachedRoutes{at: time.Now(), routes: routes}
	t.mu.Unlock()
	return routes, true
}

// routeList is the routes that a call tries, in order. It is an array so that
// picking the routes of a call allocates nothing.
type routeList struct {
	r [len(routeKinds)]*WorkerRoute
	n int
}

func (l *routeList) add(r *WorkerRoute) {
	for i := 0; i < l.n; i++ {
		if l.r[i] == r {
			return
		}
	}
	l.r[l.n] = r
	l.n++
}

// candidates returns the routes that a call to nodeID tries, in order. When the
// routes of the node cannot be read it is every route that the table holds:
// not knowing how a worker attaches says nothing about the worker, and a route
// that does not reach it fails with a not-started error, which moves the call
// on. err is nodes.ErrNoRoute when the node has no route that this table holds.
func (t *RouteTable) candidates(ctx context.Context, nodeID string) (routeList, error) {
	out, err := t.candidatesFrom(ctx, nodeID, false)
	if err != nil && t.of != nil {
		// The routes in the cache may be older than a change of the routes that
		// this frontend holds, or of how the worker attaches. A call is not
		// refused on them: they are read again once.
		out, err = t.candidatesFrom(ctx, nodeID, true)
	}
	return out, err
}

func (t *RouteTable) candidatesFrom(ctx context.Context, nodeID string, fresh bool) (routeList, error) {
	routes, known := t.routesOf(ctx, nodeID, fresh)
	has := func(k nodes.RouteKind) bool {
		if !known {
			return true
		}
		for _, r := range routes {
			if r.Kind == k {
				return true
			}
		}
		return false
	}
	t.mu.Lock()
	sticky, isSticky := t.sticky[nodeID]
	t.mu.Unlock()

	var out routeList
	if isSticky && has(sticky) {
		if r := t.Route(sticky); r != nil {
			out.add(r)
		}
	}
	for i, k := range routeKinds {
		if r := t.slots[i].Load(); r != nil && has(k) {
			out.add(r)
		}
	}
	if out.n == 0 {
		if known && len(routes) == 0 {
			return out, fmt.Errorf("%w: worker %s has no direct address on NATS and no live tunnel", nodes.ErrNoRoute, nodeID)
		}
		return out, fmt.Errorf("%w: this frontend holds no route that reaches worker %s", nodes.ErrNoRoute, nodeID)
	}
	return out, nil
}

// pick returns the first route of a node.
func (t *RouteTable) pick(ctx context.Context, nodeID string) (*WorkerRoute, error) {
	routes, err := t.candidates(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return routes.r[0], nil
}

// worked records that r reached the node.
func (t *RouteTable) worked(nodeID string, r *WorkerRoute) {
	t.mu.Lock()
	if k, ok := t.sticky[nodeID]; !ok || k != r.Kind {
		t.sticky[nodeID] = r.Kind
	}
	t.mu.Unlock()
}

// failed drops the sticky route of a node when it is r, which did not reach it.
func (t *RouteTable) failed(nodeID string, r *WorkerRoute) {
	t.mu.Lock()
	if k, ok := t.sticky[nodeID]; ok && k == r.Kind {
		delete(t.sticky, nodeID)
	}
	t.mu.Unlock()
}

// Forget drops what the table and its routes keep for a node that left or lost
// its right to a connection.
func (t *RouteTable) Forget(nodeID string) {
	t.mu.Lock()
	delete(t.cache, nodeID)
	delete(t.sticky, nodeID)
	t.mu.Unlock()
	for _, r := range t.held() {
		if r.ForgetNode != nil {
			r.ForgetNode(nodeID)
		}
	}
}

// notStarted reports whether err proves that the worker received nothing, so
// that the same call may go to another route: a route that does not exist
// (nodes.ErrNoRoute, which is also how NATS reports no responder and how the
// tunnel reports a worker with no tunnel), or a request of the peer link that
// was not started (tunnel.NotStarted).
func notStarted(err error) bool {
	return errors.Is(err, nodes.ErrNoRoute) || tunnel.NotStarted(err)
}

// call runs fn on the routes of a node in order until one does not fail with a
// not-started error.
func call[T any](ctx context.Context, t *RouteTable, nodeID string, fn func(r *WorkerRoute) (T, error)) (T, error) {
	routes, err := t.candidates(ctx, nodeID)
	if err != nil {
		var zero T
		return zero, err
	}
	var v T
	for _, r := range routes.r[:routes.n] {
		v, err = fn(r)
		if err == nil {
			t.worked(nodeID, r)
			return v, nil
		}
		if !notStarted(err) {
			return v, err
		}
		t.failed(nodeID, r)
	}
	return v, err
}

// dial is call for the dialer of the HTTP server of a worker. It is written out,
// with no closure, because an HTTP client runs it for every connection and the
// closure of call would be allocated where the dialer is inlined.
func (t *RouteTable) dial(ctx context.Context, nodeID, network, addr string) (net.Conn, error) {
	routes, err := t.candidates(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	for _, r := range routes.r[:routes.n] {
		conn, err = r.Dialer(nodeID)(ctx, network, addr)
		if err == nil {
			t.worked(nodeID, r)
			return conn, nil
		}
		if !notStarted(err) {
			return conn, err
		}
		t.failed(nodeID, r)
	}
	return conn, err
}

// call0 is call for a function with no value.
func call0(ctx context.Context, t *RouteTable, nodeID string, fn func(r *WorkerRoute) error) error {
	_, err := call(ctx, t, nodeID, func(r *WorkerRoute) (struct{}, error) { return struct{}{}, fn(r) })
	return err
}

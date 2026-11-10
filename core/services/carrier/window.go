package carrier

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// Attachment says which carriers a worker is connected to.
type Attachment struct{ NATS, Tunnel bool }

// To reports whether the worker is attached to carrier c.
func (a Attachment) To(c cluster.Carrier) bool {
	switch c {
	case cluster.CarrierNATS:
		return a.NATS
	case cluster.CarrierTunnel:
		return a.Tunnel
	}
	return false
}

// AttachmentSource says which carriers a worker is connected to now.
type AttachmentSource func(ctx context.Context, nodeID string) (Attachment, error)

// AgentSource says whether any agent worker is attached to carrier c.
type AgentSource func(ctx context.Context, c cluster.Carrier) (bool, error)

// attachmentTTL is how long an answer of the AttachmentSource is reused. The
// window lasts minutes, and the routing of a request must not cost a database
// read when a burst of requests goes to one worker.
const attachmentTTL = time.Second

// Window is the routing rule of the time in which two carriers are attached: from
// the commit of a change until the end of its drain. Outside it, every call goes
// to the active set and the window costs one atomic load.
//
// Inside it, a call to a worker goes to the active carrier when the worker is
// attached to it, else to the previous carrier when the worker is attached to
// that one, else nowhere (nodes.ErrNoRoute). The choice is read from the state of
// the worker. It is never made from the error of an earlier attempt, and a call
// is never tried on the other carrier.
type Window struct {
	prev     atomic.Pointer[Set]
	attached AttachmentSource
	agents   AgentSource

	mu    sync.Mutex
	cache map[string]cachedAttachment
}

type cachedAttachment struct {
	at time.Time
	a  Attachment
}

// NewWindow returns a closed window. agents may be nil: the agent control then
// always uses the active set.
func NewWindow(attached AttachmentSource, agents AgentSource) *Window {
	return &Window{attached: attached, agents: agents, cache: map[string]cachedAttachment{}}
}

// Open starts the window with prev as the carrier that is draining.
func (w *Window) Open(prev *Set) {
	w.mu.Lock()
	w.cache = map[string]cachedAttachment{}
	w.mu.Unlock()
	w.prev.Store(prev)
}

// Close ends the window.
func (w *Window) Close() { w.prev.Store(nil) }

// Previous returns the set that is draining, or nil outside the window.
func (w *Window) Previous() *Set { return w.prev.Load() }

func (w *Window) attachment(ctx context.Context, nodeID string) (Attachment, error) {
	w.mu.Lock()
	hit, ok := w.cache[nodeID]
	w.mu.Unlock()
	if ok && time.Since(hit.at) < attachmentTTL {
		return hit.a, nil
	}
	a, err := w.attached(ctx, nodeID)
	if err != nil {
		return Attachment{}, err
	}
	w.mu.Lock()
	w.cache[nodeID] = cachedAttachment{at: time.Now(), a: a}
	w.mu.Unlock()
	return a, nil
}

// route returns the set that a call to nodeID uses.
func (w *Window) route(ctx context.Context, cur *Set, nodeID string) (*Set, error) {
	prev := w.prev.Load()
	if prev == nil || prev == cur || w.attached == nil {
		return cur, nil
	}
	a, err := w.attachment(ctx, nodeID)
	if err != nil {
		// Not knowing where a worker is attached says nothing about the worker.
		// The active carrier is the best guess, and its own error is the answer.
		return cur, nil
	}
	switch {
	case a.To(cur.Name):
		return cur, nil
	case a.To(prev.Name):
		return prev, nil
	}
	return nil, fmt.Errorf("%w: worker %s is attached to neither the %s carrier nor the %s carrier that is draining",
		nodes.ErrNoRoute, nodeID, cur.Name, prev.Name)
}

// routeAgents returns the set that a request for an agent worker uses: the active
// one unless no agent worker is attached to it and one is attached to the
// previous one.
func (w *Window) routeAgents(ctx context.Context, cur *Set) *Set {
	prev := w.prev.Load()
	if prev == nil || prev == cur || w.agents == nil {
		return cur
	}
	if on, err := w.agents(ctx, cur.Name); err != nil || on {
		return cur
	}
	if on, err := w.agents(ctx, prev.Name); err == nil && on {
		return prev
	}
	return cur
}

// Package distributedhdr carries a per-request "which worker node served
// me" record from the distributed router (core/services/nodes) up to the
// HTTP response writer wrapper (core/http/middleware). It is the bridge
// for the X-LocalAI-Node response header without coupling those two
// packages directly or going through any shared mutable state.
//
// Why its own package: both core/http/middleware and core/services/nodes
// already import pkg/model, and the natural homes for this key would
// create an import cycle if either side hosted the helper. Putting the
// key and the tiny helpers here keeps it neutral - producer and consumer
// import a leaf package, not each other.
package distributedhdr

import (
	"context"
	"sync/atomic"
)

// ctxKey is an unexported context-key type so external packages cannot
// collide with our key by accident.
type ctxKey struct{}

// holderKey is the singleton context key used by WithHolder / Holder /
// Stamp. Unexported on purpose: producers and consumers must go through
// the helpers below so the storage representation stays an
// implementation detail.
var holderKey = ctxKey{}

// NewHolder returns an empty holder ready to be attached to a request
// context via WithHolder. The middleware creates one per HTTP request;
// the router fills it; the response writer wrapper reads it on the
// first byte. The atomic.Value gives us race-clean publish across the
// goroutines that may write (router) and read (response writer
// wrapper) the slot.
func NewHolder() *atomic.Value {
	return &atomic.Value{}
}

// WithHolder returns a derived context that carries the provided holder.
// The middleware calls this on the per-request context BEFORE the
// downstream handler chain runs, so any goroutine that inherits this
// context (notably the SmartRouter / ModelRouterAdapter) can find the
// holder via Stamp.
func WithHolder(ctx context.Context, h *atomic.Value) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, holderKey, h)
}

// Holder returns the holder attached to ctx, or nil if none was set.
// Callers that just want to publish should prefer Stamp; Holder is
// useful for tests and for propagating the holder across derived
// contexts (see Inherit).
func Holder(ctx context.Context) *atomic.Value {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(holderKey).(*atomic.Value)
	return h
}

// Stamp records the picked worker node ID into the holder attached to
// ctx. No-op when:
//   - ctx is nil
//   - no holder is attached (e.g. the X-LocalAI-Node feature is off, so
//     the middleware never ran)
//   - nodeID is empty
//
// Stamp is safe to call from any goroutine. Subsequent reads via Load
// observe the most recent stamp.
func Stamp(ctx context.Context, nodeID string) {
	if nodeID == "" {
		return
	}
	h := Holder(ctx)
	if h == nil {
		return
	}
	h.Store(nodeID)
}

// Load returns the node ID most recently stamped into the holder, or
// "" if nothing has been stamped. Intended for the response writer
// wrapper's first-byte hook.
func Load(h *atomic.Value) string {
	if h == nil {
		return ""
	}
	v, _ := h.Load().(string)
	return v
}

// Inherit copies the holder reference from src into dst when present.
// Used at request-handling seams where the request context is replaced
// with a fresh context derived from the long-lived application context
// (so the cancel semantics of the original context are preserved while
// also allowing the load path to outlive the HTTP transport). The
// holder is a pointer, so both contexts point at the same slot; a
// router stamp via Stamp(dst, ...) is observed by the middleware that
// reads through src's holder.
func Inherit(dst, src context.Context) context.Context {
	h := Holder(src)
	if h == nil {
		return dst
	}
	return WithHolder(dst, h)
}

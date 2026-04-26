package messaging

import (
	"context"
	"sync"
)

// CancelRegistry tracks cancellation functions keyed by an ID (e.g. job or agent).
// It is safe for concurrent use.
type CancelRegistry struct {
	m sync.Map
}

// Register stores a cancel function for the given key.
func (r *CancelRegistry) Register(key string, cancel context.CancelFunc) {
	r.m.Store(key, cancel)
}

// Cancel invokes and removes the cancel function for the given key.
// Returns true if the key was found and cancelled.
func (r *CancelRegistry) Cancel(key string) bool {
	if fn, ok := r.m.LoadAndDelete(key); ok {
		if cancelFn, ok := fn.(context.CancelFunc); ok {
			cancelFn()
			return true
		}
	}
	return false
}

// Deregister removes the cancel function without invoking it.
func (r *CancelRegistry) Deregister(key string) {
	r.m.Delete(key)
}

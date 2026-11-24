package nodes

import (
	"context"
	"sync"
)

// backendOpLocks hands out one lock per (node, backend). It keeps a live
// backend operation and the reconciler's retry of the same (node, backend)
// from running at the same time. The zero value is ready to use.
//
// The locks are per process. Several frontends can still send to one node at
// once; the worker is the last guard for that case.
type backendOpLocks struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func backendOpLockKey(nodeID, backend string) string {
	return nodeID + "\x00" + backend
}

func (l *backendOpLocks) slot(key string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.slots == nil {
		l.slots = map[string]chan struct{}{}
	}
	s, ok := l.slots[key]
	if !ok {
		s = make(chan struct{}, 1)
		l.slots[key] = s
	}
	return s
}

// lock waits for the (node, backend) lock or for ctx. The returned func
// releases it. When ctx ends first, it returns ctx's error.
func (l *backendOpLocks) lock(ctx context.Context, nodeID, backend string) (func(), error) {
	s := l.slot(backendOpLockKey(nodeID, backend))
	select {
	case s <- struct{}{}:
		return func() { <-s }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tryLock takes the lock only if it is free.
func (l *backendOpLocks) tryLock(nodeID, backend string) (func(), bool) {
	s := l.slot(backendOpLockKey(nodeID, backend))
	select {
	case s <- struct{}{}:
		return func() { <-s }, true
	default:
		return nil, false
	}
}

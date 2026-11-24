package model

import (
	"context"
	"math"
	"sync"

	"golang.org/x/sync/semaphore"
)

// modelOperationLocks serializes destructive lifecycle operations per model
// without coupling unrelated models. Entries are reference-counted so model IDs
// observed from requests do not accumulate forever.
type modelOperationLocks struct {
	mu      sync.Mutex
	entries map[string]*modelOperationLock
}

type modelOperationLock struct {
	sem  *semaphore.Weighted
	refs int
}

func newModelOperationLocks() *modelOperationLocks {
	return &modelOperationLocks{entries: make(map[string]*modelOperationLock)}
}

func (l *modelOperationLocks) acquire(modelID string, exclusive bool) func() {
	release, err := l.acquireContext(context.Background(), modelID, exclusive)
	if err != nil {
		panic(err)
	}
	return release
}

func (l *modelOperationLocks) acquireContext(ctx context.Context, modelID string, exclusive bool) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	entry := l.entries[modelID]
	if entry == nil {
		entry = &modelOperationLock{sem: semaphore.NewWeighted(math.MaxInt64)}
		l.entries[modelID] = entry
	}
	entry.refs++
	l.mu.Unlock()

	weight := int64(1)
	if exclusive {
		weight = math.MaxInt64
	}
	if err := entry.sem.Acquire(ctx, weight); err != nil {
		l.releaseReference(modelID, entry)
		return nil, err
	}

	return func() {
		entry.sem.Release(weight)
		l.releaseReference(modelID, entry)
	}, nil
}

func (l *modelOperationLocks) releaseReference(modelID string, entry *modelOperationLock) {
	l.mu.Lock()
	entry.refs--
	if entry.refs == 0 {
		delete(l.entries, modelID)
	}
	l.mu.Unlock()
}

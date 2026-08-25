package gallery

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
)

// ErrBackendOperationInProgress is returned when a non-blocking backend
// upgrade finds another upgrade already using the same backend path.
var ErrBackendOperationInProgress = errors.New("backend operation already in progress")

// UpgradeOption customizes UpgradeBackend behavior.
type UpgradeOption func(*upgradeOptions)

type upgradeOptions struct {
	skipIfBusy bool
}

// WithSkipIfBackendBusy makes UpgradeBackend return
// ErrBackendOperationInProgress instead of waiting for an operation already
// upgrading the same resolved backend path. Background auto-upgrades use this
// so an explicit upgrade remains the owner of the in-flight work.
func WithSkipIfBackendBusy() UpgradeOption {
	return func(options *upgradeOptions) {
		options.skipIfBusy = true
	}
}

type backendOperationEntry struct {
	token chan struct{}
	refs  int
}

type backendOperationCoordinator struct {
	mu      sync.Mutex
	entries map[string]*backendOperationEntry
}

func newBackendOperationCoordinator() *backendOperationCoordinator {
	return &backendOperationCoordinator{
		entries: make(map[string]*backendOperationEntry),
	}
}

var backendOperations = newBackendOperationCoordinator()

// acquire serializes upgrades by their normalized backend path. refs counts
// both the holder and waiters so an entry cannot be removed and replaced while
// a waiter still refers to it.
func (c *backendOperationCoordinator) acquire(ctx context.Context, path string, skipIfBusy bool) (func(), error) {
	key := filepath.Clean(path)

	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		entry = &backendOperationEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		c.entries[key] = entry
	}
	entry.refs++
	c.mu.Unlock()

	acquired := false
	if skipIfBusy {
		select {
		case <-entry.token:
			acquired = true
		default:
		}
	} else {
		select {
		case <-entry.token:
			acquired = true
		case <-ctx.Done():
		}
	}

	if !acquired {
		c.dropReference(key, entry)
		if skipIfBusy {
			return nil, ErrBackendOperationInProgress
		}
		return nil, ctx.Err()
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			entry.token <- struct{}{}
			c.dropReference(key, entry)
		})
	}
	return release, nil
}

func (c *backendOperationCoordinator) dropReference(key string, entry *backendOperationEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry.refs--
	if entry.refs == 0 && c.entries[key] == entry {
		delete(c.entries, key)
	}
}

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/xlog"
)

// libraryRefreshInterval is how often a frontend asks the owner whether the
// library sources or the official-libraries switch changed. A change on the
// owner reaches the gallery lists of a frontend within this time.
var libraryRefreshInterval = 60 * time.Second

// libraryRetryInterval is how often a frontend asks until the owner answered
// once.
const libraryRetryInterval = 5 * time.Second

// librarySnapshot is the per-instance copy of the owner's source list. Two
// frontends can show different gallery lists for up to one refresh interval
// after a change (mode 4 of .agents/distributed-state.md). Nothing is served
// from it: it only decides which galleries this frontend reads.
type librarySnapshot struct {
	mu      sync.Mutex
	version string
	loaded  bool
	stale   bool
	// The galleries the flags named. Every rebuild starts from them, so a
	// source the owner removed leaves the lists again.
	baseModels, baseBackends []config.Gallery
}

// LibraryListsStale reports that a frontend could not reach the owner at its
// last refresh, so its gallery lists may be older than the owner's sources. It
// is false before the first answer, because the lists are then the flags'.
func (a *Application) LibraryListsStale() bool {
	a.librarySnap.mu.Lock()
	defer a.librarySnap.mu.Unlock()
	return a.librarySnap.stale
}

// RefreshLibraries asks the owner for its source list and rebuilds the model
// and backend galleries and the official-libraries switch when the version
// changed. When the owner does not answer, the last lists stay and are marked
// stale; the lists are never rebuilt from nothing.
func (a *Application) RefreshLibraries(ctx context.Context) error {
	s := &a.librarySnap
	err := a.fetchLibraries(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if s.loaded && !s.stale {
			xlog.Warn("the library sources of the add-on owner cannot be read; the gallery lists may be stale", "error", err)
		}
		if s.loaded {
			s.stale = true
		}
		return err
	}
	s.stale = false
	return nil
}

func (a *Application) fetchLibraries(ctx context.Context) error {
	c, err := a.ownerCarrier()
	if err != nil {
		return err
	}
	if err := a.libraryOwnerReady(ctx, c); err != nil {
		return err
	}
	s := &a.librarySnap
	s.mu.Lock()
	known := s.version
	s.mu.Unlock()
	raw, err := c.LibrarySnapshot(ctx, known)
	if err != nil {
		return err
	}
	var l library.Listing
	if json.Unmarshal(raw, &l) != nil || l.Version == "" {
		return gateway.ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.Unchanged || (s.loaded && l.Version == s.version) {
		return nil
	}
	cfg := a.applicationConfig
	if !s.loaded {
		s.baseModels = append([]config.Gallery(nil), cfg.Galleries...)
		s.baseBackends = append([]config.Gallery(nil), cfg.BackendGalleries...)
	}
	prevM, prevB := cfg.Galleries, cfg.BackendGalleries
	// The owner's switch is the deployment's value; a frontend never sets it.
	cfg.OfficialLibraries = l.OfficialLibraries
	cfg.Galleries, cfg.BackendGalleries = library.ReconcileInfos(l.Sources, s.baseModels, s.baseBackends)
	gallery.ResetGalleryModelCacheIfChanged(prevM, prevB, cfg)
	s.version, s.loaded = l.Version, true
	return nil
}

// runLibraryRefresh keeps the gallery lists of a frontend in line with the
// owner until ctx ends.
func (a *Application) runLibraryRefresh(ctx context.Context) {
	for {
		_ = a.RefreshLibraries(ctx)
		wait := libraryRefreshInterval
		a.librarySnap.mu.Lock()
		if !a.librarySnap.loaded {
			wait = min(wait, libraryRetryInterval)
		}
		a.librarySnap.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

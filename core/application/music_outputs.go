// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	"io"
)

// MusicOutputStore is the private delivery dependency, not a storage initializer.
// Open must recheck the authorized durable manifest and expiry before returning.
type MusicOutputStore interface {
	Lookup(context.Context, string) (outputstore.Manifest, error)
	Open(context.Context, outputstore.Manifest) (io.ReadCloser, error)
}

var _ MusicOutputStore = (*outputstore.Store)(nil)

// SetMusicOutputStore injects the actual store before HTTP routes are registered.
// Ownership and shutdown remain with the constructor of that store. Replacing it
// after route registration does not change the dependency captured by handlers.
func (a *Application) SetMusicOutputStore(store MusicOutputStore) {
	a.musicOutputMu.Lock()
	defer a.musicOutputMu.Unlock()
	a.musicOutputStore = store
}

func (a *Application) MusicOutputStore() MusicOutputStore {
	a.musicOutputMu.RLock()
	defer a.musicOutputMu.RUnlock()
	return a.musicOutputStore
}

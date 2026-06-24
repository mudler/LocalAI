package credentials

import "sync/atomic"

var defaultStore atomic.Pointer[Store]

// SetDefault installs the store every download path consults and returns the
// previous one. Downloads read it per request, so a swap takes effect without
// rebuilding cached HTTP clients.
func SetDefault(s *Store) (previous *Store) {
	return defaultStore.Swap(s)
}

// Default returns the installed store, or nil. Store methods are nil-safe, so
// callers never need to check.
func Default() *Store {
	return defaultStore.Load()
}

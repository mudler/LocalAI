package gallery

import (
	"sync"
	"time"
)

// A backend can be current by tag, version and digest and still be too old
// for the server. The gallery "latest" image of a backend can lag behind the
// server code, so a tag or digest comparison reports "up to date" while the
// backend lacks a feature the server relies on. The server cannot ask an
// unloaded backend for its features, so it learns this when a request proves
// it: the request carries a field only a new backend understands, and an old
// backend answers as if the field were absent.
//
// MarkBackendStale records such an observation. CheckUpgradesAgainst then
// reports every backend installed before that moment as upgradeable, with the
// reason. Reinstalling the backend (which sets a newer InstalledAt) drops the
// mark, so no explicit clearing is needed. If the reinstalled image is still
// old, the next failing request marks it again.
//
// The marks are per process. In distributed mode each frontend learns from the
// requests it serves.

type staleMark struct {
	at     time.Time
	reason string
}

var (
	staleMu       sync.Mutex
	staleBackends = map[string]staleMark{}
	staleNotify   func()
)

// MarkBackendStale records that the backend configured as name (a concrete
// backend name or its alias, for example "llama-cpp") failed a request in a
// way that shows it is older than the server. reason is shown to the operator.
func MarkBackendStale(name, reason string) {
	if name == "" {
		return
	}
	staleMu.Lock()
	staleBackends[name] = staleMark{at: time.Now(), reason: reason}
	notify := staleNotify
	staleMu.Unlock()
	if notify != nil {
		notify()
	}
}

// SetStaleBackendNotifier registers a function called after each
// MarkBackendStale, so the upgrade cache can refresh at once instead of at
// the next periodic check. The function must not block.
func SetStaleBackendNotifier(fn func()) {
	staleMu.Lock()
	staleNotify = fn
	staleMu.Unlock()
}

// staleReason returns why the installed backend was marked stale, if a mark
// matches its name or alias and was recorded after it was installed.
func staleReason(md *BackendMetadata) (string, bool) {
	if md == nil {
		return "", false
	}
	installedAt, err := time.Parse(time.RFC3339, md.InstalledAt)
	if err != nil {
		// Unknown install time: treat the backend as installed before the mark.
		installedAt = time.Time{}
	}
	staleMu.Lock()
	defer staleMu.Unlock()
	for _, key := range []string{md.Name, md.Alias} {
		if key == "" {
			continue
		}
		if m, ok := staleBackends[key]; ok && m.at.After(installedAt) {
			return m.reason, true
		}
	}
	return "", false
}

package gallery

import "sync"

// Two in-process locks, keyed by model name, for every writer of the files
// of one installed model. They add to the cluster-wide ModelLock, which does
// not exist in a standalone process.
//
//   - TryClaimModel is a long claim: an install commit, an upgrade, a hold,
//     the removal of an old version and an adoption each hold it for the
//     length of their change, and another claim fails at once.
//   - LockSidecar is a short mutex around one read-modify-write of the gallery
//     file. A config edit does not claim the model, because it must stay
//     possible while an upgrade runs, but it records the hash of its config
//     in the gallery file, so it takes this mutex.
//
// LockSidecar never calls out while it is held and takes no other lock, so it
// cannot take part in a lock cycle.

var (
	claimMu sync.Mutex
	claimed = map[string]bool{}

	sidecarMu      sync.Mutex
	sidecarEntries = map[string]*sidecarLock{}
)

type sidecarLock struct {
	mu   sync.Mutex
	refs int
}

// TryClaimModel claims the model for one change in this process. It returns
// false when another change holds the claim; release ends the claim.
func TryClaimModel(name string) (release func(), ok bool) {
	claimMu.Lock()
	defer claimMu.Unlock()
	if claimed[name] {
		return nil, false
	}
	claimed[name] = true
	return func() {
		claimMu.Lock()
		delete(claimed, name)
		claimMu.Unlock()
	}, true
}

// LockSidecar takes the mutex of the gallery file of the model and returns
// the function that releases it. It waits for the holder, which holds it for
// one read-modify-write.
func LockSidecar(name string) (unlock func()) {
	sidecarMu.Lock()
	entry := sidecarEntries[name]
	if entry == nil {
		entry = &sidecarLock{}
		sidecarEntries[name] = entry
	}
	entry.refs++
	sidecarMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		sidecarMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(sidecarEntries, name)
		}
		sidecarMu.Unlock()
	}
}

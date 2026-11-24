package upgraderun

import (
	"cmp"
	"context"

	"github.com/mudler/LocalAI/core/services/backendupdates"
)

// heldBackRuns is how many of the newest runs HeldBack reads. A release that
// failed further back than that is tried again by the automatic update.
const heldBackRuns = 20

// releaseFailedKeys are the error keys of a failed item that say the release
// itself does not work on this server: it did not pass its integrity check
// (verify), it could not be put in place (swap), or it did not start. A new
// try of the same release ends the same way. The other keys (download,
// unavailable, unknown, interrupted, stale, ...) are about the moment, and a
// new try can work.
var releaseFailedKeys = map[string]bool{ErrKeyVerify: true, ErrKeySwap: true, ErrKeyStart: true}

// releaseFailed reports whether an item that ended this way tried a release
// that does not work here. A rolled-back item always did: its new build did
// not start, or could not be put in place, and the old one is back.
func releaseFailed(it Item) bool {
	switch it.Phase {
	case PhaseRolledBack:
		return true
	case PhaseFailed:
		return releaseFailedKeys[it.ErrorKey]
	}
	return false
}

// HeldBack is the releases the automatic update does not try again: for each
// item key, the targets whose newest attempt in a finished run failed in a way
// that a new try of the same release cannot fix (see releaseFailed).
type HeldBack map[string]map[string]bool

// Holds reports whether the release target of the item key is held back.
func (h HeldBack) Holds(key, target string) bool { return h[key][target] }

// heldBackOf reads the held-back releases from runs, newest first. The
// newest attempt of a key and a target decides: a release that failed and
// then worked, or failed for a reason of the moment, is not held back. A
// dismissed run counts: dismissing it does not make the release work.
func heldBackOf(runs []Run) HeldBack {
	out := HeldBack{}
	seen := map[string]map[string]bool{}
	for _, r := range runs {
		if r.Active() {
			continue
		}
		for _, it := range r.Items {
			target := cmp.Or(it.ExpectedTo, it.To)
			if target == "" || seen[it.Key][target] {
				continue
			}
			if seen[it.Key] == nil {
				seen[it.Key] = map[string]bool{}
			}
			seen[it.Key][target] = true
			if !releaseFailed(it) {
				continue
			}
			if out[it.Key] == nil {
				out[it.Key] = map[string]bool{}
			}
			out[it.Key][target] = true
		}
	}
	return out
}

// HeldBack returns the releases that an automatic run leaves out: the same
// target of an item already rolled back, or failed because the release does
// not work here, in one of the newest finished runs. It stays held back until
// the gallery lists another target. A manual run is never held back, so an
// administrator can try the release again.
func (s *Service) HeldBack(ctx context.Context) (HeldBack, error) {
	runs, err := s.List(ctx, ListOptions{Limit: heldBackRuns, WithDismissed: true})
	if err != nil {
		return nil, err
	}
	return heldBackOf(runs), nil
}

// dropHeldBack removes from the items of an automatic run the ones whose
// target is held back. It returns the items that stay and the keys it
// dropped.
func dropHeldBack(items []Item, held HeldBack) ([]Item, []string) {
	out := items[:0:0]
	var dropped []string
	for _, it := range items {
		if held.Holds(it.Key, cmp.Or(it.ExpectedTo, it.To)) {
			dropped = append(dropped, it.Key)
			continue
		}
		out = append(out, it)
	}
	return out, dropped
}

// MarkHeldBack sets HeldBack on the items of the backend read model whose
// target is held back, so the Updates page can say why the automatic update
// does not take them.
func MarkHeldBack(snap *backendupdates.Snapshot, held HeldBack) {
	if snap == nil || len(held) == 0 {
		return
	}
	for i := range snap.Items {
		it := &snap.Items[i]
		if held.Holds(it.ID, backendTarget(*it)) {
			it.HeldBack = true
		}
	}
}

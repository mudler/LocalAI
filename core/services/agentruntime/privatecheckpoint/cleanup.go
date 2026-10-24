// SPDX-License-Identifier: MIT
package privatecheckpoint

// This pure model does not execute cleanup or certify physical erasure. A host
// interpreter must CAS the exact binding, durably tombstone before purge, and
// deny reads from its tombstone adapter even if physical purge fails.
type CleanupState uint8

const (
	SealedPresent CleanupState = iota
	Tombstoned
	Purged
)

type CleanupEvent uint8

const (
	RequestCleanup CleanupEvent = iota
	TombstoneSucceeded
	TombstoneFailed
	PurgeSucceeded
	PurgeFailed
	Retry
	StaleBinding
)

type CleanupEffect uint8

const (
	Hold CleanupEffect = iota
	Tombstone
	Purge
	Return
)

type CleanupTransition struct {
	State  CleanupState
	Effect CleanupEffect
	Rule   string
}

func ReduceCleanup(s CleanupState, e CleanupEvent) (CleanupTransition, error) {
	if s > Purged || e > StaleBinding {
		return CleanupTransition{}, ErrDenied
	}
	t := CleanupTransition{s, Hold, "cleanup.hold"}
	if e == StaleBinding {
		t.Rule = "cleanup.stale"
		return t, nil
	}
	if s == Purged {
		t.Effect = Return
		t.Rule = "cleanup.terminal"
		return t, nil
	}
	if s == SealedPresent {
		switch e {
		case RequestCleanup, Retry:
			t.Effect = Tombstone
			t.Rule = "cleanup.request"
		case TombstoneSucceeded:
			t.State = Tombstoned
			t.Effect = Purge
			t.Rule = "cleanup.tombstoned"
		case TombstoneFailed:
			t.Rule = "cleanup.tombstone-failed"
		}
		return t, nil
	}
	switch e {
	case RequestCleanup, Retry:
		t.Effect = Purge
		t.Rule = "cleanup.retry"
	case PurgeSucceeded:
		t.State = Purged
		t.Effect = Return
		t.Rule = "cleanup.purged"
	case PurgeFailed:
		t.Rule = "cleanup.purge-failed"
	}
	return t, nil
}

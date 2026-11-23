package upgraderun

// retryableKeys are the error keys of a failure that a new try can fix
// without a person doing something first:
//
//   - download, verify, swap: the new build could not be fetched, checked or
//     put in place; the next try fetches it again;
//   - start: the new build did not start and the old one was kept; a later
//     build, or a fixed host, can start;
//   - stale: the target moved, or the update is still listed after the
//     operation; the retry reads the target fresh;
//   - unknown: the failure has no code, so nothing says a retry is useless;
//   - unavailable: the server or the add-on owner did not answer;
//   - upgrade_failed, interrupted: the add-on owner kept the installed
//     version, and says the upgrade can be tried again;
//   - stale_installed, candidate_changed, check_failed: the add-on owner
//     refused a pin that moved; the retry pins what the check offers now.
//
// Every other key needs a person first, and its item has no Retry: for
// example incompatible and integrity_error (the release cannot run here),
// recovery_required (the add-on must be recovered), forbidden and
// not_human_surface (another account or the browser), held, edited_keys,
// approval_required and approval_stale (a choice or an approval),
// upgrade_conflict and operation_conflict (another operation), not_installed
// and no_update (nothing to retry). A key this table does not know is not
// retryable either: a Retry that cannot work is worse than none.
var retryableKeys = map[string]bool{
	ErrKeyDownload: true, ErrKeyVerify: true, ErrKeySwap: true, ErrKeyStart: true,
	ErrKeyStale: true, ErrKeyUnknown: true, ErrKeyUnavailable: true,
	ErrKeyUpgradeFailed: true, ErrKeyInterrupted: true,
	ErrKeyStaleInstalled: true, ErrKeyCandidateChanged: true, ErrKeyCheckFailed: true,
}

// RetryableKey reports whether a failure with this error key can be retried
// (see retryableKeys). An empty key is the unknown key.
func RetryableKey(key string) bool { return retryableKeys[orUnknown(key)] }

// RetryEligible reports whether a retry of the run takes this item: it is
// retryable and ended failed, rolled back, or unconfirmed with no check
// pending.
func (it *Item) RetryEligible() bool { return it.Retryable && retryablePhase(it) }

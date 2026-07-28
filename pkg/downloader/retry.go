package downloader

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrTransientDownload marks a download failure that a later attempt has a
// real chance of getting past: the peer cancelled the stream, the connection
// dropped, the transfer stalled, or the server answered 5xx. Everything else
// is treated as permanent, because retrying it either loops on a condition
// that will never change (404, checksum mismatch, a full disk) or burns
// bandwidth re-fetching gigabytes for nothing.
var ErrTransientDownload = errors.New("transient download failure")

// DownloadRetryAttempts bounds how many times a single file is attempted
// before the whole plan gives up. These are multi-GB transfers, so the budget
// is deliberately small: the resume path means a retry is cheap in bytes, but
// an aggressive retry loop against a genuinely broken remote is its own
// outage. Overridable so tests (and operators on very flaky links) can adjust.
var DownloadRetryAttempts = 3

// DownloadRetryBaseDelay is the first backoff interval; each further retry
// doubles it. Backoff exists to let a momentarily overloaded remote recover,
// not to wait out a long outage, hence the small base and the low attempt cap.
var DownloadRetryBaseDelay = 2 * time.Second

// transientError wraps an error while keeping its message intact, so the
// caller-facing diagnostics stay exactly as informative as before and only the
// retry classification changes.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }

func (e *transientError) Unwrap() error { return e.err }

func (e *transientError) Is(target error) bool { return target == ErrTransientDownload }

// asTransient marks err retryable. A nil error stays nil so call sites can
// wrap unconditionally.
func asTransient(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

// IsRetryable reports whether another attempt is worth making. A cancelled
// caller is never retried through, regardless of how the failure was
// classified: the caller has already given up.
func IsRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, ErrUserCancelled) {
		return false
	}
	// An explicit transient marking outranks the cancellation sentinels. A
	// transport guard firing on a wedged peer (net/http reports a
	// ResponseHeaderTimeout as context.DeadlineExceeded) is *our* abort, not
	// the caller's, and must still be retried. A caller who genuinely gave up
	// is already caught by the ctx.Err() check above, so nothing that should
	// stop is let through here.
	if errors.Is(err, ErrTransientDownload) {
		return true
	}
	return false
}

// readErrorRecorder remembers the last non-EOF error the source returned.
// io.Copy folds read and write failures into one return value; comparing the
// copy error against the recorded one is what lets the caller say which side
// of the transfer actually failed.
type readErrorRecorder struct {
	r   io.Reader
	err error
}

func (t *readErrorRecorder) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.err = err
	}
	return n, err
}

// waitBeforeRetry sleeps for the backoff interval of the given attempt
// (1-based), returning the context error if the caller gives up while waiting.
func waitBeforeRetry(ctx context.Context, attempt int) error {
	delay := DownloadRetryBaseDelay << (attempt - 1)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

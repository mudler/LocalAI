package gallery

import "errors"

// UpgradeFailureCode names the step at which a backend upgrade failed. The
// strings are stable: clients map them to words (the add-on upgrades use the
// same shape with their own codes) and the upgrade run records one as the
// error key of a failed step. Add a code; never rename one.
type UpgradeFailureCode string

const (
	// UpgradeFailureDownload: the new build could not be fetched or staged.
	UpgradeFailureDownload UpgradeFailureCode = "download"
	// UpgradeFailureVerify: the new build was refused by an integrity or
	// signature check, or its content is not a runnable backend.
	UpgradeFailureVerify UpgradeFailureCode = "verify"
	// UpgradeFailureSwap: the new build was ready but could not be moved
	// into place. The installed build is restored.
	UpgradeFailureSwap UpgradeFailureCode = "swap"
	// UpgradeFailureStart: the new build was swapped in but did not start.
	// Reserved for the rollback step: nothing produces it yet.
	UpgradeFailureStart UpgradeFailureCode = "start"
	// UpgradeFailureNotInstalled: the backend is not installed.
	UpgradeFailureNotInstalled UpgradeFailureCode = "not_installed"
	// UpgradeFailureNoUpdate: the backend has nothing to upgrade to.
	UpgradeFailureNoUpdate UpgradeFailureCode = "no_update"
	// UpgradeFailureStale: the build that the caller asked for is no longer
	// the one that is available.
	UpgradeFailureStale UpgradeFailureCode = "stale"
	// UpgradeFailureUnknown: the upgrade failed for a reason that has no code
	// of its own.
	UpgradeFailureUnknown UpgradeFailureCode = "unknown"
)

// UpgradeError is a failed backend upgrade with the code of its step. It
// keeps the underlying error, so errors.Is and errors.As still see the
// cause, and its text is the text of the cause.
type UpgradeError struct {
	Code UpgradeFailureCode
	Err  error
}

func (e *UpgradeError) Error() string {
	if e.Err == nil {
		return string(e.Code)
	}
	return e.Err.Error()
}

func (e *UpgradeError) Unwrap() error { return e.Err }

// Is makes errors.Is(err, &UpgradeError{Code: c}) true for every error that
// carries the code c, whatever its cause.
func (e *UpgradeError) Is(target error) bool {
	t, ok := target.(*UpgradeError)
	return ok && t.Err == nil && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrUpgradeDownload     = &UpgradeError{Code: UpgradeFailureDownload}
	ErrUpgradeVerify       = &UpgradeError{Code: UpgradeFailureVerify}
	ErrUpgradeSwap         = &UpgradeError{Code: UpgradeFailureSwap}
	ErrUpgradeStart        = &UpgradeError{Code: UpgradeFailureStart}
	ErrUpgradeNotInstalled = &UpgradeError{Code: UpgradeFailureNotInstalled}
	ErrUpgradeNoUpdate     = &UpgradeError{Code: UpgradeFailureNoUpdate}
	ErrUpgradeStale        = &UpgradeError{Code: UpgradeFailureStale}
)

// WithUpgradeCode tags err with a code. An error that already carries a code
// keeps it (the inner step knows best), and nil stays nil.
func WithUpgradeCode(code UpgradeFailureCode, err error) error {
	if err == nil {
		return nil
	}
	var ue *UpgradeError
	if errors.As(err, &ue) {
		return err
	}
	return &UpgradeError{Code: code, Err: err}
}

// UpgradeCodeOf returns the code of err, or "" when err carries none.
func UpgradeCodeOf(err error) UpgradeFailureCode {
	var ue *UpgradeError
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}

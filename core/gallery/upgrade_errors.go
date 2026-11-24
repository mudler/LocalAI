package gallery

import (
	"errors"
	"strings"
)

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
	// UpgradeFailureStart: the new build was swapped in but did not start
	// (its process exited, or it did not answer a health check in time).
	// UpgradeError.RolledBack says whether the previous build is back.
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
	// RolledBack is true when the new build was in place, failed, and the
	// previous build was put back and registered again. The backend then
	// runs the version it ran before the upgrade.
	RolledBack bool
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

// ErrUpgradeRestore marks a failed upgrade whose previous build could not be
// put back. It wins over a cancel: the backend is not at the version it ran
// before, so the operation failed whatever stopped it.
var ErrUpgradeRestore = errors.New("the previous build could not be restored")

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

// UpgradeRolledBack reports whether err is a failed upgrade whose previous
// build was put back. For a cluster upgrade (UpgradeNodesError) it is true
// when every node that failed rolled back.
func UpgradeRolledBack(err error) bool {
	var ne *UpgradeNodesError
	if errors.As(err, &ne) {
		return ne.RolledBack()
	}
	var ue *UpgradeError
	return errors.As(err, &ue) && ue.RolledBack
}

// UpgradeNodePhase is the outcome of a cluster backend upgrade on one node.
// The strings are stable, like UpgradeFailureCode.
type UpgradeNodePhase string

const (
	// UpgradeNodeUpdated: the node runs the new build, and it started.
	UpgradeNodeUpdated UpgradeNodePhase = "updated"
	// UpgradeNodeFailed: the upgrade failed on the node. The node may or
	// may not run the old build: only rolled_back says that it does.
	UpgradeNodeFailed UpgradeNodePhase = "failed"
	// UpgradeNodeRolledBack: the new build did not start on the node and the
	// previous build was put back.
	UpgradeNodeRolledBack UpgradeNodePhase = "rolled_back"
	// UpgradeNodeUnconfirmed: the reply of the node never arrived (a timeout,
	// or a link that broke after the node accepted the request). The upgrade
	// may still run or may have ended either way; a later check of the node
	// decides. Never read it as failed or updated.
	UpgradeNodeUnconfirmed UpgradeNodePhase = "unconfirmed"
	// UpgradeNodeQueued: the node was not reachable for the upgrade now; the
	// intent is kept and retried when the node comes back.
	UpgradeNodeQueued UpgradeNodePhase = "queued"
)

// UpgradeNodeResult is the outcome of a cluster backend upgrade on one node.
type UpgradeNodeResult struct {
	NodeID   string             `json:"id"`
	NodeName string             `json:"name"`
	Phase    UpgradeNodePhase   `json:"phase"`
	Code     UpgradeFailureCode `json:"error_key,omitempty"`
	Message  string             `json:"message,omitempty"`
	// Err is the error of a failed or rolled back node, with its code.
	Err error `json:"-"`
}

// UpgradeNodesError is a cluster backend upgrade that failed on one or more
// nodes. It keeps the outcome of every node it was sent to, in order, so a
// reader can show each one; the nodes that succeeded or whose outcome is
// unknown are listed too.
//
// errors.Is sees the error of every failed node (errors.Is(err,
// ErrUpgradeStart) is true when any node failed to start the new build), and
// UpgradeCodeOf gives the code of the first failed node.
type UpgradeNodesError struct {
	Nodes []UpgradeNodeResult
}

func (e *UpgradeNodesError) Error() string {
	var parts []string
	for _, n := range e.Nodes {
		if n.Err == nil {
			continue
		}
		name := n.NodeName
		if name == "" {
			name = n.NodeID
		}
		parts = append(parts, name+": "+n.Err.Error())
	}
	if len(parts) == 0 {
		return "backend upgrade failed"
	}
	return strings.Join(parts, "; ")
}

// Unwrap returns the errors of the failed nodes, in node order.
func (e *UpgradeNodesError) Unwrap() []error {
	errs := make([]error, 0, len(e.Nodes))
	for _, n := range e.Nodes {
		if n.Err != nil {
			errs = append(errs, n.Err)
		}
	}
	return errs
}

// RolledBack is true when at least one node failed and every node that
// failed put its previous build back.
func (e *UpgradeNodesError) RolledBack() bool {
	failed := 0
	for _, n := range e.Nodes {
		switch n.Phase {
		case UpgradeNodeRolledBack:
			failed++
		case UpgradeNodeFailed:
			return false
		}
	}
	return failed > 0
}

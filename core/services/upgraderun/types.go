// Package upgraderun runs the update of backends, add-ons and gallery models
// as one run that the server owns. A person (or the automatic update) asks for
// a set of items; the server orders them, starts each one through the existing
// operation paths, follows the operation status, and calls an item updated only
// when its operation ended without an error and a fresh check no longer lists
// it. The run is stored in the database, so it survives a reload of the page
// and a restart of the server, and every frontend of a cluster answers for it.
//
// The state machine is a pure reducer (Reduce): events in, state and effects
// out. The runner executes the effects and feeds the results back as events.
//
// Distributed mode: shared state and a single runner per run. The run and its
// items are rows in the database (upgrade_runs, upgrade_run_items); any
// frontend reads them. One frontend runs a run, holding an advisory lock of
// that run for as long as it runs it; when that frontend dies the lock is
// freed and another frontend takes the run over.
package upgraderun

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
)

// Kind is the kind of an item. The key of an item is "<kind>:<name>", the id
// the Updates page already uses.
type Kind string

const (
	// KindGallery is a backend of a backend gallery.
	KindGallery Kind = "gallery"
	// KindLibrary is a backend of a backend library; its name is
	// "<source id>/<backend name>".
	KindLibrary Kind = "library"
	// KindAddon is an installed add-on.
	KindAddon Kind = "addon"
	// KindModel is a model installed from a model gallery.
	KindModel Kind = "model"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindGallery, KindLibrary, KindAddon, KindModel:
		return true
	}
	return false
}

// IsBackend reports whether items of this kind are backends.
func (k Kind) IsBackend() bool { return k == KindGallery || k == KindLibrary }

// Key builds the key of an item.
func Key(kind Kind, name string) string { return string(kind) + ":" + name }

// ParseKey splits a key into its kind and name.
func ParseKey(key string) (Kind, string, bool) {
	k, name, ok := strings.Cut(key, ":")
	if !ok || name == "" || !Kind(k).Valid() {
		return "", "", false
	}
	return Kind(k), name, true
}

// Source says who asked for a run.
type Source string

const (
	SourceManual Source = "manual"
	SourceAuto   Source = "auto"
)

// RunState is the state of a run.
type RunState string

const (
	RunRunning   RunState = "running"
	RunFinished  RunState = "finished"
	RunCancelled RunState = "cancelled"
)

// Phase is the phase of an item.
type Phase string

const (
	PhaseQueued     Phase = "queued"
	PhaseRunning    Phase = "running"
	PhaseUpdated    Phase = "updated"
	PhaseFailed     Phase = "failed"
	PhaseRolledBack Phase = "rolled_back"
	PhaseCancelled  Phase = "cancelled"
	PhaseSkipped    Phase = "skipped"
	// PhaseUnconfirmed: the operation ended (or its outcome was lost in a
	// restart) and no check could say yet whether the item moved. A fresh
	// check decides.
	PhaseUnconfirmed Phase = "unconfirmed"
)

// Terminal reports whether an item in this phase is done.
func (p Phase) Terminal() bool {
	switch p {
	case PhaseUpdated, PhaseFailed, PhaseRolledBack, PhaseCancelled, PhaseSkipped:
		return true
	}
	return false
}

// Error keys of an item. The keys of a failed backend step are the failure
// codes of the backend upgrade (gallery.UpgradeFailureCode), so a code the
// upgrade writes into the status is the key of the item as it is. The add-on
// failure codes of the add-on owner (upgrade_failed, interrupted,
// recovery_required, approval_stale, ...) pass through unchanged.
const (
	ErrKeyDownload   = string(gallery.UpgradeFailureDownload)
	ErrKeyVerify     = string(gallery.UpgradeFailureVerify)
	ErrKeySwap       = string(gallery.UpgradeFailureSwap)
	ErrKeyStart      = string(gallery.UpgradeFailureStart)
	ErrKeyHeld       = "held"
	ErrKeyEditedKeys = "edited_keys"
	// ErrKeyStale: the operation ended but the item is still listed, or the
	// version it would move to changed since the request.
	ErrKeyStale   = string(gallery.UpgradeFailureStale)
	ErrKeyUnknown = string(gallery.UpgradeFailureUnknown)
)

// Error keys of an add-on item. The host gives the first four; the others are
// codes of the add-on owner that the run reads (see retryableKeys).
const (
	// ErrKeyNotHumanSurface: the run was not requested from the
	// administrator's own browser session.
	ErrKeyNotHumanSurface = "not_human_surface"
	// ErrKeyForbidden: the run has no add-on identity, or the owner refused
	// the identity.
	ErrKeyForbidden = "forbidden"
	// ErrKeyUnavailable: the server or the add-on owner did not answer.
	ErrKeyUnavailable = "unavailable"
	// ErrKeyOperationConflict: the owner did not accept the upgrade as asked.
	ErrKeyOperationConflict = "operation_conflict"

	ErrKeyUpgradeFailed    = "upgrade_failed"
	ErrKeyInterrupted      = "interrupted"
	ErrKeyStaleInstalled   = "stale_installed"
	ErrKeyCandidateChanged = "candidate_changed"
	ErrKeyCheckFailed      = "check_failed"
)

// Reasons why a queued item waits.
const (
	WaitAfterBackends   = "after_backends"
	WaitAfterModels     = "after_models"
	WaitBackendLimit    = "backend_limit"
	WaitSameBackend     = "same_backend"
	WaitOneModelAtATime = "one_model_at_a_time"
	WaitOneAddonAtATime = "one_addon_at_a_time"
)

// NodeState is one node of a cluster backend item.
type NodeState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Progress int    `json:"progress"`
	Message  string `json:"message,omitempty"`
	// ErrorKey is the failure code of the upgrade on this node, when its
	// phase is error or rolled_back.
	ErrorKey string `json:"error_key,omitempty"`
}

// Item is one thing a run updates.
type Item struct {
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// Title is the name a person knows the item by (an add-on's title);
	// empty when it is Name.
	Title string `json:"title,omitempty"`
	// From and To are the versions for display: the version, or the digest
	// when there is no version.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// ExpectedTo is the version or digest the item had to move to when the
	// run was requested (for an add-on the digest of its candidate). A later
	// change of the target fails the item as stale instead of installing
	// something nobody looked at.
	ExpectedTo  string      `json:"expected_to,omitempty"`
	Phase       Phase       `json:"phase"`
	Progress    int         `json:"progress"`
	Message     string      `json:"message,omitempty"`
	ErrorKey    string      `json:"error_key,omitempty"`
	Error       string      `json:"error,omitempty"`
	Retryable   bool        `json:"retryable"`
	Cancellable bool        `json:"cancellable"`
	JobID       string      `json:"job_id,omitempty"`
	Nodes       []NodeState `json:"nodes,omitempty"`
	// QueuedReason says why a queued item waits.
	QueuedReason string `json:"queued_reason,omitempty"`

	// Verifying is set between the end of the operation and the answer of
	// the fresh check. The page reads it: an unconfirmed item that is
	// verifying is still in progress (Busy), one that is not is settled.
	Verifying bool `json:"verifying,omitempty"`
	// CancelWanted is set when a cancel of the item was asked for while its
	// operation could not stop yet; the cancel is sent once it can.
	CancelWanted bool `json:"-"`
	// CancelSent is set once the cancel of the operation was asked for, so a
	// repeated cancel does not ask again.
	CancelSent bool `json:"-"`
	// Extra carries what a job path needs beyond the target, such as the
	// installed digest of an add-on. It is stored with the item.
	Extra map[string]string `json:"-"`
}

// DisplayName is the name a person knows the item by.
func (it *Item) DisplayName() string {
	if it.Title != "" {
		return it.Title
	}
	return it.Name
}

// Busy reports whether the item holds a slot: it was started and has not
// reached its outcome. An unconfirmed item that waits for its check is busy;
// one that no check could answer is settled.
func (it *Item) Busy() bool {
	return it.Phase == PhaseRunning || (it.Phase == PhaseUnconfirmed && it.Verifying)
}

// Run is one run of updates.
type Run struct {
	ID         string     `json:"id"`
	Source     Source     `json:"source"`
	Actor      string     `json:"actor,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	State      RunState   `json:"state"`
	Items      []Item     `json:"items"`
	// CancelRequested is set when the whole run was cancelled; nothing new
	// starts, and the run ends cancelled once the running items settle.
	CancelRequested bool `json:"cancel_requested,omitempty"`
	// Dismissed hides a finished run from the default list.
	Dismissed bool `json:"dismissed,omitempty"`
}

// Active reports whether the run still runs.
func (r Run) Active() bool { return r.State == RunRunning }

// Item returns the item with key, or nil.
func (r Run) Item(key string) *Item {
	for i := range r.Items {
		if r.Items[i].Key == key {
			return &r.Items[i]
		}
	}
	return nil
}

// Clone returns a deep copy, so a reducer step never changes its input.
func (r Run) Clone() Run {
	out := r
	if r.FinishedAt != nil {
		t := *r.FinishedAt
		out.FinishedAt = &t
	}
	out.Items = make([]Item, len(r.Items))
	for i, it := range r.Items {
		c := it
		if it.Nodes != nil {
			c.Nodes = append([]NodeState(nil), it.Nodes...)
		}
		if it.Extra != nil {
			c.Extra = make(map[string]string, len(it.Extra))
			for k, v := range it.Extra {
				c.Extra[k] = v
			}
		}
		out.Items[i] = c
	}
	return out
}

// Error codes of the service API. The HTTP layer maps them to status codes:
// invalid to 400, not_found to 404, the others to 409.
const (
	CodeInvalid         = "invalid"
	CodeNotFound        = "not_found"
	CodeNoUpdate        = "no_update"
	CodeStale           = "stale"
	CodeRunActive       = "run_active"
	CodeNotCancellable  = "not_cancellable"
	CodeRunFinished     = "run_finished"
	CodeRunNotFinished  = "run_not_finished"
	CodeNothingToRetry  = "nothing_to_retry"
	CodeSourceUnchecked = "source_unchecked"
	// CodeHeldBack refuses an automatic run whose every item is a release
	// that already failed to start here (see HeldBack). A manual run never
	// gets it.
	CodeHeldBack = "held_back"
)

// Error is a typed refusal of the service API.
type Error struct {
	Code string
	// Key is the item the refusal is about, when there is one.
	Key     string
	Message string
}

func (e *Error) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Key)
	}
	return e.Code + ": " + e.Message
}

// Is matches an *Error with the same code, so errors.Is(err,
// &Error{Code: CodeStale}) works.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// CodeOf returns the code of a typed refusal, or "".
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func refuse(code, key, format string, args ...any) *Error {
	return &Error{Code: code, Key: key, Message: fmt.Sprintf(format, args...)}
}

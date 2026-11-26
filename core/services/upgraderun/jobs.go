package upgraderun

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

// Jobs reads and cancels the operations the items start. The gallery service
// implements it: every job path (backend, library, model, add-on) writes its
// status there, which is also what /api/operations shows, so a run never
// keeps a second copy of the progress of an operation.
type Jobs interface {
	GetStatus(id string) *galleryop.OpStatus
	CancelOperation(id string) error
}

// Starter starts the operation of an item and returns its job id, the id of
// the operation in Jobs.
type Starter interface {
	Start(ctx context.Context, actor string, it Item) (jobID string, err error)
}

// StartError is a refusal of a starter. Skip turns the item skipped; a
// refusal that is not a StartError fails the item as retryable with the
// unknown key.
type StartError struct {
	ErrorKey  string
	Skip      bool
	Retryable bool
	Err       error
}

func (e *StartError) Error() string {
	if e.Err == nil {
		return e.ErrorKey
	}
	return e.Err.Error()
}

func (e *StartError) Unwrap() error { return e.Err }

// startFailure turns the error of a starter into the event of the reducer.
func startFailure(key string, err error) ItemStartFailed {
	var se *StartError
	if errors.As(err, &se) {
		return ItemStartFailed{Key: key, ErrorKey: orUnknown(se.ErrorKey), Error: se.Error(), Skip: se.Skip, Retryable: se.Retryable}
	}
	return ItemStartFailed{Key: key, ErrorKey: ErrKeyUnknown, Error: err.Error(), Retryable: true}
}

// jobView is what the runner reads from the status of an operation.
type jobView struct {
	known       bool
	ended       bool
	outcome     JobOutcome
	progress    int
	message     string
	phase       string
	cancellable bool
	nodes       []NodeState
	errorKey    string
	err         string
	rolledBack  bool
}

// viewOf reads an operation status. lastPhase is the last phase seen while
// it ran, since the final status of a failure does not always carry it.
func viewOf(kind Kind, st *galleryop.OpStatus, lastPhase string) jobView {
	if st == nil {
		return jobView{}
	}
	v := jobView{
		known: true, progress: int(st.Progress), message: safeText(st.Message), phase: st.Phase,
		cancellable: st.Cancellable && !st.Processed,
	}
	for _, n := range st.Nodes {
		v.nodes = append(v.nodes, NodeState{ID: n.NodeID, Name: n.NodeName, Phase: n.Status, Progress: int(n.Percentage), Message: safeText(n.Error), ErrorKey: n.ErrorKey})
	}
	if !st.Processed {
		return v
	}
	v.ended, v.cancellable = true, false
	phase := st.Phase
	if phase == "" {
		phase = lastPhase
	}
	switch {
	case st.Cancelled:
		v.outcome = JobCancelled
	case st.Error != nil:
		v.outcome, v.err = JobError, st.Error.Error()
		v.errorKey, v.rolledBack = failureOf(kind, st, phase, v.err)
	case st.Message != galleryop.MessageCompleted && strings.HasPrefix(st.Message, "error"):
		v.outcome, v.err = JobError, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(st.Message, "error"), ":"))
		v.errorKey, v.rolledBack = failureOf(kind, st, phase, v.err)
	case st.Unconfirmed:
		// The reply of a worker never came: the operation ended without an
		// outcome, which is what a lost status means to the run.
		v.outcome = JobLost
	default:
		v.outcome = JobOK
	}
	return v
}

// failureOf gives the error key of a failed operation and whether it put the
// previous version back. The typed code and the rollback flag that the
// backend upgrade writes into the status win; the phase it failed in is the
// fallback for the operations that write neither.
func failureOf(kind Kind, st *galleryop.OpStatus, phase, msg string) (string, bool) {
	if st.ErrorKey != "" {
		return st.ErrorKey, st.RolledBack
	}
	key, rolledBack := classify(kind, phase, msg)
	return key, rolledBack || st.RolledBack
}

// addonCode finds the failure code the add-on owner puts at the end of the
// error of a failed upgrade, "the upgrade failed (upgrade_failed)".
var addonCode = regexp.MustCompile(`\(([a-z_]+)\)\s*$`)

// classify gives the error key of a failure from the phase it failed in.
func classify(kind Kind, phase, msg string) (string, bool) {
	if kind == KindAddon {
		if m := addonCode.FindStringSubmatch(msg); m != nil {
			return m[1], false
		}
	}
	p := strings.ToLower(phase)
	switch {
	case strings.Contains(p, "rolling") || strings.Contains(p, "rolled"):
		return ErrKeyStart, true
	case p == "verifying" || strings.Contains(p, "verif"):
		return ErrKeyVerify, false
	case p == "fetching" || p == "size" || p == "staging" || strings.Contains(p, "download"):
		return ErrKeyDownload, false
	case p == "unloading" || p == "committing" || p == "writing" || strings.Contains(p, "swap"):
		return ErrKeySwap, false
	case p == "loading" || strings.Contains(p, "start"):
		return ErrKeyStart, false
	}
	return ErrKeyUnknown, false
}

// BackendEnqueuer is the part of the gallery service the backend starter uses.
type BackendEnqueuer interface {
	EnqueueBackendOp(op galleryop.ManagementOp[gallery.GalleryBackend, any])
	StoreCancellationActions(id string, cancelFunc, pauseFunc context.CancelFunc)
}

// OpCacheSetter registers an operation so /api/operations shows it on the
// row of the backend (galleryop.OpCache implements it).
type OpCacheSetter interface {
	SetBackend(key string, value string)
}

// GalleryBackendStarter upgrades a gallery backend through the backend
// operation queue, as POST /api/backends/upgrade/{name} does.
type GalleryBackendStarter struct {
	Gallery   BackendEnqueuer
	OpCache   OpCacheSetter
	Galleries func() []config.Gallery
}

func (s GalleryBackendStarter) Start(_ context.Context, _ string, it Item) (string, error) {
	uid := uuid.NewString()
	if s.OpCache != nil {
		s.OpCache.SetBackend(it.Name, uid)
	}
	var galleries []config.Gallery
	if s.Galleries != nil {
		galleries = s.Galleries()
	}
	ctx, cancel, pause := galleryop.NewUserCancellableContext(context.Background())
	s.Gallery.StoreCancellationActions(uid, cancel, pause)
	s.Gallery.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
		ID: uid, GalleryElementName: it.Name, Galleries: galleries, Upgrade: true, InRun: true,
		Context: ctx, CancelFunc: cancel, PauseFunc: pause,
	})
	return uid, nil
}

// ModelEnqueuer is the part of the gallery service the model starter uses.
type ModelEnqueuer interface {
	EnqueueModelUpgrade(name string, opts galleryop.ModelUpgradeOptions) (string, error)
}

// ModelStarter upgrades a gallery model through the model operation queue,
// after the checks of POST /api/models/upgrade/{name}.
type ModelStarter struct {
	Gallery ModelEnqueuer
	// Validate runs the checks of an upgrade request (ModelValidator wraps
	// modelupdates.CheckRequest). Nil skips them; the upgrade itself checks
	// again.
	Validate func(name string, retry bool) error
}

// ModelValidator returns the checks of the model upgrade route, with no
// choices for edited keys: a run never makes them.
func ModelValidator(up func() *modelupdates.Upgrader, svc func() *modelupdates.Service) func(string, bool) error {
	return func(name string, retry bool) error {
		var u *modelupdates.Upgrader
		if up != nil {
			u = up()
		}
		if u == nil {
			return errors.New("model upgrades are not available: no model gallery is configured")
		}
		var s *modelupdates.Service
		if svc != nil {
			s = svc()
		}
		return modelupdates.CheckRequest(u, s, name, retry, nil)
	}
}

func (s ModelStarter) Start(_ context.Context, _ string, it Item) (string, error) {
	retry := it.Extra[ExtraRetry] == "true"
	if s.Validate != nil {
		if err := s.Validate(it.Name, retry); err != nil {
			var needs *modelupdates.ErrNeedsChoices
			switch {
			case errors.Is(err, modelupdates.ErrHeld):
				return "", &StartError{ErrorKey: ErrKeyHeld, Skip: true, Err: err}
			case errors.As(err, &needs):
				return "", &StartError{ErrorKey: ErrKeyEditedKeys, Skip: true, Err: err}
			case errors.Is(err, modelupdates.ErrUpToDate):
				return "", &StartError{ErrorKey: ErrKeyStale, Err: err}
			case errors.Is(err, modelupdates.ErrBusy):
				return "", &StartError{ErrorKey: ErrKeyUnknown, Retryable: true, Err: err}
			}
			return "", &StartError{ErrorKey: ErrKeyUnknown, Err: err}
		}
	}
	return s.Gallery.EnqueueModelUpgrade(it.Name, galleryop.ModelUpgradeOptions{Retry: retry, InRun: true})
}

// LibraryJobs starts the install jobs of library backends under the guard
// every library install of this node shares (backendlib.JobRunner).
type LibraryJobs interface {
	Start(ctx context.Context, status backendlib.JobStatus, job backendlib.Job) (string, error)
}

// LibraryStarter installs the newer build of a library backend through the
// same job as the install route POST /backends/library/install, with the same
// status, guard and timeout. Status should implement backendlib.JobCancels
// (the gallery service does), so the item can be cancelled.
type LibraryStarter struct {
	Jobs   LibraryJobs
	Status backendlib.JobStatus
}

func (s LibraryStarter) Start(ctx context.Context, _ string, it Item) (string, error) {
	sourceID, backend, version := it.Extra[ExtraSourceID], it.Extra[ExtraBackend], it.Extra[ExtraVersion]
	if sourceID == "" || backend == "" {
		sourceID, backend, _ = strings.Cut(it.Name, "/")
	}
	if version == "" {
		version = it.ExpectedTo
	}
	if s.Jobs == nil || s.Status == nil || sourceID == "" || backend == "" || version == "" {
		return "", &StartError{ErrorKey: ErrKeyUnknown, Err: errors.New("the library install is not available")}
	}
	id, err := s.Jobs.Start(ctx, s.Status, backendlib.Job{SourceID: sourceID, Backend: backend, Version: version, Upgrade: true})
	if errors.Is(err, backendlib.ErrInstalling) {
		return "", &StartError{ErrorKey: ErrKeyUnknown, Retryable: true, Err: err}
	}
	if err != nil {
		return "", &StartError{ErrorKey: ErrKeyUnknown, Err: err}
	}
	return id, nil
}

// AddonUpgrader asks the add-on owner for the upgrade of an add-on from the
// installed digest the run saw to the candidate digest, and returns the job
// of /api/operations that runs it. The add-on routes implement it with the
// gateway (the upgrade queue of POST /api/addons/{id}/upgrade).
//
// operationID is the operation the owner already recorded for the upgrade
// (the upgrade route records it before it starts a run of one item); empty
// records a new one.
type AddonUpgrader interface {
	StartAddonUpgrade(ctx context.Context, actor, addonID, expectedDigest, candidateDigest, operationID string) (jobID string, err error)
}

// Extra keys of an add-on item. Without them From and ExpectedTo are used.
const (
	ExtraExpectedDigest  = "expected_digest"
	ExtraCandidateDigest = "candidate_digest"
	// ExtraOperationID is the operation the upgrade route recorded on the
	// owner. It is stored with the item, so the frontend that takes the run
	// over runs that record instead of asking for a second one.
	ExtraOperationID = "operation_id"
)

// AddonStarter upgrades an add-on through an AddonUpgrader.
type AddonStarter struct {
	Upgrader AddonUpgrader
}

func (s AddonStarter) Start(ctx context.Context, actor string, it Item) (string, error) {
	if s.Upgrader == nil {
		return "", &StartError{ErrorKey: ErrKeyUnknown, Err: errors.New("add-on upgrades are not available")}
	}
	expected, candidate := it.Extra[ExtraExpectedDigest], it.Extra[ExtraCandidateDigest]
	if expected == "" {
		expected = it.From
	}
	if candidate == "" {
		candidate = it.ExpectedTo
	}
	return s.Upgrader.StartAddonUpgrade(ctx, actor, it.Name, expected, candidate, it.Extra[ExtraOperationID])
}

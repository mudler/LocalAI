// SPDX-License-Identifier: MIT
package backendlib

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
)

// ErrInstalling refuses an install of a backend name that is being installed
// on this node: two installs would write the same directory.
var ErrInstalling = errors.New("this backend is already being installed")

// JobTimeout bounds one install job. The job runs after the request that
// started it ended.
const JobTimeout = time.Hour

// InstallFunc installs one library entry (Installer.Install).
type InstallFunc func(ctx context.Context, sourceID, backend, version string, progress Progress) error

// JobStatus records the status of a job under its ID (the gallery service,
// which GET /backends/jobs/{id} and /api/operations read).
type JobStatus interface {
	UpdateStatus(id string, op *galleryop.OpStatus)
}

// JobCancels registers the cancel of a job, so /api/operations can stop it,
// and drops it when the job ends. A JobStatus that implements it makes the
// jobs cancellable.
type JobCancels interface {
	StoreCancellation(id string, cancel context.CancelFunc)
	RemoveCancellation(id string)
}

// Job names one install.
type Job struct {
	SourceID string
	Backend  string
	Version  string
	// Upgrade marks the job as the upgrade of an installed backend.
	Upgrade bool
}

// JobRunner runs library installs as jobs with a status. Every path that
// installs a library backend on this node (POST /backends/library/install
// and the library items of upgrade runs) goes through one runner, so one
// install of a backend name runs at a time.
//
// Distributed mode: per-instance state. Each node installs into its own
// backends directory, so the guard of one process is the guard of that
// directory.
type JobRunner struct {
	Install InstallFunc

	mu      sync.Mutex
	running map[string]bool
}

func (r *JobRunner) reserve(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[name] {
		return false
	}
	if r.running == nil {
		r.running = map[string]bool{}
	}
	r.running[name] = true
	return true
}

func (r *JobRunner) release(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, name)
}

// Start starts the install of job and returns the ID of its status at once.
// The install keeps the values of ctx (the actor the owner checks) and drops
// its cancellation: it runs after the request ended, until it is done, it is
// cancelled or JobTimeout runs out.
func (r *JobRunner) Start(ctx context.Context, status JobStatus, job Job) (string, error) {
	if r == nil || r.Install == nil || status == nil {
		return "", errors.New("library installs are not available")
	}
	if job.SourceID == "" || job.Backend == "" || job.Version == "" {
		return "", ErrInvalid
	}
	if !r.reserve(job.Backend) {
		return "", ErrInstalling
	}
	id := uuid.NewString()
	jobCtx, cancel, _ := galleryop.NewUserCancellableContext(context.WithoutCancel(ctx))
	jobCtx, stop := context.WithTimeout(jobCtx, JobTimeout)
	cancels, cancellable := status.(JobCancels)
	if cancellable {
		cancels.StoreCancellation(id, cancel)
	}
	post := func(op *galleryop.OpStatus) {
		op.GalleryElementName = job.Backend
		op.Upgrade = job.Upgrade
		status.UpdateStatus(id, op)
	}
	post(&galleryop.OpStatus{Phase: StepResolving, Message: StepMessage(StepResolving), Cancellable: cancellable})
	go func() {
		defer r.release(job.Backend)
		defer stop()
		defer cancel()
		if cancellable {
			defer cancels.RemoveCancellation(id)
		}
		var mu sync.Mutex
		phase := StepResolving
		current := func() string {
			mu.Lock()
			defer mu.Unlock()
			return phase
		}
		err := r.Install(jobCtx, job.SourceID, job.Backend, job.Version, Progress{
			Step: func(step string) {
				mu.Lock()
				phase = step
				mu.Unlock()
				// The metadata write is the switch: a cancel then would leave
				// a half-written backend.
				post(&galleryop.OpStatus{Phase: step, Message: StepMessage(step), Cancellable: cancellable && step != StepWriting})
			},
			Bytes: func(file, done, total string, pct float64) {
				// A late tick must not move the job back from a step it has
				// left.
				if current() != StepFetching {
					return
				}
				post(&galleryop.OpStatus{Phase: StepFetching, Message: StepMessage(StepFetching), FileName: file, Progress: pct,
					DownloadedFileSize: done, TotalFileSize: total, Cancellable: cancellable})
			},
		})
		switch {
		case err == nil:
			post(&galleryop.OpStatus{Phase: "done", Message: galleryop.MessageCompleted, Processed: true, Progress: 100})
		case jobCtx.Err() != nil && !errors.Is(jobCtx.Err(), context.DeadlineExceeded):
			post(&galleryop.OpStatus{Phase: current(), Message: "cancelled", Processed: true, Cancelled: true})
		default:
			xlog.Warn("backend library install failed", "source", job.SourceID, "backend", job.Backend, "error", err)
			// The phase stays at the step that failed, so the page can mark
			// it.
			post(&galleryop.OpStatus{Phase: current(), Error: err, Processed: true, Message: "error: " + Reason(err)})
		}
	}()
	return id, nil
}

// Run installs job and waits for the end, under the same guard as Start, for
// a caller that reports no progress (the Assistant's install tool).
func (r *JobRunner) Run(ctx context.Context, job Job) error {
	if r == nil || r.Install == nil {
		return errors.New("library installs are not available")
	}
	if !r.reserve(job.Backend) {
		return ErrInstalling
	}
	defer r.release(job.Backend)
	return r.Install(ctx, job.SourceID, job.Backend, job.Version, Progress{})
}

// StepMessage is the text of a step in the status of a job.
func StepMessage(step string) string {
	switch step {
	case StepResolving:
		return "Resolving the library entry"
	case StepVerifying:
		return "Verifying the signature"
	case StepSize:
		return "Checking the image size"
	case StepFetching:
		return "Fetching the image"
	case StepWriting:
		return "Writing the metadata"
	}
	return step
}

// Reason words the failure of an install for the administrator.
func Reason(err error) string {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		return "the library has no such entry for this host"
	case errors.Is(err, ErrCollision):
		return err.Error()
	case errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrRefresh):
		return "the library is not available: " + err.Error()
	case errors.Is(err, downloader.ErrImageTooLarge):
		return "the image is over the size limit: " + err.Error()
	}
	return err.Error()
}

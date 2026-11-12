package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/pkg/utils"
)

// RegisterAddonClientJobs registers the job operations of paired clients
// (client.jobs.*) over the job host of the chats service. It registers nothing
// when this process has no host operation channel. The service is looked up at
// each call: it is built after the broker.
func (a *Application) RegisterAddonClientJobs() error {
	if a.addonHostOps == nil {
		return nil
	}
	return hostops.RegisterClientJobs(a.addonHostOps.Broker, addonClientJobs{a: a})
}

// addonClientJobs adapts the job host to the client operations.
type addonClientJobs struct{ a *Application }

// host finds the job host and checks that the client is still paired. Every
// operation passes here first.
func (d addonClientJobs) host(c hostops.ClientCall) (*studio.Jobs, error) {
	if !auth.ClientKeyActive(d.a.authDB, c.ClientID, c.OwnerID) {
		return nil, fmt.Errorf("%w: the client is not paired", hostops.ErrForbidden)
	}
	svc := d.a.StudioService()
	if svc == nil || svc.Jobs() == nil {
		return nil, fmt.Errorf("%w: agent jobs are off", hostops.ErrNotFound)
	}
	return svc.Jobs(), nil
}

func clientJobError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, jobs.ErrNotFound):
		return fmt.Errorf("%w: job or task", hostops.ErrNotFound)
	case errors.Is(err, studio.ErrTooManyClientJobs), errors.Is(err, studio.ErrTooManyJobs), errors.Is(err, studio.ErrJobEnded):
		return fmt.Errorf("%w: %w", hostops.ErrConflict, err)
	case errors.Is(err, studio.ErrTaskDisabled), errors.Is(err, studio.ErrBadJobInput), errors.Is(err, jobs.ErrInvalid):
		return fmt.Errorf("%w: the task refused the parameters", hostops.ErrBadRequest)
	}
	return err
}

func clientJobView(j studio.JobInfo) hostops.ClientJob {
	out := hostops.ClientJob{ID: j.ID, Task: j.TaskName, Status: string(j.Status), Error: j.Error}
	out.Result = j.Result
	if len(out.Result) > hostops.MaxClientJobResult {
		// Cut on a character boundary, so the JSON never holds half a rune.
		out.Result, out.Truncated = utils.TruncateUTF8(out.Result, hostops.MaxClientJobResult), true
	}
	if !j.Created.IsZero() {
		out.CreatedAt = j.Created.UTC().Format(time.RFC3339)
	}
	if !j.CompletedAt.IsZero() {
		out.CompletedAt = j.CompletedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (d addonClientJobs) Start(ctx context.Context, c hostops.ClientCall, task string, params map[string]string) (hostops.ClientJob, error) {
	h, err := d.host(c)
	if err != nil {
		return hostops.ClientJob{}, err
	}
	t, err := h.FindTask(ctx, c.OwnerID, task)
	if err != nil {
		return hostops.ClientJob{}, clientJobError(err)
	}
	j, err := h.ExecuteForClient(ctx, c.OwnerID, c.ClientID, t.ID, studio.RunRequest{Params: params})
	if err != nil {
		return hostops.ClientJob{}, clientJobError(err)
	}
	return clientJobView(j), nil
}

func (d addonClientJobs) Get(ctx context.Context, c hostops.ClientCall, id string) (hostops.ClientJob, error) {
	h, err := d.host(c)
	if err != nil {
		return hostops.ClientJob{}, err
	}
	j, err := h.GetClientJob(ctx, c.OwnerID, c.ClientID, id)
	if err != nil {
		return hostops.ClientJob{}, clientJobError(err)
	}
	return clientJobView(j), nil
}

func (d addonClientJobs) Cancel(ctx context.Context, c hostops.ClientCall, id string) (hostops.ClientJob, error) {
	h, err := d.host(c)
	if err != nil {
		return hostops.ClientJob{}, err
	}
	if err := h.CancelClientJob(ctx, c.OwnerID, c.ClientID, id); err != nil {
		return hostops.ClientJob{}, clientJobError(err)
	}
	return d.Get(ctx, c, id)
}

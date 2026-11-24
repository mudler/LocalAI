package inproc

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/upgraderun"
)

// errUpgradeRunsOff is the refusal when the server keeps no upgrade runs.
var errUpgradeRunsOff = errors.New("upgrade runs are not available on this server")

func (c *Client) upgradeRuns() *upgraderun.Service {
	if c.UpgradeRunService == nil {
		return nil
	}
	return c.UpgradeRunService()
}

// runActor is the actor of a run the assistant starts: the acting user's id.
// It carries no add-on identity, so a run started here never upgrades an
// add-on (the tools refuse add-on items before that).
func (c *Client) runActor(ctx context.Context) string {
	if u := c.actingUser(ctx); u != nil {
		return u.ID
	}
	return ""
}

func upgradeRunOf(r upgraderun.Run) localaitools.UpgradeRun {
	out := localaitools.UpgradeRun{
		ID: r.ID, Source: string(r.Source), State: string(r.State), CancelRequested: r.CancelRequested,
		Items: make([]localaitools.UpgradeRunItem, 0, len(r.Items)),
	}
	if !r.CreatedAt.IsZero() {
		out.CreatedAt = r.CreatedAt.UTC().Format(time.RFC3339)
	}
	if r.FinishedAt != nil {
		out.FinishedAt = r.FinishedAt.UTC().Format(time.RFC3339)
	}
	for _, it := range r.Items {
		out.Items = append(out.Items, localaitools.UpgradeRunItem{
			Key: it.Key, Kind: string(it.Kind), Name: it.Name, Title: it.Title, From: it.From, To: it.To,
			Phase: string(it.Phase), Progress: it.Progress, Message: it.Message,
			ErrorKey: it.ErrorKey, Error: it.Error, JobID: it.JobID, QueuedReason: it.QueuedReason,
			Cancellable: it.Cancellable, Retryable: it.Retryable,
		})
	}
	return out
}

func (c *Client) StartUpgradeRun(ctx context.Context, req localaitools.UpgradeRunStartRequest) (*localaitools.UpgradeRun, error) {
	if err := localaitools.CheckUpgradeRunItems(req.Items); err != nil {
		return nil, err
	}
	svc := c.upgradeRuns()
	if svc == nil {
		return nil, errUpgradeRunsOff
	}
	sr := upgraderun.StartRequest{Source: upgraderun.SourceManual}
	for _, it := range req.Items {
		sr.Items = append(sr.Items, upgraderun.StartItem{Kind: upgraderun.Kind(it.Kind), Name: it.Name, ExpectedTo: it.ExpectedTo})
	}
	run, err := svc.Start(ctx, c.runActor(ctx), sr)
	if err != nil {
		return nil, err
	}
	out := upgradeRunOf(run)
	return &out, nil
}

func (c *Client) UpgradeRuns(ctx context.Context, id string) ([]localaitools.UpgradeRun, error) {
	svc := c.upgradeRuns()
	if svc == nil {
		return nil, errUpgradeRunsOff
	}
	if id != "" {
		run, err := svc.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return []localaitools.UpgradeRun{upgradeRunOf(run)}, nil
	}
	runs, err := svc.List(ctx, upgraderun.ListOptions{Limit: 5})
	if err != nil {
		return nil, err
	}
	out := make([]localaitools.UpgradeRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, upgradeRunOf(r))
	}
	return out, nil
}

func (c *Client) CancelUpgradeRun(ctx context.Context, id, key string) (*localaitools.UpgradeRun, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	svc := c.upgradeRuns()
	if svc == nil {
		return nil, errUpgradeRunsOff
	}
	var err error
	if key != "" {
		err = svc.CancelItem(ctx, id, key)
	} else {
		err = svc.Cancel(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	run, err := svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := upgradeRunOf(run)
	return &out, nil
}

// refuseIfRunActive refuses an upgrade the assistant would queue directly
// while a run runs, as the REST routes do: no direct upgrade runs beside a
// run.
func (c *Client) refuseIfRunActive() error {
	svc := c.upgradeRuns()
	if svc == nil {
		return nil
	}
	return svc.RefuseIfActive()
}

// backendGuard is the early refusal of a backend upgrade that the REST routes
// use (backendupdates.UpgradeGuard), or nil.
func (c *Client) backendGuard() *backendupdates.UpgradeGuard {
	if c.BackendUpgradeGuard == nil {
		return nil
	}
	return c.BackendUpgradeGuard()
}

// upgradeOneViaRun starts the upgrade of one item as a run of one, the way
// the REST routes do, and returns the job of the item. handled is false when
// the run's check does not list the item (not checked yet, or older than the
// caller's check): the caller then queues the upgrade directly, as before.
func (c *Client) upgradeOneViaRun(ctx context.Context, kind upgraderun.Kind, name string) (jobID string, handled bool, err error) {
	svc := c.upgradeRuns()
	if svc == nil {
		return "", false, nil
	}
	run, item, err := svc.StartOne(ctx, c.runActor(ctx), upgraderun.StartItem{Kind: kind, Name: name}, upgraderun.SourceManual, 0)
	switch upgraderun.CodeOf(err) {
	case "":
	case upgraderun.CodeNoUpdate, upgraderun.CodeSourceUnchecked, upgraderun.CodeStale:
		return "", false, nil
	default:
		return "", true, err
	}
	out := upgraderun.SingleOutcome(run, item)
	// A pending start has no job yet: the error names the run to follow, so
	// the Assistant never says the upgrade started.
	return out.JobID, true, out.Err()
}

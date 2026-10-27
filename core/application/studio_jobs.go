package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

// StudioJobs returns the agent job host of the chats, or nil when it could not
// be built or the chats are off.
func (a *Application) StudioJobs() *studio.Jobs { return a.studioJobs }

// initStudioJobStore opens the store of the agent tasks and jobs, beside the
// chats: GORM on the auth database, JSON files under the data path, or memory.
func (a *Application) initStudioJobStore() {
	var (
		st  jobs.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = jobs.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = jobs.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "jobs"))
	default:
		st, err = jobs.NewJSON("")
	}
	if err != nil {
		xlog.Warn("Agent jobs disabled: failed to initialise their store", "error", err)
		return
	}
	a.studioJobStore = st
}

// startStudioJobs builds the job host. A job runs on the frontend that received
// the request, in the same loop as every chat. The host's sweep (retention and
// bookkeeping of jobs whose watcher died) runs on one frontend at a time in a
// cluster: the one that holds the advisory lock.
func (a *Application) startStudioJobs(svc *studio.Service) {
	if a.studioJobStore == nil {
		return
	}
	cfg := a.applicationConfig
	opts := studio.JobOptions{
		Store:         a.studioJobStore,
		RunLimit:      cfg.MCPCIJobTimeout,
		RetentionDays: cfg.AgentJobRetentionDays,
		AllowPrivate:  func() bool { return cfg.MCPConnectorsAllowPrivate },
	}
	if opts.RunLimit <= 0 {
		opts.RunLimit = config.DefaultMCPCIJobTimeout
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = config.DefaultAgentJobRetentionDays
	}
	if a.Distributed() != nil && a.authDB != nil {
		lock := advisorylock.NewHeldLock(a.authDB, advisorylock.KeyAgentScheduler)
		opts.Lead = func(ctx context.Context) bool {
			if lock.Held() {
				return lock.Verify(ctx)
			}
			ok, err := lock.TryAcquire(ctx)
			if err != nil {
				xlog.Warn("Agent jobs: could not try the leader lock", "error", err)
			}
			return ok
		}
	}
	host, err := studio.NewJobs(svc, opts)
	if err != nil {
		xlog.Warn("Agent jobs disabled: failed to start", "error", err)
		return
	}
	svc.SetJobs(host)
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.studioJobs, a.studioJobsStop = host, cancel
	go host.Run(ctx)
}

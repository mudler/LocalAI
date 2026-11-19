package application

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// Recovery of interrupted model journals in a cluster.
//
// Distributed-state mode: every frontend runs the recovery pass, and the
// per-model advisory lock (modelLockFor) makes it single-runner per model. A
// frontend holds that lock for the whole of an install commit or an upgrade,
// and PostgreSQL frees the lock of a session whose frontend died (about 30 s
// with the keepalive settings of HeldLock). A pass acts on a journal only
// when it takes the lock and the journal was not touched for
// journalQuietFor, so it never undoes a change that a live frontend runs.
const (
	// journalQuietFor is how long a running journal must be untouched before
	// a frontend that took its lock ends it. A running upgrade touches its
	// journal every 30 seconds; three missed beats keep a frontend that only
	// lost its database session from being taken for a dead one.
	journalQuietFor = 90 * time.Second
	// journalRecoveryEvery is how often each frontend looks for abandoned
	// journals.
	journalRecoveryEvery = time.Minute
)

// journalPolicy decides which interrupted journals this process ends at start,
// before the database and the runtime exist. Standalone, every journal is
// from an earlier run of this process. In distributed mode the models
// directory is shared, so only this instance's own journals are ended here,
// and only with an instance ID the operator set: a generated ID changes at
// every start, so no journal can be told apart as this instance's. Every
// other journal waits for the recovery pass, which checks the lock.
func journalPolicy(options *config.ApplicationConfig) func(*gallery.Journal) bool {
	if !options.Distributed.Enabled {
		return func(*gallery.Journal) bool { return true }
	}
	self := options.Distributed.InstanceID
	if self == "" || options.Distributed.InstanceIDGenerated {
		return func(*gallery.Journal) bool { return false }
	}
	return func(j *gallery.Journal) bool { return j.Instance == self }
}

// recoverModelJournals finishes or undoes the installs that a crash
// interrupted, before the model configs are loaded.
func recoverModelJournals(options *config.ApplicationConfig) {
	recovered, err := gallery.RecoverInstallJournals(options.SystemState.Model.ModelsPath, journalPolicy(options))
	if err != nil {
		xlog.Warn("Could not recover some interrupted model installs", "error", err)
	}
	if len(recovered) > 0 {
		xlog.Info("Recovered interrupted model installs", "models", recovered)
	}
	// Nothing is loaded yet and the configs are read after this, so the
	// recovery needs no runtime. It has no Workers either: a rollback of a
	// journal in the committing or loading phase does not tell the workers
	// to drop the new files they took at the commit. That is safe because a
	// worker checks the sha256 of every file the frontend stages for a load
	// (the S3 stager sends the sha256 of the file, the HTTP stager compares
	// the hash of the file), and replaces a copy that differs, so the old
	// files come back at the next load.
	up := &modelupdates.Upgrader{SystemState: options.SystemState, Instance: options.Distributed.InstanceID}
	ended, err := up.Recover(options.Context, journalPolicy(options))
	if err != nil {
		xlog.Warn("Could not end some interrupted model upgrades", "error", err)
	}
	if len(ended) > 0 {
		xlog.Info("Ended interrupted model upgrades", "models", ended)
	}
}

// modelLockFor is the cluster-wide lock of one model: a PostgreSQL advisory
// lock on its own session, held for the length of the change.
func modelLockFor(db *gorm.DB) gallery.ModelLock {
	return func(ctx context.Context, name string) (func(), error) {
		lock := advisorylock.NewHeldLock(db, advisorylock.KeyFromString("model-change:"+name))
		ok, err := lock.TryAcquire(ctx)
		if err != nil {
			lock.Close()
			return nil, err
		}
		if !ok {
			lock.Close()
			return nil, gallery.ErrModelLocked
		}
		return lock.Close, nil
	}
}

// runJournalRecovery ends, every journalRecoveryEvery, the installs and
// upgrades whose frontend stopped, then runs after (when set) for the work
// that follows a pass. It returns when ctx ends.
func runJournalRecovery(ctx context.Context, up *modelupdates.Upgrader, after func(context.Context)) {
	t := time.NewTicker(journalRecoveryEvery)
	defer t.Stop()
	for {
		if names, err := up.RecoverAbandoned(ctx, journalQuietFor); err != nil {
			xlog.Warn("Could not end some interrupted model changes", "error", err)
		} else if len(names) > 0 {
			xlog.Info("Ended interrupted model changes of a stopped frontend", "models", names)
		}
		if after != nil {
			after(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

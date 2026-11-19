package application

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/xlog"
)

// journalStaleAfter is how long a running install or upgrade journal may go
// untouched before another frontend presumes its frontend dead and finishes or
// undoes the change. A running upgrade touches its journal at least once a
// minute.
const journalStaleAfter = 15 * time.Minute

// journalPolicy decides which interrupted journals this process may act on.
// Standalone, every journal is from an earlier run of this process. In
// distributed mode the models directory is shared, so a journal may belong to
// a frontend that is still running: only this instance's own journals and
// stale ones are touched.
func journalPolicy(options *config.ApplicationConfig) func(*gallery.Journal) bool {
	if !options.Distributed.Enabled {
		return func(*gallery.Journal) bool { return true }
	}
	self := options.Distributed.InstanceID
	return func(j *gallery.Journal) bool {
		return (self != "" && j.Instance == self) || j.Stale(time.Now(), journalStaleAfter)
	}
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
}

package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/oci"
)

// sweepInterruptedUpdates cleans what a crash in the middle of a backend
// install or upgrade left on disk: the staged build next to the backend and
// the partial download in the staging directory, and it puts back a previous
// build whose backend directory is missing (gallery.SweepSwapLeftovers). It
// runs once at start, before the gallery service or the add-ons can start an
// operation. Only in local mode: a frontend of a cluster may share its
// directories with a worker that is installing right now.
func sweepInterruptedUpdates(options *config.ApplicationConfig) {
	if options == nil || options.SystemState == nil || options.Distributed.Enabled {
		return
	}
	gallery.SweepSwapLeftovers(options.SystemState)
	oci.SweepStagingDir(options.SystemState.StagingPath)
}

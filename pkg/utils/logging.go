package utils

import (
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
)

// The timers are shared by every download progress callback. Gallery
// operations can run concurrently, so they are stored as atomic Unix
// nanoseconds.
var lastProgress atomic.Int64
var startTime atomic.Int64

func init() {
	ResetDownloadTimers()
}

func ResetDownloadTimers() {
	now := time.Now().UnixNano()
	lastProgress.Store(now)
	startTime.Store(now)
}

func DisplayDownloadFunction(fileName string, current string, total string, percentage float64) {
	currentTime := time.Now()

	last := lastProgress.Load()
	if currentTime.Sub(time.Unix(0, last)) >= 5*time.Second && lastProgress.CompareAndSwap(last, currentTime.UnixNano()) {

		// calculate ETA based on percentage and elapsed time
		var eta time.Duration
		if percentage > 0 {
			elapsed := currentTime.Sub(time.Unix(0, startTime.Load()))
			eta = time.Duration(float64(elapsed)*(100/percentage) - float64(elapsed))
		}

		if total != "" {
			xlog.Info("Downloading", "fileName", fileName, "current", current, "total", total, "percentage", percentage, "eta", eta)
		} else {
			xlog.Info("Downloading", "current", current)
		}
	}
}

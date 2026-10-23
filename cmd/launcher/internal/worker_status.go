// SPDX-License-Identifier: MIT
package launcher

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/workerstatus"
)

var errWorkerUpgrade = errors.New("worker runtime status contract unavailable; upgrade LocalAI before starting a worker")

// capabilityOutput drains but never retains unbounded or diagnostic output.
type capabilityOutput struct {
	data     []byte
	overflow bool
}

func (w *capabilityOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.data)+n > 256 {
		w.overflow = true
		return n, nil
	}
	w.data = append(w.data, p...)
	return n, nil
}
func checkWorkerRuntime(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "worker-status-capability")
	// No saved or inherited credentials, paths, or role selection reach a probe.
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.WaitDelay = time.Second
	var output capabilityOutput
	cmd.Stdout = &output
	if cmd.Run() != nil || output.overflow || strings.TrimSpace(string(output.data)) != workerstatus.Capability {
		return errWorkerUpgrade
	}
	return nil
}

// workerStatusReader is deliberately not connected to StartLocalAI yet. The
// lifecycle controller must establish ownership before polling or displaying it.
type workerStatusReader struct {
	path    string
	tracker workerstatus.Tracker
}

func (r *workerStatusReader) Read(now time.Time) string {
	s, err := workerstatus.Read(r.path, r.tracker.Generation)
	if err != nil {
		return "status_unavailable"
	}
	return r.tracker.Classify(s, now)
}

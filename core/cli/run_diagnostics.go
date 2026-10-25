// SPDX-License-Identifier: MIT
package cli

import "github.com/mudler/LocalAI/pkg/diagnostics"

func (r *RunCMD) diagnosticsOptions() diagnostics.Options {
	return diagnostics.Options{Pprof: r.Pprof, Address: r.PprofAddress, MutexProfileFraction: r.PprofMutexProfileFraction, BlockProfileRate: r.PprofBlockProfileRate, RequestPhaseTiming: r.RequestPhaseTiming}
}

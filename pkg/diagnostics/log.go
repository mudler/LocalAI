// SPDX-License-Identifier: MIT
package diagnostics

import "github.com/mudler/xlog"

// LogEvent emits only the closed diagnostic schema at the normal Info level.
// Operator-selected warn/error filtering suppresses these events.
func LogEvent(event Event) {
	if !validEvent(event) {
		return
	}
	xlog.Info("diagnostic_phase", "id", event.ID, "kind", event.Kind,
		"phase", event.Phase, "state", event.State, "outcome", event.Outcome,
		"elapsed", event.Elapsed, "count", event.Count)
}

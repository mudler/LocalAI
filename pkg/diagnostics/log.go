// SPDX-License-Identifier: MIT
package diagnostics

import "github.com/mudler/xlog"

// LogEvent emits only the closed diagnostic schema at the normal Info level.
// Operator-selected warn/error filtering suppresses these events.
func LogEvent(event Event) {
	if !validEvent(event) {
		return
	}
	if d := event.Dispatch; d != nil {
		xlog.Info("diagnostic_phase", "id", event.ID, "kind", event.Kind,
			"phase", event.Phase, "state", event.State, "outcome", event.Outcome,
			"elapsed", event.Elapsed, "count", event.Count, "offset", d.Offset,
			"span", d.Span, "parent", d.Parent, "attempt", d.Attempt, "connection", d.Connection,
			"dropped", d.Dropped, "emitted", d.Emitted, "sink_elapsed", d.SinkElapsed)
		return
	}
	xlog.Info("diagnostic_phase", "id", event.ID, "kind", event.Kind,
		"phase", event.Phase, "state", event.State, "outcome", event.Outcome,
		"elapsed", event.Elapsed, "count", event.Count)
}

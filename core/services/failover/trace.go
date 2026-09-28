package failover

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/trace"
)

// RecordAttemptTrace shows in the Traces UI why a target was skipped.
func RecordAttemptTrace(enabled bool, chain, target string, err error) {
	if !enabled || err == nil {
		return
	}
	trace.RecordBackendTrace(trace.BackendTrace{
		Timestamp: time.Now(),
		Type:      trace.BackendTraceFailover,
		ModelName: target,
		Summary:   fmt.Sprintf("failover chain %s: %s failed, trying the next target", chain, target),
		Error:     err.Error(),
		Data:      map[string]any{"chain": chain},
	})
}

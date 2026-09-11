// SPDX-License-Identifier: MIT

package backend

import (
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// BackendAdmissionError reports that the process-wide backend execution
// ceiling is full. HTTP callers map it to 503; internal callers receive the
// same typed error instead of silently queueing and growing in-flight state.
type BackendAdmissionError struct {
	Limit      int
	RetryAfter time.Duration
}

func (e *BackendAdmissionError) Error() string {
	return fmt.Sprintf("backend inference capacity reached (max_concurrent=%d); retry after %s", e.Limit, e.RetryAfter)
}

var backendAdmission = struct {
	sync.RWMutex
	limit int
	slots chan struct{}
}{}

// ConfigureGlobalBackendAdmission sets the process-wide ceiling. It is called
// during application construction, before backend work can begin.
func ConfigureGlobalBackendAdmission(limit int) {
	if limit <= 0 {
		limit = config.DefaultMaxConcurrentBackendRequests
	}
	backendAdmission.Lock()
	backendAdmission.limit = limit
	backendAdmission.slots = make(chan struct{}, limit)
	backendAdmission.Unlock()
}

// AcquireGlobalBackendSlot admits one backend operation without queueing.
// Callers must invoke release on every completion path.
func AcquireGlobalBackendSlot() (release func(), err error) {
	backendAdmission.RLock()
	limit, slots := backendAdmission.limit, backendAdmission.slots
	backendAdmission.RUnlock()
	if slots == nil {
		backendAdmission.Lock()
		if backendAdmission.slots == nil {
			backendAdmission.limit = config.DefaultMaxConcurrentBackendRequests
			backendAdmission.slots = make(chan struct{}, backendAdmission.limit)
		}
		limit, slots = backendAdmission.limit, backendAdmission.slots
		backendAdmission.Unlock()
	}
	select {
	case slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots }) }, nil
	default:
		return nil, &BackendAdmissionError{Limit: limit, RetryAfter: time.Second}
	}
}

// GlobalBackendInFlight is the current number of admitted backend operations.
func GlobalBackendInFlight() int {
	backendAdmission.RLock()
	defer backendAdmission.RUnlock()
	return len(backendAdmission.slots)
}

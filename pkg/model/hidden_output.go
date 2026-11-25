package model

import (
	"sync"
	"time"
)

// HiddenOutputLine is what the server logs, and keeps in the backend logs,
// in place of a line of a backend's output while a request that may not keep
// content runs on it.
const HiddenOutputLine = "[redacted: zero-retention request in progress]"

// hiddenOutputGrace keeps the output hidden for a moment after the request
// ends: the lines it made reach the log a little later.
const hiddenOutputGrace = 2 * time.Second

// hiddenOutput counts, per model, the requests whose backend output must not
// be logged or kept.
type hiddenOutput struct {
	mu    sync.Mutex
	count map[string]int
}

// HideBackendOutput hides the output of the backend of modelID until the
// returned release is called, and for a short grace after it. A backend can
// print what it receives; while a zero-retention request runs, the server
// does not relay those lines to its log or keep them in the backend logs.
// The lines of other requests to the same backend are hidden too: privacy
// wins over the completeness of the logs.
func (ml *ModelLoader) HideBackendOutput(modelID string) (release func()) {
	h := &ml.hiddenOutput
	h.mu.Lock()
	if h.count == nil {
		h.count = map[string]int{}
	}
	h.count[modelID]++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			time.AfterFunc(hiddenOutputGrace, func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				if h.count[modelID]--; h.count[modelID] <= 0 {
					delete(h.count, modelID)
				}
			})
		})
	}
}

// outputLine is the line of a backend's output as the server may log it.
func (ml *ModelLoader) outputLine(modelID, line string) string {
	h := &ml.hiddenOutput
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count[modelID] > 0 {
		return HiddenOutputLine
	}
	return line
}

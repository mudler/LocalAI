package concurrency

import (
	"runtime/debug"

	"github.com/mudler/xlog"
)

// SafeGo runs fn in a goroutine with panic recovery.
// If fn panics, the panic is logged with a stack trace.
func SafeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				xlog.Error("goroutine panicked", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

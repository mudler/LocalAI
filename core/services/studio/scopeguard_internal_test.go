package studio

import (
	"runtime/debug"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
)

// A status read without an execution scope is a programming error. Every
// spec fails when one happens, with the stack of the first.
var scopeless atomic.Pointer[string]

var _ = BeforeSuite(func() {
	onScopeless = func() {
		stack := string(debug.Stack())
		scopeless.CompareAndSwap(nil, &stack)
	}
})

var _ = BeforeEach(func() { scopeless.Store(nil) })

var _ = AfterEach(func() {
	if stack := scopeless.Load(); stack != nil {
		Fail("the execution status was read outside an execution scope:\n" + *stack)
	}
})

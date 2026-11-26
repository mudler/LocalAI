package signals

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func registeredHooks() int {
	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	return len(signalHandlers)
}

var _ = Describe("A termination handler", func() {
	It("can be removed again, once, without touching the others", func() {
		before := registeredHooks()
		keep := RegisterGracefulTerminationHandler(func() {})
		drop := RegisterGracefulTerminationHandler(func() {})
		Expect(registeredHooks()).To(Equal(before + 2))
		drop()
		drop()
		Expect(registeredHooks()).To(Equal(before + 1))
		keep()
		Expect(registeredHooks()).To(Equal(before))
	})
})

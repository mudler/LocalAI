package application

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/signals"
)

var _ = Describe("the close of the chats at shutdown", func() {
	It("ends inside the hooks' budget and leaves time to mark the cut turns", func() {
		Expect(studioCloseTimeout).To(BeNumerically(">", 0))
		Expect(signals.DefaultHookTimeout - studioCloseTimeout).To(BeNumerically(">=", 5*time.Second))
	})
})

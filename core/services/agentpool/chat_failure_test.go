package agentpool

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coreTypes "github.com/mudler/LocalAGI/core/types"
)

var _ = Describe("chatFailure", func() {
	It("reports a cancelled run", func() {
		Expect(chatFailure(nil)).To(Equal("agent request failed or was cancelled"))
	})

	It("reports the run error", func() {
		Expect(chatFailure(&coreTypes.JobResult{Error: errors.New("boom")})).To(Equal("boom"))
	})

	It("reports an empty or whitespace-only response as a failure", func() {
		Expect(chatFailure(&coreTypes.JobResult{Response: ""})).To(Equal("agent finished without producing a response"))
		Expect(chatFailure(&coreTypes.JobResult{Response: " \n\t"})).To(Equal("agent finished without producing a response"))
	})

	It("lets a real answer through", func() {
		Expect(chatFailure(&coreTypes.JobResult{Response: "done"})).To(BeEmpty())
	})
})

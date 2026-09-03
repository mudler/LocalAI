// SPDX-License-Identifier: MIT

package routes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// Every MCP endpoint decides between its LOCAL sessions and an agent worker by
// asking whether this value is nil. That check means what its call sites think
// it means only if the nil that reaches it is an untyped one.
var _ = Describe("the MCP endpoints' agent control port", func() {
	It("is nil in a deployment with no distributed services", func() {
		Expect(mcpAgentControlOf(nil)).To(BeNil())
	})

	It("is nil, and not a non-nil interface holding a nil pointer, when nothing built the client", func() {
		// The trap, stated as an assertion. A nil *nodes.AgentControlClient
		// assigned straight into the interface compares NON-nil, so every MCP
		// request would take the distributed path and fail, in a deployment
		// that has perfectly good in-process sessions.
		Expect(mcpAgentControlOf(&application.DistributedServices{})).To(BeNil())
	})

	It("carries the client through when there is one", func() {
		// The negative control: a function that returned nil unconditionally
		// would pass both assertions above and disable distributed MCP
		// entirely, with the local path silently taken instead.
		client := nodes.NewAgentControlClient(nil, nil)
		Expect(mcpAgentControlOf(&application.DistributedServices{AgentControl: client})).To(BeIdenticalTo(client))
	})
})

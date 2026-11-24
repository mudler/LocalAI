// SPDX-License-Identifier: MIT
package studio

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the locks of the first share of an agent", func() {
	It("are a fixed set: one agent always gets the same lock, and any number of agents shares the set", func() {
		Expect(agentShareLock("ag1")).To(BeIdenticalTo(agentShareLock("ag1")))
		seen := map[any]bool{}
		for i := range 10000 {
			seen[agentShareLock(fmt.Sprintf("agent-%d", i))] = true
		}
		Expect(len(seen)).To(BeNumerically("<=", agentShareStripes))
		Expect(len(seen)).To(BeNumerically(">", 1), "the agents are spread over the set")
	})
})

// SPDX-License-Identifier: MIT
package protocol

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Run", func() {
	It("refuses without the protocol flag and builds no runtime", func() {
		built := false
		factory := func(Startup) contract.Runtime { built = true; return nil }
		for _, args := range [][]string{{"x"}, {"x", "--other"}, {"x", "--localai-addon-v1", "extra"}} {
			Expect(Run(args, factory)).To(Equal(2), "args %v", args)
		}
		Expect(built).To(BeFalse(), "the factory ran before the protocol flag was checked")
	})
})

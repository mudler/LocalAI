// SPDX-License-Identifier: MIT
package hostops

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Host operation request admission", func() {
	It("uses registered exact names and literal nonempty grant prefixes without self grants", func() {
		req := &contract.HostOperations{Required: []string{"workspace.materialize", "future.op"}, Optional: []string{"egress.check"}}
		s := Requests(req, []string{"workspace.materialize", "egress.check"}, []string{"workspace."})
		Expect(s.MissingRequired).To(Equal([]string{"future.op"}))
		Expect(s.MissingOptional).To(Equal([]string{"egress.check"}))
		Expect(s.Granted).To(Equal([]string{"workspace.materialize"}))
		Expect(Requests(req, []string{"workspace.materialize"}, []string{""}).MissingRequired).To(HaveLen(2))
		Expect(Requests(&contract.HostOperations{Required: []string{"workspace.materialize"}}, []string{"workspace.materialize"}, []string{"workspace.materialize.narrow."}).MissingRequired).To(HaveLen(1))
		Expect(Requests(nil, nil, nil).MissingRequired).To(BeEmpty())
	})
	It("permits missing optional requests and bounds diagnostics to declaration names", func() {
		req := &contract.HostOperations{Optional: []string{"egress.check", "future.op"}}
		s := Requests(req, SupportedOperations(), []string{"egress."})
		Expect(s.MissingRequired).To(BeEmpty())
		Expect(s.MissingOptional).To(Equal([]string{"future.op"}))
		Expect(s.OptionalCount).To(Equal(2))
		Expect(s.GrantedCount).To(Equal(1))
	})
})

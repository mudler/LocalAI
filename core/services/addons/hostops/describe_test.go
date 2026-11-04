// SPDX-License-Identifier: MIT
package hostops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DescribeOperation", func() {
	It("describes every supported operation, and only those", func() {
		for _, op := range SupportedOperations() {
			title, text, risk, ok := DescribeOperation(op)
			Expect(ok).To(BeTrue(), op)
			Expect(title).ToNot(BeEmpty(), op)
			Expect(text).To(HaveSuffix("."), op)
			Expect(risk).To(BeElementOf(RiskRead, RiskChangesData, RiskNetwork, RiskRunsJobs), op)
		}
		Expect(descriptions).To(HaveLen(len(SupportedOperations())))
		_, _, _, ok := DescribeOperation("not.registered")
		Expect(ok).To(BeFalse())
	})
})

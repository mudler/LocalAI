// SPDX-License-Identifier: MIT
package contract_test

import (
	"errors"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("a refusal that comes from the service behind the add-on", func() {
	It("is found in a wrapped error and keeps the cause", func() {
		cause := errors.New("invalid request: nope")
		err := fmt.Errorf("wrapped: %w", &contract.UpstreamError{Status: 403, Hint: "h", Cause: cause})
		u, ok := contract.UpstreamOf(err)
		Expect(ok).To(BeTrue())
		Expect(u.Status).To(Equal(403))
		Expect(errors.Is(err, cause)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("invalid request: nope"))
		_, ok = contract.UpstreamOf(cause)
		Expect(ok).To(BeFalse())
	})
	DescribeTable("accepts only statuses a service refuses with",
		func(n int, want bool) { Expect(contract.ValidUpstreamStatus(n)).To(Equal(want)) },
		Entry("zero", 0, false), Entry("success", 200, false), Entry("just below 400", 399, false), Entry("400", 400, true),
		Entry("403", 403, true), Entry("599", 599, true), Entry("600", 600, false),
	)
	It("cleans a hint to printable ASCII on one line, cut at the limit", func() {
		Expect(contract.CleanHint("line one\nline two\r\n\tthree")).To(Equal("line one line two three"))
		Expect(contract.CleanHint("tok\x00en\x7f é")).To(Equal("token"))
		Expect(len(contract.CleanHint(strings.Repeat("word ", 200)))).To(BeNumerically("<=", contract.MaxUpstreamHint))
		Expect(contract.CleanHint("")).To(BeEmpty())
	})
})

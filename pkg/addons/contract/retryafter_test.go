// SPDX-License-Identifier: MIT
package contract

import (
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseRetryAfter", func() {
	DescribeTable("reads the header value",
		func(value string, def, want int) {
			Expect(ParseRetryAfter(value, def)).To(Equal(want))
		},
		Entry("seconds", "12", 30, 12),
		Entry("spaces around seconds", " 12 ", 30, 12),
		Entry("zero is raised to one", "0", 30, 1),
		Entry("a negative number is raised to one", "-4", 30, 1),
		Entry("a long wait is cut", "999999", 30, MaxRetryAfter),
		Entry("empty gives the default", "", 30, 30),
		Entry("text gives the default", "soon", 5, 5),
	)

	It("reads an HTTP date", func() {
		at := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
		got := ParseRetryAfter(at, 30)
		Expect(got).To(BeNumerically("~", 90, 3), strconv.Itoa(got))
	})

	It("raises a date in the past to one second", func() {
		at := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
		Expect(ParseRetryAfter(at, 30)).To(Equal(1))
	})
})

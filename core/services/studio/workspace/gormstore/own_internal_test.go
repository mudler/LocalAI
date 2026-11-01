package gormstore

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("notInRevs", func() {
	// The checkpoint queries must stay plain SQL: json_each takes an array only
	// on SQLite, and PostgreSQL is not available to the unit tests.
	It("is portable SQL", func() {
		cond, args := notInRevs("j.rev", nil)
		Expect(cond).To(Equal("1 = 1"), "empty list: want a condition that always holds")
		Expect(args).To(BeEmpty())
		cond, args = notInRevs("j.rev", []int64{3, 5, 8})
		Expect(cond).To(Equal("j.rev NOT IN (?, ?, ?)"))
		Expect(args).To(Equal([]any{int64(3), int64(5), int64(8)}))
		Expect(strings.ToLower(cond)).ToNot(ContainSubstring("json"), "condition is not portable: %q", cond)
	})
})

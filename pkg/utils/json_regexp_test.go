// SPDX-License-Identifier: MIT
package utils_test

import (
	"testing"

	"github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EscapeNewLines", func() {
	DescribeTable("preserves quoted-string boundaries",
		func(input, want string) {
			Expect(utils.EscapeNewLines(input)).To(Equal(want))
		},
		Entry("unchanged JSON", "{\"message\":\"hello\"}", "{\"message\":\"hello\"}"),
		Entry("newline in string", "{\"message\":\"hello\nworld\"}", `{"message":"hello\nworld"}`),
		Entry("outside whitespace", "{\n\"message\":\"hello\"\n}", "{\n\"message\":\"hello\"\n}"),
		Entry("escaped quote", "{\"message\":\"say \\\"hi\\\"\nnow\"}", `{"message":"say \"hi\"\nnow"}`),
	)
})

func BenchmarkEscapeNewLines(b *testing.B) {
	const input = "{\"message\":\"hello\nworld\",\"count\":1}"
	b.ReportAllocs()
	for b.Loop() {
		utils.EscapeNewLines(input)
	}
}

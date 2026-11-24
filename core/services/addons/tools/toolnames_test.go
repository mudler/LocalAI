// SPDX-License-Identifier: MIT
package tools_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("qualified add-on tool names", func() {
	It("round trips valid components and enforces the callable length boundary", func() {
		for _, id := range []string{"a", "runner-9", "r" + strings.Repeat("x", 63)} {
			for _, tool := range []string{"t", "read-doc", "t" + strings.Repeat("z", 63)} {
				name := tools.ToolName(id, tool)
				gotID, gotTool, ok := tools.ParseQualifiedToolName(name)
				Expect(ok).To(BeTrue())
				Expect(gotID).To(Equal(id))
				Expect(gotTool).To(Equal(tool))
				_, _, callable := tools.ParseToolName(name)
				Expect(callable).To(Equal(len(name) <= tools.MaxNameLength))
			}
		}
		for _, length := range []int{64, 65} {
			name := tools.ToolName("a", "t"+strings.Repeat("x", length-11))
			Expect(len(name)).To(Equal(length))
			_, _, ok := tools.ParseToolName(name)
			Expect(ok).To(Equal(length == 64))
		}
	})
	It("rejects malformed qualified identities while reserving their namespace", func() {
		for _, name := range []string{"addon__bad_id__tool", "addon__a__bad_tool", "addon__9a__tool", "addon__a__tool__extra", "addon____tool", "addon__a__", "addon__"} {
			_, _, ok := tools.ParseQualifiedToolName(name)
			Expect(ok).To(BeFalse(), name)
			Expect(tools.IsToolName(name)).To(BeTrue(), name)
		}
		for _, name := range []string{"bash", "a__tool", "mcp__a__tool"} {
			_, _, ok := tools.ParseToolName(name)
			Expect(ok).To(BeFalse())
			Expect(tools.IsToolName(name)).To(BeFalse())
		}
	})
})

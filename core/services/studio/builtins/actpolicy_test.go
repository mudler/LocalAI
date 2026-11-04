package builtins_test

import (
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the action policy of an unattended run", func() {
	read := builtins.ActTool{Name: "list", Read: true, Class: builtins.ClassAddons, AddonID: "notes"}
	write := builtins.ActTool{Name: "write_file", Class: builtins.ClassFiles}
	addon := builtins.ActTool{Name: "post", Class: builtins.ClassAddons, AddonID: "notes"}
	credential := builtins.ActTool{Name: "push", Class: builtins.ClassAddons, AddonID: "git", External: true}
	send := builtins.ActTool{Name: "reply", Class: builtins.ClassSend}
	spend := builtins.ActTool{Name: "mcp_search", Class: builtins.ClassSpend}

	DescribeTable("decides each tool by the mode and the grants",
		func(p builtins.ActPolicy, t builtins.ActTool, allowed bool, reason, missing string) {
			v := p.Decide(t)
			Expect(v.Allowed).To(Equal(allowed))
			Expect(v.Reason).To(Equal(reason))
			Expect(v.Missing).To(Equal(missing))
			if allowed {
				Expect(v.Sentence).To(BeEmpty())
			} else {
				Expect(v.Sentence).To(ContainSubstring("I did not run " + t.Name))
			}
		},
		// A read always runs, even when the run is fixed to read only.
		Entry("read, no grants", builtins.ActPolicy{}, read, true, "read", ""),
		Entry("read, read only", builtins.ActPolicy{ReadOnly: true}, read, true, "read", ""),
		// Deny by default.
		Entry("file write, no grants", builtins.ActPolicy{}, write, false, "not_granted", "act.files"),
		Entry("add-on action, no grants", builtins.ActPolicy{}, addon, false, "not_granted", "act.addons:notes"),
		Entry("send, no grants", builtins.ActPolicy{}, send, false, "not_granted", "act.send"),
		Entry("external tool, no grants", builtins.ActPolicy{}, spend, false, "not_granted", "act.spend"),
		// Each class needs its own grant.
		Entry("file write with act.files", builtins.ActPolicy{Held: []string{"act.files"}}, write, true, "granted", ""),
		Entry("file write with act.addons only", builtins.ActPolicy{Held: []string{"act.addons", "act.send", "act.spend"}}, write, false, "not_granted", "act.files"),
		Entry("add-on action with act.addons", builtins.ActPolicy{Held: []string{"act.addons"}}, addon, true, "granted", ""),
		Entry("add-on action with act.addons for this add-on", builtins.ActPolicy{Held: []string{"act.addons:notes"}}, addon, true, "granted", ""),
		Entry("add-on action with act.addons for another add-on", builtins.ActPolicy{Held: []string{"act.addons:other"}}, addon, false, "not_granted", "act.addons:notes"),
		Entry("send with act.send", builtins.ActPolicy{Held: []string{"act.send"}}, send, true, "granted", ""),
		Entry("external tool with act.spend", builtins.ActPolicy{Held: []string{"act.spend"}}, spend, true, "granted", ""),
		// A credentialed add-on action also reaches outside.
		Entry("credentialed action with act.addons only", builtins.ActPolicy{Held: []string{"act.addons"}}, credential, false, "not_granted", "act.spend"),
		Entry("credentialed action with both", builtins.ActPolicy{Held: []string{"act.addons", "act.spend"}}, credential, true, "granted", ""),
		// A read only run refuses every action, whatever it holds.
		Entry("read only run with every grant, file write", builtins.ActPolicy{ReadOnly: true, Held: builtins.ActGrants()}, write, false, "read_only", ""),
		Entry("read only run with every grant, add-on action", builtins.ActPolicy{ReadOnly: true, Held: builtins.ActGrants()}, addon, false, "read_only", ""),
		Entry("read only run with every grant, send", builtins.ActPolicy{ReadOnly: true, Held: builtins.ActGrants()}, send, false, "read_only", ""),
		Entry("read only run with every grant, external tool", builtins.ActPolicy{ReadOnly: true, Held: builtins.ActGrants()}, spend, false, "read_only", ""),
		// A tool with no class is refused, never assumed harmless.
		Entry("unclassified tool with every grant", builtins.ActPolicy{Held: builtins.ActGrants()}, builtins.ActTool{Name: "mystery"}, false, "not_granted", "act.unclassified"),
	)

	It("names the grants a trigger may carry", func() {
		for _, ok := range []string{"act.files", "act.addons", "act.send", "act.spend", "act.addons:notes", "act.addons:git.hub"} {
			Expect(builtins.ValidActGrant(ok)).To(BeTrue(), ok)
		}
		for _, bad := range []string{"", "files", "act.", "act.addons:", "act.addons:Bad Name", "act.other", "memory.write"} {
			Expect(builtins.ValidActGrant(bad)).To(BeFalse(), bad)
		}
	})
})

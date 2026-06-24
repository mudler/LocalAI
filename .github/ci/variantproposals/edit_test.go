package main

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ApplyFamilies", func() {
	apply := func(ix *Index, families []Family) []string {
		lines, err := ApplyFamilies(ix, families)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return lines
	}

	// insertedLines is what a reviewer would see in the diff. A textual editor
	// that reflowed the file would show thousands here, which is the failure
	// this whole approach exists to avoid.
	insertedLines := func(before, after []string) int {
		remaining := map[string]int{}
		for _, l := range before {
			remaining[l]++
		}
		n := 0
		for _, l := range after {
			if remaining[l] > 0 {
				remaining[l]--
				continue
			}
			n++
		}
		return n
	}

	It("adds a variants block right after the entry's name and touches nothing else", func() {
		ix := indexOf(
			entryYAML("foo-model", "acme/repo", "foo-model-Q4_K_M.gguf", "aa"),
			entryYAML("foo-model-q8_0", "acme/repo", "foo-model-Q8_0.gguf", "bb"),
		)
		out := apply(ix, []Family{{Parent: "foo-model", Proposals: []Proposal{{Variant: "foo-model-q8_0"}}}})

		Expect(out[0]).To(Equal("- name: foo-model"))
		Expect(out[1]).To(Equal("  variants:"))
		Expect(out[2]).To(Equal("    - model: foo-model-q8_0"))
		Expect(len(out)).To(Equal(len(ix.Lines) + 2))
		Expect(insertedLines(ix.Lines, out)).To(Equal(2))
	})

	It("appends to a variants block that already exists", func() {
		ix := indexOf(`- name: partial
  variants:
    - model: partial-q8_0
  url: u
  overrides:
    parameters:
      model: partial-Q4_K_M.gguf
`, entryYAML("partial-f16", "acme/repo", "partial-f16.gguf", "cc"))
		out := apply(ix, []Family{{Parent: "partial", Proposals: []Proposal{{Variant: "partial-f16"}}}})

		Expect(out[1]).To(Equal("  variants:"))
		Expect(out[2]).To(Equal("    - model: partial-q8_0"))
		Expect(out[3]).To(Equal("    - model: partial-f16"))
		Expect(out[4]).To(Equal("  url: u"))
	})

	It("replaces an explicit empty list rather than leaving two variants keys", func() {
		ix := indexOf(`- name: emptied
  variants: []
  url: u
`, entryYAML("emptied-q8_0", "acme/repo", "emptied-Q8_0.gguf", "cc"))
		out := apply(ix, []Family{{Parent: "emptied", Proposals: []Proposal{{Variant: "emptied-q8_0"}}}})

		Expect(strings.Join(out[:4], "\n")).To(Equal("- name: emptied\n  variants:\n    - model: emptied-q8_0\n  url: u"))
		Expect(strings.Count(strings.Join(out, "\n"), "variants:")).To(Equal(1))
	})

	It("quotes a config-suffixed name so the reference stays a string", func() {
		ix := indexOf(
			entryYAML("phi-2-chat", "acme/repo", "phi-2-chat-Q4_K_M.gguf", "aa"),
			entryYAML("phi-2-chat:Q8_0", "acme/repo", "phi-2-chat-Q8_0.gguf", "bb"),
		)
		out := apply(ix, []Family{{Parent: "phi-2-chat", Proposals: []Proposal{{Variant: "phi-2-chat:Q8_0"}}}})
		Expect(out[2]).To(Equal(`    - model: "phi-2-chat:Q8_0"`))

		// The result has to still be a gallery, and the reference has to
		// resolve to the entry it names.
		reparsed, err := ParseIndex(strings.Join(out, "\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(reparsed.Entries[0].Variants).To(ConsistOf(VariantRef{Model: "phi-2-chat:Q8_0"}))
	})

	It("keeps line numbers correct when several entries are edited at once", func() {
		ix := indexOf(
			entryYAML("alpha", "acme/repo", "alpha-Q4_K_M.gguf", "aa"),
			entryYAML("alpha-q8_0", "acme/repo", "alpha-Q8_0.gguf", "bb"),
			entryYAML("beta", "acme/repo", "beta-Q4_K_M.gguf", "cc"),
			entryYAML("beta-q8_0", "acme/repo", "beta-Q8_0.gguf", "dd"),
		)
		out := apply(ix, []Family{
			{Parent: "alpha", Proposals: []Proposal{{Variant: "alpha-q8_0"}}},
			{Parent: "beta", Proposals: []Proposal{{Variant: "beta-q8_0"}}},
		})

		reparsed, err := ParseIndex(strings.Join(out, "\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(reparsed.Entries).To(HaveLen(4))
		Expect(reparsed.Entries[0].Variants).To(ConsistOf(VariantRef{Model: "alpha-q8_0"}))
		Expect(reparsed.Entries[2].Variants).To(ConsistOf(VariantRef{Model: "beta-q8_0"}))
		Expect(reparsed.Entries[1].Variants).To(BeEmpty())
		Expect(reparsed.Entries[3].Variants).To(BeEmpty())
	})

	It("fails loudly rather than editing an entry it cannot find", func() {
		ix := indexOf(entryYAML("only", "acme/repo", "only-Q4_K_M.gguf", "aa"))
		_, err := ApplyFamilies(ix, []Family{{Parent: "missing", Proposals: []Proposal{{Variant: "x"}}}})
		Expect(err).To(MatchError(ContainSubstring("not in the index")))
	})
})

var _ = Describe("ParseIndex", func() {
	It("records the anchor an entry defines and the anchor an entry merges", func() {
		ix := indexOf(`- &anc
  name: anchored
  url: u
`, `- !!merge <<: *anc
  name: child
`)
		Expect(ix.Entries[0].AnchorName).To(Equal("anc"))
		Expect(ix.Entries[1].MergesFrom).To(Equal("anc"))
		Expect(ix.MergeChildren("anc")).To(HaveLen(1))
	})

	It("carries merged values into the child, so an inherited variants key is visible", func() {
		ix := indexOf(`- &anc
  name: anchored
  url: u
  variants:
    - model: something
`, `- !!merge <<: *anc
  name: child
`)
		Expect(ix.Entries[1].HasVariants()).To(BeTrue())
	})

	It("refuses a list item that decodes to nothing", func() {
		// Every line number the editor works from comes from pairing decoded
		// entries with top level list items. If those two views can disagree,
		// the editor writes into the wrong entry, so the parse refuses instead.
		_, err := ParseIndex("- name: one\n  url: u\n-\n")
		Expect(err).To(MatchError(ContainSubstring("empty")))
	})
})

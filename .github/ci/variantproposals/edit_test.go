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

	It("adds a variants block at the end of the model body and touches nothing else", func() {
		ix := indexOf(
			entryYAML("foo-model", "acme/repo", "foo-model-Q4_K_M.gguf", "aa"),
			entryYAML("foo-model-q8_0", "acme/repo", "foo-model-Q8_0.gguf", "bb"),
		)
		out := apply(ix, []Family{{Parent: "foo-model", Proposals: []Proposal{{Variant: "foo-model-q8_0"}}}})

		end := ix.Entries[0].EndLine
		Expect(out[0]).To(Equal("- kind: model"))
		Expect(out[end-1]).To(Equal(ix.Lines[end-1]))
		Expect(out[end]).To(Equal("    variants:"))
		Expect(out[end+1]).To(Equal("      - model: foo-model-q8_0"))
		Expect(out[end+2]).To(Equal("- kind: model"))
		Expect(len(out)).To(Equal(len(ix.Lines) + 2))
		Expect(insertedLines(ix.Lines, out)).To(Equal(2))
	})

	It("appends to a variants block that already exists", func() {
		ix := indexOf(`- kind: model
  id: partial
  name: partial
  model:
    variants:
      - model: partial-q8_0
    url: u
    overrides:
      parameters:
        model: partial-Q4_K_M.gguf
`, entryYAML("partial-f16", "acme/repo", "partial-f16.gguf", "cc"))
		out := apply(ix, []Family{{Parent: "partial", Proposals: []Proposal{{Variant: "partial-f16"}}}})

		Expect(out[4]).To(Equal("    variants:"))
		Expect(out[5]).To(Equal("      - model: partial-q8_0"))
		Expect(out[6]).To(Equal("      - model: partial-f16"))
		Expect(out[7]).To(Equal("    url: u"))
	})

	It("replaces an explicit empty list rather than leaving two variants keys", func() {
		ix := indexOf(`- kind: model
  id: emptied
  name: emptied
  model:
    variants: []
    url: u
`, entryYAML("emptied-q8_0", "acme/repo", "emptied-Q8_0.gguf", "cc"))
		out := apply(ix, []Family{{Parent: "emptied", Proposals: []Proposal{{Variant: "emptied-q8_0"}}}})

		Expect(strings.Join(out[3:7], "\n")).To(Equal("  model:\n    variants:\n      - model: emptied-q8_0\n    url: u"))
		Expect(strings.Count(strings.Join(out, "\n"), "variants:")).To(Equal(1))
	})

	It("quotes a config-suffixed name so the reference stays a string", func() {
		ix := indexOf(
			entryYAML("phi-2-chat", "acme/repo", "phi-2-chat-Q4_K_M.gguf", "aa"),
			entryYAML("phi-2-chat:Q8_0", "acme/repo", "phi-2-chat-Q8_0.gguf", "bb"),
		)
		out := apply(ix, []Family{{Parent: "phi-2-chat", Proposals: []Proposal{{Variant: "phi-2-chat:Q8_0"}}}})
		Expect(out[ix.Entries[0].EndLine+1]).To(Equal(`      - model: "phi-2-chat:Q8_0"`))

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
	It("refuses a list item that decodes to nothing", func() {
		// Every line number the editor works from comes from pairing decoded
		// entries with top level list items. If those two views can disagree,
		// the editor writes into the wrong entry, so the parse refuses instead.
		_, err := ParseIndex("- kind: model\n  id: one\n-\n")
		Expect(err).To(MatchError(ContainSubstring("empty")))
	})
})

var _ = Describe("the checked-in model library", func() {
	It("parses, and a proposal edit keeps it parsing with the same entries", func() {
		ix, err := LoadIndex("../../../gallery/models/index.yaml")
		Expect(err).ToNot(HaveOccurred())
		Expect(ix.Entries).ToNot(BeEmpty())
		for _, e := range ix.Entries {
			Expect(e.Name).ToNot(BeEmpty())
		}

		// Add one variant to the first and the last entry: the first has a
		// block, the last may have none, and both must stay valid.
		first, last := ix.Entries[0], ix.Entries[len(ix.Entries)-1]
		out, err := ApplyFamilies(ix, []Family{
			{Parent: first.Name, Proposals: []Proposal{{Variant: "x-added-one"}}},
			{Parent: last.Name, Proposals: []Proposal{{Variant: "x-added-two"}}},
		})
		Expect(err).ToNot(HaveOccurred())
		reparsed, err := ParseIndex(strings.Join(out, "\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(reparsed.Entries).To(HaveLen(len(ix.Entries)))
		Expect(reparsed.Entries[0].Variants).To(ContainElement(VariantRef{Model: "x-added-one"}))
		Expect(reparsed.Entries[len(ix.Entries)-1].Variants).To(ContainElement(VariantRef{Model: "x-added-two"}))
	})
})

package localaitools

import (
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SystemPrompt assembler", func() {
	It("includes every embedded markdown file in lexicographic order", func() {
		got := SystemPrompt(Options{})

		paths := embeddedPromptPaths()
		Expect(paths).ToNot(BeEmpty(), "embed directive must surface at least one file")

		// Each file's basename should appear in the output, and in order.
		var lastIdx int
		for _, p := range paths {
			section := strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".md")
			needle := "# section: " + section
			idx := strings.Index(got, needle)
			Expect(idx).To(BeNumerically(">=", 0), "section %q (%s) missing from output", section, p)
			Expect(idx).To(BeNumerically(">=", lastIdx), "section %q out of lexicographic order", section)
			lastIdx = idx
		}
	})

	It("exposes the embedded paths sorted", func() {
		paths := embeddedPromptPaths()
		Expect(sort.StringsAreSorted(paths)).To(BeTrue(), "paths %v are not sorted", paths)
	})

	It("contains the safety anchors that the LLM relies on", func() {
		out := SystemPrompt(Options{})

		// Guards against accidental safety-rule deletion. These strings
		// also live in tools_*.go (the Tool* constants); the prompt MUST
		// stay aligned because the LLM uses these names verbatim.
		mustContain := []string{
			"LocalAI Assistant",
			"Confirm before mutating",
			"Surface tool errors verbatim",
			"install_model",
			"delete_model",
			// Skill files we ship.
			"Skill: Install a chat model",
			"Skill: Upgrade a backend",
			"Skill: System status",
			"Skill: Safely edit a model config",
		}
		for _, s := range mustContain {
			Expect(out).To(ContainSubstring(s), "system prompt missing required anchor %q", s)
		}
	})
})

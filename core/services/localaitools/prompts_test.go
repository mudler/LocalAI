package localaitools

import (
	"regexp"
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
			"Skill: Manage a router corpus",
			"Skill: Manage shared folders",
			"get_router_corpus_stats",
		}
		for _, s := range mustContain {
			Expect(out).To(ContainSubstring(s), "system prompt missing required anchor %q", s)
		}
	})

	It("names every mutating tool in the confirmation safety rule", func() {
		raw, err := promptsFS.ReadFile("prompts/10_safety.md")
		Expect(err).NotTo(HaveOccurred())
		out := string(raw)
		for _, name := range mutatingToolNames {
			Expect(out).To(ContainSubstring("`"+name+"`"),
				"system prompt confirmation rule is missing mutating tool %q", name)
		}
	})
})

// compactBudgetBytes caps CompactSystemPrompt. The studio chats send it on
// every turn of every chat that has the admin tools, so it must stay small:
// the full text of a recipe belongs in its skill, which the model loads only
// when a task needs it. The value is the measured size (4,266 bytes, about
// 1,070 tokens) plus about 10%. Raise it only with a reason in the commit.
const compactBudgetBytes = 4700

var _ = Describe("admin recipes", func() {
	skillName := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

	It("has one recipe per embedded file, with a valid name and a description", func() {
		var files int
		for _, p := range embeddedPromptPaths() {
			if strings.HasPrefix(p, "prompts/skills/") {
				files++
			}
		}
		recipes := Recipes()
		Expect(recipes).To(HaveLen(files))
		seen := map[string]bool{}
		for _, r := range recipes {
			Expect(r.Name).To(HavePrefix(SkillPrefix))
			Expect(skillName.MatchString(r.Name)).To(BeTrue(), "%q is not a valid skill name", r.Name)
			Expect(seen[r.Name]).To(BeFalse(), "%q twice", r.Name)
			seen[r.Name] = true
			Expect(r.Description).NotTo(BeEmpty(), r.Name)
			Expect(len(r.Description)).To(BeNumerically("<=", 120), "%s: keep the description to one line", r.Name)
			Expect(r.Body).To(HavePrefix("# Skill:"), r.Name)
			Expect(r.Body).NotTo(ContainSubstring("description:"), "%s: the front matter leaked into the body", r.Name)
		}
	})

	It("sends every recipe inline in the full prompt", func() {
		full := SystemPrompt(Options{})
		for _, r := range Recipes() {
			Expect(full).To(ContainSubstring(r.Body), r.Name)
		}
		Expect(full).NotTo(MatchRegexp(`(?m)^description:`), "front matter must not reach the model")
		Expect(full).To(ContainSubstring("# section: 20_tools"))
	})

	It("lists exactly the existing recipes in the compact prompt, without their text", func() {
		compact := CompactSystemPrompt(Options{})
		listed := regexp.MustCompile("(?m)^- `("+SkillPrefix+"[a-z0-9-]+)`: (.+)$").FindAllStringSubmatch(compact, -1)
		var got, want []string
		for _, m := range listed {
			got = append(got, m[1])
		}
		for _, r := range Recipes() {
			want = append(want, r.Name)
			Expect(compact).To(ContainSubstring("- `"+r.Name+"`: "+r.Description), r.Name)
			Expect(compact).NotTo(ContainSubstring(r.Body), "%s: the compact prompt must not carry the recipe", r.Name)
		}
		Expect(got).To(Equal(want))
		Expect(compact).To(ContainSubstring("`load_skill`"))
	})

	It("keeps the role and the safety rules in the compact prompt", func() {
		compact := CompactSystemPrompt(Options{})
		for _, anchor := range []string{"LocalAI Assistant", "Confirm before mutating", "Disambiguate before mutating", "Surface tool errors verbatim", "Never invent identifiers", "Polling"} {
			Expect(compact).To(ContainSubstring(anchor), anchor)
		}
		for _, name := range mutatingToolNames {
			Expect(compact).To(ContainSubstring("`"+name+"`"), "the confirmation rule must name %s", name)
		}
		Expect(compact).NotTo(ContainSubstring("# section: 20_tools"))
	})

	It("keeps the compact prompt within its budget and well under the full one", func() {
		compact, full := CompactSystemPrompt(Options{}), SystemPrompt(Options{})
		Expect(len(compact)).To(BeNumerically("<=", compactBudgetBytes),
			"the compact prompt is paid on every turn; move text into a recipe skill instead of raising the budget")
		Expect(len(compact)*10).To(BeNumerically("<", len(full)*4), "the compact prompt must be at least 60%% smaller than the full one")
	})
})

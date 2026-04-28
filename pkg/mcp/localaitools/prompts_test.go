package localaitools

import (
	"sort"
	"strings"
	"testing"
)

// TestSystemPromptIncludesAllEmbeddedFiles asserts the assembler walks every
// embedded markdown file and that the output is in lexicographic order.
func TestSystemPromptIncludesAllEmbeddedFiles(t *testing.T) {
	got := SystemPrompt(Options{})

	paths := embeddedPromptPaths()
	if len(paths) == 0 {
		t.Fatal("no embedded prompts found — embed directive likely broken")
	}

	// Each file's basename should appear in the output, and in order.
	var lastIdx int
	for _, p := range paths {
		section := strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".md")
		needle := "# section: " + section
		idx := strings.Index(got, needle)
		if idx < 0 {
			t.Errorf("section %q (%s) missing from output", section, p)
			continue
		}
		if idx < lastIdx {
			t.Errorf("section %q out of lexicographic order (idx=%d, lastIdx=%d)", section, idx, lastIdx)
		}
		lastIdx = idx
	}
}

func TestEmbeddedPathsAreSorted(t *testing.T) {
	paths := embeddedPromptPaths()
	if !sort.StringsAreSorted(paths) {
		t.Errorf("embeddedPromptPaths not sorted: %v", paths)
	}
}

// TestPromptsContainSafetyAnchors guards against accidental safety-rule deletion.
func TestPromptsContainSafetyAnchors(t *testing.T) {
	out := SystemPrompt(Options{})

	mustContain := []string{
		"LocalAI Assistant",
		"Confirm before mutating",
		"Surface tool errors verbatim",
		"install_model",
		"delete_model",
		// Skill files we ship in this PR.
		"Skill: Install a chat model",
		"Skill: Upgrade a backend",
		"Skill: System status",
		"Skill: Safely edit a model config",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("system prompt missing required anchor: %q", s)
		}
	}
}

package localaitools

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed prompts/*.md prompts/skills/*.md
var promptsFS embed.FS

// SystemPrompt assembles the assistant system prompt from the embedded
// markdown files. The walk is deterministic (lexicographic).
//
// Panics if the embedded FS walk fails: the only realistic cause is a
// build-time misconfiguration of the //go:embed directive, and serving
// a silently-empty prompt to the LLM is far worse than crashing the
// init path. The TestSystemPromptIncludesAllEmbeddedFiles test catches
// regressions in CI before they ship.
func SystemPrompt(_ Options) string {
	var paths []string
	if err := fs.WalkDir(promptsFS, "prompts", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		panic(fmt.Errorf("localaitools: walk embedded prompts: %w", err))
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		data, err := promptsFS.ReadFile(p)
		if err != nil {
			continue
		}
		// File-level header for traceability ("which skill is the model citing?").
		// We use the file basename without extension.
		section := strings.TrimSuffix(path.Base(p), ".md")
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("<!-- file: ")
		b.WriteString(p)
		b.WriteString(" -->\n")
		b.WriteString("# section: ")
		b.WriteString(section)
		b.WriteString("\n\n")
		b.WriteString(string(data))
	}
	return b.String()
}

// embeddedPromptPaths returns the lexicographically-sorted list of embedded
// prompt files. Exposed for tests.
func embeddedPromptPaths() []string {
	var paths []string
	_ = fs.WalkDir(promptsFS, "prompts", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	sort.Strings(paths)
	return paths
}

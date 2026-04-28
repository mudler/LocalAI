package localaitools

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed prompts/*.md prompts/skills/*.md
var promptsFS embed.FS

// SystemPrompt assembles the assistant system prompt from the embedded
// markdown files. The walk is deterministic (lexicographic).
func SystemPrompt(_ Options) string {
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

package localaitools

import (
	"embed"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed prompts/*.md prompts/skills/*.md
var promptsFS embed.FS

// SkillPrefix starts the name of every admin recipe skill. The chats load a
// recipe by this name; the prefix keeps the names apart from other skills.
const SkillPrefix = "localai-admin-"

// recipesDir is the folder of the embedded recipes.
const recipesDir = "prompts/skills/"

// Recipe is one admin recipe: step-by-step text for a task, kept in one
// embedded file. The standalone MCP server sends every recipe inline in its
// system prompt (SystemPrompt). The chats offer each as a skill that the model
// loads by name (Recipes, CompactSystemPrompt).
type Recipe struct {
	// Name is the skill name: SkillPrefix and the file name in kebab case.
	Name string
	// Description is one line that says when to use the recipe.
	Description string
	// Body is the recipe text, without the front matter.
	Body string
}

// splitRecipe separates the front matter of a recipe file from its text. The
// front matter is a first line "---", "description: <text>" and a line "---".
func splitRecipe(data string) (description, body string) {
	rest, ok := strings.CutPrefix(data, "---\n")
	if !ok {
		return "", data
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return "", data
	}
	for _, line := range strings.Split(head, "\n") {
		if v, found := strings.CutPrefix(line, "description:"); found {
			description = strings.TrimSpace(v)
		}
	}
	return description, body
}

// Recipes returns the admin recipes, sorted by name. Each is a built-in skill
// of the chats that offer the admin tools.
func Recipes() []Recipe {
	var out []Recipe
	for _, p := range embeddedPromptPaths() {
		if !strings.HasPrefix(p, recipesDir) {
			continue
		}
		data, err := promptsFS.ReadFile(p)
		if err != nil {
			continue
		}
		desc, body := splitRecipe(string(data))
		file := strings.TrimSuffix(path.Base(p), ".md")
		out = append(out, Recipe{Name: SkillPrefix + strings.ReplaceAll(file, "_", "-"), Description: desc, Body: body})
	}
	return out
}

// section writes one prompt section with the header that names its file.
func section(b *strings.Builder, p, body string) {
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("<!-- file: ")
	b.WriteString(p)
	b.WriteString(" -->\n# section: ")
	b.WriteString(strings.TrimSuffix(path.Base(p), ".md"))
	b.WriteString("\n\n")
	b.WriteString(body)
}

// SystemPrompt assembles the full assistant system prompt from the embedded
// markdown files: the role, the safety rules, the tool catalog and every
// recipe inline. The walk is deterministic (lexicographic). The standalone MCP
// server uses it, and any caller that cannot load skills.
//
// Panics if the embedded FS walk fails: the only realistic cause is a
// build-time misconfiguration of the //go:embed directive, and serving
// a silently-empty prompt to the LLM is far worse than crashing the
// init path. The TestSystemPromptIncludesAllEmbeddedFiles test catches
// regressions in CI before they ship.
func SystemPrompt(_ Options) string {
	paths := embeddedPromptPaths()
	if len(paths) == 0 {
		panic(errors.New("localaitools: no embedded prompts"))
	}
	var b strings.Builder
	for _, p := range paths {
		data, err := promptsFS.ReadFile(p)
		if err != nil {
			continue
		}
		body := string(data)
		if strings.HasPrefix(p, recipesDir) {
			_, body = splitRecipe(body)
		}
		section(&b, p, body)
	}
	return b.String()
}

// loadSkillTool is the chat tool that loads a skill by name. The studio tests
// check it against the real name.
const loadSkillTool = "load_skill"

// CompactSystemPrompt is the prompt for the chats, which can load skills: the
// role, the safety rules and the names of the recipes. The full text of each
// recipe is a skill (Recipes), and the tool schemas already say what each tool
// does, so the catalog stays out. The chats pay this prompt on every turn, so
// it stays small (see compactBudgetBytes in the tests).
func CompactSystemPrompt(_ Options) string {
	var b strings.Builder
	for _, p := range embeddedPromptPaths() {
		if p != "prompts/00_role.md" && p != "prompts/10_safety.md" {
			continue
		}
		data, err := promptsFS.ReadFile(p)
		if err != nil {
			continue
		}
		section(&b, p, string(data))
	}
	var r strings.Builder
	r.WriteString("Recipes are skills. Before a task that matches one, call `" + loadSkillTool + "` with its name and follow it. The safety rules above always apply.\n")
	for _, rc := range Recipes() {
		r.WriteString("\n- `" + rc.Name + "`: " + rc.Description)
	}
	section(&b, "prompts/recipes.md", r.String())
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

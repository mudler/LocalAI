package content_test

import (
	"encoding/json"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/content"
)

var _ = Describe("CodeFiles", func() {
	base := map[string]string{
		".claude-plugin/plugin.json": `{"name": "fixture"}`,
		"scripts/fmt.sh":             "echo",
		"scripts/lint.sh":            "echo",
		"scripts/unused.sh":          "echo",
		"server/index.js":            "js",
		"server/lib/util.js":         "js",
		"tools/run.py":               "py",
		"bin/fmt":                    "+xbin",
		"bin/data.txt":               "plain",
		"helpers/exec":               "+xexec",
		"helpers/shebang":            "#!/bin/sh\n",
		"skills/a/SKILL.md":          "---\nname: a\ndescription: a\n---\nbody",
		"skills/a/scripts/run.sh":    "+x#!/bin/sh\n",
		"commands/attach.md":         "Read @notes.md.",
		"commands/plain.md":          "plain",
		"README.md":                  "readme",
	}

	// load writes base plus extra, checks it and loads it with nib.
	load := func(extra map[string]string) []string {
		files := maps.Clone(base)
		maps.Copy(files, extra)
		dir := tree(files)
		t, err := content.Check(dir, content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		p, err := claudeplugin.Load(dir, claudeplugin.LoadOptions{})
		Expect(err).NotTo(HaveOccurred())
		return content.CodeFiles(t, p)
	}
	hooks := func(commands ...string) map[string]string {
		var hs []map[string]string
		for _, c := range commands {
			hs = append(hs, map[string]string{"type": "command", "command": c})
		}
		b, err := json.Marshal(map[string]any{"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "Bash", "hooks": hs}},
		}})
		Expect(err).NotTo(HaveOccurred())
		return map[string]string{"hooks/hooks.json": string(b)}
	}

	It("lists the configuration files that hold code", func() {
		extra := hooks("true")
		extra[".mcp.json"] = `{"mcpServers": {"srv": {"command": "node", "args": ["./server/index.js"]}}}`
		got := load(extra)
		Expect(got).To(ContainElements("hooks/hooks.json", ".mcp.json", "server/index.js"))
		Expect(got).To(ContainElement(".claude-plugin/plugin.json"), "nib lists the manifest of a plugin that runs code")
	})

	It("does not list the manifest of a text-only plugin", func() {
		dir := tree(map[string]string{
			".claude-plugin/plugin.json": `{"name": "fixture"}`,
			".mcp.json":                  `{"mcpServers": {"remote": {"type": "http", "url": "https://example.test/mcp"}}}`,
			"skills/a/SKILL.md":          "---\nname: a\ndescription: a\n---\nbody",
		})
		t, err := content.Check(dir, content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		p, err := claudeplugin.Load(dir, claudeplugin.LoadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.RunsCode()).To(BeFalse())
		Expect(content.CodeFiles(t, p)).NotTo(ContainElement(".claude-plugin/plugin.json"))
	})

	DescribeTable("lists the files that a hook command names",
		func(command string, want []string) {
			got := load(hooks(command))
			Expect(got).To(ContainElements(want))
			Expect(got).NotTo(ContainElement("scripts/unused.sh"))
			Expect(got).NotTo(ContainElement("README.md"))
		},
		Entry("plugin root with braces", "${CLAUDE_PLUGIN_ROOT}/scripts/fmt.sh --check", []string{"scripts/fmt.sh"}),
		Entry("quoted plugin root", `bash "${CLAUDE_PLUGIN_ROOT}"/scripts/lint.sh`, []string{"scripts/lint.sh"}),
		Entry("plugin root without braces", "sh $CLAUDE_PLUGIN_ROOT/scripts/fmt.sh;", []string{"scripts/fmt.sh"}),
		Entry("dot relative path", "node ./server/index.js", []string{"server/index.js"}),
		Entry("relative path without a dot", "python3 tools/run.py", []string{"tools/run.py"}),
		Entry("a folder", "node ${CLAUDE_PLUGIN_ROOT}/server", []string{"server/index.js", "server/lib/util.js"}),
	)

	It("ignores absolute paths and names that are not in the tree", func() {
		got := load(hooks("/usr/bin/node /etc/passwd ./nothing.js https://example.test/a/b npx -y pkg"))
		Expect(got).To(Equal([]string{".claude-plugin/plugin.json", "bin/data.txt", "bin/fmt", "helpers/exec", "helpers/shebang", "hooks/hooks.json"}))
	})

	It("lists every file in bin/", func() {
		Expect(load(nil)).To(ContainElements("bin/fmt", "bin/data.txt"))
	})

	It("lists executable files and scripts outside the skill folders", func() {
		got := load(nil)
		Expect(got).To(ContainElements("helpers/exec", "helpers/shebang"))
		Expect(got).NotTo(ContainElement("skills/a/scripts/run.sh"), "skill scripts are stored, not run")
	})

	It("lists the files with a shell injection, not those with an @file reference", func() {
		got := load(map[string]string{"commands/inject.md": "Today is !`date`."})
		Expect(got).To(ContainElement("commands/inject.md"))
		Expect(got).NotTo(ContainElements("commands/plain.md", "commands/attach.md"))
	})

	It("returns each file once, sorted", func() {
		got := load(hooks("${CLAUDE_PLUGIN_ROOT}/bin/fmt", "./bin/fmt"))
		Expect(got).To(Equal([]string{".claude-plugin/plugin.json", "bin/data.txt", "bin/fmt", "helpers/exec", "helpers/shebang", "hooks/hooks.json"}))
	})

	It("lists the files of a tree without a parsed plugin", func() {
		t, err := content.Check(tree(base), content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		Expect(content.CodeFiles(t, nil)).To(Equal([]string{"bin/data.txt", "bin/fmt", "helpers/exec", "helpers/shebang"}))
	})
})

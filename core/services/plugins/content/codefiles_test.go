package content_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/content"
)

var _ = Describe("CodeFiles", func() {
	files := map[string]string{
		".claude-plugin/plugin.json": "{}",
		"hooks/hooks.json":           "{}",
		".mcp.json":                  "{}",
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
		"skills/a/SKILL.md":          "a",
		"skills/a/scripts/run.sh":    "+x#!/bin/sh\n",
		"commands/inject.md":         "!`date`",
		"commands/plain.md":          "plain",
		"README.md":                  "readme",
	}
	var t content.Tree
	BeforeEach(func() {
		var err error
		t, err = content.Check(tree(files), content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
	})

	It("lists the configuration files that hold code", func() {
		got := content.CodeFiles(t, content.CodeSources{ConfigFiles: []string{"hooks/hooks.json", ".mcp.json", "missing.json"}})
		Expect(got).To(ContainElements("hooks/hooks.json", ".mcp.json"))
		Expect(got).NotTo(ContainElement("missing.json"))
		Expect(got).NotTo(ContainElement(".claude-plugin/plugin.json"))
	})

	DescribeTable("lists the files that a command names",
		func(c content.Command, want []string) {
			got := content.CodeFiles(t, content.CodeSources{Commands: []content.Command{c}})
			for _, w := range want {
				Expect(got).To(ContainElement(w))
			}
			Expect(got).NotTo(ContainElement("scripts/unused.sh"))
			Expect(got).NotTo(ContainElement("README.md"))
		},
		Entry("plugin root with braces", content.Command{Command: "${CLAUDE_PLUGIN_ROOT}/scripts/fmt.sh --check"}, []string{"scripts/fmt.sh"}),
		Entry("quoted plugin root", content.Command{Command: `bash "${CLAUDE_PLUGIN_ROOT}"/scripts/lint.sh`}, []string{"scripts/lint.sh"}),
		Entry("plugin root without braces", content.Command{Command: "sh $CLAUDE_PLUGIN_ROOT/scripts/fmt.sh;"}, []string{"scripts/fmt.sh"}),
		Entry("relative argument", content.Command{Command: "node", Args: []string{"./server/index.js"}}, []string{"server/index.js"}),
		Entry("relative path without a dot", content.Command{Command: "python3 tools/run.py"}, []string{"tools/run.py"}),
		Entry("a folder", content.Command{Command: "node", Args: []string{"${CLAUDE_PLUGIN_ROOT}/server"}}, []string{"server/index.js", "server/lib/util.js"}),
	)

	It("ignores absolute paths and names that are not in the tree", func() {
		got := content.CodeFiles(t, content.CodeSources{Commands: []content.Command{
			{Command: "/usr/bin/node /etc/passwd ./nothing.js https://example.test/a/b npx -y pkg"},
		}})
		Expect(got).To(Equal([]string{"bin/data.txt", "bin/fmt", "helpers/exec", "helpers/shebang"}))
	})

	It("lists every file in bin/", func() {
		got := content.CodeFiles(t, content.CodeSources{})
		Expect(got).To(ContainElements("bin/fmt", "bin/data.txt"))
	})

	It("lists executable files and scripts outside the skill folders", func() {
		got := content.CodeFiles(t, content.CodeSources{})
		Expect(got).To(ContainElements("helpers/exec", "helpers/shebang"))
		Expect(got).NotTo(ContainElement("skills/a/scripts/run.sh"), "skill scripts are stored, not run")
	})

	It("lists the files with a shell injection", func() {
		got := content.CodeFiles(t, content.CodeSources{InjectionFiles: []string{"commands/inject.md"}})
		Expect(got).To(ContainElement("commands/inject.md"))
		Expect(got).NotTo(ContainElement("commands/plain.md"))
	})

	It("returns each file once, sorted", func() {
		got := content.CodeFiles(t, content.CodeSources{
			ConfigFiles: []string{".mcp.json", ".mcp.json"},
			Commands:    []content.Command{{Command: "${CLAUDE_PLUGIN_ROOT}/bin/fmt"}, {Command: "./bin/fmt"}},
		})
		Expect(got).To(Equal([]string{".mcp.json", "bin/data.txt", "bin/fmt", "helpers/exec", "helpers/shebang"}))
	})
})

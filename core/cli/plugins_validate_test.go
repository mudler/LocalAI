package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/fetch"
)

func writeTree(root string, files map[string]string) {
	GinkgoHelper()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
		Expect(os.WriteFile(full, []byte(body), 0o644)).To(Succeed())
	}
}

func fixturePlugin(name string) map[string]string {
	return map[string]string{
		".claude-plugin/plugin.json":   `{"name": "` + name + `", "version": "1.0.0"}`,
		"skills/" + name + "/SKILL.md": "---\nname: " + name + "\ndescription: Do it\n---\nBody",
		"commands/run.md":              "---\ndescription: Run it\n---\nRun $ARGUMENTS",
	}
}

// remoteFetcher serves the remote entries of a marketplace from fixtures.
type remoteFetcher struct{ trees map[string]map[string]string }

func (f remoteFetcher) FetchPlugin(_ context.Context, _ fetch.MarketplaceSource, _ *claudeplugin.Marketplace, e *claudeplugin.Entry, _ string) (*fetch.Fetched, error) {
	dir := GinkgoT().TempDir()
	writeTree(dir, f.trees[e.Name])
	return &fetch.Fetched{Dir: dir, SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Done: func() {}}, nil
}

var _ = Describe("local-ai plugins validate", func() {
	var out *bytes.Buffer
	BeforeEach(func() { out = &bytes.Buffer{} })

	validate := func(dir string, strict bool) error {
		c := &PluginsValidateCMD{Dir: dir, Strict: strict, output: out}
		return c.Run(nil)
	}

	It("passes the fixture plugin and prints its digest and contents", func() {
		dir := GinkgoT().TempDir()
		writeTree(dir, fixturePlugin("fmt"))
		Expect(validate(dir, true)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("ok    fmt 1.0.0"), ContainSubstring("digest:    sha256:"),
			ContainSubstring("1 skill, 1 command, 0 agents, 0 MCP servers, 0 hooks"), ContainSubstring("runs code: no"),
		))
	})

	It("passes a plugin that runs code and names the reasons and the code files", func() {
		dir := GinkgoT().TempDir()
		files := fixturePlugin("hook")
		files["hooks/hooks.json"] = `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/check.sh"}]}]}}`
		files["check.sh"] = "#!/bin/sh\nexit 0\n"
		writeTree(dir, files)
		Expect(validate(dir, false)).To(Succeed())
		Expect(out.String()).To(SatisfyAll(ContainSubstring("runs code: yes (hook-command)"), ContainSubstring("code file: check.sh")))
	})

	DescribeTable("fails each tree and parse check",
		func(change func(dir string), want string) {
			dir := GinkgoT().TempDir()
			writeTree(dir, fixturePlugin("fmt"))
			change(dir)
			Expect(validate(dir, false)).To(MatchError("the plugin failed the checks"))
			Expect(out.String()).To(SatisfyAll(ContainSubstring("FAIL"), ContainSubstring(want)))
		},
		Entry("a skill file over 64 KiB", func(dir string) {
			writeTree(dir, map[string]string{"skills/fmt/SKILL.md": "---\nname: fmt\ndescription: d\n---\n" + strings.Repeat("x", 65<<10)})
		}, "SKILL.md"),
		Entry("a link out of the plugin", func(dir string) {
			Expect(os.Symlink("/etc/hostname", filepath.Join(dir, "leak"))).To(Succeed())
		}, "leak"),
		Entry("two paths that differ only in case", func(dir string) {
			// A file system that ignores case (the default on Windows and
			// macOS) cannot hold both paths: the second write replaces the
			// first.
			probe := filepath.Join(dir, "CaseProbe")
			Expect(os.WriteFile(probe, nil, 0o600)).To(Succeed())
			_, err := os.Stat(filepath.Join(dir, "caseprobe"))
			Expect(os.Remove(probe)).To(Succeed())
			if err == nil {
				Skip("the file system ignores case")
			}
			writeTree(dir, map[string]string{"README.md": "a", "readme.md": "b"})
		}, "readme.md"),
		Entry("a plugin.json that does not parse", func(dir string) {
			writeTree(dir, map[string]string{".claude-plugin/plugin.json": "{"})
		}, "plugin.json"),
		Entry("a reserved plugin name", func(dir string) {
			writeTree(dir, map[string]string{".claude-plugin/plugin.json": `{"name": "plugin"}`})
		}, "reserved"),
	)

	It("warns on a skipped component and fails on it with --strict", func() {
		dir := GinkgoT().TempDir()
		files := fixturePlugin("fmt")
		files[".claude-plugin/plugin.json"] = `{"name": "fmt", "version": "1.0.0", "commands": ["./missing.md"]}`
		writeTree(dir, files)
		Expect(validate(dir, false)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("skipped:   command"))
		out.Reset()
		Expect(validate(dir, true)).To(MatchError("the plugin failed the checks"))
		Expect(out.String()).To(ContainSubstring("components were skipped (--strict)"))
	})

	It("checks every entry of a marketplace, relative ones from the files and the others fetched", func() {
		root := GinkgoT().TempDir()
		writeTree(root, map[string]string{
			".claude-plugin/marketplace.json": `{"name": "team", "owner": {"name": "o"}, "plugins": [
				{"name": "fmt", "source": "./plugins/fmt"},
				{"name": "bad", "source": "./plugins/bad"},
				{"name": "npmx", "source": {"source": "npm", "package": "x"}},
				{"name": "remote", "source": {"source": "github", "repo": "o/remote"}},
				{"name": "gone", "source": "./plugins/gone"}
			]}`,
		})
		for name, files := range map[string]map[string]string{"fmt": fixturePlugin("fmt"), "bad": {".claude-plugin/plugin.json": "{"}} {
			writeTree(filepath.Join(root, "plugins", name), files)
		}
		c := &PluginsValidateCMD{Marketplace: filepath.Join(root, ".claude-plugin", "marketplace.json"), output: out,
			fetcher: remoteFetcher{trees: map[string]map[string]string{"remote": fixturePlugin("remote")}}}
		err := c.Run(nil)
		Expect(err).To(MatchError("3 of 5 entries failed the checks"))
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("ok    fmt 1.0.0"), ContainSubstring("ok    remote 1.0.0"),
			ContainSubstring("FAIL  bad@team"), ContainSubstring("FAIL  npmx@team"), ContainSubstring("FAIL  gone@team"),
		))
	})

	It("passes a marketplace whose entries all pass", func() {
		root := GinkgoT().TempDir()
		writeTree(root, map[string]string{".claude-plugin/marketplace.json": `{"name": "team", "owner": {"name": "o"}, "plugins": [{"name": "fmt", "source": "./plugins/fmt"}]}`})
		writeTree(filepath.Join(root, "plugins", "fmt"), fixturePlugin("fmt"))
		c := &PluginsValidateCMD{Marketplace: filepath.Join(root, ".claude-plugin", "marketplace.json"), output: out}
		Expect(c.Run(nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("All 1 plugins of team pass."))
	})

	It("fails an entry whose metadata.localai.runsCode differs from the checks", func() {
		root := GinkgoT().TempDir()
		writeTree(root, map[string]string{
			".claude-plugin/marketplace.json": `{"name": "team", "owner": {"name": "o"}, "plugins": [
				{"name": "text", "source": "./plugins/text", "metadata": {"localai": {"runsCode": false}}},
				{"name": "hook", "source": "./plugins/hook", "metadata": {"localai": {"runsCode": false}}},
				{"name": "said", "source": "./plugins/said", "metadata": {"localai": {"runsCode": true}}},
				{"name": "plain", "source": "./plugins/plain"}
			]}`,
		})
		hook := fixturePlugin("hook")
		hook["hooks/hooks.json"] = `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/check.sh"}]}]}}`
		hook["check.sh"] = "#!/bin/sh\nexit 0\n"
		for name, files := range map[string]map[string]string{
			"text": fixturePlugin("text"), "hook": hook, "said": fixturePlugin("said"), "plain": fixturePlugin("plain"),
		} {
			writeTree(filepath.Join(root, "plugins", name), files)
		}
		c := &PluginsValidateCMD{Marketplace: filepath.Join(root, ".claude-plugin", "marketplace.json"), output: out}
		Expect(c.Run(nil)).To(MatchError("2 of 4 entries failed the checks"))
		Expect(out.String()).To(SatisfyAll(
			ContainSubstring("FAIL  hook@team: metadata.localai.runsCode is false, but the checks found code (hook-command)"),
			ContainSubstring("FAIL  said@team: metadata.localai.runsCode is true, but the checks found no code"),
			Not(ContainSubstring("FAIL  text@team")), Not(ContainSubstring("FAIL  plain@team")),
		))
	})

	It("needs a folder or a marketplace, not both", func() {
		Expect((&PluginsValidateCMD{output: out}).Run(nil)).To(MatchError(ContainSubstring("name a plugin folder")))
		Expect((&PluginsValidateCMD{Dir: ".", Marketplace: "x", output: out}).Run(nil)).To(MatchError(ContainSubstring("not both")))
	})
})

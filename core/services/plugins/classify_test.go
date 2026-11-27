package plugins_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
)

var _ = Describe("CodeReasons", func() {
	check := func(extra map[string]string) plugins.Checked {
		files := pluginFiles(textPlugin, "v1")
		for k, v := range extra {
			files[k] = v
		}
		v, err := plugins.CheckDir(writeTree(files), content.DefaultLimits(), claudeplugin.LoadOptions{})
		Expect(err).NotTo(HaveOccurred())
		return v
	}

	It("counts an MCP server with a headersHelper as code: the helper is a shell command", func() {
		v := check(map[string]string{".mcp.json": `{"mcpServers": {"api": {"type": "http", "url": "https://api.example.com/mcp", "headersHelper": "./bin/token.sh"}}}`})
		Expect(v.CodeReasons).To(ContainElement(plugins.CodeMCPHeadersHelper))
		Expect(v.Model.MCPServers).To(ContainElement(HaveField("HeadersHelper", "./bin/token.sh")))
		Expect(plugins.CodeComponents(v.Model)).To(ContainElement(ContainSubstring("./bin/token.sh")))
	})

	It("counts an http hook as code and shows its host and URL", func() {
		v := check(map[string]string{"hooks/hooks.json": `{"hooks": {"PostToolUse": [{"hooks": [{"type": "http", "url": "https://collect.example.com/hook"}]}]}}`})
		Expect(v.CodeReasons).To(ContainElement(plugins.CodeHookHTTP))
		Expect(v.Model.Hooks[0].Handlers[0].URL).To(Equal("https://collect.example.com/hook"))
		Expect(v.Model.Hosts()).To(ContainElement("collect.example.com"))
		Expect(plugins.CodeComponents(v.Model)).To(ContainElement(ContainSubstring("https://collect.example.com/hook")))
	})

	It("counts a bin/ folder that holds only a dotfile as code", func() {
		v := check(map[string]string{"bin/.tool": "#!/bin/sh\necho hi\n"})
		Expect(v.CodeReasons).To(ContainElement(claudeplugin.CodeBin))
		Expect(v.CodeFiles).To(ContainElement("bin/.tool"))
	})

	It("keeps a plain text plugin text-only", func() {
		Expect(check(nil).CodeReasons).To(BeEmpty())
	})
})

package cli

import (
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func getTestApp() *kong.Application {
	var testCLI struct {
		Run    struct{} `cmd:"" help:"Run the server"`
		Models struct {
			List    struct{} `cmd:"" help:"List models"`
			Install struct{} `cmd:"" help:"Install a model"`
		} `cmd:"" help:"Manage models"`
		Completion CompletionCMD `cmd:"" help:"Generate shell completions"`
	}

	k := kong.Must(&testCLI)
	return k.Model
}

var _ = Describe("Shell completions", func() {
	var app *kong.Application

	BeforeEach(func() {
		app = getTestApp()
	})

	Describe("generateBashCompletion", func() {
		It("generates valid bash completion script", func() {
			script := generateBashCompletion(app)
			Expect(script).To(ContainSubstring("complete -F _local_ai_completions local-ai"))
			Expect(script).To(ContainSubstring("run"))
			Expect(script).To(ContainSubstring("models"))
			Expect(script).To(ContainSubstring("completion"))
		})
	})

	Describe("generateZshCompletion", func() {
		It("generates valid zsh completion script", func() {
			script := generateZshCompletion(app)
			Expect(script).To(ContainSubstring("#compdef local-ai"))
			Expect(script).To(ContainSubstring("run"))
			Expect(script).To(ContainSubstring("models"))
		})
	})

	Describe("generateFishCompletion", func() {
		It("generates valid fish completion script", func() {
			script := generateFishCompletion(app)
			Expect(script).To(ContainSubstring("complete -c local-ai"))
			Expect(script).To(ContainSubstring("__fish_use_subcommand"))
			Expect(script).To(ContainSubstring("run"))
			Expect(script).To(ContainSubstring("models"))
		})
	})

	Describe("collectCommands", func() {
		It("collects all commands and subcommands", func() {
			cmds := collectCommands(app.Node, "")

			names := make(map[string]bool)
			for _, cmd := range cmds {
				names[cmd.fullName] = true
			}

			Expect(names).To(HaveKey("run"))
			Expect(names).To(HaveKey("models"))
			Expect(names).To(HaveKey("models list"))
			Expect(names).To(HaveKey("models install"))
		})
	})
})

package cli

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

func getTestApp() *kong.Application {
	var testCLI struct {
		Run        struct{} `cmd:"" help:"Run the server"`
		Models     struct {
			List    struct{} `cmd:"" help:"List models"`
			Install struct{} `cmd:"" help:"Install a model"`
		} `cmd:"" help:"Manage models"`
		Completion CompletionCMD `cmd:"" help:"Generate shell completions"`
	}

	k := kong.Must(&testCLI)
	return k.Model
}

func TestGenerateBashCompletion(t *testing.T) {
	app := getTestApp()
	script := generateBashCompletion(app)

	if !strings.Contains(script, "complete -F _local_ai_completions local-ai") {
		t.Error("bash completion missing complete command registration")
	}
	if !strings.Contains(script, "run") {
		t.Error("bash completion missing 'run' command")
	}
	if !strings.Contains(script, "models") {
		t.Error("bash completion missing 'models' command")
	}
	if !strings.Contains(script, "completion") {
		t.Error("bash completion missing 'completion' command")
	}
}

func TestGenerateZshCompletion(t *testing.T) {
	app := getTestApp()
	script := generateZshCompletion(app)

	if !strings.Contains(script, "#compdef local-ai") {
		t.Error("zsh completion missing compdef header")
	}
	if !strings.Contains(script, "run") {
		t.Error("zsh completion missing 'run' command")
	}
	if !strings.Contains(script, "models") {
		t.Error("zsh completion missing 'models' command")
	}
}

func TestGenerateFishCompletion(t *testing.T) {
	app := getTestApp()
	script := generateFishCompletion(app)

	if !strings.Contains(script, "complete -c local-ai") {
		t.Error("fish completion missing complete command")
	}
	if !strings.Contains(script, "__fish_use_subcommand") {
		t.Error("fish completion missing subcommand detection")
	}
	if !strings.Contains(script, "run") {
		t.Error("fish completion missing 'run' command")
	}
	if !strings.Contains(script, "models") {
		t.Error("fish completion missing 'models' command")
	}
}

func TestCollectCommands(t *testing.T) {
	app := getTestApp()
	cmds := collectCommands(app.Node, "")

	names := make(map[string]bool)
	for _, cmd := range cmds {
		names[cmd.fullName] = true
	}

	if !names["run"] {
		t.Error("missing 'run' command")
	}
	if !names["models"] {
		t.Error("missing 'models' command")
	}
	if !names["models list"] {
		t.Error("missing 'models list' subcommand")
	}
	if !names["models install"] {
		t.Error("missing 'models install' subcommand")
	}
}

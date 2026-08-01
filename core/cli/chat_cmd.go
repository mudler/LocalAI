package cli

import (
	"context"
	"os"

	chatcli "github.com/mudler/LocalAI/core/cli/chat"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

// ChatCMD runs the built-in terminal agent. Everything after the first
// positional argument is forwarded to the agent verbatim, so its own
// subcommands (plugin, skill, mcp) and their flags work unchanged. LocalAI's
// own flags must therefore come first.
type ChatCMD struct {
	Model     string `short:"m" help:"Model to use. Defaults to the only model the server offers, or asks when there are several"`
	Endpoint  string `env:"LOCALAI_CHAT_ENDPOINT" default:"http://127.0.0.1:8080" help:"LocalAI server endpoint. The /v1 path is added automatically when omitted"`
	APIKey    string `env:"LOCALAI_API_KEY,API_KEY" help:"API key to use when the LocalAI server requires authentication"`
	ConfigDir string `env:"LOCALAI_CHAT_CONFIG_DIR" help:"Directory holding the agent's config, plugins, and skills. Defaults to ~/.config/localai/chat" type:"path"`
	TraceDir  string `env:"LOCALAI_CHAT_TRACE_DIR" help:"Write a session LLM trace (NDJSON) to this directory" type:"path"`

	CLI    bool   `help:"Run in plain CLI mode instead of the full-screen interface"`
	TUI    bool   `help:"Force the full-screen interface"`
	Height string `help:"Run as an inline drop-down of this height, e.g. '40%'"`
	Tmux   bool   `help:"Run in a tmux split"`
	NoTmux bool   `name:"no-tmux" help:"Never use a tmux split, even inside tmux"`
	Init   string `help:"Print the shell integration script for Ctrl+Space (zsh, bash, or fish)"`
	Yolo   bool   `env:"LOCALAI_CHAT_YOLO" help:"Auto-approve every tool call without prompting"`

	Args []string `arg:"" optional:"" passthrough:"" help:"Arguments forwarded to the agent, e.g. 'plugin install <url>', 'skill list', 'mcp add'"`
}

func (c *ChatCMD) Run(ctx *cliContext.Context) error {
	err := chatcli.Run(context.Background(), chatcli.Options{
		Args:     c.agentArgs(),
		Endpoint: c.Endpoint,
		BaseURL:  chatAPIBaseURL(c.Endpoint),
		APIKey:   c.APIKey,
		Model:    c.Model,
		StateDir: c.ConfigDir,
		TraceDir: c.TraceDir,
		Yolo:     c.Yolo,
		In:       os.Stdin,
		Out:      os.Stdout,
		ErrOut:   os.Stderr,
	})
	// The agent explains its own failures on stderr and hands back a code, so
	// carry the code out and leave the explanation to stand alone.
	if code, reported := chatcli.ExitStatus(err); reported {
		return ExitCodeError{Code: code}
	}
	return err
}

// agentArgs rebuilds the argument vector the agent expects: LocalAI's mode
// flags are declared here for discoverability and shell completion, so they
// have to be translated back into the agent's own flag names.
func (c *ChatCMD) agentArgs() []string {
	var args []string
	if c.CLI {
		args = append(args, "--cli")
	}
	if c.TUI {
		args = append(args, "--tui")
	}
	if c.Height != "" {
		args = append(args, "--height", c.Height)
	}
	if c.Tmux {
		args = append(args, "--tmux")
	}
	if c.NoTmux {
		args = append(args, "--no-tmux")
	}
	if c.Init != "" {
		args = append(args, "--init", c.Init)
	}
	return append(args, c.Args...)
}

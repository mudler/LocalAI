package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mudler/nib/app"
	nibconfig "github.com/mudler/nib/config"
	nibtypes "github.com/mudler/nib/types"
	"golang.org/x/term"
)

// Options is everything the chat command passes down from its flags.
type Options struct {
	Args     []string // forwarded to the agent verbatim
	Endpoint string   // the server root, e.g. http://127.0.0.1:8080
	BaseURL  string   // the API base, e.g. http://127.0.0.1:8080/v1
	APIKey   string
	Model    string
	StateDir string
	TraceDir string
	Yolo     bool

	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// Run starts the agent: resolve where state lives, make sure a server is
// reachable, pick a model, then hand off to nib.
func Run(ctx context.Context, opts Options) error {
	dir, err := StateDir(opts.StateDir)
	if err != nil {
		return err
	}
	if err := EnsureStateDir(dir, opts.BaseURL); err != nil {
		return err
	}

	// Management subcommands (plugin, skill, mcp) touch only local state, so
	// they must work with no server running.
	if isManagementArgs(opts.Args) {
		return runAgent(ctx, dir, "", opts)
	}

	interactive := isTerminal(opts.In)

	models, err := Probe(ctx, opts.BaseURL, opts.APIKey)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return fmt.Errorf("the LocalAI server at %s rejected the API key. Pass --api-key or set LOCALAI_API_KEY", opts.Endpoint)
		}
		if !errors.Is(err, ErrUnreachable) {
			return err
		}

		var confirm Confirmer
		if interactive {
			confirm = func(q string) (bool, error) { return askYesNo(opts.In, opts.ErrOut, q) }
		}
		started, startErr := OfferToStart(ctx, StartOptions{
			Endpoint: opts.Endpoint,
			Confirm:  confirm,
			Stderr:   opts.ErrOut,
		})
		if startErr != nil {
			if errors.Is(startErr, ErrDeclined) {
				return fmt.Errorf("no LocalAI server at %s. Start one with 'local-ai run', or point elsewhere with --endpoint", opts.Endpoint)
			}
			return startErr
		}
		defer started.Stop()
		fmt.Fprintf(opts.ErrOut, "Started a temporary LocalAI server; it stops when you exit. Use 'local-ai run' for a persistent one.\n")

		if models, err = Probe(ctx, opts.BaseURL, opts.APIKey); err != nil {
			return err
		}
	}

	var chooser ModelChooser
	if interactive {
		chooser = func(available []string) (string, error) { return askChoice(opts.In, opts.ErrOut, available) }
	}
	model, err := ResolveModel(ModelRequest{
		Flag:       opts.Model,
		Configured: configuredModel(dir),
		Available:  models,
		StateDir:   dir,
		Choose:     chooser,
	})
	if err != nil {
		return err
	}

	return runAgent(ctx, dir, model, opts)
}

func runAgent(ctx context.Context, dir, model string, opts Options) error {
	defaults := nibtypes.Config{
		Model:    model,
		APIKey:   opts.APIKey,
		BaseURL:  opts.BaseURL,
		TraceDir: opts.TraceDir,
	}
	if opts.Yolo {
		defaults.ApprovalMode = "auto"
	}

	return app.Run(ctx, app.Options{
		Args:        opts.Args,
		ProgramName: "local-ai chat",
		BaseDir:     dir,
		Defaults:    defaults,
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       opts.In,
		Stdout:      opts.Out,
		Stderr:      opts.ErrOut,
	})
}

// isManagementArgs reports whether the forwarded arguments address nib's local
// state rather than starting a session.
func isManagementArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "plugin", "skill", "mcp":
		return true
	}
	return false
}

// configuredModel reads the model already recorded in the agent config, if any.
func configuredModel(dir string) string {
	cfg := nibconfig.LoadWith(nibconfig.LoadOptions{BaseDir: dir, SkipBareEnv: true})
	return cfg.Model
}

func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func askYesNo(in io.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N]: ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func askChoice(in io.Reader, out io.Writer, models []string) (string, error) {
	fmt.Fprintln(out, "Several models are available:")
	for i, m := range models {
		fmt.Fprintf(out, "  %d) %s\n", i+1, m)
	}
	fmt.Fprintf(out, "Pick one [1-%d]: ", len(models))

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the choice: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(models) {
		return "", fmt.Errorf("not a valid choice: %q", strings.TrimSpace(line))
	}
	return models[n-1], nil
}

package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/nib/app"
	nibcmd "github.com/mudler/nib/cmd"
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
	// ProbeTimeout bounds each check of the server. Zero means
	// defaultProbeTimeout.
	ProbeTimeout time.Duration

	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// ExitStatus reports the status the process should exit with for an agent run
// that failed, and whether err is such a failure.
//
// nib writes what went wrong to the error stream itself and hands back nothing
// but a code, so an error that satisfies this has already been explained to the
// user and must not be reported a second time. The refusal to render the
// full-screen interface into a pipe arrives this way, and it is the one a user
// is most likely to meet: it names --cli, and burying that under a second
// message would hide the fix.
func ExitStatus(err error) (int, bool) {
	var exit app.ExitError
	if errors.As(err, &exit) {
		return exit.Code, true
	}
	return 0, false
}

// shutdownSignals end the session. SIGHUP is one of them because this is a
// terminal program: once the terminal is gone there is nobody left to talk to,
// and a server started for the session has to go with it.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// shutdownContext derives a context that is cancelled when the process is
// asked to stop.
//
// Without it a signal kills this process where it stands, skipping the
// deferred Stop below, and a 'local-ai run' started for the session is
// reparented to init with nothing left that knows to shut it down. An
// interactive Ctrl+C happens to be safe already, because the child shares this
// process' foreground process group and the terminal signals all of it, but a
// SIGTERM from a supervisor or a script reaches only this process.
//
// A handler rather than SysProcAttr.Pdeathsig on the child: Pdeathsig is
// Linux-only, and in Go it is delivered when the OS thread that forked exits
// rather than when the process does, so it can fire on a perfectly healthy
// parent. Setpgid is not an alternative either, since taking the child out of
// the foreground process group is what would break the Ctrl+C that works
// today. SIGKILL stays uncovered, as it must: nothing in the process can
// observe it.
//
// It doubles as the agent's own cancellation. nib's app.Run installs no
// handler, deliberately leaving that to whoever embeds it.
func shutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, shutdownSignals...)
}

// Run starts the agent: resolve where state lives, make sure a server is
// reachable, pick a model, then hand off to nib.
func Run(ctx context.Context, opts Options) error {
	ctx, stop := shutdownContext(ctx)
	defer stop()

	p, err := prepare(ctx, opts, isTerminal(opts.In))
	if err != nil {
		return err
	}
	// A server this process started belongs to this session, and Stop is
	// nil-safe, so one defer covers both cases.
	defer p.server.Stop()

	return runAgent(ctx, p.dir, p.model, opts)
}

// preparation is what the agent needs once the environment is ready: where its
// state lives, which model to talk to, and the server this process started on
// the user's behalf, if any.
type preparation struct {
	dir    string
	model  string
	server *StartedServer
}

// prepare does everything that has to happen before the agent takes over the
// terminal. It is split out of Run because all of it is testable and none of
// what follows is: once app.Run has the terminal there is no seam left.
//
// interactive says whether there is a user to prompt. It is a parameter rather
// than a second read of opts.In so the prompts can be driven over a pipe.
func prepare(ctx context.Context, opts Options, interactive bool) (_ *preparation, err error) {
	dir, dirErr := StateDir(opts.StateDir)
	if dirErr != nil {
		return nil, dirErr
	}
	if err := EnsureStateDir(dir, opts.BaseURL); err != nil {
		return nil, err
	}

	if isLocalOnlyArgs(opts.Args) {
		return &preparation{dir: dir}, nil
	}

	// One prompter for every question this run asks; see its doc comment for
	// why the reader cannot be rebuilt per question.
	var prompts *prompter
	if interactive {
		prompts = newPrompter(opts.In, opts.ErrOut)
	}

	var started *StartedServer
	defer func() {
		// Nothing after the spawn may leave a server behind: the caller only
		// learns about it through a successful return.
		if err != nil {
			started.Stop()
		}
	}()

	models, err := probeModels(ctx, opts)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return nil, fmt.Errorf("the LocalAI server at %s rejected the API key. Pass --api-key or set LOCALAI_API_KEY", opts.Endpoint)
		}
		if !errors.Is(err, ErrUnreachable) {
			return nil, err
		}

		var confirm Confirmer
		if interactive {
			confirm = prompts.yesNo
		}
		var startErr error
		started, startErr = OfferToStart(ctx, StartOptions{
			Endpoint: opts.Endpoint,
			Confirm:  confirm,
			Stderr:   opts.ErrOut,
		})
		if startErr != nil {
			err = startErr
			if errors.Is(startErr, ErrDeclined) {
				err = fmt.Errorf("no LocalAI server at %s. Start one with 'local-ai run', or point elsewhere with --endpoint", opts.Endpoint)
			}
			return nil, err
		}
		fmt.Fprintf(opts.ErrOut, "Started a temporary LocalAI server; it stops when you exit. Use 'local-ai run' for a persistent one.\n")

		if models, err = probeModels(ctx, opts); err != nil {
			return nil, err
		}
	}

	var chooser ModelChooser
	if interactive {
		chooser = prompts.choose
	}
	model, err := ResolveModel(ModelRequest{
		Flag:       opts.Model,
		Configured: configuredModel(dir),
		Available:  models,
		StateDir:   dir,
		Choose:     chooser,
		Notify:     func(message string) { fmt.Fprintln(opts.ErrOut, message) },
	})
	if err != nil {
		return nil, err
	}

	return &preparation{dir: dir, model: model, server: started}, nil
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

// defaultProbeTimeout bounds a check of the server. Listing models is cheap,
// so this is long enough that a loaded server is never given up on and short
// enough that a hung one does not leave the user staring at nothing.
const defaultProbeTimeout = 30 * time.Second

// probeModels lists what the endpoint offers, under a budget.
func probeModels(ctx context.Context, opts Options) ([]string, error) {
	timeout := opts.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	// A real deadline rather than a cancel plus a timer. Probe reads
	// context.Canceled as "the caller gave up", which is a statement about the
	// caller and not about the endpoint, and only a deadline as "nothing
	// answered in time". Expiring the budget as a cancellation would stop
	// ErrUnreachable firing for precisely the hung servers that the offer to
	// start one exists for.
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return Probe(probeCtx, opts.BaseURL, opts.APIKey)
}

// isLocalOnlyArgs reports whether the forwarded arguments do their work
// without ever reaching a model, in which case demanding a running server (and
// offering to start one) would be an obstacle rather than a service.
//
// Two groups qualify. The management subcommands edit nib's own state: plugin,
// skill, and the mcp verbs that add or remove configured servers, which is
// asked of nib rather than restated, because bare 'mcp' and its transport
// flags do serve the agent and do need a model. The other group is the flags
// that only print something, above all --init: its shell snippet goes into an
// rc file, typically long before any server exists.
func isLocalOnlyArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	// A scan rather than a look at args[0]: the mode flags this command
	// translates are prepended, so --init is not necessarily first. Positional
	// text cannot be mistaken for a flag here, since nib ignores what is left
	// after flag parsing.
	for _, a := range args {
		switch {
		case a == "--init", a == "-init", strings.HasPrefix(a, "--init="), strings.HasPrefix(a, "-init="):
			return true
		case a == "--version", a == "-version":
			return true
		}
	}
	switch args[0] {
	case "plugin", "skill":
		return true
	case "mcp":
		return len(args) >= 2 && nibcmd.IsMCPManageSubcommand(args[1])
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

// prompter asks this run's questions on the user's terminal.
//
// It owns the buffered reader rather than wrapping opts.In per question,
// because bufio reads ahead: a throwaway reader for the "start a server?"
// question swallows the model choice that was typed behind it, and the next
// question then sees EOF. A real run asks both, one after the other.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), out: out}
}

// yesNo satisfies Confirmer. Anything that is not an explicit yes is a no, so
// a closed stream declines rather than proceeding on the user's behalf.
func (p *prompter) yesNo(question string) (bool, error) {
	fmt.Fprintf(p.out, "%s [y/N]: ", question)
	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading the answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// choose satisfies ModelChooser. It answers with a list index rather than with
// what the user typed, so the result can only ever be one of the models it was
// offered: a model name is not something to accept unvalidated here, since
// ResolveModel persists whatever comes back and every later run then starts
// against it.
func (p *prompter) choose(models []string) (string, error) {
	if len(models) == 0 {
		return "", errors.New("there is nothing to choose from")
	}
	fmt.Fprintln(p.out, "Several models are available:")
	for i, m := range models {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, m)
	}
	fmt.Fprintf(p.out, "Pick one [1-%d]: ", len(models))

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the choice: %w", err)
	}
	answer := strings.TrimSpace(line)
	n, err := strconv.Atoi(answer)
	if err != nil || n < 1 || n > len(models) {
		return "", fmt.Errorf("not a valid choice: %q. Pick a number between 1 and %d, or pass --model", answer, len(models))
	}
	return models[n-1], nil
}

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
// user and must not be reported a second time. The refusal to open a
// full-screen session on a stdin that cannot be read arrives this way, and it
// is the one a user is most likely to meet: 'echo q | local-ai chat' names
// --cli, and burying that under a second message would hide the fix.
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
// Without it a signal kills this process where it stands, skipping every
// deferred call, and a 'local-ai run' started for the session is reparented to
// init with nothing left that knows to shut it down. An interactive Ctrl+C is
// safe on its own, because the child shares this process' foreground process
// group and the terminal signals all of it, but a SIGTERM from a supervisor or
// a script reaches only this process.
//
// Since nib v0.5.1 cancelling this context does end the session: RunTUI passes
// it to bubbletea, which unwinds the program and reports the context's own
// error. The server is still stopped on cancellation rather than on the way
// out (see runSession), because registering here removes SIGHUP's default
// terminate disposition, and a guarantee about a server this process owns is
// not worth resting on how promptly a third party unwinds its interface.
//
// A handler rather than SysProcAttr.Pdeathsig on the child: Pdeathsig is
// Linux-only, and in Go it is delivered when the OS thread that forked exits
// rather than when the process does, so it can fire on a perfectly healthy
// parent. Setpgid is not an alternative either, since taking the child out of
// the foreground process group is what would break the Ctrl+C that works
// today. SIGKILL stays uncovered, as it must: nothing in the process can
// observe it.
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
	// nil-safe and idempotent, so one defer covers both cases and costs nothing
	// when runSession has already stopped it.
	defer p.server.Stop()

	return runSession(ctx, p.server, func(ctx context.Context) error {
		return runAgent(ctx, p.dir, p.model, opts)
	})
}

// runSession hands the terminal to agent, and stops a server started for this
// session as soon as the context is cancelled rather than when agent returns.
//
// The difference matters because the deferred Stop in Run is only reached once
// agent returns, and how long that takes is nib's business rather than ours.
// nib v0.5.1 does unwind the TUI on a cancelled context, so it does return; a
// SIGHUP no longer leaves the interface on screen with the server behind it,
// which it did before, when bubbletea's own SIGINT and SIGTERM handler was the
// only thing that ever quit the program and registering for SIGHUP had removed
// the default disposition that used to end the process. Watching the context
// keeps the guarantee independent of what the agent does with it.
func runSession(ctx context.Context, server *StartedServer, agent func(context.Context) error) error {
	returned := make(chan struct{})
	defer close(returned)

	go func() {
		select {
		case <-ctx.Done():
			server.Stop()
		case <-returned:
		}
	}()

	return agent(ctx)
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
		say(opts.ErrOut, "Started a temporary LocalAI server; it stops when you exit. Use 'local-ai run' for a persistent one.\n")

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
		Notify:     func(message string) { say(opts.ErrOut, "%s\n", message) },
	})
	if err != nil {
		return nil, err
	}

	return &preparation{dir: dir, model: model, server: started}, nil
}

func runAgent(ctx context.Context, dir, model string, opts Options) error {
	return app.Run(ctx, agentOptions(dir, model, opts))
}

// agentOptions builds the request handed to nib. It is split out of runAgent
// because app.Run takes the terminal and cannot be called from a test, while
// what is asked of it is exactly the part worth pinning.
//
// The stream fields are the interesting ones, and they are not symmetric.
//
// nib reads a non-nil stream as "the embedder wants this used", and refuses
// every mode but --cli when such a stream is not a terminal, because the
// full-screen interface renders on /dev/tty and would otherwise ignore it in
// silence. Nil means "not injected": nib falls back to the process stream and
// behaves as standalone nib does.
//
// Stdin is passed through as it comes. A piped or redirected stdin really is
// ignored by the interface, so the refusal is the honest answer there, and it
// is the one users meet: 'echo q | local-ai chat' says to re-run with --cli
// rather than opening a full-screen session that will never read the question.
//
// Stdout is different, and the process stream is deliberately sent as nil. The
// interface does write to stdout even when it is a pipe: that is the whole of
// nib's shell-capture idiom, out=$(local-ai chat --height 50%), which is what
// the Ctrl+Space widget emitted by --init is built on. Injecting os.Stdout
// there would refuse the widget for a stream nib was going to use anyway.
//
// The test is identity with os.Stdout rather than whether it happens to be a
// terminal, which means a shell redirect goes the same way as the widget:
// 'local-ai chat > out.txt' no longer refuses either, and renders on /dev/tty
// with the capture line landing in the file. That is not a second decision, it
// is the same one. Both are the process stdout as the shell handed it over,
// differing only in being a pipe rather than a regular file, which nib's gate
// does not look at and should not. Refusing one would refuse the other.
//
// What stays injected, and so stays subject to the refusal, is a writer some
// in-process caller chose for itself rather than inherited: a bytes.Buffer, or
// an *os.File it opened. The specs rely on that.
//
// Stderr is never gated by nib, so it is passed through unchanged.
//
// The config values go through Overrides rather than Defaults, and that is not
// a detail. Defaults are seeds: they sit BENEATH the config file, so the file
// silently undoes them. Everything here is a decision this invocation already
// made on the user's behalf, and a flag that the file can undo is not a flag.
// It was not a rare case either, since EnsureStateDir writes base_url on the
// first run and an interactive choice writes model, so from the second run on
// the file carried a value for both and --endpoint and --model did nothing.
//
// The one asymmetry to plan around is that nib cannot tell "set to the zero
// value" from "not set", so an override only ever raises a field. --yolo can
// turn approval off, but nothing on the command line can turn it back on over
// an approval_mode: auto in the file; that needs a config edit. Same shape for
// the strings, which is what makes an unset --api-key or --trace-dir leave the
// file's value standing, as it should.
//
// nib's own --trace-dir and --yolo, and their NIB_TRACE_DIR and NIB_YOLO twins,
// are resolved after the config load and so still outrank these. That is
// deliberate upstream: they are instructions to nib rather than ambient
// environment.
func agentOptions(dir, model string, opts Options) app.Options {
	// Model is the model this run resolved, which already prefers --model and
	// falls back to the file's own model, so the override restates the file's
	// value rather than fighting it whenever no flag was given.
	//
	// BaseURL is the endpoint this run probed, offered to start a server for,
	// and seeded the config with. Handing nib a different one is precisely the
	// split that made --endpoint a no-op, so the agent talks to the server
	// LocalAI checked. Pointing somewhere else for good is LOCALAI_CHAT_ENDPOINT
	// or --endpoint, not a hand-edited base_url the probe never reads.
	//
	// APIKey and TraceDir are the flags as given, empty when they were not, and
	// an empty override leaves the file alone. TraceDir is runtime-only in nib
	// (yaml:"-"), so no file value exists for it to beat today; it belongs here
	// with the other flags rather than one rung down for a reason that could
	// quietly stop being true.
	overrides := nibtypes.Config{
		Model:    model,
		APIKey:   opts.APIKey,
		BaseURL:  opts.BaseURL,
		TraceDir: opts.TraceDir,
	}
	if opts.Yolo {
		overrides.ApprovalMode = "auto"
	}

	return app.Options{
		Args:        opts.Args,
		ProgramName: "local-ai chat",
		BaseDir:     dir,
		Overrides:   overrides,
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       opts.In,
		Stdout:      ownStdout(opts.Out),
		Stderr:      opts.ErrOut,
	}
}

// ownStdout reports the writer as nib's own rather than as an injected one when
// it is the process stdout, by answering nil for it. See agentOptions for why
// that distinction is the difference between a working Ctrl+Space widget and a
// refused one.
func ownStdout(w io.Writer) io.Writer {
	if f, ok := w.(*os.File); ok && f == os.Stdout {
		return nil
	}
	return w
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

// say writes a line of interactive chatter: a question, or a notice about
// something that did not stop the session. A write that fails is not worth
// failing over, and when the terminal really is gone the read that follows the
// question says so.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
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
	say(p.out, "%s [y/N]: ", question)
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
	say(p.out, "Several models are available:\n")
	for i, m := range models {
		say(p.out, "  %d) %s\n", i+1, m)
	}
	say(p.out, "Pick one [1-%d]: ", len(models))

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

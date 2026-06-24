package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/nib/app"
	nibconfig "github.com/mudler/nib/config"
	nibtypes "github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// modelServer answers /v1/models with the given ids, as LocalAI does.
func modelServer(ids ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		Expect(json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})).To(Succeed())
	}))
}

var _ = Describe("prepare", func() {
	var (
		dir    string
		errOut *bytes.Buffer
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		errOut = &bytes.Buffer{}
	})

	// optionsFor points a run at srv, with no input to read: the default is a
	// session nobody can be asked anything in.
	optionsFor := func(srv *httptest.Server) Options {
		endpoint := "http://127.0.0.1:0"
		base := endpoint + "/v1"
		if srv != nil {
			endpoint, base = srv.URL, srv.URL+"/v1"
		}
		return Options{
			Endpoint: endpoint,
			BaseURL:  base,
			StateDir: dir,
			In:       strings.NewReader(""),
			Out:      &bytes.Buffer{},
			ErrOut:   errOut,
		}
	}

	It("uses the only model the server offers", func() {
		srv := modelServer("the-only-model")
		defer srv.Close()

		p, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("the-only-model"))
		Expect(p.dir).To(Equal(dir))
		Expect(p.server).To(BeNil(), "nothing was started, so nothing is owned")
	})

	It("seeds the agent config with the endpoint on first run", func() {
		srv := modelServer("m")
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile(ConfigPath(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(srv.URL + "/v1"))
	})

	It("lets --model win over what the server offers", func() {
		srv := modelServer("a", "b")
		defer srv.Close()

		opts := optionsFor(srv)
		opts.Model = "not-listed-yet"
		p, err := prepare(context.Background(), opts, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("not-listed-yet"))
	})

	It("advises about the API key when the server rejects it", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("--api-key"))
		Expect(err.Error()).To(ContainSubstring(srv.URL))
	})

	// Not interactive means nobody can answer the offer, so the advice has to
	// stand on its own.
	It("advises how to start a server when none is reachable", func() {
		srv := modelServer()
		url := srv.URL
		srv.Close() // nothing is listening now

		opts := optionsFor(nil)
		opts.Endpoint, opts.BaseURL = url, url+"/v1"
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai run"))
		Expect(err.Error()).To(ContainSubstring(url))
	})

	// A server that accepts the connection and then never replies is the case
	// the offer to start one exists for, so the budget has to expire as a
	// deadline: Probe reads a cancellation as "the caller gave up" and refuses
	// to call the endpoint unreachable on the strength of it.
	It("treats a server that never answers as one that is not there", func(ctx SpecContext) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)

		opts := optionsFor(srv)
		opts.ProbeTimeout = 100 * time.Millisecond
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai run"), "want the offer-a-server advice, got %v", err)
	}, SpecTimeout(30*time.Second))

	It("asks which model to use and remembers the answer", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		opts := optionsFor(srv)
		opts.In = strings.NewReader("2\n")
		p, err := prepare(context.Background(), opts, true)
		Expect(err).ToNot(HaveOccurred())
		// The list is sorted before it is shown, so 2 is zeta, not the second
		// thing the server happened to name.
		Expect(p.model).To(Equal("zeta"))
		Expect(errOut.String()).To(ContainSubstring("1) alpha"))
		Expect(errOut.String()).To(ContainSubstring("2) zeta"))

		data, err := os.ReadFile(ConfigPath(dir))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("zeta"))
	})

	// The choice is prompted for once and remembered. When remembering it fails
	// the user is about to be asked again on every future run, so they have to
	// be told here: a log line is invisible at the default log level.
	It("says so on the prompt when the choice cannot be remembered", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		// A directory where the config file belongs: writable state dir,
		// unwritable config, on any platform and as any user.
		Expect(os.MkdirAll(ConfigPath(dir), 0o700)).To(Succeed())

		opts := optionsFor(srv)
		opts.In = strings.NewReader("1\n")
		p, err := prepare(context.Background(), opts, true)

		// Failing to remember the choice must not cost the user their session.
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("alpha"))
		Expect(errOut.String()).To(ContainSubstring("could not be saved"), "the user has to learn they will be asked again")
	})

	It("does not ask again once a model is recorded", func() {
		srv := modelServer("zeta", "alpha")
		defer srv.Close()

		Expect(PersistModel(dir, "alpha")).To(Succeed())

		opts := optionsFor(srv)
		opts.In = strings.NewReader("") // an answer would have nothing to read
		p, err := prepare(context.Background(), opts, true)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.model).To(Equal("alpha"))
		Expect(errOut.String()).To(BeEmpty())
	})

	It("says what to install when the server has no models", func() {
		srv := modelServer()
		defer srv.Close()

		_, err := prepare(context.Background(), optionsFor(srv), false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("models install"))
	})

	Describe("arguments that only touch local state", func() {
		unreachable := func(args ...string) Options {
			opts := optionsFor(nil) // port 0: nothing can ever answer here
			opts.Args = args
			return opts
		}

		DescribeTable("skips the server entirely",
			func(args ...string) {
				p, err := prepare(context.Background(), unreachable(args...), false)
				Expect(err).ToNot(HaveOccurred())
				Expect(p.model).To(BeEmpty())
				Expect(p.server).To(BeNil())
			},
			Entry("plugin", "plugin", "list"),
			Entry("skill", "skill", "list"),
			Entry("mcp add", "mcp", "add", "srv"),
			Entry("mcp list", "mcp", "list"),
			// The shell snippet is what a user puts in their rc file, long
			// before any server exists.
			Entry("the shell integration script", "--init", "zsh"),
			Entry("the version", "--version"),
		)

		// Bare 'mcp' and its transport flags serve the agent over MCP, so they
		// need a model like any other session. Only the verbs that edit the
		// configured servers are local.
		DescribeTable("still needs a server",
			func(args ...string) {
				_, err := prepare(context.Background(), unreachable(args...), false)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("local-ai run"))
			},
			Entry("mcp over stdio", "mcp", "--stdio"),
			Entry("bare mcp", "mcp"),
		)
	})

	// A reader per question would read ahead into a buffer it then discards, so
	// the second question would see EOF whenever both answers were typed ahead.
	// That is the shape of a real run: the offer to start a server is followed
	// by the model prompt.
	It("keeps reading answers from the same stream across questions", func() {
		out := &bytes.Buffer{}
		p := newPrompter(strings.NewReader("y\n2\n"), out)

		yes, err := p.yesNo("Start one now?")
		Expect(err).ToNot(HaveOccurred())
		Expect(yes).To(BeTrue())

		chosen, err := p.choose([]string{"alpha", "zeta"})
		Expect(err).ToNot(HaveOccurred())
		Expect(chosen).To(Equal("zeta"))
	})

	// Whatever the chooser returns is persisted and used for every later run,
	// so an answer that is not one of the offered models must never come back
	// as one.
	Describe("the model prompt", func() {
		offered := []string{"alpha", "zeta"}

		DescribeTable("refuses an answer that is not one of the numbers shown",
			func(answer string) {
				chosen, err := newPrompter(strings.NewReader(answer), &bytes.Buffer{}).choose(offered)
				Expect(err).To(HaveOccurred())
				Expect(chosen).To(BeEmpty())
			},
			Entry("nothing at all", ""),
			Entry("a blank line", "\n"),
			Entry("only spaces", "   \n"),
			Entry("zero", "0\n"),
			Entry("past the end", "3\n"),
			Entry("negative", "-1\n"),
			Entry("a model name", "zeta\n"),
			Entry("a number with a suffix", "1x\n"),
		)

		It("says how to answer when the answer was not a number", func() {
			_, err := newPrompter(strings.NewReader("banana\n"), &bytes.Buffer{}).choose(offered)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("between 1 and 2"))
			Expect(err.Error()).To(ContainSubstring("--model"))
		})

		It("returns the model shown against the number", func() {
			chosen, err := newPrompter(strings.NewReader("1\n"), &bytes.Buffer{}).choose(offered)
			Expect(err).ToNot(HaveOccurred())
			Expect(chosen).To(Equal("alpha"))
		})

		It("refuses to ask when there is nothing to offer", func() {
			chosen, err := newPrompter(strings.NewReader("1\n"), &bytes.Buffer{}).choose(nil)
			Expect(err).To(HaveOccurred())
			Expect(chosen).To(BeEmpty())
		})
	})

	// A server started for this session is stopped by a deferred call, which a
	// signal skips: the process dies where it stands and leaves 'local-ai run'
	// reparented to init.
	Describe("shutdown signals", func() {
		It("ends the session when the terminal goes away", func() {
			ctx, stop := shutdownContext(context.Background())
			defer stop()

			self, err := os.FindProcess(os.Getpid())
			Expect(err).ToNot(HaveOccurred())
			Expect(self.Signal(syscall.SIGHUP)).To(Succeed())

			Eventually(ctx.Done()).WithTimeout(5 * time.Second).Should(BeClosed())
			Expect(ctx.Err()).To(MatchError(context.Canceled))
		})

		// SIGINT and SIGTERM cannot be delivered here to prove the same thing:
		// Ginkgo registers for both to abort the suite, and a signal goes to
		// every registered listener.
		It("also listens for an interrupt and a terminate", func() {
			Expect(shutdownSignals).To(ContainElements(os.Signal(os.Interrupt), os.Signal(syscall.SIGTERM)))
		})
	})

	// Cancelling the context does unwind nib's TUI since v0.5.1, but how long
	// that takes is nib's business, and the deferred Stop in Run is only reached
	// once the agent returns. A server this process started is ours to end, so
	// the guarantee is made here instead, where it does not depend on the agent
	// at all. Before v0.5.1 there was no guarantee to be had on the SIGHUP path:
	// bubbletea's own SIGINT and SIGTERM handler was the only thing that ever
	// quit the program, and registering for SIGHUP took away the default
	// disposition that used to end the process.
	Describe("runSession", func() {
		It("stops the session's server on cancellation, without waiting for the agent", func() {
			server, proc := stoppableServer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := runSession(ctx, server, func(ctx context.Context) error {
				cancel()
				Eventually(func() int32 { return proc.interrupts.Load() }).
					WithTimeout(5 * time.Second).
					Should(BeNumerically(">", 0), "the server has to be stopped while the agent is still running")
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(proc.lastSignal.Load()).To(Equal(os.Interrupt))
		})

		It("leaves the server alone for as long as the session lasts", func() {
			server, proc := stoppableServer()

			Expect(runSession(context.Background(), server, func(context.Context) error {
				return nil
			})).To(Succeed())
			Expect(proc.interrupts.Load()).To(BeZero())
			Expect(proc.kills.Load()).To(BeZero())
		})

		It("returns what the agent returned", func() {
			failed := errors.New("the agent gave up")
			server, _ := stoppableServer()

			Expect(runSession(context.Background(), server, func(context.Context) error {
				return failed
			})).To(MatchError(failed))
		})

		// Most sessions run against a server the user already had, and there is
		// nothing to stop then.
		It("copes with a session that started no server", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			Expect(runSession(ctx, nil, func(context.Context) error {
				return nil
			})).To(Succeed())
		})
	})

	// Which streams reach nib decides two user-visible behaviours at once, and
	// they pull in opposite directions, so both are pinned here rather than left
	// to whoever next edits the literal.
	//
	// nib refuses every mode but --cli when a stream it was handed is not a
	// terminal. That refusal is wanted for stdin, where it is what tells someone
	// piping a question to re-run with --cli. It is not wanted for the process
	// stdout, where it would refuse the Ctrl+Space widget that --init emits:
	// out=$(local-ai chat --height 50%) puts a pipe on stdout by construction,
	// and writing the chosen command into that pipe is the entire point.
	Describe("agentOptions", func() {
		// optionsWithStreams is a request that differs from the next only in
		// what it was told to read and write.
		optionsWithStreams := func(in io.Reader, out, errOut io.Writer) Options {
			return Options{
				BaseURL: "http://127.0.0.1:8080/v1",
				In:      in,
				Out:     out,
				ErrOut:  errOut,
			}
		}

		Describe("stdout", func() {
			// The regression this exists to catch: reinstating
			// 'Stdout: opts.Out' breaks Ctrl+Space and nothing else notices.
			It("hands nib nothing for the process stdout, so the capture widget is not refused", func() {
				o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr))
				Expect(o.Stdout).To(BeNil(), "injecting os.Stdout is what refuses out=$(local-ai chat)")
			})

			It("keeps a stdout the caller chose, which the refusal still guards", func() {
				out := &bytes.Buffer{}
				o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, out, os.Stderr))
				Expect(o.Stdout).To(BeIdenticalTo(out))
			})

			// Being an *os.File is not what makes a stream nib's own; being the
			// process stdout is. This is a file an in-process caller opened for
			// itself, not one a shell redirect handed over as stdout, which
			// still arrives as os.Stdout and is still nil-ed. It was never going
			// to receive the interface, so it stays injected and stays refused.
			It("keeps a file that is not the process stdout", func() {
				f, err := os.CreateTemp(GinkgoT().TempDir(), "captured")
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(f.Close)

				o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, f, os.Stderr))
				Expect(o.Stdout).To(BeIdenticalTo(f))
			})
		})

		Describe("stdin", func() {
			// The opposite regression: nilling stdin the way stdout is nilled
			// would silently drop the refusal that names --cli.
			It("hands the process stdin over, so a piped session is still refused", func() {
				o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr))
				Expect(o.Stdin).To(BeIdenticalTo(os.Stdin))
			})

			It("hands over a stdin the caller chose", func() {
				in := strings.NewReader("a question")
				o := agentOptions(dir, "a-model", optionsWithStreams(in, os.Stdout, os.Stderr))
				Expect(o.Stdin).To(BeIdenticalTo(in))
			})
		})

		// nib gates stdin and stdout and nothing else, so there is no reason to
		// hide the error stream from it.
		It("hands the error stream over whatever it is", func() {
			errOut := &bytes.Buffer{}
			o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, errOut))
			Expect(o.Stderr).To(BeIdenticalTo(errOut))

			o = agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr))
			Expect(o.Stderr).To(BeIdenticalTo(os.Stderr))
		})

		It("names the command a user would type, not the binary nib ships as", func() {
			o := agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr))
			Expect(o.ProgramName).To(Equal("local-ai chat"),
				"the --init widget invokes this name, so a user has to be able to run it")
		})

		It("carries the resolved session through to nib", func() {
			opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
			opts.Args = []string{"--cli"}
			opts.APIKey = "a-key"
			opts.TraceDir = "/traces"

			o := agentOptions(dir, "the-model", opts)
			Expect(o.Args).To(Equal([]string{"--cli"}))
			Expect(o.BaseDir).To(Equal(dir))
			Expect(o.Overrides.Model).To(Equal("the-model"))
			Expect(o.Overrides.APIKey).To(Equal("a-key"))
			Expect(o.Overrides.BaseURL).To(Equal("http://127.0.0.1:8080/v1"))
			Expect(o.Overrides.TraceDir).To(Equal("/traces"))
			// The model and the server are settled before nib starts, and the
			// bare MODEL and API_KEY variables belong to some other tool.
			Expect(o.SkipSetup).To(BeTrue())
			Expect(o.SkipBareEnv).To(BeTrue())
		})

		// Defaults sit beneath the config file. Anything routed through them is
		// accepted from the command line and then thrown away the moment the
		// file carries the same key, which is the normal state rather than an
		// edge case. Nothing this command resolves belongs there, so the channel
		// stays empty and this says so: it is what fails if the block is moved
		// back a rung.
		It("seeds nothing, because a seed is not a flag", func() {
			opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
			opts.APIKey = "a-key"
			opts.TraceDir = "/traces"
			opts.Yolo = true

			Expect(agentOptions(dir, "the-model", opts).Defaults).To(Equal(nibtypes.Config{}),
				"Defaults lose to the config file, so a value placed there is a flag that does nothing")
		})

		It("asks for automatic approval only when --yolo was given", func() {
			opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
			Expect(agentOptions(dir, "a-model", opts).Overrides.ApprovalMode).To(BeEmpty())

			opts.Yolo = true
			Expect(agentOptions(dir, "a-model", opts).Overrides.ApprovalMode).To(Equal("auto"))
		})

		// The specs above pin what is handed over. These pin what nib does with
		// it, which is the part that was wrong: every value below reached
		// app.Options intact and was then discarded by the config load, so a
		// spec that stops at the struct cannot see the bug. Resolving the config
		// the way app.Run resolves it can.
		Describe("the config nib actually resolves", func() {
			// writeConfig puts a config file where nib will read it, with values
			// that disagree with every flag under test.
			writeConfig := func(body string) {
				Expect(os.WriteFile(ConfigPath(dir), []byte(body), 0o600)).To(Succeed())
			}

			// resolve loads the config exactly as app.Run does, so the precedence
			// under test is nib's own rather than a restatement of it here.
			resolve := func(o app.Options) nibtypes.Config {
				return nibconfig.LoadWith(nibconfig.LoadOptions{
					BaseDir:     o.BaseDir,
					Defaults:    o.Defaults,
					Overrides:   o.Overrides,
					SkipBareEnv: o.SkipBareEnv,
				})
			}

			It("sends the requests to the endpoint the flag named, not the one on disk", func() {
				writeConfig("base_url: http://127.0.0.1:9999/v1\n")

				opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
				opts.BaseURL = "http://127.0.0.1:8080/v1"

				cfg := resolve(agentOptions(dir, "a-model", opts))
				Expect(cfg.BaseURL).To(Equal("http://127.0.0.1:8080/v1"),
					"--endpoint probed 8080; every turn has to go there too")
			})

			It("uses the model the flag named, not the one the picker recorded", func() {
				writeConfig("model: recorded-model\n")

				cfg := resolve(agentOptions(dir, "flag-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)))
				Expect(cfg.Model).To(Equal("flag-model"))
			})

			It("uses the key the flag named, not the one nib saved", func() {
				writeConfig("api_key: saved-key\n")

				opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
				opts.APIKey = "flag-key"

				cfg := resolve(agentOptions(dir, "a-model", opts))
				Expect(cfg.APIKey).To(Equal("flag-key"))
			})

			It("turns approval off for --yolo even when the file demands it", func() {
				writeConfig("approval_mode: prompt\n")

				opts := optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)
				opts.Yolo = true

				cfg := resolve(agentOptions(dir, "a-model", opts))
				Expect(cfg.ApprovalMode).To(Equal("auto"))
			})

			// The other half of the same rule, and the reason an unset flag is
			// not a demand for the empty string: an override only ever raises a
			// field, so what the user configured survives a run that said
			// nothing about it.
			It("leaves what the file configured alone when no flag was given", func() {
				writeConfig("api_key: saved-key\napproval_mode: prompt\n")

				cfg := resolve(agentOptions(dir, "a-model", optionsWithStreams(os.Stdin, os.Stdout, os.Stderr)))
				Expect(cfg.APIKey).To(Equal("saved-key"))
				Expect(cfg.ApprovalMode).To(Equal("prompt"))
			})
		})
	})

	// nib reports its own failures on the error stream and returns nothing but
	// a status, so anything that reaches here as one has already been explained
	// once. The refusal to open a full-screen session on a stdin that cannot be
	// read is the one users meet: 'echo q | local-ai chat' names --cli, and a
	// second message on top would bury the fix.
	Describe("ExitStatus", func() {
		It("recognises a status the agent already explained", func() {
			code, reported := ExitStatus(app.ExitError{Code: 2})
			Expect(reported).To(BeTrue())
			Expect(code).To(Equal(2))
		})

		It("finds one that has been wrapped", func() {
			code, reported := ExitStatus(fmt.Errorf("running the agent: %w", app.ExitError{Code: 1}))
			Expect(reported).To(BeTrue())
			Expect(code).To(Equal(1))
		})

		It("leaves an ordinary failure to be reported", func() {
			_, reported := ExitStatus(errors.New("no LocalAI server at http://127.0.0.1:8080"))
			Expect(reported).To(BeFalse())
		})

		It("says nothing about a run that succeeded", func() {
			_, reported := ExitStatus(nil)
			Expect(reported).To(BeFalse())
		})
	})

	It("reports a state dir it cannot create", func() {
		blocked := filepath.Join(dir, "a-file")
		Expect(os.WriteFile(blocked, []byte("not a dir"), 0o600)).To(Succeed())

		opts := optionsFor(nil)
		opts.StateDir = filepath.Join(blocked, "chat")
		_, err := prepare(context.Background(), opts, false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("agent state dir"))
	})
})

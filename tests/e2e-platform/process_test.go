package e2eplatform_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// run runs the local-ai binary to completion and returns its combined output.
func run(timeout time.Duration, env map[string]string, args ...string) (string, error) {
	cmd := exec.Command(localAIBin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + workDir}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	Expect(cmd.Start()).To(Succeed())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return ansi.ReplaceAllString(buf.String(), ""), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return ansi.ReplaceAllString(buf.String(), ""), fmt.Errorf("timeout after %s", timeout)
	}
}

func freeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

var _ = Describe("The process and the command line", Label("platform", "cli"), func() {
	Describe("help", func() {
		It("lists no option of the removed agent pool, jobs or skills in run --help or in the global help", func() {
			for _, args := range [][]string{{"run", "--help"}, {"--help"}, {"agent-worker", "--help"}} {
				out, err := run(60*time.Second, nil, args...)
				Expect(err).ToNot(HaveOccurred(), out)
				for _, gone := range []string{"AGENT_POOL", "agent-pool", "AGENT_HUB", "LOCALAI_DISABLE_AGENTS",
					"LOCALAI_API_TOKEN", "LOCALAI_AGENT_SUBJECT", "LOCALAI_AGENT_QUEUE", "localagi", "LocalAGI"} {
					Expect(out).NotTo(ContainSubstring(gone), strings.Join(args, " ")+" shows a removed option")
				}
			}
		})

		It("lists the collections options under their new names and the commands that remain", func() {
			out, err := run(60*time.Second, nil, "run", "--help")
			Expect(err).ToNot(HaveOccurred(), out)
			for _, want := range []string{"--disable-collections", "LOCALAI_COLLECTIONS_STATE_DIR", "LOCALAI_COLLECTIONS_VECTOR_ENGINE", "--pprof"} {
				Expect(out).To(ContainSubstring(want))
			}
			top, err := run(60*time.Second, nil, "--help")
			Expect(err).ToNot(HaveOccurred(), top)
			Expect(top).To(MatchRegexp(`(?m)^\s+chat `))
			Expect(top).To(MatchRegexp(`(?m)^\s+agent-worker `))
			Expect(top).NotTo(MatchRegexp(`(?m)^\s+agent \[`), "the removed `local-ai agent` command is still listed")
		})

		It("does not describe the data path as holding agent state or jobs", func() {
			out, err := run(60*time.Second, nil, "run", "--help")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).NotTo(MatchRegexp(`(?i)agent state, tasks, jobs`))
		})
	})

	Describe("the default start", Ordered, func() {
		var in *instance

		BeforeAll(func() {
			in = startInstance(startOpts{Name: "defaults"})
		})

		It("boots with the defaults, logs no error while idle and serves the chats API with no database", func() {
			Expect(in.logs()).NotTo(MatchRegexp(`(?m)^\S+ \d+ \S+\s+ERROR`), "an error was logged during a plain start")
			out := in.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			Expect(out).To(HaveKeyWithValue("persistent", true))
			Expect(out).To(HaveKeyWithValue("files", true))
		})

		It("answers the removed APIs with 404 and keeps the new ones", func() {
			for _, p := range []string{"/api/agents", "/api/agents/skills", "/api/conversations"} {
				code, out := in.api.do(http.MethodGet, p, nil)
				Expect(code).To(Equal(http.StatusNotFound), p+": "+string(out))
			}
			// The agent jobs stayed: the tasks and jobs routes answer.
			for _, p := range []string{"/api/agent/tasks", "/api/agent/jobs"} {
				code, out := in.api.do(http.MethodGet, p, nil)
				Expect(code).To(Equal(http.StatusOK), p+": "+string(out))
			}
		})

		It("opens no profiling socket unless asked", func() {
			resp, err := http.Get(in.api.base + "/debug/pprof/") //nolint:noctx // a probe
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).NotTo(Equal(http.StatusOK))
		})
	})

	Describe("options of the removed agent runtime", func() {
		It("accepts them, starts, and logs one warning that names the ones that were set", func() {
			in := startInstance(startOpts{Name: "removed", Env: map[string]string{
				"LOCALAI_AGENT_POOL_TIMEOUT":       "5m",
				"LOCALAI_AGENT_POOL_ENABLE_SKILLS": "true",
				"LOCALAI_AGENT_HUB_URL":            "https://example.invalid/hub",
			}})
			in.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			var warns []string
			for _, l := range strings.Split(in.logs(), "\n") {
				if strings.Contains(l, "were removed with the old agents") {
					warns = append(warns, l)
				}
			}
			Expect(warns).To(HaveLen(1), in.logs())
			for _, name := range []string{"LOCALAI_AGENT_POOL_TIMEOUT", "LOCALAI_AGENT_POOL_ENABLE_SKILLS", "LOCALAI_AGENT_HUB_URL"} {
				Expect(warns[0]).To(ContainSubstring(name))
			}
			Expect(warns[0]).NotTo(ContainSubstring("example.invalid"), "the warning must name the options, not their values")
		})

		It("accepts them as flags of the agent worker too", func() {
			out, err := run(20*time.Second, map[string]string{"LOCALAI_AGENT_SUBJECT": "x", "LOCALAI_API_TOKEN": "secret-token-value"}, "agent-worker", "--help")
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	Describe("the agent worker", func() {
		It("refuses to start without NATS and a frontend to register to, with a clear message and a non-zero exit", func() {
			out, err := run(30*time.Second, nil, "agent-worker")
			Expect(err).To(HaveOccurred())
			Expect(out).To(ContainSubstring("nats-url"))
			Expect(out).To(ContainSubstring("register-to"))
			Expect(out).NotTo(ContainSubstring("panic"))
		})

		It("keeps retrying its registration with a growing delay when the frontend is not distributed, and stops on a signal without a panic", func() {
			in := startInstance(startOpts{Name: "worker-frontend"})
			cmd := exec.Command(localAIBin, "agent-worker", "--nats-url", "nats://127.0.0.1:1", "--register-to", in.api.base)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + workDir}
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			Expect(cmd.Start()).To(Succeed())
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			DeferCleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			Eventually(func() string { return ansi.ReplaceAllString(buf.String(), "") }, 15*time.Second, 200*time.Millisecond).
				Should(And(ContainSubstring("Starting agent worker"), ContainSubstring("Registration failed, retrying")))
			select {
			case err := <-done:
				Fail(fmt.Sprintf("the worker exited by itself: %v\n%s", err, buf.String()))
			default:
			}
			Expect(syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)).To(Succeed())
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				Fail("the worker did not stop on SIGTERM")
			}
			Expect(buf.String()).NotTo(ContainSubstring("panic:"))
		})
	})

	Describe("the terminal agent", func() {
		It("reaches the server, sends the message and runs the tool calls of the model", func() {
			in := startInstance(startOpts{Name: "chatcli"})
			home := filepath.Join(in.dir, "home")
			Expect(os.MkdirAll(home, 0o750)).To(Succeed())
			cmd := exec.Command(localAIBin, "chat", "--cli", "-m", "mock-model", "--endpoint", in.api.base, "--config-dir", filepath.Join(home, "cfg"), "--yolo")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
			stdin, err := cmd.StdinPipe()
			Expect(err).ToNot(HaveOccurred())
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			Expect(cmd.Start()).To(Succeed())
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			DeferCleanup(func() { _ = cmd.Process.Kill() })
			_, _ = stdin.Write([]byte("say hello\n"))
			screen := func() string { return regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`).ReplaceAllString(buf.String(), "") }
			Eventually(screen, 60*time.Second, 250*time.Millisecond).Should(ContainSubstring("agent_logs"), screen())
			// The mock model answers every request that has tools with a tool call, so the
			// agent loops on tools; what this checks is that the agent reaches the server.
			Expect(in.logs()).To(MatchRegexp(`path="/v1/chat/completions" status=200`))
			_, _ = stdin.Write([]byte("exit\n"))
			_ = stdin.Close()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				Fail("the terminal agent did not exit on `exit`")
			}
		})

		It("says plainly that no server answers", func() {
			out, err := run(30*time.Second, map[string]string{"HOME": workDir}, "chat", "--cli", "--endpoint", "http://"+freeAddr(), "--config-dir", filepath.Join(workDir, "chatcfg"))
			Expect(out).To(ContainSubstring("no LocalAI server"))
			_ = err
		})
	})

	Describe("diagnostics", func() {
		It("serves the profiling listener on a loopback address only, hides the command line, and keeps it off the API port", func() {
			addr := freeAddr()
			in := startInstance(startOpts{Name: "pprof", Args: []string{"--pprof", "--pprof-address", addr}})
			resp, err := http.Get("http://" + addr + "/debug/pprof/") //nolint:noctx // a probe
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			resp, err = http.Get("http://" + addr + "/debug/pprof/cmdline") //nolint:noctx // a probe
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
			resp, err = http.Get(in.api.base + "/debug/pprof/") //nolint:noctx // a probe
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).NotTo(Equal(http.StatusOK))
		})

		It("refuses a profiling address that is not numeric loopback, and sampling without --pprof", func() {
			for name, args := range map[string][]string{
				"hostname":     {"--pprof", "--pprof-address", "localhost:6060"},
				"wildcard":     {"--pprof", "--pprof-address", "0.0.0.0:6060"},
				"no port":      {"--pprof", "--pprof-address", "127.0.0.1"},
				"mutex alone":  {"--pprof-mutex-profile-fraction", "5"},
				"negative":     {"--pprof", "--pprof-block-profile-rate", "-1"},
				"invalid even": {"--pprof-address", "example.com:80"},
			} {
				in := startInstance(startOpts{Name: "pprof-bad", Args: args, NoReady: true})
				Eventually(func() bool { _, done := in.exited(); return done }, 60*time.Second, 200*time.Millisecond).Should(BeTrue(), name)
				err, _ := in.exited()
				Expect(err).To(HaveOccurred(), name)
				Expect(in.logs()).To(MatchRegexp(`(?i)pprof|profil|diagnostic|loopback|sampling`), name)
			}
		})

		It("emits phase timing events only when asked", func() {
			in := startInstance(startOpts{Name: "timing", Args: []string{"--request-phase-timing"}})
			in.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			in.api.do(http.MethodGet, "/v1/models", nil)
			Eventually(in.logs, 10*time.Second, 200*time.Millisecond).Should(ContainSubstring("diagnostic_phase"))
		})
	})

	Describe("the log level", func() {
		It("honours --log-level: error keeps the log quiet, info drops debug lines", func() {
			quiet := startInstance(startOpts{Name: "lvl-error", Args: []string{"--log-level", "error"}})
			quiet.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			Expect(quiet.logs()).NotTo(MatchRegexp(`(?m)\s(INFO|DEBUG|TRACE)\s`))
			quiet.stop()

			info := startInstance(startOpts{Name: "lvl-info", Args: []string{"--log-level", "info"}})
			info.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			Expect(info.logs()).To(MatchRegexp(`(?m)\sINFO\s`))
			Expect(info.logs()).NotTo(MatchRegexp(`(?m)\s(DEBUG|TRACE)\s`))
		})

		It("also honours LOCALAI_LOG_LEVEL and the json format", func() {
			in := startInstance(startOpts{Name: "lvl-env", Args: []string{"--log-format", "json"}, Env: map[string]string{"LOCALAI_LOG_LEVEL": "warn"}})
			in.api.mustJSON(http.MethodGet, "/api/chats", nil, http.StatusOK)
			Expect(in.logs()).NotTo(MatchRegexp(`"level":"(info|debug)"|\sINFO\s|\sDEBUG\s`))
		})
	})
})

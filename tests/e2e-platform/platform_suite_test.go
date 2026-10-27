// Package e2eplatform_test runs the real local-ai binary and drives the
// platform features of the chats release through HTTP, the CLI and real
// child processes: MCP connectors and apps, collections, the process flags
// and the permission of every route. Only external services are stubbed
// (the model is the mock backend, MCP servers are tests/e2e-platform/mcpfixture).
//
// Run it with `make test-e2e-platform`. Set LOCALAI_E2E_BINARY to use a
// binary you built (with -tags auth); otherwise the suite builds one.
package e2eplatform_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestPlatformE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Platform e2e")
}

var (
	repoRoot    string
	workDir     string
	localAIBin  string
	mockBackend string
	fixtureBin  string
)

// build compiles a Go package of the repository into the work directory.
func build(out, pkg string, tags ...string) string {
	args := []string{"build"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "-o", out, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	o, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), string(o))
	return out
}

var _ = BeforeSuite(func() {
	wd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	repoRoot = filepath.Join(wd, "..", "..")
	if dir := os.Getenv("LOCALAI_E2E_WORKDIR"); dir != "" {
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		workDir = dir
	} else {
		workDir, err = os.MkdirTemp("", "e2e-platform-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(workDir) })
	}
	localAIBin = os.Getenv("LOCALAI_E2E_BINARY")
	if localAIBin == "" {
		localAIBin = build(filepath.Join(workDir, "local-ai"), "./cmd/local-ai", "auth")
	}
	mockBackend = build(filepath.Join(workDir, "mock-backend"), "./tests/e2e/mock-backend")
	fixtureBin = build(filepath.Join(workDir, "mcpfixture"), "./tests/e2e-platform/mcpfixture")
})

// api is a small HTTP client for one identity.
type api struct {
	base  string
	token string
	hc    *http.Client
}

func newAPI(base string) *api {
	jar, _ := cookiejar.New(nil)
	return &api{base: base, hc: &http.Client{Jar: jar, Timeout: 150 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (a *api) withToken(tok string) *api {
	b := newAPI(a.base)
	b.token = tok
	return b
}

// do sends a request; body is JSON-encoded unless it is nil, a string or bytes.
func (a *api) do(method, path string, body any) (int, []byte) {
	code, out, _ := a.doH(method, path, body, nil)
	return code, out
}

func (a *api) doH(method, path string, body any, hdr map[string]string) (int, []byte, http.Header) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		Expect(err).ToNot(HaveOccurred())
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.base+path, rd)
	Expect(err).ToNot(HaveOccurred())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.hc.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return resp.StatusCode, out, resp.Header
}

type obj = map[string]any

func decode(raw []byte) obj {
	var m obj
	ExpectWithOffset(1, json.Unmarshal(raw, &m)).To(Succeed(), string(raw))
	return m
}

func (a *api) mustJSON(method, path string, body any, want int) obj {
	code, out := a.do(method, path, body)
	ExpectWithOffset(1, code).To(Equal(want), method+" "+path+": "+string(out))
	if len(out) == 0 {
		return obj{}
	}
	return decode(out)
}

// instance is one running local-ai process.
type instance struct {
	api  *api
	dir  string
	log  string
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	exit error
}

type startOpts struct {
	// Name is the directory of the instance under the work directory.
	Name string
	// Args are added after the standard flags.
	Args []string
	// Env is added to a minimal environment (PATH and HOME only).
	Env map[string]string
	// Auth turns on LocalAI authentication.
	Auth bool
	// NoReady skips the wait for the readiness probe.
	NoReady bool
	// Keep starts again on the directory of an earlier instance of the same name.
	Keep bool
	// Models are extra model configs: file name to YAML.
	Models map[string]string
}

// listenPort returns the port of the instance: LOCALAI_E2E_PORT when set (the
// runs of one instance never overlap), else a free one.
func listenPort() int {
	if p := os.Getenv("LOCALAI_E2E_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		Expect(err).ToNot(HaveOccurred())
		return n
	}
	p, err := freeport.GetFreePort()
	Expect(err).ToNot(HaveOccurred())
	return p
}

// The context size is large enough for the tool schemas of the terminal agent.
const mockModel = "name: mock-model\nbackend: mock-backend\ncontext_size: 32768\nparameters:\n  model: mock-model.bin\n"

func startInstance(o startOpts) *instance {
	dir := filepath.Join(workDir, o.Name)
	if !o.Keep {
		Expect(os.RemoveAll(dir)).To(Succeed())
	}
	for _, d := range []string{"models", "data", "backends", "cfg"} {
		Expect(os.MkdirAll(filepath.Join(dir, d), 0o750)).To(Succeed())
	}
	Expect(os.WriteFile(filepath.Join(dir, "models", "mock-model.yaml"), []byte(mockModel), 0o600)).To(Succeed())
	for name, body := range o.Models {
		Expect(os.WriteFile(filepath.Join(dir, "models", name), []byte(body), 0o600)).To(Succeed())
	}
	port := listenPort()
	args := []string{"run", "--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--models-path", filepath.Join(dir, "models"), "--data-path", filepath.Join(dir, "data"),
		"--backends-path", filepath.Join(dir, "backends"), "--localai-config-dir", filepath.Join(dir, "cfg"),
		"--generated-content-path", filepath.Join(dir, "gen"), "--upload-path", filepath.Join(dir, "up"),
		"--external-grpc-backends", "mock-backend:" + mockBackend}
	if _, set := o.Env["LOCALAI_LOG_LEVEL"]; !set && !slices.Contains(o.Args, "--log-level") {
		args = append(args, "--log-level", "debug")
	}
	if o.Auth {
		args = append(args, "--auth-enabled", "--auth-database-url", filepath.Join(dir, "auth.db"))
	}
	args = append(args, o.Args...)
	in := &instance{dir: dir, log: filepath.Join(dir, "server.log"), done: make(chan struct{}),
		api: newAPI(fmt.Sprintf("http://127.0.0.1:%d", port))}
	lf, err := os.Create(in.log)
	Expect(err).ToNot(HaveOccurred())
	cmd := exec.Command(localAIBin, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	for k, v := range o.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	Expect(cmd.Start()).To(Succeed())
	in.cmd = cmd
	go func() {
		err := cmd.Wait()
		in.mu.Lock()
		in.exit = err
		in.mu.Unlock()
		_ = lf.Close()
		close(in.done)
	}()
	DeferCleanup(in.stop)
	if !o.NoReady {
		Eventually(func() int {
			select {
			case <-in.done:
				Fail("local-ai exited while starting:\n" + in.logs())
			default:
			}
			resp, err := http.Get(in.api.base + "/readyz") //nolint:noctx // a probe
			if err != nil {
				return 0
			}
			defer resp.Body.Close()
			return resp.StatusCode
		}, 90*time.Second, 250*time.Millisecond).Should(Equal(200), in.logs())
	}
	return in
}

func (in *instance) stop() {
	select {
	case <-in.done:
		return
	default:
	}
	_ = syscall.Kill(-in.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-in.done:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-in.cmd.Process.Pid, syscall.SIGKILL)
		<-in.done
	}
}

func (in *instance) logs() string {
	raw, _ := os.ReadFile(in.log)
	return ansi.ReplaceAllString(string(raw), "")
}

// exited reports the exit error of a stopped process and whether it stopped.
func (in *instance) exited() (error, bool) {
	select {
	case <-in.done:
		in.mu.Lock()
		defer in.mu.Unlock()
		return in.exit, true
	default:
		return nil, false
	}
}

// fixtureServer is a running mcpfixture over HTTP.
type fixtureServer struct {
	Addr string
	URL  string
	cmd  *exec.Cmd
}

func startFixtureHTTP() *fixtureServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	addr := ln.Addr().String()
	Expect(ln.Close()).To(Succeed())
	cmd := exec.Command(fixtureBin, "-http", addr)
	cmd.Stdout, cmd.Stderr = GinkgoWriter, GinkgoWriter
	Expect(cmd.Start()).To(Succeed())
	f := &fixtureServer{Addr: addr, URL: "http://" + addr + "/mcp", cmd: cmd}
	DeferCleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	Eventually(func() error {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
		}
		return err
	}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
	return f
}

// stats reads what the fixture saw.
func (f *fixtureServer) stats() (requests int64, headers map[string]string) {
	resp, err := http.Get("http://" + f.Addr + "/debug/stats") //nolint:noctx // a test probe
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	var s struct {
		Requests    int64             `json:"requests"`
		LastHeaders map[string]string `json:"last_headers"`
	}
	Expect(json.NewDecoder(resp.Body).Decode(&s)).To(Succeed())
	return s.Requests, s.LastHeaders
}

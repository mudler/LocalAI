//go:build linux

package distributed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This file holds the harness of the scenarios with real processes: it starts
// local-ai processes (frontends and workers) and talks to them over HTTP. It
// has no dependency on the code under test, so a regression cannot hide in the
// harness.

const (
	stackAPIKey   = "stack-e2e-key"
	stackRegToken = "stack-e2e-token"
	stackModel    = "stack-model"
)

// stackProc is a process of the cluster under test.
type stackProc struct {
	name string
	cmd  *exec.Cmd
	log  string
	done chan struct{}
}

func (p *stackProc) pid() int { return p.cmd.Process.Pid }

func (p *stackProc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// killGroup ends the process and everything it started.
func (p *stackProc) killGroup() {
	if p.alive() {
		_ = syscall.Kill(-p.pid(), syscall.SIGKILL)
	}
}

// logTail returns the last bytes of the log of the process.
func (p *stackProc) logTail(n int) string {
	b, err := os.ReadFile(p.log)
	if err != nil {
		return ""
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// procStack is the set of binaries, directories and processes of one run.
type procStack struct {
	root       string
	localAI    string
	mock       string
	pgURL      string
	natsURL    string
	procs      []*stackProc
	httpClient *http.Client
}

// stackEnv is the environment of a child: the minimum, so that a LOCALAI_
// variable of the machine of the developer cannot change the run.
func stackEnv(extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return append(env, extra...)
}

func (s *procStack) start(name, dir string, env []string, args ...string) *stackProc {
	GinkgoHelper()
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	logPath := filepath.Join(s.root, "logs", name+"-"+strconv.Itoa(len(s.procs))+".log")
	Expect(os.MkdirAll(filepath.Dir(logPath), 0o755)).To(Succeed())
	f, err := os.Create(logPath)
	Expect(err).ToNot(HaveOccurred())

	cmd := exec.Command(s.localAI, args...)
	cmd.Dir = dir
	cmd.Env = stackEnv(env...)
	cmd.Stdout = f
	cmd.Stderr = f
	// Its own process group, so that the backends of a worker can be ended with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	Expect(cmd.Start()).To(Succeed())

	p := &stackProc{name: name, cmd: cmd, log: logPath, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		_ = f.Close()
		close(p.done)
	}()
	s.procs = append(s.procs, p)
	return p
}

func (s *procStack) stopAll() {
	for _, p := range s.procs {
		p.killGroup()
	}
	for _, p := range s.procs {
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
		}
	}
}

// dumpLogs prints the end of every log. It runs when a spec fails.
func (s *procStack) dumpLogs() {
	for _, p := range s.procs {
		_, _ = fmt.Fprintf(GinkgoWriter, "\n===== %s (pid %d, alive=%v) =====\n%s\n", p.name, p.pid(), p.alive(), p.logTail(6000))
	}
}

func freePort() int {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

var (
	workerPortMu   sync.Mutex
	workerPortNext = 20000 + os.Getpid()%4000*2
)

// workerBasePort returns the base port of a worker. The worker serves files on
// the port below it and starts its backends on the ports above it, so the whole
// block must be free. It is below the range that the system gives to outgoing
// connections, where nothing else takes a port from it.
func workerBasePort() int {
	GinkgoHelper()
	workerPortMu.Lock()
	defer workerPortMu.Unlock()
	for range 200 {
		workerPortNext += 20
		base := workerPortNext
		free := true
		for p := base - 1; p < base+16; p++ {
			l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
			if err != nil {
				free = false
				break
			}
			_ = l.Close()
		}
		if free {
			return base
		}
	}
	Fail("no free block of ports for a worker")
	return 0
}

// stackFrontend is a frontend process.
type stackFrontend struct {
	*stackProc
	port int
	dir  string
	env  []string
	args []string
}

func (f *stackFrontend) url() string { return fmt.Sprintf("http://127.0.0.1:%d", f.port) }

// frontend starts a frontend. extraEnv is added to its environment: a chat
// worker is a frontend with LOCALAI_CHAT_WORKER set.
func (s *procStack) frontend(name string, port int, extraEnv ...string) *stackFrontend {
	GinkgoHelper()
	dir := filepath.Join(s.root, name)
	for _, d := range []string{"models", "backends", "cfg", "gen", "up"} {
		Expect(os.MkdirAll(filepath.Join(dir, d), 0o755)).To(Succeed())
	}
	Expect(os.MkdirAll(filepath.Join(s.root, "shared-data"), 0o755)).To(Succeed())
	// The context is large enough for the tools of a chat turn, which an agent
	// job on a chat worker runs.
	cfg := "name: " + stackModel + "\nbackend: mock\ncontext_size: 32768\nparameters:\n  model: " + stackModel + ".bin\n"
	Expect(os.WriteFile(filepath.Join(dir, "models", stackModel+".yaml"), []byte(cfg), 0o644)).To(Succeed())

	fe := &stackFrontend{port: port, dir: dir}
	fe.env = []string{
		"LOCALAI_DISTRIBUTED=true",
		"LOCALAI_NATS_URL=" + s.natsURL,
		"LOCALAI_AUTH=true",
		"LOCALAI_AUTH_DATABASE_URL=" + s.pgURL,
		"LOCALAI_API_KEY=" + stackAPIKey,
		"LOCALAI_REGISTRATION_TOKEN=" + stackRegToken,
		"LOCALAI_AUTO_APPROVE_NODES=true",
		"LOCALAI_DISTRIBUTED_DISK_HEADROOM_CHECK=false",
		"LOCALAI_PEER_ADDRESS=" + fmt.Sprintf("127.0.0.1:%d", port),
		// Unattended turns go to a chat worker when one is live, and run on the
		// frontend otherwise.
		"LOCALAI_DISTRIBUTED_CHAT_WORKERS=true",
	}
	fe.env = append(fe.env, extraEnv...)
	fe.args = []string{"run",
		"--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--models-path", filepath.Join(dir, "models"),
		// The frontends share the data path, as the frontends of a deployment
		// without S3 share their volume: a frontend refuses to start on a blob
		// folder that the other frontends of the database do not see.
		"--data-path", filepath.Join(s.root, "shared-data"),
		"--backends-path", filepath.Join(dir, "backends"),
		"--backends-system-path", filepath.Join(dir, "nosys"),
		"--localai-config-dir", filepath.Join(dir, "cfg"),
		"--generated-content-path", filepath.Join(dir, "gen"),
		"--upload-path", filepath.Join(dir, "up"),
		"--log-level", "debug",
		// The frontends run on one host, and the add-on owner of each would
		// take the same loopback port. The scenarios need no add-on.
		"--addons-enabled=false",
	}
	fe.stackProc = s.start(name, dir, fe.env, fe.args...)
	return fe
}

// api calls the admin API of a frontend and returns the status and the body.
func (s *procStack) api(fe *stackFrontend, method, path string, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, []byte(err.Error())
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, fe.url()+path, rd)
	if err != nil {
		return 0, []byte(err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+stackAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// busOf reads the broadcast bus of a frontend from the cluster settings.
func (s *procStack) busOf(fe *stackFrontend) (string, error) {
	code, body := s.api(fe, http.MethodGet, "/api/cluster/settings", nil)
	if code != http.StatusOK {
		return "", fmt.Errorf("GET cluster settings: %d %s", code, body)
	}
	var v struct {
		Bus struct {
			Bus string `json:"bus"`
		} `json:"bus"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	return v.Bus.Bus, nil
}

// chat sends one chat request through a frontend and returns an error if it
// was not answered with 200 and a non-empty body.
func (s *procStack) chat(fe *stackFrontend) error {
	code, body := s.api(fe, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    stackModel,
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	if code != http.StatusOK {
		return fmt.Errorf("chat via %s: %d %s", fe.name, code, truncate(string(body), 300))
	}
	if !bytes.Contains(body, []byte("choices")) {
		return fmt.Errorf("chat via %s: no choices in %s", fe.name, truncate(string(body), 300))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// stackBinaries finds or builds local-ai and the mock backend. LOCALAI_E2E_BIN
// and LOCALAI_E2E_MOCK_BACKEND name binaries that exist already.
func stackBinaries(dir string) (localAI, mock string, skip string) {
	repo := filepath.Join(thisFileDir(), "..", "..", "..")
	pick := func(env, out, pkg string) (string, string) {
		if p := os.Getenv(env); p != "" {
			if _, err := os.Stat(p); err != nil {
				return "", env + " names a file that does not exist"
			}
			return p, ""
		}
		target := filepath.Join(dir, out)
		cmd := exec.Command("go", "build", "-o", target, pkg)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Sprintf("cannot build %s: %v\n%s", pkg, err, truncate(string(b), 800))
		}
		return target, ""
	}
	if _, err := exec.LookPath("go"); err != nil && os.Getenv("LOCALAI_E2E_BIN") == "" {
		return "", "", "no go toolchain and LOCALAI_E2E_BIN is not set"
	}
	var why string
	if localAI, why = pick("LOCALAI_E2E_BIN", "local-ai", "./cmd/local-ai"); why != "" {
		return "", "", why
	}
	if mock, why = pick("LOCALAI_E2E_MOCK_BACKEND", "mock-backend", "./tests/e2e/mock-backend"); why != "" {
		return "", "", why
	}
	return localAI, mock, ""
}

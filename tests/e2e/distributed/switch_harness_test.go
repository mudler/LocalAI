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
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This file holds the harness of the carrier switch scenario: it starts real
// local-ai processes (frontends and workers) and talks to them over HTTP. It
// has no dependency on the code under test, so a regression in the switch
// cannot hide in the harness.

const (
	switchAPIKey   = "switch-e2e-key"
	switchRegToken = "switch-e2e-token"
	switchModel    = "switch-model"
)

// switchProc is a process of the cluster under test.
type switchProc struct {
	name string
	cmd  *exec.Cmd
	log  string
	done chan struct{}
}

func (p *switchProc) pid() int { return p.cmd.Process.Pid }

func (p *switchProc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// signal sends a signal to the process only. Its children are left alone.
func (p *switchProc) signal(s syscall.Signal) {
	if p.alive() {
		_ = p.cmd.Process.Signal(s)
	}
}

// kill ends the process at once and waits until it is gone.
func (p *switchProc) kill() {
	p.signal(syscall.SIGKILL)
	Eventually(p.done, 20*time.Second).Should(BeClosed(), "%s did not exit", p.name)
}

// killGroup ends the process and everything it started.
func (p *switchProc) killGroup() {
	if p.alive() {
		_ = syscall.Kill(-p.pid(), syscall.SIGKILL)
	}
}

// logTail returns the last bytes of the log of the process.
func (p *switchProc) logTail(n int) string {
	b, err := os.ReadFile(p.log)
	if err != nil {
		return ""
	}
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// switchStack is the set of binaries, directories and processes of one run.
type switchStack struct {
	root       string
	localAI    string
	mock       string
	pgURL      string
	natsURL    string
	procs      []*switchProc
	httpClient *http.Client
}

// switchEnv is the environment of a child: the minimum, so that a LOCALAI_
// variable of the machine of the developer cannot change the run.
func switchEnv(extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return append(env, extra...)
}

func (s *switchStack) start(name, dir string, env []string, args ...string) *switchProc {
	GinkgoHelper()
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	logPath := filepath.Join(s.root, "logs", name+"-"+strconv.Itoa(len(s.procs))+".log")
	Expect(os.MkdirAll(filepath.Dir(logPath), 0o755)).To(Succeed())
	f, err := os.Create(logPath)
	Expect(err).ToNot(HaveOccurred())

	cmd := exec.Command(s.localAI, args...)
	cmd.Dir = dir
	cmd.Env = switchEnv(env...)
	cmd.Stdout = f
	cmd.Stderr = f
	// Its own process group, so that the backends of a worker can be ended with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	Expect(cmd.Start()).To(Succeed())

	p := &switchProc{name: name, cmd: cmd, log: logPath, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		_ = f.Close()
		close(p.done)
	}()
	s.procs = append(s.procs, p)
	return p
}

func (s *switchStack) stopAll() {
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
func (s *switchStack) dumpLogs() {
	for _, p := range s.procs {
		_, _ = fmt.Fprintf(GinkgoWriter, "\n===== %s (pid %d, alive=%v) =====\n%s\n", p.name, p.pid(), p.alive(), p.logTail(6000))
	}
}

func switchFreePort() int {
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

// switchFrontend is a frontend process.
type switchFrontend struct {
	*switchProc
	port int
	dir  string
	env  []string
	args []string
}

func (f *switchFrontend) url() string { return fmt.Sprintf("http://127.0.0.1:%d", f.port) }

func (s *switchStack) frontend(name string, port int) *switchFrontend {
	GinkgoHelper()
	dir := filepath.Join(s.root, name)
	for _, d := range []string{"models", "data", "backends", "cfg", "gen", "up"} {
		Expect(os.MkdirAll(filepath.Join(dir, d), 0o755)).To(Succeed())
	}
	cfg := "name: " + switchModel + "\nbackend: mock\nparameters:\n  model: " + switchModel + ".bin\n"
	Expect(os.WriteFile(filepath.Join(dir, "models", switchModel+".yaml"), []byte(cfg), 0o644)).To(Succeed())

	fe := &switchFrontend{port: port, dir: dir}
	fe.env = []string{
		"LOCALAI_DISTRIBUTED=true",
		"LOCALAI_NATS_URL=" + s.natsURL,
		"LOCALAI_AUTH=true",
		"LOCALAI_AUTH_DATABASE_URL=" + s.pgURL,
		"LOCALAI_API_KEY=" + switchAPIKey,
		"LOCALAI_REGISTRATION_TOKEN=" + switchRegToken,
		"LOCALAI_AUTO_APPROVE_NODES=true",
		"LOCALAI_DISTRIBUTED_DISK_HEADROOM_CHECK=false",
		"LOCALAI_PEER_ADDRESS=" + fmt.Sprintf("127.0.0.1:%d", port),
	}
	fe.args = []string{"run",
		"--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--models-path", filepath.Join(dir, "models"),
		"--data-path", filepath.Join(dir, "data"),
		"--backends-path", filepath.Join(dir, "backends"),
		"--backends-system-path", filepath.Join(dir, "nosys"),
		"--localai-config-dir", filepath.Join(dir, "cfg"),
		"--generated-content-path", filepath.Join(dir, "gen"),
		"--upload-path", filepath.Join(dir, "up"),
		"--log-level", "debug",
	}
	fe.switchProc = s.start(name, dir, fe.env, fe.args...)
	return fe
}

// restart starts the frontend again with the same flags.
func (s *switchStack) restart(fe *switchFrontend) {
	GinkgoHelper()
	fe.switchProc = s.start(fe.name, fe.dir, fe.env, fe.args...)
}

// switchWorker is a worker process.
type switchWorker struct {
	*switchProc
	name string
	dir  string
	args []string
	env  []string
	base int
}

// worker starts a worker. With dual set it has an address, so it can be
// reached directly and can follow a change to NATS. Without one it is
// tunnel-only: it dials out and nothing dials in.
func (s *switchStack) worker(name string, register string, dual bool) *switchWorker {
	GinkgoHelper()
	dir := filepath.Join(s.root, name)
	backend := filepath.Join(dir, "backends", "mock")
	Expect(os.MkdirAll(backend, 0o755)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(dir, "models"), 0o755)).To(Succeed())
	// A backend is a directory with a run.sh. The script starts the mock backend.
	run := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", s.mock)
	Expect(os.WriteFile(filepath.Join(backend, "run.sh"), []byte(run), 0o755)).To(Succeed())

	w := &switchWorker{name: name, dir: dir, base: workerBasePort()}
	w.env = []string{
		"LOCALAI_FOLLOW_MAX_DELAY=2s",
		"LOCALAI_HEARTBEAT_INTERVAL=3s",
	}
	w.args = []string{"worker",
		"--backends-path", filepath.Join(dir, "backends"),
		"--backends-system-path", filepath.Join(dir, "nosys"),
		"--models-path", filepath.Join(dir, "models"),
		"--register-to", register,
		"--registration-token", switchRegToken,
		"--node-name", name,
		"--log-level", "debug",
	}
	if dual {
		addr := fmt.Sprintf("127.0.0.1:%d", w.base)
		w.args = append(w.args, "--addr", addr, "--advertise-addr", addr)
	} else {
		w.args = append(w.args, "--serve-addr", fmt.Sprintf("127.0.0.1:%d", w.base))
	}
	w.switchProc = s.start(name, dir, w.env, w.args...)
	return w
}

func (s *switchStack) restartWorker(w *switchWorker) {
	GinkgoHelper()
	w.switchProc = s.start(w.name, w.dir, w.env, w.args...)
}

// childPIDs lists the processes whose parent is pid.
func childPIDs(pid int) []int {
	var out []int
	entries, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, e := range entries {
		b, err := os.ReadFile(e)
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...; comm may hold spaces, so cut after the last ")".
		s := string(b)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			continue
		}
		if pp, _ := strconv.Atoi(f[1]); pp == pid {
			c, _ := strconv.Atoi(strings.Split(strings.TrimPrefix(e, "/proc/"), "/")[0])
			out = append(out, c)
		}
	}
	return out
}

// backendPIDs lists the backend processes of a worker, found by the command
// line of the mock backend, whatever the shell in between is.
func backendPIDs(w *switchWorker) []int {
	var out []int
	for _, c := range childPIDs(w.pid()) {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", c))
		if bytes.Contains(b, []byte("mock-backend")) || bytes.Contains(b, []byte("run.sh")) {
			out = append(out, c)
		}
	}
	return out
}

// api calls the admin API of a frontend and returns the status and the body.
func (s *switchStack) api(fe *switchFrontend, method, path string, body any) (int, []byte) {
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
	req.Header.Set("Authorization", "Bearer "+switchAPIKey)
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

// carrierWorker is a worker as the carrier report lists it.
type carrierWorker struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Attached    []string `json:"attached"`
	Follow      []string `json:"follow"`
	FollowError string   `json:"follow_error"`
	CanFollow   bool     `json:"can_follow"`
	Reason      string   `json:"reason"`
}

type carrierBlocker struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Reason    string `json:"reason"`
	Forceable bool   `json:"forceable"`
}

type carrierReplica struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	ReadyEpoch int64  `json:"ready_epoch"`
}

// carrierReport is the part of the answer of the carrier API that the scenario reads.
type carrierReport struct {
	Active   string           `json:"active"`
	Target   string           `json:"target"`
	Epoch    int64            `json:"epoch"`
	State    string           `json:"state"`
	OK       bool             `json:"ok"`
	Blockers []carrierBlocker `json:"blockers"`
	Replicas []carrierReplica `json:"replicas"`
	Workers  []carrierWorker  `json:"workers"`
}

func (s *switchStack) carrier(fe *switchFrontend) (carrierReport, error) {
	code, body := s.api(fe, http.MethodGet, "/api/cluster/carrier", nil)
	var r carrierReport
	if code != http.StatusOK {
		return r, fmt.Errorf("GET carrier: %d %s", code, body)
	}
	return r, json.Unmarshal(body, &r)
}

func (r carrierReport) worker(name string) (carrierWorker, bool) {
	for _, w := range r.Workers {
		if w.Name == name {
			return w, true
		}
	}
	return carrierWorker{}, false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// attachedTo is a gomega-friendly check used with Eventually: the worker is
// listed, and is attached to the carrier.
func (s *switchStack) workerAttached(fe *switchFrontend, name, carrier string) bool {
	r, err := s.carrier(fe)
	if err != nil {
		return false
	}
	w, ok := r.worker(name)
	return ok && contains(w.Attached, carrier)
}

// nodeInfo is one row of the node list.
type nodeInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *switchStack) nodes(fe *switchFrontend) ([]nodeInfo, error) {
	code, body := s.api(fe, http.MethodGet, "/api/nodes", nil)
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET nodes: %d %s", code, body)
	}
	var out []nodeInfo
	return out, json.Unmarshal(body, &out)
}

func (s *switchStack) node(fe *switchFrontend, name string) (nodeInfo, bool) {
	ns, err := s.nodes(fe)
	if err != nil {
		return nodeInfo{}, false
	}
	for _, n := range ns {
		if n.Name == name {
			return n, true
		}
	}
	return nodeInfo{}, false
}

// chat sends one chat request through a frontend and returns an error if it
// was not answered with 200 and a non-empty body.
func (s *switchStack) chat(fe *switchFrontend) error {
	code, body := s.api(fe, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    switchModel,
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

// chatLoad sends chat requests through frontends until it is stopped, and
// keeps every failure.
type chatLoad struct {
	stop context.CancelFunc
	wg   sync.WaitGroup
	mu   sync.Mutex
	ok   int
	errs []error
}

func (s *switchStack) startChatLoad(fes ...*switchFrontend) *chatLoad {
	ctx, cancel := context.WithCancel(context.Background())
	l := &chatLoad{stop: cancel}
	for _, fe := range fes {
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			defer GinkgoRecover()
			for ctx.Err() == nil {
				err := s.chat(fe)
				l.mu.Lock()
				if err != nil {
					l.errs = append(l.errs, err)
				} else {
					l.ok++
				}
				l.mu.Unlock()
				select {
				case <-ctx.Done():
				case <-time.After(100 * time.Millisecond): // pacing of the load, not a wait for an event
				}
			}
		}()
	}
	return l
}

// finish stops the load and returns the number of answers and the failures.
func (l *chatLoad) finish() (int, []error) {
	l.stop()
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ok, append([]error(nil), l.errs...)
}

// switchBinaries finds or builds local-ai and the mock backend. LOCALAI_E2E_BIN
// and LOCALAI_E2E_MOCK_BACKEND name binaries that exist already.
func switchBinaries(dir string) (localAI, mock string, skip string) {
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

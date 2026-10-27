package e2echats_test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// instance is one local-ai process with its own models and data folders.
// The folders outlive a restart, which is how persistence is tested.
type instance struct {
	root string
	port int
	cmd  *exec.Cmd
	done chan struct{}

	mu  sync.Mutex
	log bytes.Buffer
}

func e2ePort() int {
	if p, err := strconv.Atoi(os.Getenv("E2E_CHATS_PORT")); err == nil && p > 0 {
		return p
	}
	return 8211
}

func newInstance() *instance {
	root, err := os.MkdirTemp("", "e2e-chats-run-*")
	Expect(err).ToNot(HaveOccurred())
	in := &instance{root: root, port: e2ePort()}
	Expect(os.MkdirAll(in.models(), 0o755)).To(Succeed())
	Expect(os.MkdirAll(in.data(), 0o755)).To(Succeed())
	in.writeModels()
	return in
}

func (in *instance) models() string { return filepath.Join(in.root, "models") }
func (in *instance) data() string   { return filepath.Join(in.root, "data") }
func (in *instance) URL() string    { return fmt.Sprintf("http://127.0.0.1:%d", in.port) }

func (in *instance) writeModel(name string, cfg map[string]any) {
	b, err := yaml.Marshal(cfg)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(in.models(), name+".yaml"), b, 0o644)).To(Succeed())
}

func proxyModel(name, upstream string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"name": name, "backend": "cloud-proxy",
		"parameters": map[string]any{"model": name + ".bin"},
		"proxy": map[string]any{"mode": "translate", "provider": "openai",
			"upstream_url": upstream + "/v1/chat/completions", "api_key_env": "E2E_CHATS_UPSTREAM_KEY"},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func (in *instance) writeModels() {
	in.writeModel("fake", proxyModel("fake", llm.URL(), nil))
	// A small window: the tool schemas and the prompt take about 6700 tokens of it, so long messages fill it quickly.
	in.writeModel("fake-small", proxyModel("fake-small", llm.URL(), map[string]any{"context_size": 16384}))
	// A usual size for a local model.
	in.writeModel("fake-8k", proxyModel("fake-8k", llm.URL(), map[string]any{"context_size": 8192}))
	// Nothing listens on port 1: the backend is down.
	in.writeModel("fake-down", proxyModel("fake-down", "http://127.0.0.1:1", nil))
	in.writeModel("mock-model", map[string]any{"name": "mock-model", "backend": "mock-backend", "parameters": map[string]any{"model": "mock-model.bin"}})
}

// Start launches the server and waits until it answers.
func (in *instance) Start(args ...string) { in.StartEnv(nil, args...) }

func (in *instance) StartEnv(env []string, args ...string) {
	base := []string{"run", "--address", fmt.Sprintf("127.0.0.1:%d", in.port),
		"--models-path", in.models(), "--backends-path", filepath.Join(in.root, "backends"),
		"--data-path", in.data(), "--localai-config-dir", filepath.Join(in.root, "config"),
		"--external-grpc-backends", "mock-backend:" + mockBackendBin + ",cloud-proxy:" + cloudProxyBin}
	in.cmd = exec.Command(localAIBin, append(base, args...)...)
	in.cmd.Dir = in.root
	// The environment is clean: a stray API_KEY would switch the API-key mode on.
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		if k == "API_KEY" || k == "OPENAI_API_KEY" || strings.HasPrefix(k, "LOCALAI_") || strings.HasPrefix(k, "AUTH_") {
			continue
		}
		in.cmd.Env = append(in.cmd.Env, e)
	}
	in.cmd.Env = append(in.cmd.Env, "E2E_CHATS_UPSTREAM_KEY=sk-e2e", "NO_COLOR=1")
	in.cmd.Env = append(in.cmd.Env, env...)
	in.cmd.Stdout, in.cmd.Stderr = &lockedWriter{in}, &lockedWriter{in}
	in.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	Expect(in.cmd.Start()).To(Succeed())
	in.done = make(chan struct{})
	go func() { _ = in.cmd.Wait(); close(in.done) }()
	Eventually(func() error {
		select {
		case <-in.done:
			return fmt.Errorf("server exited early:\n%s", in.Log())
		default:
		}
		resp, err := http.Get(in.URL() + "/readyz")
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("readyz %d", resp.StatusCode)
		}
		return nil
	}, 90*time.Second, 250*time.Millisecond).Should(Succeed())
}

type lockedWriter struct{ in *instance }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.in.mu.Lock()
	defer w.in.mu.Unlock()
	return w.in.log.Write(p)
}

// Log is everything the server printed so far.
func (in *instance) Log() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return ansi.ReplaceAllString(in.log.String(), "")
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func (in *instance) stop(sig syscall.Signal) {
	if in.cmd == nil || in.cmd.Process == nil {
		return
	}
	select {
	case <-in.done:
		return
	default:
	}
	// The group, so the backend processes go too.
	_ = syscall.Kill(-in.cmd.Process.Pid, sig)
	select {
	case <-in.done:
	case <-time.After(30 * time.Second):
		_ = syscall.Kill(-in.cmd.Process.Pid, syscall.SIGKILL)
		<-in.done
	}
}

// Stop shuts the server down the way an operator does.
func (in *instance) Stop() { in.stop(syscall.SIGTERM) }

// Kill ends the server with no chance to clean up, like a crash.
func (in *instance) Kill() { in.stop(syscall.SIGKILL) }

// Restart stops the server and starts it again on the same folders.
func (in *instance) Restart(args ...string) {
	in.Stop()
	in.log.Reset()
	in.Start(args...)
}

// Cleanup stops the server and removes its folders.
func (in *instance) Cleanup() {
	in.Kill()
	_ = os.RemoveAll(in.root)
}

// Package cluster runs LocalAI as real child processes for end-to-end tests.
//
// The in-process suites cannot express frontend-replica failure: there is no
// process to kill, no second replica to race, and no real HTTP boundary between
// a worker and the frontend it registered with. This package starts the same
// binary an operator runs, one process per frontend replica and one per worker,
// against containerised Postgres and NATS.
package cluster

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"

	"github.com/phayes/freeport"
)

// Options configures a cluster. Every field without a default is required.
type Options struct {
	// Binary is the path to a built local-ai.
	Binary string
	// MockBackend is the path to the mock-backend binary. When set it is copied
	// into each worker's backends directory as "mock-backend", which is the name
	// model YAML refers to (see tests/e2e/e2e_suite_test.go:75).
	MockBackend string
	// PGDSN and NatsURL point at infrastructure the caller already started.
	PGDSN   string
	NatsURL string
	// LogDir receives one file per process. Never empty: a cluster failure is
	// unreadable without them.
	LogDir string

	RegistrationToken string // default "e2e-token"
	AdminEmail        string // default "admin@e2e.local"

	Frontends int
	Workers   int

	// SpreadWorkerRegistrations sends worker i to frontend i%Frontends instead
	// of sending every worker to frontend 0.
	//
	// Off by default, and deliberately so: the cross-replica session specs read
	// a node at frontend 1 that only frontend 0 was ever told about, and
	// spreading registrations would leave them passing while proving nothing.
	// It exists for the racing-replicas spec, which is about two replicas
	// writing to one roster concurrently and cannot express that at all while
	// every worker registers through the same process.
	SpreadWorkerRegistrations bool
}

// Process is one running local-ai.
type Process struct {
	Name string
	// Cmd is exposed for signalling only. Never call Cmd.Wait on it: the reaper
	// goroutine started in spawn owns it, a second Wait races the first, and
	// waitErr is only safe to read after <-p.exited.
	Cmd     *exec.Cmd
	Port    int
	LogPath string

	logFile *os.File
	// exited closes once the reaper has collected the process. Only the reaper
	// calls Cmd.Wait, so nothing else may: a second Wait on the same Cmd races
	// the first and corrupts ProcessState.
	//
	// A closed exited proves the child is gone. An open one proves nothing: it
	// is still open for the whole interval between the child exiting and waitid
	// collecting it, during which the child is a zombie that signal 0 reports as
	// alive. Anything asserting on a process being dead must poll, not sample.
	exited  chan struct{}
	waitErr error
}

// Cluster is a running set of frontend and worker processes.
type Cluster struct {
	opts      Options
	frontends []*Process
	workers   []*Process
	baseDir   string
}

const (
	defaultRegistrationToken = "e2e-token"
	defaultAdminEmail        = "admin@e2e.local"
	// testHMACSecret is shared by every frontend so a session minted at one
	// replica validates at all of them. See the note in startFrontend.
	testHMACSecret   = "e2e-cluster-hmac-secret"
	readinessTimeout = 90 * time.Second
	readinessPoll    = 200 * time.Millisecond
	// processExitTimeout bounds the post-SIGKILL wait in terminate. An unbounded
	// wait turns one stuck child (D state, or a Wait that never returns) into a
	// suite-wide Ginkgo timeout that names nothing.
	processExitTimeout = 10 * time.Second
)

func (o *Options) applyDefaults() {
	if o.RegistrationToken == "" {
		o.RegistrationToken = defaultRegistrationToken
	}
	if o.AdminEmail == "" {
		o.AdminEmail = defaultAdminEmail
	}
}

func (o Options) validate() error {
	if o.Frontends < 1 {
		return fmt.Errorf("cluster needs at least one frontend, got %d", o.Frontends)
	}
	if o.LogDir == "" {
		return fmt.Errorf("cluster needs a LogDir: process logs are the only way to read a cluster failure")
	}
	if st, err := os.Stat(o.Binary); err != nil || st.IsDir() {
		return fmt.Errorf("local-ai binary not found at %q (run: make build)", o.Binary)
	}
	return nil
}

// Start brings up the cluster. It blocks until every frontend answers /readyz
// and every worker process has been spawned. It does NOT wait for workers to
// register: that needs an authenticated admin session, so a caller that depends
// on registration must poll /api/nodes itself.
func Start(opts Options) (*Cluster, error) {
	opts.applyDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}

	baseDir, err := os.MkdirTemp("", "localai-cluster-*")
	if err != nil {
		return nil, fmt.Errorf("creating cluster work dir: %w", err)
	}

	c := &Cluster{opts: opts, baseDir: baseDir}

	for i := 0; i < opts.Frontends; i++ {
		p, err := c.startFrontend(i, 0)
		if err != nil {
			c.Stop()
			return nil, err
		}
		c.frontends = append(c.frontends, p)
	}
	for i := 0; i < opts.Workers; i++ {
		p, err := c.startWorker(i)
		if err != nil {
			c.Stop()
			return nil, err
		}
		c.workers = append(c.workers, p)
	}
	return c, nil
}

// startFrontend starts frontend i. A port <= 0 allocates a fresh one; a pinned
// port exists for restart: workers take LOCALAI_REGISTER_TO once at boot and
// never re-resolve it, so a replica that comes back on a new port is
// unreachable by exactly the workers that registered with it.
func (c *Cluster) startFrontend(i int, port int) (*Process, error) {
	if port <= 0 {
		allocated, err := freeport.GetFreePort()
		if err != nil {
			return nil, fmt.Errorf("allocating frontend port: %w", err)
		}
		port = allocated
	}
	name := frontendName(i)
	dir := c.frontendDir(i)
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s dirs: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "backends"), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s dirs: %w", name, err)
	}
	// Without an explicit LOCALAI_DATA_PATH every child resolves DataPath to
	// ${cwd}/data (core/cli/run.go:48), which under `go test` is inside the
	// source tree and shared by every replica: one collectiondb, one task and
	// job store for processes that are meant to be independent.
	dataPath := c.frontendDataDir(i)
	if err := os.MkdirAll(dataPath, 0o750); err != nil {
		return nil, fmt.Errorf("creating %s dirs: %w", name, err)
	}

	cmd := exec.Command(c.opts.Binary, "run",
		"--address", fmt.Sprintf("127.0.0.1:%d", port),
		"--models-path", filepath.Join(dir, "models"),
		"--backends-path", filepath.Join(dir, "backends"),
	)
	// Cmd.Environ() is the parent environment this Cmd would already run with;
	// the children need PATH, HOME and the Go/CI environment intact.
	cmd.Env = append(cmd.Environ(),
		"LOCALAI_DISTRIBUTED=true",
		"LOCALAI_NATS_URL="+c.opts.NatsURL,
		"LOCALAI_AUTH=true",
		"LOCALAI_AUTH_DATABASE_URL="+c.opts.PGDSN,
		"LOCALAI_ADMIN_EMAIL="+c.opts.AdminEmail,
		"LOCALAI_DATA_PATH="+dataPath,
		// Session rows are keyed by HMAC-SHA256(token, APIKeyHMACSecret), and
		// the secret is generated per instance into {DataPath}/.hmac_secret
		// unless pinned (core/application/startup.go:141-148). Now that each
		// replica owns its data directory, an unpinned secret would differ per
		// replica, so the cookie minted at frontend 0 would hash to a session
		// row that does not exist at frontend 1 and every post-failover
		// /api/nodes call would 401 with nothing in the logs to explain it.
		// Pinning makes the cross-replica session a property of the harness.
		"LOCALAI_AUTH_HMAC_SECRET="+testHMACSecret,
		"LOCALAI_REGISTRATION_TOKEN="+c.opts.RegistrationToken,
		"LOCALAI_AUTO_APPROVE_NODES=true",
		"DEBUG=true",
	)

	p, err := c.spawn(name, cmd, port)
	if err != nil {
		return nil, err
	}
	if err := waitReady(p, fmt.Sprintf("http://127.0.0.1:%d/readyz", port)); err != nil {
		// The caller never sees this process, so Stop() will never reach it:
		// reap it here or it outlives the suite holding a port and a log handle.
		p.terminate()
		return nil, fmt.Errorf("%s never became ready (see %s): %w", name, p.LogPath, err)
	}
	return p, nil
}

func (c *Cluster) startWorker(i int) (*Process, error) {
	// Two independent free ports: the worker's file-transfer server defaults to
	// basePort-1, which freeport never reserved and which is basePort of another
	// worker whenever two allocations land adjacent.
	ports, err := freeport.GetFreePorts(2)
	if err != nil {
		return nil, fmt.Errorf("allocating worker ports: %w", err)
	}
	grpcPort, httpPort := ports[0], ports[1]
	name := fmt.Sprintf("worker-%d", i)
	dir := filepath.Join(c.baseDir, name)
	backends := filepath.Join(dir, "backends")
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s dirs: %w", name, err)
	}
	if err := os.MkdirAll(backends, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s dirs: %w", name, err)
	}
	if c.opts.MockBackend != "" {
		if err := copyExecutable(c.opts.MockBackend, filepath.Join(backends, "mock-backend")); err != nil {
			return nil, fmt.Errorf("installing mock backend for %s: %w", name, err)
		}
	}

	cmd := exec.Command(c.opts.Binary, "worker",
		"--models-path", filepath.Join(dir, "models"),
		"--backends-path", backends,
	)
	cmd.Env = append(cmd.Environ(),
		fmt.Sprintf("LOCALAI_SERVE_ADDR=127.0.0.1:%d", grpcPort),
		fmt.Sprintf("LOCALAI_ADVERTISE_ADDR=127.0.0.1:%d", grpcPort),
		fmt.Sprintf("LOCALAI_HTTP_ADDR=127.0.0.1:%d", httpPort),
		fmt.Sprintf("LOCALAI_ADVERTISE_HTTP_ADDR=127.0.0.1:%d", httpPort),
		// Workers register with frontend 0 ONLY unless the caller opts into
		// SpreadWorkerRegistrations, and the cross-replica session specs depend
		// on that default. They prove a session minted at frontend 0 resolves at
		// frontend 1 by reading a node that only frontend 0 was ever told about;
		// register the worker everywhere and they still pass while proving
		// nothing.
		//
		// Nothing in those specs can detect the change. The registry keys nodes
		// by name and preserves ids across the shared Postgres
		// (core/services/nodes/registry.go:522-527), so a roster read at
		// frontend 1 looks identical either way. Anyone changing the default
		// here must revisit tests/e2e/distributed/cluster_baseline_test.go by
		// hand.
		//
		// The registrar also fixes where this worker's heartbeats go for the
		// rest of its life: the loop posts to the URL it was given at boot and
		// never re-resolves it (core/cli/workerregistry/client.go), so killing a
		// worker's registrar orphans that worker rather than failing it over.
		"LOCALAI_REGISTER_TO="+c.FrontendURL(c.registrarFor(i)),
		"LOCALAI_NODE_NAME="+name,
		"LOCALAI_REGISTRATION_TOKEN="+c.opts.RegistrationToken,
		"LOCALAI_NATS_URL="+c.opts.NatsURL,
		"DEBUG=true",
	)

	return c.spawn(name, cmd, grpcPort)
}

func (c *Cluster) spawn(name string, cmd *exec.Cmd, port int) (*Process, error) {
	logPath := filepath.Join(c.opts.LogDir, name+".log")
	// Append rather than truncate: a restarted process reopens the same path, and
	// the log of the instance that died is the one a failover post-mortem needs.
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("creating log file for %s: %w", name, err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	p := &Process{Name: name, Cmd: cmd, Port: port, LogPath: logPath, logFile: f, exited: make(chan struct{})}
	// One reaper per process, joined by terminate(): a child that dies on its own
	// is collected immediately, so waiters learn about it instead of polling a
	// dead port until the readiness timeout.
	go func() {
		p.waitErr = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// terminate kills the process, waits for the reaper, and releases the log
// handle. Safe to call more than once and on a process that already exited.
func (p *Process) terminate() {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	_ = p.Cmd.Process.Kill()
	select {
	case <-p.exited:
	case <-time.After(processExitTimeout):
		fmt.Printf("warning: %s did not exit within %s after SIGKILL; continuing teardown\n", p.Name, processExitTimeout)
	}
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
}

// FrontendURL is the base URL of frontend i.
func (c *Cluster) FrontendURL(i int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", c.frontends[i].Port)
}

// WorkerName is the node name worker i registered under.
func (c *Cluster) WorkerName(i int) string {
	return c.workers[i].Name
}

// registrarFor is the frontend index worker i registers and heartbeats with.
//
// It is read at spawn time and baked into the worker's environment, so it is
// also the answer to "which replica's death orphans this worker".
func (c *Cluster) registrarFor(worker int) int {
	if !c.opts.SpreadWorkerRegistrations || c.opts.Frontends < 1 {
		return 0
	}
	return worker % c.opts.Frontends
}

// WorkerRegistrar is registrarFor, exported so a spec can say which replica it
// is about to kill relative to a worker instead of re-deriving the rule.
//
// It returns an error rather than indexing blindly, like every other exported
// method here that takes an index. Gomega treats the trailing error as one that
// must be nil, so Expect(c.WorkerRegistrar(0)).To(...) reads unchanged at the
// call site while an out-of-range index fails the spec by name instead of
// silently answering 0, which is a real frontend index and would send a spec
// off to kill the wrong replica.
func (c *Cluster) WorkerRegistrar(worker int) (int, error) {
	if err := c.checkWorkerIndex(worker); err != nil {
		return 0, err
	}
	return c.registrarFor(worker), nil
}

// Stop terminates every process and removes the work directory. Logs survive in
// LogDir, which the caller owns.
func (c *Cluster) Stop() {
	// Start returns (nil, err) after stopping itself, so a spec that defers
	// c.Stop before asserting the error would otherwise nil-deref.
	if c == nil {
		return
	}
	for _, p := range append(append([]*Process{}, c.workers...), c.frontends...) {
		p.terminate()
	}
	if c.baseDir != "" {
		_ = os.RemoveAll(c.baseDir)
	}
}

// DumpLogs writes every process log to stdout. Call from an AfterEach guarded by
// CurrentSpecReport().Failed().
func (c *Cluster) DumpLogs() {
	for _, p := range append(append([]*Process{}, c.frontends...), c.workers...) {
		if p == nil {
			continue
		}
		data, err := os.ReadFile(p.LogPath)
		if err != nil {
			fmt.Printf("=== %s: log unreadable: %v\n", p.Name, err)
			continue
		}
		fmt.Printf("=== %s (%s) ===\n%s\n", p.Name, p.LogPath, string(data))
	}
}

func waitReady(p *Process, url string) error {
	deadline := time.Now().Add(readinessTimeout)
	client := httpclient.NewWithTimeout(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			if p.waitErr == nil {
				return fmt.Errorf("process exited cleanly before becoming ready")
			}
			return fmt.Errorf("process exited before becoming ready: %w", p.waitErr)
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(readinessPoll)
	}
	return fmt.Errorf("not ready within %s: %w", readinessTimeout, last)
}

func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return nil
}

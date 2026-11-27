//go:build linux

package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A worker that gets SIGTERM while it loads a model, with real processes: a
// frontend, a worker whose backend takes ten minutes to load, and a second
// worker. The load job of the first worker must be released within seconds
// (the worker reports that its loads ended), the worker must exit within its
// shutdown window, and the model must then load on the second worker.
//
// It runs with `make test-e2e-distributed-worker-shutdown` and needs docker
// and a Go toolchain, or LOCALAI_E2E_BIN and LOCALAI_E2E_MOCK_BACKEND (see
// process_harness_test.go).
var _ = Describe("A worker that shuts down while it loads a model, with real processes", Ordered, Label("WorkerShutdown"), func() {
	var (
		stack *procStack
		fe    *stackFrontend
		db    *gorm.DB
	)

	const (
		settle = 120 * time.Second
		poll   = 500 * time.Millisecond
	)

	type nodeRow struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	node := func(want string) (nodeRow, bool) {
		code, body := stack.api(fe, http.MethodGet, "/api/nodes", nil)
		if code != http.StatusOK {
			return nodeRow{}, false
		}
		var all []nodeRow
		if json.Unmarshal(body, &all) != nil {
			return nodeRow{}, false
		}
		for _, n := range all {
			if n.Name == want {
				return n, true
			}
		}
		return nodeRow{}, false
	}
	startWorker := func(name string, env ...string) *stackProc {
		dir := filepath.Join(stack.root, name)
		backend := filepath.Join(dir, "backends", "mock")
		Expect(os.MkdirAll(backend, 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "models"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(backend, "run.sh"), []byte(fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", stack.mock)), 0o755)).To(Succeed())
		env = append(env, "LOCALAI_HEARTBEAT_INTERVAL=3s", "LOCALAI_REGISTRATION_TOKEN="+stackRegToken)
		return stack.start(name, dir, env, "worker",
			"--register-to", fe.url(),
			"--routes", "websocket",
			"--backends-path", filepath.Join(dir, "backends"),
			"--backends-system-path", filepath.Join(dir, "nosys"),
			"--models-path", filepath.Join(dir, "models"),
			"--node-name", name,
			"--serve-addr", fmt.Sprintf("127.0.0.1:%d", workerBasePort()),
			"--log-level", "debug",
		)
	}

	BeforeAll(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			Skip("docker is not installed")
		}
		stack = &procStack{root: GinkgoT().TempDir(), httpClient: &http.Client{Timeout: 60 * time.Second}}
		var skip string
		stack.localAI, stack.mock, skip = stackBinaries(stack.root)
		if skip != "" {
			Skip(skip)
		}
		ctx := context.Background()
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("shutdown"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = pg.Terminate(context.Background()) })
		stack.pgURL, err = pg.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())
		nc, err := tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = nc.Terminate(context.Background()) })
		stack.natsURL, err = nc.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(stack.stopAll)

		fe = stack.frontend("frontend-shutdown", freePort())
		Eventually(func() error {
			_, err := stack.busOf(fe)
			return err
		}, settle, poll).Should(Succeed(), "the frontend did not come up")

		db, err = gorm.Open(pgdriver.Open(stack.pgURL), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
	})

	JustAfterEach(func() {
		if CurrentSpecReport().Failed() && stack != nil {
			stack.dumpLogs()
		}
	})

	It("releases the load at once and loads the model on the other worker", func() {
		slow := startWorker("worker-slow", "MOCK_LOAD_DELAY=10m")
		Eventually(func() string { n, _ := node("worker-slow"); return n.Status }, settle, poll).Should(Equal("healthy"))
		slowNode, _ := node("worker-slow")

		// The first request starts a load on the only worker, which hangs in it.
		go func() {
			defer GinkgoRecover()
			_ = stack.chat(fe)
		}()
		type jobRow struct {
			NodeID       string
			State        string
			OpConfirmed  bool
			StopDeadline *time.Time
		}
		slowJob := func() (jobRow, bool) {
			var rows []jobRow
			if db.Raw("SELECT node_id, state, op_confirmed, stop_deadline FROM model_load_jobs WHERE tracking_key = ? AND node_id = ?", stackModel, slowNode.ID).Scan(&rows).Error != nil || len(rows) == 0 {
				return jobRow{}, false
			}
			return rows[0], true
		}
		var loading int64
		Eventually(func() int64 {
			db.Raw("SELECT COUNT(*) FROM node_models WHERE node_id = ? AND model_name = ? AND state = 'loading'", slowNode.ID, stackModel).Scan(&loading)
			return loading
		}, settle, poll).Should(BeNumerically(">=", 1), "the load reached the backend of the slow worker")

		startWorker("worker-fast")
		Eventually(func() string { n, _ := node("worker-fast"); return n.Status }, settle, poll).Should(Equal("healthy"))

		sigterm := time.Now()
		Expect(syscall.Kill(slow.pid(), syscall.SIGTERM)).To(Succeed())

		// The job of the slow worker is failed and confirmed ended within
		// seconds: it no longer holds the model for the stop window.
		Eventually(func() bool {
			j, ok := slowJob()
			return ok && j.State == "failed" && j.OpConfirmed
		}, 30*time.Second, 200*time.Millisecond).Should(BeTrue(), "the load job is released by the shutdown")
		Expect(time.Since(sigterm)).To(BeNumerically("<", 30*time.Second))

		// The worker exits within its shutdown window.
		Eventually(slow.done, 90*time.Second).Should(BeClosed())

		// The model loads on the other worker.
		Eventually(func() error { return stack.chat(fe) }, 60*time.Second, time.Second).Should(Succeed())
		fastNode, _ := node("worker-fast")
		var onFast int64
		db.Raw("SELECT COUNT(*) FROM node_models WHERE node_id = ? AND model_name = ? AND state = 'loaded'", fastNode.ID, stackModel).Scan(&onFast)
		Expect(onFast).To(BeNumerically(">=", 1))
	})
})

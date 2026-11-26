package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/model/backendtest"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A worker upgrade keeps the build it has until the new one starts, with the
// readiness check of a model load, and answers what happened. Nothing here
// talks to NATS: the check and the restore are the same for a worker that
// holds only a tunnel.
var _ = Describe("Worker upgrade of a backend whose new build fails", func() {
	const name = "my-backend"
	var (
		root     string
		pidDir   string
		backends string
		src      string
		sup      *backendSupervisor
	)

	installedVersion := func() string {
		b, err := os.ReadFile(filepath.Join(backends, name, "version"))
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}
	upgrade := func(mode backendtest.Mode) workerctl.BackendUpgradeReply {
		Expect(os.RemoveAll(src)).To(Succeed())
		Expect(backendtest.WriteBuild(src, mode, "v2", pidDir)).To(Succeed())
		return sup.serveUpgrade(context.Background(), workerctl.BackendUpgradeRequest{Backend: name, URI: src, Name: name}, nil)
	}
	// running starts a process of the installed build for a model, as a
	// load would, so the upgrade has something to stop and start again.
	running := func(key string) {
		_, err := sup.startBackend(key, name, sup.findBackend(name), map[string]string{"MODEL_ENV": key})
		Expect(err).NotTo(HaveOccurred())
	}
	alive := func(key string) bool {
		sup.mu.Lock()
		defer sup.mu.Unlock()
		bp := sup.processes[key]
		return bp != nil && model.ProcessAlive(bp.proc)
	}

	BeforeEach(func() {
		skipOnWindows("the fixture backends are shell scripts, which Windows cannot execute")
		root = GinkgoT().TempDir()
		pidDir = filepath.Join(root, "pids")
		backends = filepath.Join(root, "backends")
		src = filepath.Join(root, "new-build")
		Expect(os.MkdirAll(pidDir, 0o750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithBackendSystemPath(filepath.Join(root, "system")), system.WithModelPath(filepath.Join(root, "models")))
		Expect(err).NotTo(HaveOccurred())
		Expect(backendtest.WriteBuild(filepath.Join(backends, name), backendtest.Serve, "v1", pidDir)).To(Succeed())
		meta, err := json.Marshal(gallery.BackendMetadata{Name: name, Version: "1.0.0"})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(backends, name, "metadata.json"), meta, 0o600)).To(Succeed())
		ml := model.NewModelLoader(state)
		Expect(gallery.RegisterBackends(state, ml)).To(Succeed())
		sup = &backendSupervisor{
			cfg:         &Config{BackendsPath: backends},
			ml:          ml,
			systemState: state,
			processes:   map[string]*backendProcess{},
			minPort:     43000, maxPort: 44000, nextPort: 43000,
		}
		DeferCleanup(func() {
			sup.stopAllBackends(true)
			Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "100ms").Should(BeEmpty(), "a backend process was left running")
		})
	})

	It("restores the previous build, answers start and rolled back with the cause, and starts the stopped processes again", func() {
		running("model#0")

		reply := upgrade(backendtest.Exit)

		Expect(reply.Success).To(BeFalse())
		Expect(reply.Code).To(Equal(string(gallery.UpgradeFailureStart)))
		Expect(reply.RolledBack).To(BeTrue())
		Expect(reply.Verified).To(BeFalse())
		Expect(reply.Cause).To(ContainSubstring(backendtest.ExitMessage))
		Expect(len(reply.Cause)).To(BeNumerically("<=", workerctl.CauseLimit))
		Expect(reply.StoppedProcessKeys).To(Equal([]string{"model#0"}))
		Expect(reply.ReportsStoppedProcesses).To(BeTrue())
		Expect(reply.RestartedProcessKeys).To(Equal([]string{"model#0"}))
		Expect(reply.RestartError).To(BeEmpty())

		Expect(installedVersion()).To(Equal("v1"))
		meta, err := os.ReadFile(filepath.Join(backends, name, "metadata.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(meta)).To(ContainSubstring(`"1.0.0"`))
		for _, leftover := range []string{".install-backup", ".install-tmp", ".failed"} {
			Expect(filepath.Join(backends, name+leftover)).NotTo(BeAnExistingFile(), leftover)
		}

		Expect(alive("model#0")).To(BeTrue(), "the stopped process runs again, on the restored build")
		sup.mu.Lock()
		Expect(sup.processes).NotTo(HaveKey(name+upgradeCheckKeySuffix), "the candidate start is not tracked after the check")
		Expect(sup.processes["model#0"].envVars).To(Equal(map[string]string{"MODEL_ENV": "model#0"}))
		sup.mu.Unlock()
		Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "100ms").Should(HaveLen(1), "only the restarted process runs")
	})

	It("keeps a new build that starts and says it was verified", func() {
		running("model#0")

		reply := upgrade(backendtest.Serve)

		Expect(reply.Success).To(BeTrue(), reply.Error)
		Expect(reply.Code).To(BeEmpty())
		Expect(reply.Verified).To(BeTrue())
		Expect(reply.RolledBack).To(BeFalse())
		Expect(reply.RestartedProcessKeys).To(Equal([]string{"model#0"}))
		Expect(installedVersion()).To(Equal("v2"))
		Expect(filepath.Join(backends, name+".install-backup")).NotTo(BeAnExistingFile())
		Expect(alive("model#0")).To(BeTrue())
		Eventually(func() []int { return backendtest.LivePIDs(pidDir) }, "10s", "100ms").Should(HaveLen(1))
	})

	It("stops a candidate that never becomes ready", func() {
		sup.startTimeout = 2 * time.Second
		started := time.Now()
		reply := upgrade(backendtest.Hang)
		Expect(time.Since(started)).To(BeNumerically(">=", 2*time.Second))
		Expect(reply.Cause).To(BeEmpty(), "a backend that never wrote anything has no output to report")
		Expect(reply.Error).To(ContainSubstring("did not become ready within 2s"))
		Expect(reply.Code).To(Equal(string(gallery.UpgradeFailureStart)))
		Expect(reply.RolledBack).To(BeTrue())
		Expect(installedVersion()).To(Equal("v1"))
	})

	It("answers a failure before the swap with its code and nothing rolled back", func() {
		reply := sup.serveUpgrade(context.Background(), workerctl.BackendUpgradeRequest{Backend: name, URI: filepath.Join(root, "missing"), Name: name}, nil)
		Expect(reply.Success).To(BeFalse())
		Expect(reply.Code).NotTo(BeEmpty())
		Expect(reply.RolledBack).To(BeFalse())
		Expect(reply.Verified).To(BeFalse())
		Expect(installedVersion()).To(Equal("v1"))
	})
})

var _ = Describe("BoundCause", func() {
	It("keeps the end of the output on one line within the limit", func() {
		long := ""
		for range 200 {
			long += "line of output\n"
		}
		long += "\x1b[31mfatal: the end\x1b[0m\n"
		got := workerctl.BoundCause(long)
		Expect(len(got)).To(BeNumerically("<=", workerctl.CauseLimit))
		Expect(got).To(HaveSuffix("fatal: the end"))
		Expect(got).NotTo(ContainSubstring("[31m"), "the terminal escape is removed whole")
		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).NotTo(ContainSubstring("\x1b"))
	})
})

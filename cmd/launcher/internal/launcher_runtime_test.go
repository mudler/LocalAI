// SPDX-License-Identifier: MIT
package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/workerledger"
	"github.com/mudler/LocalAI/pkg/workerstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The launcher owns one runtime at a time. These specs drive Launcher with a
// real child process standing in for the LocalAI binary.
var _ = Describe("Launcher runtime ownership", func() {
	var (
		dir     string
		runtime string
		l       *Launcher
	)

	fakeRuntime := func(capability string) {
		body := "#!/bin/sh\n" +
			"if [ \"$1\" = worker-status-capability ]; then echo " + capability + "; exit 0; fi\n" +
			"trap 'exit 0' TERM\n" +
			"env > \"$(dirname \"$0\")/child.env\"\n" +
			": > \"$(dirname \"$0\")/ready\"\n" +
			"while :; do sleep 0.05; done\n"
		Expect(os.WriteFile(runtime, []byte(body), 0o700)).To(Succeed())
	}
	waitReady := func() {
		Eventually(func() error { _, err := os.Stat(filepath.Join(dir, "ready")); return err }, 5*time.Second, 10*time.Millisecond).Should(Succeed())
	}
	workerDescription := func() launchDescription {
		return launchDescription{
			role:   roleWorker,
			args:   []string{"worker"},
			env:    []string{"PATH=/usr/bin:/bin", "LOCALAI_REGISTRATION_TOKEN=token-secret"},
			dir:    dir,
			ledger: filepath.Join(dir, "data", "worker-processes.json"),
		}
	}
	newLauncher := func() *Launcher {
		n := &Launcher{config: &Config{ModelsPath: filepath.Join(dir, "models")}}
		n.lockDir = dir
		n.life = newLifecycle()
		n.life.termGrace = 500 * time.Millisecond
		n.life.killGrace = 2 * time.Second
		return n
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		runtime = filepath.Join(dir, "local-ai")
		l = newLauncher()
		DeferCleanup(func() { _ = l.Shutdown() })
	})

	It("runs a standalone runtime and stops it with confirmation", func() {
		fakeRuntime("irrelevant")
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		waitReady()
		Expect(l.IsRunning()).To(BeTrue())
		pid := l.life.PID()
		Expect(l.StopLocalAI()).To(Succeed())
		Expect(l.IsRunning()).To(BeFalse())
		Expect(workerledger.GroupAlive(pid)).To(BeFalse())
		Expect(errors.Is(l.StopLocalAI(), errNotRunning)).To(BeTrue())
	})

	It("does not start a worker whose runtime lacks the status contract", func() {
		fakeRuntime("old-version")
		Expect(l.launchRuntime(runtime, workerDescription())).To(MatchError(errWorkerUpgrade))
		Expect(l.IsRunning()).To(BeFalse())
		_, err := os.Stat(filepath.Join(dir, "ready"))
		Expect(err).To(HaveOccurred(), "the unsupported runtime must never run")
	})

	It("gives a worker only the private status channel and never reports ready without evidence", func() {
		fakeRuntime(workerstatus.Capability)
		Expect(l.launchRuntime(runtime, workerDescription())).To(Succeed())
		waitReady()
		env, err := os.ReadFile(filepath.Join(dir, "child.env"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(env)).To(ContainSubstring(workerstatus.PathEnv + "="))
		Expect(string(env)).To(ContainSubstring(workerstatus.GenerationEnv + "="))
		// Process liveness alone is not readiness.
		Expect(l.WorkerStatus()).ToNot(ContainSubstring("Ready"))
		Expect(l.WorkerStatus()).To(ContainSubstring("Connection problem"))
	})

	It("reports ready only from a current, complete snapshot of the running child", func() {
		fakeRuntime(workerstatus.Capability)
		Expect(l.launchRuntime(runtime, workerDescription())).To(Succeed())
		waitReady()
		path, generation := l.statusPath, l.statusGeneration
		Expect(path).ToNot(BeEmpty())
		now := time.Now()
		snapshot := workerstatus.Snapshot{
			PID: l.life.PID(), Schema: workerstatus.Schema, Generation: generation, Sequence: 1, Time: now,
			Registration: "healthy", RegistrationTime: now, Credentials: true, Authenticated: true, Connected: true,
			Subscriptions: true, Armed: true, LocalReady: true, Heartbeat: now, HeartbeatInterval: 10 * time.Second,
		}
		Expect(workerstatus.Write(path, snapshot)).To(Succeed())
		Expect(l.WorkerStatus()).To(ContainSubstring("Ready"))

		// A snapshot written by some other process id is not evidence about this child.
		snapshot.PID, snapshot.Sequence = l.life.PID()+1, 2
		Expect(workerstatus.Write(path, snapshot)).To(Succeed())
		Expect(l.WorkerStatus()).ToNot(ContainSubstring("Ready"))
	})

	It("refuses a second launcher for the same user and keeps the first running", func() {
		fakeRuntime("irrelevant")
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		other := newLauncher()
		DeferCleanup(func() { _ = other.Shutdown() })
		Expect(other.launchRuntime(runtime, d)).To(MatchError(errLauncherLocked))
		Expect(l.IsRunning()).To(BeTrue())
	})

	It("lets a new launcher start after the first one shut down", func() {
		fakeRuntime("irrelevant")
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		Expect(l.Shutdown()).To(Succeed())
		other := newLauncher()
		DeferCleanup(func() { _ = other.Shutdown() })
		Expect(other.launchRuntime(runtime, d)).To(Succeed())
	})

	It("does not report stopped or release the lock when shutdown is incomplete", func() {
		fakeRuntime(workerstatus.Capability)
		d := workerDescription()
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		waitReady()
		child := exec.Command("/bin/sleep", "300")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		Expect(child.Start()).To(Succeed())
		go func() { _ = child.Wait() }()
		DeferCleanup(func() { _ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL) })
		Expect(workerledger.WriteFile(d.ledger, []workerledger.Entry{{Key: "m#0", PID: child.Process.Pid, StartTime: workerledger.ReadStartTime(child.Process.Pid)}})).To(Succeed())

		realKill, realReap := workerledger.KillGroup, workerledger.ReapTimeout
		workerledger.KillGroup = func(int) error { return nil }
		workerledger.ReapTimeout = 200 * time.Millisecond
		DeferCleanup(func() { workerledger.KillGroup, workerledger.ReapTimeout = realKill, realReap })

		err := l.StopLocalAI()
		Expect(errors.Is(err, errShutdownIncomplete)).To(BeTrue())
		Expect(l.IsRunning()).To(BeTrue(), "stop stays available for a retry")
		Expect(l.Shutdown()).To(MatchError(errShutdownIncomplete))
		Expect(strings.ToLower(l.StatusText())).To(ContainSubstring("shutdown incomplete"))
		Expect(newLauncher().launchRuntime(runtime, d)).To(MatchError(errLauncherLocked), "no overlapping role while ownership is unresolved")

		workerledger.KillGroup = realKill
		Expect(l.StopLocalAI()).To(Succeed())
		Expect(l.IsRunning()).To(BeFalse())
	})
})

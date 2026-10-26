// SPDX-License-Identifier: MIT
package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/workerledger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Role changes and launch-setting changes never overlap a live runtime: the
// owner confirms a stop, the stop is verified, and only then is the new profile
// committed. Nothing starts the replacement automatically.
var _ = Describe("Applying settings to a running launcher", func() {
	var (
		dir     string
		runtime string
		l       *Launcher
	)

	cfg := func(mode string) *Config {
		c := &Config{Mode: mode, ModelsPath: filepath.Join(dir, "models"), BackendsPath: filepath.Join(dir, "backends"), Address: ":8080", LogLevel: "info"}
		c.Worker.Connection = WorkerConnectionConfig{FrontendURL: "https://frontend.example", RegistrationToken: "token-secret", AdvertisedAddress: "worker.example:50051"}
		c.Worker.NATS.URL = "tls://nats.example:4222"
		return c
	}
	runStandalone := func() {
		body := "#!/bin/sh\ntrap 'exit 0' TERM\n: > \"$(dirname \"$0\")/ready\"\nwhile :; do sleep 0.05; done\n"
		Expect(os.WriteFile(runtime, []byte(body), 0o700)).To(Succeed())
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		Eventually(func() error { _, err := os.Stat(filepath.Join(dir, "ready")); return err }, 5*time.Second, 10*time.Millisecond).Should(Succeed())
	}
	saved := func() string {
		data, err := os.ReadFile(filepath.Join(dir, ".localai", "launcher.json"))
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", dir)
		Expect(os.MkdirAll(filepath.Join(dir, ".localai"), 0o700)).To(Succeed())
		runtime = filepath.Join(dir, "local-ai")
		l = &Launcher{config: cfg("standalone"), lockDir: dir, life: newLifecycle()}
		l.life.termGrace = 500 * time.Millisecond
		l.life.killGrace = 2 * time.Second
		Expect(l.SetConfig(l.config)).To(Succeed())
		DeferCleanup(func() { _ = l.Shutdown() })
	})

	It("commits a change at once when nothing runs", func() {
		Expect(l.ApplySettings(cfg("worker"), false)).To(Succeed())
		Expect(l.GetConfig().Mode).To(Equal("worker"))
		Expect(saved()).To(ContainSubstring(`"mode": "worker"`))
		Expect(l.IsRunning()).To(BeFalse(), "applying never starts a role")
	})

	It("asks for a confirmed stop before a role switch and changes nothing until then", func() {
		runStandalone()
		before := saved()
		err := l.ApplySettings(cfg("worker"), false)
		Expect(errors.Is(err, errStopRequired)).To(BeTrue())
		Expect(l.GetConfig().Mode).To(Equal("standalone"), "cancel leaves the active configuration unchanged")
		Expect(saved()).To(Equal(before))
		Expect(l.IsRunning()).To(BeTrue())
	})

	It("stops, verifies, then commits, and does not start the replacement", func() {
		runStandalone()
		pid := l.life.PID()
		Expect(l.ApplySettings(cfg("worker"), true)).To(Succeed())
		Expect(l.IsRunning()).To(BeFalse())
		Expect(workerledger.GroupAlive(pid)).To(BeFalse())
		Expect(l.GetConfig().Mode).To(Equal("worker"))
		Expect(saved()).To(ContainSubstring(`"mode": "worker"`))
	})

	It("asks for a stop when a launch setting of the active profile changes", func() {
		runStandalone()
		next := cfg("standalone")
		next.Address = ":9090"
		Expect(errors.Is(l.ApplySettings(next, false), errStopRequired)).To(BeTrue())
		next = cfg("standalone")
		next.ModelsPath = filepath.Join(dir, "other-models")
		Expect(errors.Is(l.ApplySettings(next, false), errStopRequired)).To(BeTrue())
	})

	It("lets an inactive profile and unrelated settings change under a running role", func() {
		runStandalone()
		next := cfg("standalone")
		next.Worker.Connection.FrontendURL = "https://other.example"
		welcome := false
		next.ShowWelcome = &welcome
		Expect(l.ApplySettings(next, false)).To(Succeed())
		Expect(l.IsRunning()).To(BeTrue(), "the running role is untouched")
		Expect(l.GetConfig().Worker.Connection.FrontendURL).To(Equal("https://other.example"))
	})

	It("does not commit and keeps stop available when shutdown is incomplete", func() {
		body := "#!/bin/sh\ntrap 'exit 0' TERM\n: > \"$(dirname \"$0\")/ready\"\nwhile :; do sleep 0.05; done\n"
		Expect(os.WriteFile(runtime, []byte(body), 0o700)).To(Succeed())
		ledger := filepath.Join(dir, "data", "worker-processes.json")
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir, ledger: ledger}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		Eventually(func() error { _, err := os.Stat(filepath.Join(dir, "ready")); return err }, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		backend := exec.Command("/bin/sleep", "300")
		backend.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		Expect(backend.Start()).To(Succeed())
		go func() { _ = backend.Wait() }()
		DeferCleanup(func() { _ = syscall.Kill(-backend.Process.Pid, syscall.SIGKILL) })
		Expect(workerledger.WriteFile(ledger, []workerledger.Entry{{Key: "m#0", PID: backend.Process.Pid, StartTime: workerledger.ReadStartTime(backend.Process.Pid)}})).To(Succeed())

		realKill, realReap := workerledger.KillGroup, workerledger.ReapTimeout
		workerledger.KillGroup = func(int) error { return nil } // the signal never lands
		workerledger.ReapTimeout = 200 * time.Millisecond
		DeferCleanup(func() { workerledger.KillGroup, workerledger.ReapTimeout = realKill, realReap })

		before := saved()
		err := l.ApplySettings(cfg("worker"), true)
		Expect(errors.Is(err, errShutdownIncomplete)).To(BeTrue())
		Expect(l.GetConfig().Mode).To(Equal("standalone"), "a role is never switched over unresolved ownership")
		Expect(saved()).To(Equal(before))
		Expect(l.IsRunning()).To(BeTrue(), "stop stays available for a retry")
		workerledger.KillGroup = realKill
	})

	It("refuses start, save and a second apply while a stop-and-apply is under way", func() {
		body := "#!/bin/sh\ntrap '' TERM\n: > \"$(dirname \"$0\")/ready\"\nwhile :; do sleep 0.05; done\n"
		Expect(os.WriteFile(runtime, []byte(body), 0o700)).To(Succeed())
		d := launchDescription{role: roleStandalone, args: []string{"run"}, env: []string{"PATH=/usr/bin:/bin"}, dir: dir}
		Expect(l.launchRuntime(runtime, d)).To(Succeed())
		Eventually(func() error { _, err := os.Stat(filepath.Join(dir, "ready")); return err }, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		done := make(chan error, 1)
		go func() { done <- l.ApplySettings(cfg("worker"), true) }()
		Eventually(l.Applying).Should(BeTrue())
		Expect(errors.Is(l.ApplySettings(cfg("worker"), true), errLifecycleBusy)).To(BeTrue())
		Expect(errors.Is(l.StartLocalAI(), errLifecycleBusy)).To(BeTrue())
		Expect(errors.Is(l.DownloadUpdate("v0.0.0", nil), errLifecycleBusy)).To(BeTrue())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		Expect(l.Applying()).To(BeFalse())
	})

	Describe("starting the selected mode when the launcher opens", func() {
		It("keeps the legacy default for standalone and requires an explicit choice for a worker", func() {
			c := cfg("standalone")
			l.config = c
			Expect(l.ShouldAutoStartServer()).To(BeTrue(), "unset still means enabled for standalone")
			c = cfg("worker")
			l.config = c
			Expect(l.ShouldAutoStartServer()).To(BeFalse(), "joining a deployment is never an unasked default")
			yes := true
			c.AutoStart = &yes
			Expect(l.ShouldAutoStartServer()).To(BeTrue())
			Expect(l.GetConfig().Mode).To(Equal("worker"))
		})
	})

	Describe("the deployment UI", func() {
		It("is the saved frontend URL without credentials", func() {
			c := cfg("worker")
			c.Worker.Connection.FrontendURL = "https://frontend.example/prefix/"
			l.config = c
			u, err := l.DeploymentURL()
			Expect(err).ToNot(HaveOccurred())
			Expect(u).To(Equal("https://frontend.example/prefix"))
			Expect(u).ToNot(ContainSubstring("token-secret"))
			Expect(l.GetWebUIURL()).To(Equal(u), "worker mode has no local server UI")
		})

		It("refuses an unsafe or missing frontend URL", func() {
			for _, bad := range []string{"", "https://user:pw@frontend.example", "ftp://frontend.example", "https://frontend.example?x=1"} {
				c := cfg("worker")
				c.Worker.Connection.FrontendURL = bad
				l.config = c
				_, err := l.DeploymentURL()
				Expect(err).To(HaveOccurred(), bad)
			}
		})
	})

	Describe("plaintext connections", func() {
		It("start only after an explicit acknowledgement for this launcher session", func() {
			c := cfg("worker")
			c.Worker.Connection.FrontendURL = "http://frontend.example"
			l.config = c
			_, err := l.describeLaunch()
			Expect(err).To(HaveOccurred())
			l.SetPlaintextAcknowledged(true)
			_, err = l.describeLaunch()
			Expect(err).ToNot(HaveOccurred())
			l.SetPlaintextAcknowledged(false)
			_, err = l.describeLaunch()
			Expect(err).To(HaveOccurred())
		})
	})
})

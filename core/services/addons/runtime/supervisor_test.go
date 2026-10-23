//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRuntime(t *testing.T) {
	if root := os.Getenv("ADDON_OWNER_HELPER"); root != "" {
		b, e := os.ReadFile(os.Getenv("ADDON_HELPER_RECEIPT"))
		if e != nil {
			os.Exit(20)
		}
		var p packages.PackageReceipt
		if json.Unmarshal(b, &p) != nil {
			os.Exit(21)
		}
		c := Config{Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second}
		setStateRoot(&c, root)
		s, e := NewSupervisor(c)
		if e != nil {
			os.Exit(22)
		}
		i, e := s.Start(context.Background(), p, 1)
		if e != nil {
			os.Exit(23)
		}
		if os.WriteFile(filepath.Join(root, "helper-pid"), []byte(strconv.Itoa(i.PID)), 0600) != nil {
			os.Exit(24)
		}
		select {}
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Addon runtime")
}

func setStateRoot(c *Config, root string) { c.StateRoot = root }
func processRunning(pid int) bool {
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if os.IsNotExist(e) {
		return false
	}
	Expect(e).NotTo(HaveOccurred())
	fields := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+1:])
	return fields[0] != "Z" && fields[0] != "X"
}

var binary string
var _ = BeforeSuite(func() {
	binary = filepath.Join(GinkgoT().TempDir(), "fixture")
	cmd := exec.Command("go", "build", "-o", binary, "../../../../tests/addons/fixture/cmd")
	out, e := cmd.CombinedOutput()
	Expect(e).NotTo(HaveOccurred(), string(out))
})

func fixture(mode string, capabilities contract.Capabilities) packages.PackageReceipt {
	dir := GinkgoT().TempDir()
	b, e := os.ReadFile(binary)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "fixture"), b, 0500)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"`+mode+`"}`), 0600)).To(Succeed())
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "fixture", OS: goruntime.GOOS, Arch: goruntime.GOARCH, Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: capabilities}
	p := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:fixture"}
	b, e = json.Marshal(p)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0400)).To(Succeed())
	return p
}
func supervisor() *Supervisor {
	l := contract.DefaultLimits()
	l.ReadTimeout = 300 * time.Millisecond
	l.ShutdownTimeout = 100 * time.Millisecond
	l.BackoffInitial = 20 * time.Millisecond
	l.BackoffMax = 40 * time.Millisecond
	l.BackoffAttempts = 2
	s, e := NewSupervisor(Config{StateRoot: GinkgoT().TempDir(), Limits: l, ReadyTimeout: 300 * time.Millisecond, StableHealth: time.Second})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
	return s
}

var service = contract.Capabilities{Service: true}
var _ = Describe("Managed subprocess", func() {
	It("registers only ready generation bound service headless and UI capabilities", func() {
		for _, c := range []contract.Capabilities{service, {Tools: []string{"read"}}, {Tools: []string{"read"}, UI: &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 1, Max: 2}}}} {
			s := supervisor()
			i, e := s.Start(context.Background(), fixture("normal", c), 7)
			Expect(e).NotTo(HaveOccurred())
			Expect(i.PID).To(BeNumerically(">", 0))
			Expect(i.Incarnation).NotTo(BeEmpty())
			r, e := s.Registry.Snapshot(context.Background())
			Expect(e).NotTo(HaveOccurred())
			Expect(r).To(HaveLen(1))
			Expect(r[0].Generation).To(Equal(uint64(7)))
			Expect(r[0].Capabilities).To(Equal(c))
			Expect(s.Stop(context.Background(), i)).To(Succeed())
			r, e = s.Registry.Snapshot(context.Background())
			Expect(e).NotTo(HaveOccurred())
			Expect(r).To(BeEmpty())
		}
	})
	It("rejects forged registration nonloopback and stale incarnation handshakes", func() {
		for _, mode := range []string{"forged", "nonloopback", "stale", "wrong-version"} {
			s := supervisor()
			_, e := s.Start(context.Background(), fixture(mode, service), 1)
			Expect(e).To(HaveOccurred())
			r, e := s.Registry.Snapshot(context.Background())
			Expect(e).NotTo(HaveOccurred())
			Expect(r).To(BeEmpty())
		}
	})
	It("bounds readiness and handles exit during start and stop", func() {
		for _, mode := range []string{"never-ready", "exit-start"} {
			s := supervisor()
			start := time.Now()
			_, e := s.Start(context.Background(), fixture(mode, service), 1)
			Expect(e).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		}
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})
	It("withdraws immediately drains boundedly and rejects stale process identities", func() {
		s := supervisor()
		p := fixture("normal", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		forged := i
		forged.Incarnation = "another"
		Expect(s.Stop(context.Background(), forged)).To(HaveOccurred())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		j, e := s.Start(context.Background(), p, 2)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(r[0].Incarnation).To(Equal(j.Incarnation))
	})
	It("exhausts restart budget without retaining dead registrations", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("crash", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Eventually(func() int { return s.Attempts("sample") }, 3*time.Second).Should(Equal(2))
		Eventually(func() int { r, _ := s.Registry.Snapshot(context.Background()); return len(r) }, 3*time.Second).Should(BeZero())
		Consistently(func() int { return s.Attempts("sample") }, 200*time.Millisecond).Should(Equal(2))
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})
	It("does not inherit secrets and exits when the private parent channel closes", func() {
		GinkgoT().Setenv("LOCALAI_RUNTIME_TEST_SECRET", "must-not-leak")
		GinkgoT().Setenv("HTTP_PROXY", "http://127.0.0.1:1")
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("env-check", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.parent.Close()).To(Succeed())
		Eventually(i.process.done, 2*time.Second).Should(BeClosed())
	})
})

var _ = Describe("Incarnation recovery", func() {
	It("rejects foreign supervisor handles and numeric PID substitutions", func() {
		s := supervisor()
		other := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(other.Stop(context.Background(), i)).To(HaveOccurred())
		forged := i
		forged.PID = os.Getpid()
		Expect(s.Stop(context.Background(), forged)).To(HaveOccurred())
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
	})
	It("resets restart budget only after sustained authenticated health", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() int { return s.Attempts("sample") }, 2*time.Second).Should(Equal(1))
		Eventually(func() int { return s.Attempts("sample") }, 3*time.Second).Should(BeZero())
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(r[0].Incarnation).NotTo(Equal(i.Incarnation))
	})
})

var _ = Describe("Observed state during restart", func() {
	It("never reports failed between a launch starting and the restarted process being published", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		var seen atomic.Int32
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				launching := false
				for {
					select {
					case <-stop:
						return
					default:
					}
					switch s.State("sample", 1) {
					case StateStarting:
						launching = true
					case StateFailed:
						if launching {
							seen.Add(1)
						}
					case StateRunning:
						launching = false
					}
				}
			}()
		}
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() int { return s.Attempts("sample") }, 2*time.Second).Should(Equal(1))
		Eventually(func() string { r, _ := s.Registry.Snapshot(context.Background()); return fmt.Sprint(len(r)) }, 3*time.Second).Should(Equal("1"))
		Eventually(func() string { return s.State("sample", 1) }, 3*time.Second).Should(Equal(StateRunning))
		close(stop)
		wg.Wait()
		Expect(seen.Load()).To(BeZero(), "a launch under way was reported as failed")
	})
})

var _ = Describe("Restart safety regressions", func() {
	It("reserves a draining slot against concurrent starts", func() {
		s := supervisor()
		p := fixture("normal", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		stream, e := i.Client.OpenOutput(context.Background(), envelope("output"), "slow")
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(stream.Close)
		done := make(chan error, 1)
		go func() { done <- s.Stop(context.Background(), i) }()
		Eventually(func() bool { return i.process.client.admitting() }).Should(BeFalse())
		select {
		case <-i.process.done:
			Fail("process exited before drain race was exercised")
		default:
		}
		_, e = s.Start(context.Background(), p, 2)
		Expect(e).To(HaveOccurred(), "Start overlapped the draining process")
		Expect(<-done).To(Succeed())
		Expect(i.process.done).To(BeClosed())
	})
	It("waits for a restart launch worker before acknowledging stop", func() {
		s := supervisor()
		p := fixture("normal", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		started := make(chan *process, 1)
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() { once.Do(func() { close(release) }) })
		// This boundary pauses a real launch after exec and before readiness. It is
		// not a substitute process; cancellation must join the actual launch worker.
		s.mu.Lock()
		s.launchStarted = func(p *process) { started <- p; <-release }
		s.mu.Unlock()
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		var child *process
		Eventually(started, time.Second).Should(Receive(&child))
		done := make(chan error, 1)
		go func() { done <- s.Stop(context.Background(), i) }()
		Consistently(done, 50*time.Millisecond).ShouldNot(Receive(), "Stop acknowledged a live unpublished restart child")
		once.Do(func() { close(release) })
		Eventually(done, time.Second).Should(Receive(Succeed()))
		Expect(child.done).To(BeClosed())
	})
	It("exposes a new incarnation client after restart without reviving the old client", func() {
		s := supervisor()
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() string {
			r, _ := s.Registry.Snapshot(context.Background())
			if len(r) == 0 {
				return ""
			}
			return r[0].Incarnation
		}, time.Second).Should(And(Not(BeEmpty()), Not(Equal(i.Incarnation))))
		lookup, ok := any(s).(interface {
			Current(context.Context, string, uint64) (Instance, error)
		})
		Expect(ok).To(BeTrue(), "ready registry has no current-incarnation client lookup")
		next, e := lookup.Current(context.Background(), "sample", 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(next.Incarnation).NotTo(Equal(i.Incarnation))
		_, e = next.Client.Read(context.Background(), envelope("read"))
		Expect(e).NotTo(HaveOccurred())
		_, e = i.Client.Apply(context.Background(), envelope("apply"), contract.Revision{Schema: 1, Data: 1})
		Expect(e).To(HaveOccurred())
	})
})

var _ = Describe("Process state observation", func() {
	// slowBackoff leaves a window between a crash and the next launch in which
	// the failed state can be observed without racing the restart.
	slowBackoff := func() *Supervisor {
		l := contract.DefaultLimits()
		l.ReadTimeout = 300 * time.Millisecond
		l.ShutdownTimeout = 100 * time.Millisecond
		l.BackoffInitial = 1500 * time.Millisecond
		l.BackoffMax = 2 * time.Second
		l.BackoffAttempts = 3
		s, e := NewSupervisor(Config{StateRoot: GinkgoT().TempDir(), Limits: l, ReadyTimeout: 300 * time.Millisecond, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		return s
	}
	It("reports stopped, running, failed, starting and running again across a crash and a restart", func() {
		s := slowBackoff()
		Expect(s.State("sample", 1)).To(Equal(StateStopped), "nothing was started")
		i, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.State("sample", 1)).To(Equal(StateRunning))
		Expect(s.State("sample", 2)).To(Equal(StateStopped), "another generation is not this process")
		Expect(s.State("other", 1)).To(Equal(StateStopped))

		// Hold the restart after exec and before readiness: the process is
		// starting, not failed and not running.
		started := make(chan *process, 1)
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() { once.Do(func() { close(release) }) })
		s.mu.Lock()
		s.launchStarted = func(p *process) { started <- p; <-release }
		s.mu.Unlock()
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() string { return s.State("sample", 1) }, time.Second).Should(Equal(StateFailed), "the child died and no launch is under way")
		Consistently(func() string { return s.State("sample", 1) }, 100*time.Millisecond).Should(Equal(StateFailed))
		Eventually(started, 3*time.Second).Should(Receive())
		Expect(s.State("sample", 1)).To(Equal(StateStarting))
		once.Do(func() { close(release) })
		Eventually(func() string { return s.State("sample", 1) }, 3*time.Second).Should(Equal(StateRunning))
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1), "the observation agrees with the registry")

		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Expect(s.State("sample", 1)).To(Equal(StateStopped))
	})
	It("keeps reporting failed once the restart budget is exhausted", func() {
		s := supervisor()
		_, e := s.Start(context.Background(), fixture("crash", service), 1)
		Expect(e).NotTo(HaveOccurred())
		s.mu.Lock()
		slot := s.slots["sample"]
		s.mu.Unlock()
		// The monitor returns only when the budget is spent.
		Eventually(slot.workerDone, 5*time.Second).Should(BeClosed())
		Expect(s.Attempts("sample")).To(Equal(2))
		Expect(s.State("sample", 1)).To(Equal(StateFailed))
		Consistently(func() string { return s.State("sample", 1) }, 300*time.Millisecond).Should(Equal(StateFailed))
	})
})

var _ = Describe("Durable orphan recovery", func() {
	It("kills the exact paused child of a killed owner before launching its replacement", func() {
		root := GinkgoT().TempDir()
		pkg := fixture("normal", service)
		helper := exec.Command(os.Args[0], "-test.run=^TestRuntime$")
		helper.Env = append(os.Environ(), "ADDON_OWNER_HELPER="+root, "ADDON_HELPER_RECEIPT="+filepath.Join(pkg.Directory, "receipt.json"))
		Expect(helper.Start()).To(Succeed())
		DeferCleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
		var pid int
		Eventually(func() int {
			b, _ := os.ReadFile(filepath.Join(root, "helper-pid"))
			pid, _ = strconv.Atoi(string(b))
			return pid
		}, 3*time.Second).Should(BeNumerically(">", 0))
		DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		Expect(syscall.Kill(pid, syscall.SIGSTOP)).To(Succeed())
		Expect(helper.Process.Kill()).To(Succeed())
		Expect(helper.Wait()).To(HaveOccurred())
		Expect(processRunning(pid)).To(BeTrue(), "paused child must survive owner death to exercise recovery")
		c := Config{Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second}
		setStateRoot(&c, root)
		s, e := NewSupervisor(c)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		_, e = s.Start(context.Background(), pkg, 2)
		Expect(e).NotTo(HaveOccurred())
		Expect(processRunning(pid)).To(BeFalse(), "new owner launched while the prior owner's exact child was still paused")
	})
})

var _ = Describe("Recovery fencing", func() {
	It("fails closed on ambiguous launch identities and preserves unrelated reused PIDs", func() {
		for _, mode := range []string{"pending", "missing-start", "reused", "boot"} {
			root := GinkgoT().TempDir()
			other := exec.Command("sleep", "30")
			Expect(other.Start()).To(Succeed())
			DeferCleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
			ident, e := identity(other.Process.Pid)
			Expect(e).NotTo(HaveOccurred())
			ident.Incarnation = "test"
			switch mode {
			case "pending":
				ident.Pending = true
			case "missing-start":
				ident.Start = ""
			case "reused":
				ident.Start = "1"
			case "boot":
				ident.Boot = "prior-boot"
			}
			b, e := json.Marshal(map[string]processIdentity{"sample": ident})
			Expect(e).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(root, "processes.json"), b, 0600)).To(Succeed())
			s, e := NewSupervisor(Config{StateRoot: root, Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
			if mode == "pending" || mode == "missing-start" {
				Expect(e).To(HaveOccurred())
			} else {
				Expect(e).NotTo(HaveOccurred())
				Expect(s.Close(context.Background())).To(Succeed())
			}
			Expect(processRunning(other.Process.Pid)).To(BeTrue())
		}
	})
	It("retains a canceled restart slot until cleanup completes and rejects duplicate supervisors", func() {
		s := supervisor()
		p := fixture("normal", service)
		_, e := NewSupervisor(s.config)
		Expect(e).To(HaveOccurred())
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		started := make(chan *process, 1)
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() { once.Do(func() { close(release) }) })
		s.mu.Lock()
		s.launchStarted = func(p *process) { started <- p; <-release }
		s.mu.Unlock()
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		var child *process
		Eventually(started, time.Second).Should(Receive(&child))
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(s.Stop(canceled, i)).To(HaveOccurred())
		_, e = s.Start(context.Background(), p, 2)
		Expect(e).To(HaveOccurred())
		Expect(s.Current(context.Background(), "sample", 1)).Error().To(HaveOccurred())
		once.Do(func() { close(release) })
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Expect(child.done).To(BeClosed())
	})
})

var _ = Describe("Durable cleanup failures", func() {
	It("refuses a missing established ledger and fences failed cleanup publication", func() {
		s := supervisor()
		p := fixture("normal", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		root := s.config.StateRoot
		// A filesystem obstruction makes the next durable removal fail.
		Expect(os.Mkdir(filepath.Join(root, "processes.tmp"), 0700)).To(Succeed())
		Expect(s.Stop(context.Background(), i)).To(HaveOccurred())
		Expect(i.process.done).To(BeClosed())
		_, e = s.Start(context.Background(), p, 2)
		Expect(e).To(HaveOccurred())
		Expect(os.Remove(filepath.Join(root, "processes.tmp"))).To(Succeed())
		// The safety fence deliberately survives local I/O repair. Leave cleanup to
		// a newly recovered supervisor only after closing this test-owned lock.
		Expect(s.Close(context.Background())).To(HaveOccurred())
		s.ledger.mu.Lock()
		s.ledger.failed = false
		s.ledger.mu.Unlock()
		// Preserve the error slot until Close has reported it; this test-only reset
		// permits resource cleanup without changing production recovery behavior.
		s.mu.Lock()
		delete(s.slots, "sample")
		s.mu.Unlock()
		Expect(s.Close(context.Background())).To(Succeed())
		Expect(os.Remove(filepath.Join(root, "processes.json"))).To(Succeed())
		next, e := NewSupervisor(s.config)
		if e == nil {
			_ = next.Close(context.Background())
		}
		Expect(e).To(HaveOccurred(), "an established owner must not forget its missing process ledger")
	})
})

var _ = Describe("Close all slots", func() {
	It("withdraws and initiates every live slot before a cleanup timeout", func() {
		s := supervisor()
		var instances []Instance
		for _, id := range []string{"sample", "second", "third"} {
			p := fixture("normal", service)
			p.Manifest.ID = id
			b, e := json.Marshal(p)
			Expect(e).NotTo(HaveOccurred())
			Expect(os.Chmod(filepath.Join(p.Directory, "receipt.json"), 0600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(p.Directory, "receipt.json"), b, 0400)).To(Succeed())
			i, e := s.Start(context.Background(), p, 1)
			Expect(e).NotTo(HaveOccurred())
			instances = append(instances, i)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(s.Close(canceled)).To(HaveOccurred())
		for _, i := range instances {
			Expect(i.process.client.admitting()).To(BeFalse())
			s.mu.Lock()
			stopped := i.slot.stopped
			s.mu.Unlock()
			Expect(stopped).To(BeTrue())
		}
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(BeEmpty())
		Expect(s.Close(context.Background())).To(Succeed())
		for _, i := range instances {
			Expect(processRunning(i.PID)).To(BeFalse())
		}
	})
})

var _ = Describe("Addon data directory", func() {
	withID := func(id string) packages.PackageReceipt {
		p := fixture("normal", service)
		p.Manifest.ID = id
		b, e := json.Marshal(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(os.Chmod(filepath.Join(p.Directory, "receipt.json"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p.Directory, "receipt.json"), b, 0400)).To(Succeed())
		return p
	}
	supervisorWith := func(dataRoot string) *Supervisor {
		l := contract.DefaultLimits()
		l.ReadTimeout = 300 * time.Millisecond
		l.ShutdownTimeout = 100 * time.Millisecond
		s, e := NewSupervisor(Config{StateRoot: GinkgoT().TempDir(), DataRoot: dataRoot, Limits: l, ReadyTimeout: 2 * time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		return s
	}
	// seen reads what the fixture received in its startup object.
	seen := func(p packages.PackageReceipt) string {
		var b []byte
		Eventually(func() error {
			var e error
			b, e = os.ReadFile(filepath.Join(p.Directory, "fixture.datadir"))
			return e
		}, 2*time.Second).Should(Succeed())
		return string(b)
	}

	It("creates one private data directory per addon and passes its path in the startup object", func() {
		root := filepath.Join(GinkgoT().TempDir(), "data")
		s := supervisorWith(root)
		first, second := withID("sample"), withID("second")
		_, e := s.Start(context.Background(), first, 1)
		Expect(e).NotTo(HaveOccurred())
		_, e = s.Start(context.Background(), second, 1)
		Expect(e).NotTo(HaveOccurred())
		for _, p := range []packages.PackageReceipt{first, second} {
			want := filepath.Join(root, p.Manifest.ID)
			Expect(seen(p)).To(Equal(want))
			fi, e := os.Lstat(want)
			Expect(e).NotTo(HaveOccurred())
			Expect(fi.IsDir()).To(BeTrue())
			Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o700)))
			// The addon could write there, and the package directory stays
			// free of data.
			Expect(filepath.Join(want, "fixture.probe")).To(BeARegularFile())
			Expect(filepath.Join(p.Directory, "fixture.probe")).NotTo(BeAnExistingFile())
		}
	})

	It("gives no data directory without a data root and refuses an unsafe root or addon ID", func() {
		s := supervisor()
		p := withID("sample")
		_, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(seen(p)).To(BeEmpty())

		relative := supervisorWith("relative/data")
		_, e = relative.Start(context.Background(), withID("sample"), 1)
		Expect(e).To(HaveOccurred())

		root := filepath.Join(GinkgoT().TempDir(), "data")
		strict := supervisorWith(root)
		_, e = strict.Start(context.Background(), withID("a/b"), 1)
		Expect(e).To(HaveOccurred())
		entries, _ := os.ReadDir(root)
		Expect(entries).To(BeEmpty())
	})
	hostOpsSeen := func(p packages.PackageReceipt) (endpoint, credential string) {
		var b []byte
		Eventually(func() error {
			var e error
			b, e = os.ReadFile(filepath.Join(p.Directory, "fixture.hostops"))
			return e
		}, 2*time.Second).Should(Succeed())
		parts := strings.SplitN(string(b), "\n", 2)
		return parts[0], parts[1]
	}
	hostOpsSupervisor := func(allowed map[string][]string) (*Supervisor, *hostops.InFlight) {
		l := contract.DefaultLimits()
		l.ReadTimeout = 300 * time.Millisecond
		l.ShutdownTimeout = 100 * time.Millisecond
		in := hostops.NewInFlight()
		b := hostops.New(in, func(string, string) bool { return true }, l)
		Expect(b.Register(hostops.Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: func(context.Context, hostops.Call) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		}})).To(Succeed())
		s, e := NewSupervisor(Config{StateRoot: GinkgoT().TempDir(), Limits: l, ReadyTimeout: 2 * time.Second, StableHealth: time.Second, HostOps: &HostOps{Broker: b, InFlight: in, Allowed: allowed}})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		return s, in
	}

	It("gives an add-on that the host setting names a host endpoint of its own", func() {
		s, in := hostOpsSupervisor(map[string][]string{"sample": {"finetune."}})
		p := withID("sample")
		inst, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		endpoint, credential := hostOpsSeen(p)
		Expect(endpoint).To(HavePrefix("http://127.0.0.1:"))
		Expect(credential).To(HaveLen(64))
		// Not the credential of the host-to-add-on channel.
		Expect(credential).NotTo(Equal(inst.Client.(*Client).credential))
		Expect(endpoint).NotTo(Equal(inst.Client.(*Client).registration.Endpoint))
		// The endpoint answers, and a call needs the live envelope the client records.
		call := func(cred string) int {
			body := strings.NewReader(`{"operationId":"op1","op":"finetune.get","arguments":{}}`)
			req, _ := http.NewRequest("POST", endpoint+"/host", body)
			req.Header.Set(contract.HostCredentialHeader, cred)
			resp, e := http.DefaultClient.Do(req)
			Expect(e).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			return resp.StatusCode
		}
		Expect(call("wrong")).To(Equal(http.StatusUnauthorized))
		Expect(call(credential)).To(Equal(http.StatusForbidden))
		end := in.Begin(contract.Envelope{Request: contract.Request{AddonID: "sample", OperationID: "op1", Generation: 1, Action: "a", Arguments: json.RawMessage(`{}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)}})
		Expect(call(credential)).To(Equal(http.StatusOK))
		end()
		Expect(call(credential)).To(Equal(http.StatusForbidden))
	})

	It("gives an add-on that the host setting does not name no host endpoint", func() {
		s, _ := hostOpsSupervisor(map[string][]string{"other": {"finetune."}})
		p := withID("sample")
		_, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		endpoint, credential := hostOpsSeen(p)
		Expect(endpoint).To(BeEmpty())
		Expect(credential).To(BeEmpty())
		// No setting at all is the same.
		plain := supervisor()
		q := withID("second")
		_, e = plain.Start(context.Background(), q, 1)
		Expect(e).NotTo(HaveOccurred())
		endpoint, credential = hostOpsSeen(q)
		Expect(endpoint + credential).To(BeEmpty())
	})

	It("closes the host endpoint when the incarnation ends", func() {
		s, _ := hostOpsSupervisor(map[string][]string{"sample": {"finetune."}})
		p := withID("sample")
		inst, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		endpoint, _ := hostOpsSeen(p)
		Expect(s.Stop(context.Background(), inst)).To(Succeed())
		Eventually(func() error {
			c, e := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "http://"), 200*time.Millisecond)
			if e == nil {
				_ = c.Close()
			}
			return e
		}, 2*time.Second).Should(HaveOccurred())
	})
})

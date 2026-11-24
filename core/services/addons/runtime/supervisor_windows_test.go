//go:build windows

// SPDX-License-Identifier: MIT

// These specs run only on Windows. They start the real protocol fixture as a
// Windows executable through the supervisor, with the channels passed as
// named handles, and check the Windows process ledger.
package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var winFixture string

var _ = BeforeSuite(func() {
	winFixture = filepath.Join(GinkgoT().TempDir(), "fixture.exe")
	out, e := exec.Command("go", "build", "-o", winFixture, "../../../../tests/addons/fixture/cmd").CombinedOutput()
	Expect(e).NotTo(HaveOccurred(), string(out))
})

// windowsFixture lays a package of the fixture in a new directory, with the
// given mode.
func windowsFixture(mode string) packages.PackageReceipt {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	b, e := os.ReadFile(winFixture)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "fixture.exe"), b, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"`+mode+`"}`), 0o600)).To(Succeed())
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "fixture.exe", OS: goruntime.GOOS, Arch: goruntime.GOARCH, Host: v, API: contract.VersionRange{Min: 2, Max: 2}, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true}}
	p := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:fixture"}
	b, e = json.Marshal(p)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0o600)).To(Succeed())
	return p
}

func windowsSupervisor() *Supervisor {
	GinkgoHelper()
	l := contract.DefaultLimits()
	l.ReadTimeout = time.Second
	l.ShutdownTimeout = 500 * time.Millisecond
	l.BackoffInitial = 50 * time.Millisecond
	l.BackoffMax = 100 * time.Millisecond
	l.BackoffAttempts = 2
	s, e := NewSupervisor(Config{StateRoot: GinkgoT().TempDir(), DataRoot: GinkgoT().TempDir(), Limits: l, ReadyTimeout: 10 * time.Second, StableHealth: time.Second})
	Expect(e).NotTo(HaveOccurred())
	Expect(s.reportEndpoint).To(BeTrue(), "Windows must pass the channels as named handles")
	DeferCleanup(func() { _ = s.Close(context.Background()) })
	return s
}

var _ = Describe("The supervisor on Windows", func() {
	It("starts an add-on, serves it and stops it", func() {
		s := windowsSupervisor()
		i, e := s.Start(context.Background(), windowsFixture("normal"), 1)
		Expect(e).NotTo(HaveOccurred())
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(r[0].Endpoint).To(HavePrefix("http://127.0.0.1:"))
		Expect(s.State("sample", 1)).To(Equal(StateRunning))
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Eventually(i.process.done, 5*time.Second).Should(BeClosed())
	})
	It("gives the child the fixed environment and stops it by closing the startup channel", func() {
		GinkgoT().Setenv("LOCALAI_RUNTIME_TEST_SECRET", "must-not-leak")
		s := windowsSupervisor()
		i, e := s.Start(context.Background(), windowsFixture("env-check"), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.parent.Close()).To(Succeed())
		Eventually(i.process.done, 5*time.Second).Should(BeClosed())
		Expect(i.process.cmd.ProcessState.ExitCode()).To(Equal(0), "5 means an unexpected environment")
	})
	It("refuses a child that reports a wrong endpoint, none, or exits", func() {
		for _, mode := range []string{"report-remote", "report-silent", "exit-start"} {
			s := windowsSupervisor()
			s.config.ReadyTimeout = 2 * time.Second
			start := time.Now()
			_, e := s.Start(context.Background(), windowsFixture(mode), 1)
			Expect(e).To(HaveOccurred(), mode)
			Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second), mode)
		}
	})
	It("restarts a crashed add-on", func() {
		s := windowsSupervisor()
		receipt := windowsFixture("normal")
		i, e := s.Start(context.Background(), receipt, 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() string {
			r, _ := s.Registry.Snapshot(context.Background())
			if len(r) != 1 {
				return ""
			}
			return r[0].Incarnation
		}, 10*time.Second).ShouldNot(Or(BeEmpty(), Equal(i.Incarnation)))
		// Windows cannot delete the program of a running process, and the
		// restarted one runs from the folder that the spec cleans up. Stop it
		// and wait until the program can go.
		Expect(s.Close(context.Background())).To(Succeed())
		exe := filepath.Join(receipt.Directory, "fixture.exe")
		Eventually(func() error { return os.Remove(exe) }, 10*time.Second, 100*time.Millisecond).Should(Succeed())
	})
	It("starts only a .exe file", func() {
		p := windowsFixture("normal")
		_, ok := executablePath(p.Directory, "fixture.exe")
		Expect(ok).To(BeTrue())
		Expect(os.WriteFile(filepath.Join(p.Directory, "plain"), []byte("x"), 0o700)).To(Succeed())
		_, ok = executablePath(p.Directory, "plain")
		Expect(ok).To(BeFalse())
		_, ok = executablePath(p.Directory, "missing.exe")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("The process ledger on Windows", func() {
	It("stops a recorded orphan when it opens", func() {
		p := windowsFixture("report-silent")
		// In this mode the fixture waits a minute when it gets an endpoint
		// handle: it stands for an orphan the owner lost.
		c := exec.Command(filepath.Join(p.Directory, "fixture.exe"), "--localai-addon-v1")
		c.Dir = p.Directory
		c.Env = append(os.Environ(), contract.EndpointHandleEnv+"=1") //nolint:forbidigo // the orphan gets the environment of the test
		Expect(c.Start()).To(Succeed())
		DeferCleanup(func() { _ = c.Process.Kill() })
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		id, e := identity(c.Process.Pid)
		Expect(e).NotTo(HaveOccurred())
		id.Incarnation = "inc"
		root := GinkgoT().TempDir()
		b, e := json.Marshal(map[string]processIdentity{"a": id})
		Expect(e).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(root, "processes.json"), b, 0o600)).To(Succeed())
		l, e := openLedger(root, 5*time.Second)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = l.close() })
		Eventually(done, 5*time.Second).Should(BeClosed())
	})
	It("spares a process with another creation time and refuses a foreign record", func() {
		id, e := identity(os.Getpid())
		Expect(e).NotTo(HaveOccurred())
		id.Incarnation = "inc"
		id.Start = "1" // a recycled PID has another creation time
		Expect(recoverProcess(context.Background(), id)).To(Succeed())
		id.Boot = "prior-boot" // not a Windows record: fail closed
		Expect(recoverProcess(context.Background(), id)).NotTo(Succeed())
	})
})

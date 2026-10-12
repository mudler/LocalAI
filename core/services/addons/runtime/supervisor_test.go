// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRuntime(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon runtime") }

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
	s, e := NewSupervisor(Config{Limits: l, ReadyTimeout: 300 * time.Millisecond, StableHealth: time.Second})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
	return s
}

var service = contract.Capabilities{Service: true}
var _ = Describe("Managed subprocess", func() {
	It("registers only ready generation bound service headless and UI capabilities", func() {
		for _, c := range []contract.Capabilities{service, {Tools: []string{"read"}}, {Tools: []string{"read"}, UI: &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 1, Max: 1}}}} {
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

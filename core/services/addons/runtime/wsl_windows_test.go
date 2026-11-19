//go:build windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/wsl"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wslPackage lays a package with osSettings.windows.execution wsl2 whose
// executable is the start of a Linux program. The supervisor never runs it in
// these specs.
func wslPackage() packages.PackageReceipt {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "fixture"), append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...), 0o700)).To(Succeed())
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "fixture", OS: "windows", Arch: "amd64", OSSettings: &contract.OSSettings{Windows: &contract.WindowsSettings{Execution: contract.ExecutionWSL2}}, FeatureLevel: 10, Host: v, API: contract.VersionRange{Min: 2, Max: 2}, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true}}
	p := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:fixture"}
	b, e := json.Marshal(p)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0o600)).To(Succeed())
	return p
}

type refusingWSL struct{ err error }

func (w refusingWSL) Prepare(context.Context, packages.PackageReceipt) (WSLLaunch, error) {
	return WSLLaunch{}, w.err
}

var _ = Describe("A package with osSettings.windows.execution wsl2 on Windows", func() {
	It("is refused with ErrNoWSL when this LocalAI has no WSL launcher", func() {
		s := windowsSupervisor()
		_, e := s.Start(context.Background(), wslPackage(), 1)
		Expect(e).To(MatchError(ErrRuntime))
		Expect(e).To(MatchError(ErrNoWSL))
	})
	It("is refused with the reason of the launcher, and nothing runs on Windows", func() {
		s := windowsSupervisor()
		s.config.WSL = refusingWSL{errors.New("WSL 2 is not installed")}
		_, e := s.Start(context.Background(), wslPackage(), 1)
		Expect(e).To(MatchError(ContainSubstring("WSL 2 is not installed")))
		r, _ := s.Registry.Snapshot(context.Background())
		Expect(r).To(BeEmpty())
	})
})

// These specs need a real WSL 2 distribution and run only with
// LOCALAI_TEST_WSL=1. They start the protocol fixture, built for Linux, in
// the default distribution through the real launcher.
var _ = Describe("An add-on in a real WSL 2 distribution", func() {
	It("starts from its copy in the distribution, serves it and stops it", func() {
		if os.Getenv("LOCALAI_TEST_WSL") != "1" {
			Skip("set LOCALAI_TEST_WSL=1 on a machine with a WSL 2 distribution")
		}
		dir := GinkgoT().TempDir()
		build := exec.Command("go", "build", "-o", filepath.Join(dir, "fixture"), "../../../../tests/addons/fixture/cmd")
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		out, e := build.CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
		Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"graceful"}`), 0o600)).To(Succeed())
		v := contract.VersionRange{Min: 1, Max: 1}
		m := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "fixture", OS: "windows", Arch: "amd64", OSSettings: &contract.OSSettings{Windows: &contract.WindowsSettings{Execution: contract.ExecutionWSL2}}, FeatureLevel: 10, Host: v, API: contract.VersionRange{Min: 2, Max: 2}, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true}}
		p := packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:wsltest"}
		b, e := json.Marshal(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0o600)).To(Succeed())
		s := windowsSupervisor()
		launcher := wsl.New(wsl.Options{})
		s.config.WSL = launcher
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.State("sample", 1)).To(Equal(StateRunning))
		host := s.HostDataDir("sample")
		Expect(host).To(HavePrefix(`\\wsl.localhost\`))
		Eventually(filepath.Join(host, "fixture.probe"), 10*time.Second).Should(BeARegularFile())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Eventually(i.process.done, 15*time.Second).Should(BeClosed())
	})
})

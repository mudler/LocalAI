//go:build windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wslPackage lays a package with execution wsl2 whose executable is the start
// of a Linux program. The supervisor never runs it in these specs.
func wslPackage() packages.PackageReceipt {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "fixture"), append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...), 0o700)).To(Succeed())
	v := contract.VersionRange{Min: 1, Max: 1}
	m := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "fixture", OS: "windows", Arch: "amd64", Execution: contract.ExecutionWSL2, FeatureLevel: 10, Host: v, API: contract.VersionRange{Min: 2, Max: 2}, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true}}
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

var _ = Describe("A package with execution wsl2 on Windows", func() {
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

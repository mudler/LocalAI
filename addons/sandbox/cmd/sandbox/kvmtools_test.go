// SPDX-License-Identifier: MIT
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func writeProgram(dir, name, content string) {
	Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
}

var _ = Describe("Packaged KVM programs", func() {
	It("copies the packaged programs as executable and uses them", func() {
		src, dst := GinkgoT().TempDir(), filepath.Join(GinkgoT().TempDir(), "kvm-bin")
		writeProgram(src, "cloud-hypervisor", "chv")
		writeProgram(src, "virtiofsd", "vfs")
		pins := map[string]map[string]string{"amd64": {"cloud-hypervisor": sum("chv"), "virtiofsd": sum("vfs")}}
		bins, err := installKVMTools(src, dst, "amd64", pins)
		Expect(err).NotTo(HaveOccurred(), "installKVMTools")
		Expect(bins.CloudHypervisor).To(Equal(filepath.Join(dst, "cloud-hypervisor")))
		Expect(bins.Virtiofsd).To(Equal(filepath.Join(dst, "virtiofsd")))
		for _, p := range []string{bins.CloudHypervisor, bins.Virtiofsd} {
			fi, err := os.Stat(p)
			Expect(err).NotTo(HaveOccurred(), p)
			Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o755)), p)
		}
	})

	It("never uses a program that does not match its pin", func() {
		src, dst := GinkgoT().TempDir(), filepath.Join(GinkgoT().TempDir(), "kvm-bin")
		writeProgram(src, "cloud-hypervisor", "replaced")
		pins := map[string]map[string]string{"amd64": {"cloud-hypervisor": sum("chv")}}
		_, err := installKVMTools(src, dst, "amd64", pins)
		Expect(err).To(MatchError(sandbox.ErrUnsupported), "want ErrUnsupported naming the checksum")
		Expect(err).To(MatchError(ContainSubstring("checksum")))
		_, statErr := os.Stat(filepath.Join(dst, "cloud-hypervisor"))
		Expect(statErr).To(HaveOccurred(), "a program that failed the check was copied")
	})

	It("leaves a missing program to the system lookup", func() {
		src, dst := GinkgoT().TempDir(), filepath.Join(GinkgoT().TempDir(), "kvm-bin")
		writeProgram(src, "cloud-hypervisor", "chv")
		pins := map[string]map[string]string{"arm64": {"cloud-hypervisor": sum("chv")}}
		bins, err := installKVMTools(src, dst, "arm64", pins)
		Expect(err).NotTo(HaveOccurred(), "installKVMTools")
		Expect(bins.Virtiofsd).To(BeEmpty(), "want only cloud-hypervisor: %+v", bins)
		Expect(bins.CloudHypervisor).NotTo(BeEmpty(), "want only cloud-hypervisor: %+v", bins)
	})

	It("refuses a packaged program for an architecture without a pin", func() {
		src := GinkgoT().TempDir()
		writeProgram(src, "virtiofsd", "vfs")
		_, err := installKVMTools(src, filepath.Join(GinkgoT().TempDir(), "kvm-bin"), "riscv64", map[string]map[string]string{})
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
	})

	// The values the code pins are the ones in the packaging description.
	It("pins in the code the values that the package describes", func() {
		raw, err := os.ReadFile("../../package/kvm.json")
		Expect(err).NotTo(HaveOccurred())
		var d map[string]json.RawMessage
		Expect(json.Unmarshal(raw, &d)).To(Succeed())
		for _, program := range []string{"cloud-hypervisor", "virtiofsd"} {
			var p struct {
				Platforms map[string]struct {
					Sha256 string `json:"sha256"`
				} `json:"platforms"`
			}
			Expect(json.Unmarshal(d[program], &p)).To(Succeed())
			for arch, want := range map[string]string{"amd64": p.Platforms["linux/amd64"].Sha256, "arm64": p.Platforms["linux/arm64"].Sha256} {
				Expect(kvmPins[arch][program]).To(Equal(want), "%s %s: code vs description", program, arch)
			}
		}
	})

	DescribeTable("decides whether to skip bwrap from the variable, then the container",
		func(env string, set, inCont, want bool) {
			lookup := func(string) (string, bool) { return env, set }
			Expect(noBwrap(lookup, inCont)).To(Equal(want))
		},
		Entry("variable on", "1", true, false, true),
		Entry("variable true", "true", true, false, true),
		Entry("variable off in a container", "0", true, true, false),
		Entry("unset in a container", "", false, true, true),
		Entry("unset on a host", "", false, false, false),
		Entry("empty on a host", "", true, false, false),
	)

	It("recognises a container by its marker files", func() {
		dir := GinkgoT().TempDir()
		Expect(inContainer(filepath.Join(dir, "a"), filepath.Join(dir, "b"))).To(BeFalse(), "no marker file, yet a container")
		marker := filepath.Join(dir, "marker")
		Expect(os.WriteFile(marker, nil, 0o600)).To(Succeed())
		Expect(inContainer(filepath.Join(dir, "a"), marker)).To(BeTrue(), "a marker file was not recognised")
	})
})

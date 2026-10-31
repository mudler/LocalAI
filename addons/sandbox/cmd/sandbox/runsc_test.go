// SPDX-License-Identifier: MIT
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func pack(runsc string) (src string, pin string) {
	src = GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(src, "runsc"), []byte(runsc), 0o644)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(src, "gvisor-bin"), 0o755)).To(Succeed())
	for _, n := range []string{"gvisor_sentry", "checkpointgofer"} {
		Expect(os.WriteFile(filepath.Join(src, "gvisor-bin", n), []byte(n), 0o644)).To(Succeed())
	}
	sum := sha256.Sum256([]byte(runsc))
	return src, hex.EncodeToString(sum[:])
}

var _ = Describe("Packaged runsc", func() {
	It("copies a pinned runsc as executable with its helpers", func() {
		src, pin := pack("the real runsc")
		dst := filepath.Join(GinkgoT().TempDir(), "runsc")
		got, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(filepath.Join(dst, "runsc")))
		for _, f := range []string{"runsc", "gvisor-bin/gvisor_sentry", "gvisor-bin/checkpointgofer"} {
			fi, err := os.Stat(filepath.Join(dst, f))
			Expect(err).NotTo(HaveOccurred(), f)
			Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o755)), f)
		}
		// A second call changes nothing and still answers.
		again, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
		Expect(err).NotTo(HaveOccurred(), "second call")
		Expect(again).To(Equal(got))
	})

	It("never uses a runsc that does not match the pin", func() {
		src, _ := pack("a runsc someone replaced")
		dst := filepath.Join(GinkgoT().TempDir(), "runsc")
		_, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": strings.Repeat("0", 64)})
		Expect(err).To(MatchError(sandbox.ErrUnsupported), "want ErrUnsupported naming the checksum")
		Expect(err).To(MatchError(ContainSubstring("checksum")))
		_, statErr := os.Stat(dst)
		Expect(statErr).To(HaveOccurred(), "a runsc that failed the check was copied")
	})

	It("refuses an architecture without a pin and does not find a missing runsc", func() {
		src, pin := pack("x")
		_, err := installRunsc(src, filepath.Join(GinkgoT().TempDir(), "r"), "riscv64", map[string]string{"amd64": pin})
		Expect(err).To(MatchError(sandbox.ErrUnsupported), "no pin for the architecture")
		_, err = installRunsc(GinkgoT().TempDir(), filepath.Join(GinkgoT().TempDir(), "r"), "amd64", map[string]string{"amd64": pin})
		Expect(err).To(MatchError(errNoPackagedRunsc), "no runsc in the package")
	})

	It("replaces a corrupted copy", func() {
		src, pin := pack("good")
		dst := filepath.Join(GinkgoT().TempDir(), "runsc")
		_, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dst, "runsc"), []byte("tampered"), 0o755)).To(Succeed())
		_, err = installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
		Expect(err).NotTo(HaveOccurred())
		b, _ := os.ReadFile(filepath.Join(dst, "runsc"))
		Expect(string(b)).To(Equal("good"), "the tampered copy stayed")
	})

	// The values the code pins are the ones in the packaging description.
	It("pins in the code the values that the release describes", func() {
		raw, err := os.ReadFile("../../package/runsc.json")
		Expect(err).NotTo(HaveOccurred())
		var d struct {
			Release   string `json:"release"`
			Platforms map[string]struct {
				Sha256 string `json:"runscSha256"`
				URL    string `json:"url"`
			} `json:"platforms"`
		}
		Expect(json.Unmarshal(raw, &d)).To(Succeed())
		Expect(d.Release).To(Equal(runscRelease))
		for arch, want := range map[string]string{"amd64": d.Platforms["linux/amd64"].Sha256, "arm64": d.Platforms["linux/arm64"].Sha256} {
			Expect(want).NotTo(BeEmpty(), arch)
			Expect(runscPins[arch]).To(Equal(want), arch)
		}
		for p, v := range d.Platforms {
			Expect(v.URL).To(HavePrefix("https://storage.googleapis.com/gvisor/releases/release/"+d.Release+"/"), p)
		}
	})

	It("copies the profile tool next to runsc", func() {
		src, pin := pack("runsc")
		Expect(os.WriteFile(filepath.Join(src, "gvisor-profile"), []byte("tool"), 0o644)).To(Succeed())
		dst := filepath.Join(GinkgoT().TempDir(), "runsc")
		_, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
		Expect(err).NotTo(HaveOccurred())
		fi, err := os.Stat(filepath.Join(dst, "gvisor-profile"))
		Expect(err).NotTo(HaveOccurred(), "gvisor-profile")
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o755)))
	})
})

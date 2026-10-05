package model

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vulkanICDEnv", func() {
	It("returns nil when the backend ships no vulkan/icd.d (CPU/CUDA/SYCL builds)", func() {
		Expect(vulkanICDEnv(GinkgoT().TempDir(), nil)).To(BeNil())
	})

	It("returns nil when icd.d exists but holds no .json manifests", func() {
		work := GinkgoT().TempDir()
		icdDir := filepath.Join(work, "vulkan", "icd.d")
		Expect(os.MkdirAll(icdDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(icdDir, "README.txt"), []byte("not a manifest"), 0o644)).To(Succeed())
		// A directory whose name ends in .json must be ignored.
		Expect(os.MkdirAll(filepath.Join(icdDir, "nested.json"), 0o755)).To(Succeed())

		Expect(vulkanICDEnv(work, nil)).To(BeNil())
	})

	It("adds bundled manifests without hiding host NVIDIA ICDs or replacing operator overrides", func() {
		work := GinkgoT().TempDir()
		icdDir := filepath.Join(work, "vulkan", "icd.d")
		Expect(os.MkdirAll(icdDir, 0o755)).To(Succeed())
		for _, name := range []string{"intel_icd.json", "lvp_icd.json"} {
			Expect(os.WriteFile(filepath.Join(icdDir, name), []byte("{}"), 0o644)).To(Succeed())
		}

		env := vulkanICDEnv(work, nil)
		Expect(env).To(HaveLen(1))

		got := map[string]string{}
		for _, kv := range env {
			k, v, ok := strings.Cut(kv, "=")
			Expect(ok).To(BeTrue(), "malformed env entry %q", kv)
			got[k] = v
		}

		Expect(got).NotTo(HaveKey("VK_DRIVER_FILES"))
		Expect(got).NotTo(HaveKey("VK_ICD_FILENAMES"))
		Expect(got).To(HaveKey("VK_ADD_DRIVER_FILES"))
		parts := strings.Split(got["VK_ADD_DRIVER_FILES"], string(os.PathListSeparator))
		Expect(parts).To(HaveLen(2))
		for _, p := range parts {
			Expect(filepath.IsAbs(p)).To(BeTrue(), "manifest path %q must be absolute", p)
			Expect(p).To(HaveSuffix(".json"))
		}
	})

	It("merges inherited and model-specific additive paths while preserving explicit overrides", func() {
		work := GinkgoT().TempDir()
		icdDir := filepath.Join(work, "vulkan", "icd.d")
		Expect(os.MkdirAll(icdDir, 0o755)).To(Succeed())
		manifest := filepath.Join(icdDir, "intel.json")
		Expect(os.WriteFile(manifest, []byte("{}"), 0o644)).To(Succeed())
		env := []string{"OTHER=value", "VK_DRIVER_FILES=/explicit.json", "VK_ICD_FILENAMES=/legacy.json", "VK_ADD_DRIVER_FILES=/host.json", "VK_ADD_DRIVER_FILES=/model.json"}
		Expect(vulkanICDEnv(work, env)).To(Equal([]string{
			"OTHER=value", "VK_DRIVER_FILES=/explicit.json", "VK_ICD_FILENAMES=/legacy.json",
			"VK_ADD_DRIVER_FILES=" + strings.Join([]string{manifest, "/host.json", "/model.json"}, string(os.PathListSeparator)),
		}))
		Expect(vulkanICDEnv(GinkgoT().TempDir(), env)).To(Equal(env))
	})
})

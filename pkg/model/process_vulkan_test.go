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
		Expect(vulkanICDEnv(GinkgoT().TempDir())).To(BeNil())
	})

	It("returns nil when icd.d exists but holds no .json manifests", func() {
		work := GinkgoT().TempDir()
		icdDir := filepath.Join(work, "vulkan", "icd.d")
		Expect(os.MkdirAll(icdDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(icdDir, "README.txt"), []byte("not a manifest"), 0o644)).To(Succeed())
		// A directory whose name ends in .json must be ignored.
		Expect(os.MkdirAll(filepath.Join(icdDir, "nested.json"), 0o755)).To(Succeed())

		Expect(vulkanICDEnv(work)).To(BeNil())
	})

	It("points VK_DRIVER_FILES/VK_ICD_FILENAMES at the bundled manifests", func() {
		work := GinkgoT().TempDir()
		icdDir := filepath.Join(work, "vulkan", "icd.d")
		Expect(os.MkdirAll(icdDir, 0o755)).To(Succeed())
		for _, name := range []string{"intel_icd.json", "lvp_icd.json"} {
			Expect(os.WriteFile(filepath.Join(icdDir, name), []byte("{}"), 0o644)).To(Succeed())
		}

		env := vulkanICDEnv(work)
		Expect(env).To(HaveLen(2))

		got := map[string]string{}
		for _, kv := range env {
			k, v, ok := strings.Cut(kv, "=")
			Expect(ok).To(BeTrue(), "malformed env entry %q", kv)
			got[k] = v
		}

		for _, key := range []string{"VK_DRIVER_FILES", "VK_ICD_FILENAMES"} {
			Expect(got).To(HaveKey(key))
			// Both manifests must be listed as absolute paths, joined by the
			// OS path-list separator the Vulkan loader expects.
			parts := strings.Split(got[key], string(os.PathListSeparator))
			Expect(parts).To(HaveLen(2))
			for _, p := range parts {
				Expect(filepath.IsAbs(p)).To(BeTrue(), "%s entry %q must be absolute", key, p)
				Expect(p).To(HaveSuffix(".json"))
			}
		}
	})
})

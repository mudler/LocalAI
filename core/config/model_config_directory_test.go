package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("MergeDirectorySnapshot", func() {
	It("replaces directory configs and keeps configs defined outside the directory", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "kept.yaml"), []byte("name: kept\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "removed.yaml"), []byte("name: removed\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		cfgFile := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(cfgFile, []byte("- name: external\n  backend: llama-cpp\n- name: kept\n  backend: from-config-file\n"), 0644)).To(Succeed())

		current := config.NewModelConfigLoader(dir)
		Expect(current.LoadModelConfigsFromPath(dir)).To(Succeed())
		Expect(current.LoadMultipleModelConfigsSingleFile(cfgFile)).To(Succeed())

		Expect(os.Remove(filepath.Join(dir, "removed.yaml"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "added.yaml"), []byte("name: added\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		snapshot := config.NewModelConfigLoader(dir)
		Expect(snapshot.LoadModelConfigsFromPathStrict(dir)).To(Succeed())

		merged := config.MergeDirectorySnapshot(current.GetAllModelsConfigs(), snapshot.GetAllModelsConfigs(), dir)
		byName := map[string]string{}
		for _, cfg := range merged {
			byName[cfg.Name] = cfg.Backend
		}
		Expect(byName).To(Equal(map[string]string{
			"added":    "llama-cpp",
			"external": "llama-cpp",
			"kept":     "from-config-file",
		}))
	})

	It("treats a config with no recorded file as one from the directory", func() {
		cfg := config.ModelConfig{Name: "synthesized"}
		Expect(cfg.DefinedOutside(GinkgoT().TempDir())).To(BeFalse())
	})
})

var _ = Describe("ModelConfigLoader.ConfigRevisionOf", func() {
	It("returns the stamped revision of a loaded config and the deletion revision of an absent one", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "present.yaml"), []byte("name: present\nbackend: llama-cpp\n"), 0644)).To(Succeed())
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())

		revision, err := loader.RevisionForPath("present", dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(loader.ConfigRevisionOf("present")).To(Equal(revision))
		Expect(loader.ConfigRevisionOf("absent")).To(Equal(config.DeletedModelConfigRevision("absent")))
	})
})

// SPDX-License-Identifier: MIT
package application

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("development backend preference", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "default")
	})
	installedState := func() *system.SystemState {
		root := GinkgoT().TempDir()
		for _, name := range []string{"cpu-example-engine", "cpu-example-engine-development"} {
			dir := filepath.Join(root, name)
			Expect(os.MkdirAll(dir, 0755)).To(Succeed())
			data, err := json.Marshal(gallery.BackendMetadata{Name: name, Alias: "example-engine"})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, "run.sh"), nil, 0644)).To(Succeed())
		}
		st, err := system.GetSystemState(system.WithBackendPath(root), system.WithBackendSystemPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		return st
	}
	It("synchronizes saved true at startup, including default-valued explicit false", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"), []byte(`{"prefer_development_backends":true}`), 0644)).To(Succeed())
		st := installedState()
		cfg := config.NewApplicationConfig(config.WithSystemState(st), config.WithPreferDevelopmentBackends(false))
		cfg.DynamicConfigsDir = dir
		loadStartupSettings(cfg)
		Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		Expect(st.PreferDevelopmentBackends).To(BeTrue())
		ml := model.NewModelLoader(st)
		Expect(gallery.RegisterBackends(st, ml)).To(Succeed())
		path := ml.GetExternalBackend("example-engine")
		Expect(path).To(HaveSuffix("cpu-example-engine-development/run.sh"))
	})
	It("preserves system-state-only true", func() {
		st, err := system.GetSystemState(system.WithPreferDevelopmentBackends(true))
		Expect(err).NotTo(HaveOccurred())
		cfg := config.NewApplicationConfig(config.WithSystemState(st))
		loadStartupSettings(cfg)
		Expect(st.PreferDevelopmentBackends).To(BeTrue())
	})
	It("does not request watchdog reconfiguration or hot-switch API or file-watcher state", func() {
		st := installedState()
		cfg := config.NewApplicationConfig(config.WithSystemState(st))
		ml := model.NewModelLoader(st)
		Expect(gallery.RegisterBackends(st, ml)).To(Succeed())
		original := ml.GetExternalBackend("example-engine")
		Expect(original).To(HaveSuffix("cpu-example-engine/run.sh"))
		yes := true
		Expect(cfg.ApplyRuntimeSettings(&config.RuntimeSettings{PreferDevelopmentBackends: &yes})).To(BeFalse())
		Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		Expect(st.PreferDevelopmentBackends).To(BeFalse())
		cfg.PreferDevelopmentBackends = false
		Expect(readRuntimeSettingsJson(config.ApplicationConfig{})([]byte(`{"prefer_development_backends":true}`), cfg)).To(Succeed())
		Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		Expect(st.PreferDevelopmentBackends).To(BeFalse())
		current := ml.GetExternalBackend("example-engine")
		Expect(current).To(Equal(original))
	})
})

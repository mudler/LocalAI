// SPDX-License-Identifier: MIT
package worker

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("development backend preference", func() {
	DescribeTable("parses worker configuration before registration", func(env, value string, args []string, want bool) {
		GinkgoT().Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "default")
		for _, key := range []string{"LOCALAI_PREFER_DEV_BACKENDS", "PREFER_DEV_BACKENDS"} {
			// Setenv registers restoration of the original value, then unset
			// so Kong sees an absent bool rather than an invalid empty bool.
			GinkgoT().Setenv(key, "false")
			Expect(os.Unsetenv(key)).To(Succeed())
		}
		if env != "" {
			GinkgoT().Setenv(env, value)
		}
		cfg := Config{}
		parser, err := kong.New(&cfg, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]"})
		Expect(err).NotTo(HaveOccurred())
		_, err = parser.Parse(append([]string{"--nats-url=nats://example.invalid:4222", "--register-to=http://example.invalid:8080"}, args...))
		Expect(err).NotTo(HaveOccurred())
		cfg.BackendsPath = GinkgoT().TempDir()
		cfg.BackendsSystemPath = GinkgoT().TempDir()
		for _, name := range []string{"cpu-example-engine", "cpu-example-engine-development"} {
			dir := filepath.Join(cfg.BackendsPath, name)
			Expect(os.MkdirAll(dir, 0755)).To(Succeed())
			data, err := json.Marshal(gallery.BackendMetadata{Name: name, Alias: "example-engine"})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, "run.sh"), nil, 0644)).To(Succeed())
		}
		st, err := cfg.systemState()
		Expect(err).NotTo(HaveOccurred())
		Expect(st.PreferDevelopmentBackends).To(Equal(want))
		ml := model.NewModelLoader(st)
		Expect(gallery.RegisterBackends(st, ml)).To(Succeed())
		supervisor := &backendSupervisor{cfg: &cfg, systemState: st, ml: ml}
		chosen := "cpu-example-engine"
		if want {
			chosen += "-development"
		}
		Expect(supervisor.findBackend("example-engine")).To(Equal(filepath.Join(cfg.BackendsPath, chosen, "run.sh")))
		Expect(supervisor.findBackend("cpu-example-engine")).To(Equal(filepath.Join(cfg.BackendsPath, "cpu-example-engine", "run.sh")))
	},
		Entry("default", "", "", []string{}, false),
		Entry("primary true", "LOCALAI_PREFER_DEV_BACKENDS", "true", []string{}, true),
		Entry("legacy true", "PREFER_DEV_BACKENDS", "true", []string{}, true),
		Entry("explicit false", "LOCALAI_PREFER_DEV_BACKENDS", "false", []string{}, false),
		Entry("CLI true", "", "", []string{"--prefer-development-backends"}, true),
	)
})

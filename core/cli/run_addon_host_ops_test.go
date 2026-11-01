// SPDX-License-Identifier: MIT
package cli

import (
	"os"
	"path/filepath"
	"reflect"

	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the add-on host operations flag", func() {
	It("defaults to the documented setting", func() {
		f, ok := reflect.TypeOf(RunCMD{}).FieldByName("AddonHostOps")
		Expect(ok).To(BeTrue(), "the flag is missing")
		Expect(f.Tag.Get("default")).To(Equal(config.DefaultAddonHostOps))
		Expect(f.Tag.Get("env")).To(Equal("LOCALAI_ADDON_HOST_OPS"))
	})
})

var _ = Describe("addon host policy CLI precedence", Serial, func() {
	parse := func(args ...string) *RunCMD {
		command := struct {
			Run RunCMD `cmd:"" default:"withargs"`
		}{}
		p, err := kong.New(&command, kong.Vars{
			"basepath": kong.ExpandPath("."), "generatedcontentpath": DefaultGeneratedContentPath(), "uploadpath": DefaultUploadPath(),
			"galleries": config.DefaultGalleriesJSON, "backends": config.DefaultBackendGalleriesJSON,
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Parse(args)
		Expect(err).NotTo(HaveOccurred())
		return &command.Run
	}
	BeforeEach(func() {
		for _, key := range []string{"LOCALAI_ADDON_HOST_OPS", "LOCALAI_ADDON_HOST_POLICY"} {
			old, ok := os.LookupEnv(key)
			DeferCleanup(func() {
				if ok {
					Expect(os.Setenv(key, old)).To(Succeed())
				} else {
					Expect(os.Unsetenv(key)).To(Succeed())
				}
			})
			Expect(os.Unsetenv(key)).To(Succeed())
		}
	})
	It("selects policy over the temporary legacy default", func() {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(`{"schemaVersion":1,"grants":{"custom":["workspace."]}}`), 0600)).To(Succeed())
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", p)
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Source).To(Equal("policy"))
		Expect(got.Grants).To(Equal(map[string][]string{"custom": {"workspace."}}))
	})
	It("uses flag presence even when empty and ignores an invalid policy", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "env=x.")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		got, err := parse("--addon-host-ops=").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("flag"))
		Expect(got.PolicyIgnored).To(BeTrue())
	})
	It("uses explicit empty environment instead of the default or policy", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("environment"))
		Expect(got.PolicyIgnored).To(BeTrue())
	})
	It("does not union a nonempty flag with environment or policy", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "env=x.")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		got, err := parse("--addon-host-ops=flag=y.").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(Equal(map[string][]string{"flag": {"y."}}))
	})
	It("lets an empty policy flag disable environment policy without restoring defaults", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		got, err := parse("--addon-host-policy=").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("empty"))
	})
	It("lets a policy flag replace environment policy", func() {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(`{"schemaVersion":1,"grants":{}}`), 0600)).To(Succeed())
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		got, err := parse("--addon-host-policy", p).resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Source).To(Equal("policy"))
		Expect(got.Grants).To(BeEmpty())
	})
	It("fails selected invalid policy rather than falling back", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		_, err := parse().resolveAddonHostOps()
		Expect(err).To(HaveOccurred())
	})
	It("fails startup before creating directories for a selected invalid policy", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		r := parse()
		r.ModelsPath = filepath.Join(GinkgoT().TempDir(), "not-created")
		Expect(r.Run(nil)).To(MatchError(ContainSubstring("addon host operations (policy)")))
		_, err := os.Stat(r.ModelsPath)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
	It("keeps a resolved policy snapshot when the file changes", func() {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(`{"schemaVersion":1,"grants":{"a":["workspace."]}}`), 0600)).To(Succeed())
		got, err := parse("--addon-host-policy=" + p).resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(p, []byte(`{"schemaVersion":1,"grants":{}}`), 0600)).To(Succeed())
		Expect(got.Grants).To(Equal(map[string][]string{"a": {"workspace."}}))
	})
	It("temporarily preserves legacy CLI defaults only when all selectors are absent", func() {
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		legacy, err := config.ParseAddonHostOps(config.DefaultAddonHostOps)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(Equal(legacy))
		Expect(got.Source).To(Equal("legacy-default"))
	})
})

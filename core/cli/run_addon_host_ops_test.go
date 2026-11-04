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
	It("has no compiled grant default", func() {
		f, ok := reflect.TypeOf(RunCMD{}).FieldByName("AddonHostOps")
		Expect(ok).To(BeTrue(), "the flag is missing")
		Expect(f.Tag.Get("default")).To(BeEmpty())
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
		for _, key := range []string{"LOCALAI_ADDON_HOST_OPS", "LOCALAI_ADDON_HOST_POLICY", "LOCALAI_ADDON_HOST_DENY"} {
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
	It("selects an explicitly configured policy", func() {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(`{"schemaVersion":1,"grants":{"custom":["workspace."]}}`), 0600)).To(Succeed())
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", p)
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Source).To(Equal("policy:" + p))
		Expect(got.Grants).To(Equal(map[string][]string{"custom": {"workspace."}}))
	})
	writePolicy := func(body string) string {
		p := filepath.Join(GinkgoT().TempDir(), "policy.json")
		Expect(os.WriteFile(p, []byte(body), 0600)).To(Succeed())
		return p
	}
	It("uses flag presence even when empty, ignores the file grants and still applies its deny", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "env=x.")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", writePolicy(`{"schemaVersion":2,"grants":{"file":["z."]},"deny":{"*":["egress."]}}`))
		got, err := parse("--addon-host-ops=").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("flag"))
		Expect(got.GrantsIgnored).To(BeTrue())
		Expect(got.Deny).To(Equal(map[string][]string{"*": {"egress."}}))
	})
	It("uses explicit empty environment instead of file grants and still applies its deny", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", writePolicy(`{"schemaVersion":2,"deny":{"a":["*"]}}`))
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("environment"))
		Expect(got.GrantsIgnored).To(BeTrue())
		Expect(got.Deny).To(Equal(map[string][]string{"a": {"*"}}))
	})
	It("stops startup on an invalid selected file even when a flat list replaces its grants", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", "/missing/policy")
		_, err := parse("--addon-host-ops=flag=y.").resolveAddonHostOps()
		Expect(err).To(HaveOccurred())
	})
	It("does not union a nonempty flag with the file grants", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_OPS", "env=x.")
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_POLICY", writePolicy(`{"schemaVersion":1,"grants":{"file":["z."]}}`))
		got, err := parse("--addon-host-ops=flag=y.").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(Equal(map[string][]string{"flag": {"y."}}))
	})
	It("unions the deny flag with the file deny", func() {
		got, err := parse("--addon-host-policy="+writePolicy(`{"schemaVersion":2,"deny":{"web":["workspace.admin"]}}`), "--addon-host-deny=web=egress.").resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Deny).To(Equal(map[string][]string{"web": {"workspace.admin", "egress."}}))
	})
	It("reads the deny environment variable", func() {
		GinkgoT().Setenv("LOCALAI_ADDON_HOST_DENY", "*=connector.")
		got, err := parse("--addon-host-policy=" + writePolicy(`{"schemaVersion":2,"deny":{"web":["workspace.admin"]}}`)).resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Deny).To(Equal(map[string][]string{"web": {"workspace.admin"}, "*": {"connector."}}))
	})
	It("rejects an invalid deny flag and blanket mode", func() {
		_, err := parse("--addon-host-deny=nonsense").resolveAddonHostOps()
		Expect(err).To(HaveOccurred())
		_, err = config.ParseAddonHostBlanket("sometimes")
		Expect(err).To(HaveOccurred())
	})
	It("leaves UI grants on and the blanket unset by default", func() {
		r := parse()
		Expect(r.AddonHostUIGrants).To(BeTrue())
		Expect(r.AddonHostBlanket).To(BeEmpty())
		Expect(parse("--addon-host-ui-grants=false").AddonHostUIGrants).To(BeFalse())
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
		Expect(got.Source).To(Equal("policy:" + p))
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
	It("grants nothing when all selectors are absent", func() {
		got, err := parse().resolveAddonHostOps()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Grants).To(BeEmpty())
		Expect(got.Source).To(Equal("empty"))
	})
})

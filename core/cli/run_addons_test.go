// SPDX-License-Identifier: MIT
package cli

import (
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Add-on owner flags", Serial, func() {
	parse := func(args ...string) (*RunCMD, error) {
		command := struct {
			cliContext.Context `embed:""`
			Run                RunCMD `cmd:"" default:"withargs"`
		}{}
		p, err := kong.New(&command, kong.Vars{
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
		})
		if err != nil {
			return &command.Run, err
		}
		_, err = p.Parse(args)
		return &command.Run, err
	}
	BeforeEach(func() {
		for _, key := range []string{"LOCALAI_ADDONS_ENABLED", "LOCALAI_ADDONS_OWNERSHIP_MODE"} {
			value, present := os.LookupEnv(key)
			DeferCleanup(func() {
				if present {
					Expect(os.Setenv(key, value)).To(Succeed())
				} else {
					Expect(os.Unsetenv(key)).To(Succeed())
				}
			})
			Expect(os.Unsetenv(key)).To(Succeed())
		}
	})

	It("turns the owner on by default, in named-user mode", func() {
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsEnabled).To(BeTrue())
		Expect(r.AddonsOwnershipMode).To(Equal("named-user"))
		dir := GinkgoT().TempDir()
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, dir, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeTrue())
		Expect(c.InstanceID).To(Equal(c.OwnerID))
		Expect(c.OwnershipMode).To(Equal("named-user"))
		Expect(c.SharedStorage).To(BeFalse())
		Expect(c.StorageID).To(Equal("local"))
		Expect(c.Root).To(Equal(filepath.Join(dir, "addons")))
		Expect(c.Transport).NotTo(BeNil())
		Expect(c.ListenAddress).NotTo(BeEmpty())
		Expect(c.OwnerAddress).To(Equal("https://" + c.ListenAddress))
		Expect(c.Transport.OwnerAddress).To(Equal(c.OwnerAddress))
	})

	It("turns the owner off completely with --addons-enabled=false", func() {
		r, err := parse("--addons-enabled=false")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsEnabled).To(BeFalse())
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), true)
		Expect(err).NotTo(HaveOccurred())
		Expect(c).To(Equal(config.AddonConfig{}))
		Expect(c.Transport).To(BeNil())
	})

	It("turns the owner off from the environment", func() {
		Expect(os.Setenv("LOCALAI_ADDONS_ENABLED", "false")).To(Succeed())
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsEnabled).To(BeFalse())
	})

	It("refuses an unknown ownership mode", func() {
		r, err := parse("--addons-ownership-mode=everyone")
		Expect(err).NotTo(HaveOccurred())
		_, err = addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), true)
		Expect(err).To(MatchError(ContainSubstring("--addons-ownership-mode")))
	})

	It("accepts local-owner without authentication", func() {
		c, err := addonConfigFor(true, "local-owner", GinkgoT().TempDir(), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeTrue())
		Expect(c.OwnershipMode).To(Equal("local-owner"))
	})

	It("keeps named-user off, without failing, when authentication is off", func() {
		c, err := addonConfigFor(true, "named-user", GinkgoT().TempDir(), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeFalse())
	})
})

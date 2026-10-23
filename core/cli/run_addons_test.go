// SPDX-License-Identifier: MIT
package cli

import (
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
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
		for _, key := range []string{"LOCALAI_ADDONS_ENABLED", "LOCALAI_ADDONS_OWNERSHIP_MODE", "LOCALAI_ADDONS_OFFICIAL_LIBRARY", "LOCALAI_ADDONS_ROLE", "LOCALAI_ADDONS_OWNER_ADDRESS", "LOCALAI_ADDONS_OWNER_LISTEN", "LOCALAI_ADDONS_CARRIER_DIR"} {
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

	It("turns the owner on by default, in named-user mode when authentication is on", func() {
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsEnabled).To(BeTrue())
		Expect(r.AddonsOwnershipMode).To(BeEmpty())
		Expect(r.AddonsOfficialLibrary).To(BeTrue())
		// The pilot backend library is a default source too.
		Expect(r.AddonsBackendLibrary).To(BeTrue())
		dir := GinkgoT().TempDir()
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, dir, true, addonRoleFlags{})
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

	It("defaults to local-owner when authentication is off", func() {
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), false, addonRoleFlags{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeTrue())
		Expect(c.OwnershipMode).To(Equal("local-owner"))
	})

	It("lets an explicit named-user win over the auth-off default", func() {
		r, err := parse("--addons-ownership-mode=named-user")
		Expect(err).NotTo(HaveOccurred())
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), false, addonRoleFlags{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c).To(Equal(config.AddonConfig{}))
	})

	It("reads the ownership mode and the official library from the environment", func() {
		Expect(os.Setenv("LOCALAI_ADDONS_OWNERSHIP_MODE", "named-user")).To(Succeed())
		Expect(os.Setenv("LOCALAI_ADDONS_OFFICIAL_LIBRARY", "false")).To(Succeed())
		r, err := parse()
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsOwnershipMode).To(Equal("named-user"))
		Expect(r.AddonsOfficialLibrary).To(BeFalse())
	})

	It("turns the owner off completely with --addons-enabled=false", func() {
		r, err := parse("--addons-enabled=false")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.AddonsEnabled).To(BeFalse())
		c, err := addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), true, addonRoleFlags{})
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
		_, err = addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, GinkgoT().TempDir(), true, addonRoleFlags{})
		Expect(err).To(MatchError(ContainSubstring("--addons-ownership-mode")))
	})

	It("accepts local-owner without authentication", func() {
		c, err := addonConfigFor(true, "local-owner", GinkgoT().TempDir(), false, addonRoleFlags{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeTrue())
		Expect(c.OwnershipMode).To(Equal("local-owner"))
	})

	It("keeps named-user off, without failing, when authentication is off", func() {
		c, err := addonConfigFor(true, "named-user", GinkgoT().TempDir(), false, addonRoleFlags{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Enabled).To(BeFalse())
	})
	Context("roles", func() {
		const ownerAddress = "https://owner.example:8443"
		var keys string
		BeforeEach(func() {
			keys = filepath.Join(GinkgoT().TempDir(), "keys")
			Expect(gateway.GenerateCarrierKeys(keys, []string{"owner.example"})).To(Succeed())
		})
		resolve := func(dataPath string, args ...string) (config.AddonConfig, error) {
			r, err := parse(args...)
			Expect(err).NotTo(HaveOccurred())
			return addonConfigFor(r.AddonsEnabled, r.AddonsOwnershipMode, dataPath, true, addonRoleFlags{Role: r.AddonsRole, OwnerAddress: r.AddonsOwnerAddress, OwnerListen: r.AddonsOwnerListen, CarrierDir: r.AddonsCarrierDir})
		}

		It("defaults to the combined role", func() {
			r, err := parse()
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AddonsRole).To(Equal("combined"))
			Expect(r.AddonsOwnerAddress).To(BeEmpty())
			Expect(r.AddonsCarrierDir).To(BeEmpty())
		})

		It("keeps the combined output of a single node", func() {
			dir := GinkgoT().TempDir()
			explicit, err := resolve(dir, "--addons-role=combined")
			Expect(err).NotTo(HaveOccurred())
			plain, err := addonConfigFor(true, "", dir, true, addonRoleFlags{})
			Expect(err).NotTo(HaveOccurred())
			// The carrier keys are made fresh at every call; the rest is equal.
			Expect(explicit.Transport).NotTo(BeNil())
			explicit.Transport, plain.Transport = nil, nil
			Expect(explicit).To(Equal(plain))
			Expect(plain.InstanceID).To(Equal("local"))
			Expect(plain.OwnerID).To(Equal("local"))
			Expect(plain.Root).To(Equal(filepath.Join(dir, "addons")))
			Expect(plain.OwnerAddress).To(Equal("https://" + plain.ListenAddress))
		})

		It("gives the owner role its own root, identity and listen address", func() {
			dir := GinkgoT().TempDir()
			c, err := resolve(dir, "--addons-role=owner", "--addons-owner-address="+ownerAddress, "--addons-carrier-dir="+keys)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Enabled).To(BeTrue())
			Expect(c.InstanceID).To(Equal("local"))
			Expect(c.OwnerID).To(Equal("local"))
			Expect(c.OwnerAddress).To(Equal(ownerAddress))
			Expect(c.Root).To(Equal(filepath.Join(dir, "addons")))
			Expect(c.ListenAddress).To(Equal(":8443"))
			Expect(c.StorageID).To(Equal("local"))
			Expect(c.Transport).NotTo(BeNil())
			Expect(c.Transport.InstanceID).To(Equal(c.InstanceID))
			Expect(c.Transport.OwnerAddress).To(Equal(ownerAddress))
			Expect(c.Transport.VerificationKeys).To(HaveKey("frontend"))
		})

		It("lets the owner listen on another address", func() {
			c, err := resolve(GinkgoT().TempDir(), "--addons-role=owner", "--addons-owner-address="+ownerAddress, "--addons-carrier-dir="+keys, "--addons-owner-listen=127.0.0.1:9443")
			Expect(err).NotTo(HaveOccurred())
			Expect(c.ListenAddress).To(Equal("127.0.0.1:9443"))
		})

		It("gives the frontend role no listener and creates no addons directory", func() {
			dir := GinkgoT().TempDir()
			c, err := resolve(dir, "--addons-role=frontend", "--addons-owner-address="+ownerAddress, "--addons-carrier-dir="+keys)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Enabled).To(BeTrue())
			Expect(c.InstanceID).To(Equal("frontend"))
			Expect(c.OwnerID).To(Equal("local"))
			Expect(c.OwnerAddress).To(Equal(ownerAddress))
			// The root is only a name: the manager needs an absolute path.
			Expect(c.Root).To(Equal(filepath.Join(dir, "addons")))
			Expect(c.ListenAddress).To(BeEmpty())
			Expect(c.Transport).NotTo(BeNil())
			Expect(c.Transport.SigningKey).NotTo(BeEmpty())
			Expect(filepath.Join(dir, "addons")).NotTo(BeADirectory())
			entries, err := os.ReadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})

		It("reads the role flags from the environment", func() {
			Expect(os.Setenv("LOCALAI_ADDONS_ROLE", "frontend")).To(Succeed())
			Expect(os.Setenv("LOCALAI_ADDONS_OWNER_ADDRESS", ownerAddress)).To(Succeed())
			Expect(os.Setenv("LOCALAI_ADDONS_CARRIER_DIR", keys)).To(Succeed())
			c, err := resolve(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			Expect(c.InstanceID).To(Equal("frontend"))
		})

		DescribeTable("refuses a bad role setup and names the flag",
			func(flag string, args ...string) {
				_, err := resolve(GinkgoT().TempDir(), args...)
				Expect(err).To(MatchError(ContainSubstring(flag)))
			},
			Entry("unknown role", "--addons-role", "--addons-role=worker"),
			Entry("owner without an address", "--addons-owner-address", "--addons-role=owner", "--addons-carrier-dir=/keys"),
			Entry("owner without a key directory", "--addons-carrier-dir", "--addons-role=owner", "--addons-owner-address="+ownerAddress),
			Entry("frontend without an address", "--addons-owner-address", "--addons-role=frontend", "--addons-carrier-dir=/keys"),
			Entry("frontend without a key directory", "--addons-carrier-dir", "--addons-role=frontend", "--addons-owner-address="+ownerAddress),
			Entry("combined with an address", "--addons-owner-address", "--addons-owner-address="+ownerAddress),
			Entry("combined with a key directory", "--addons-carrier-dir", "--addons-carrier-dir=/keys"),
			Entry("an address that is not https", "--addons-owner-address", "--addons-role=frontend", "--addons-owner-address=http://owner.example:8443", "--addons-carrier-dir=/keys"),
			Entry("an address with a path", "--addons-owner-address", "--addons-role=frontend", "--addons-owner-address=https://owner.example:8443/addons", "--addons-carrier-dir=/keys"),
			Entry("an address without a port", "--addons-owner-address", "--addons-role=owner", "--addons-owner-address=https://owner.example", "--addons-carrier-dir=/keys"),
		)

		It("names the key directory when it holds no key set", func() {
			_, err := resolve(GinkgoT().TempDir(), "--addons-role=frontend", "--addons-owner-address="+ownerAddress, "--addons-carrier-dir="+GinkgoT().TempDir())
			Expect(err).To(MatchError(ContainSubstring("--addons-carrier-dir")))
		})

		It("ignores the role when add-ons are off", func() {
			c, err := resolve(GinkgoT().TempDir(), "--addons-enabled=false", "--addons-role=worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(c).To(Equal(config.AddonConfig{}))
		})
	})
})

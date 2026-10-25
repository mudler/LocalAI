package cli

import (
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

var _ = Describe("credentials file", Serial, func() {
	Describe("resolveCredentialsFile", func() {
		It("prefers the flag", func() {
			dataPath := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dataPath, "credentials.yaml"), nil, 0o600)).To(Succeed())
			Expect(resolveCredentialsFile("/etc/localai/creds.yaml", dataPath)).To(Equal("/etc/localai/creds.yaml"))
		})

		It("falls back to credentials.yaml in the data path when present", func() {
			dataPath := GinkgoT().TempDir()
			p := filepath.Join(dataPath, "credentials.yaml")
			Expect(os.WriteFile(p, nil, 0o600)).To(Succeed())
			Expect(resolveCredentialsFile("", dataPath)).To(Equal(p))
		})

		It("returns nothing when neither is set", func() {
			Expect(resolveCredentialsFile("", GinkgoT().TempDir())).To(BeEmpty())
			Expect(resolveCredentialsFile("", "")).To(BeEmpty())
		})
	})

	Describe("LoadCredentials", func() {
		BeforeEach(func() {
			prev := credentials.SetDefault(nil)
			DeferCleanup(func() { credentials.SetDefault(prev) })
		})

		It("installs the store and resolves _env from the process environment", func() {
			GinkgoT().Setenv("LOCALAI_TEST_REGISTRY_TOKEN", "from-env")
			p := filepath.Join(GinkgoT().TempDir(), "credentials.yaml")
			Expect(os.WriteFile(p, []byte("- match: ghcr.io/acme\n  bearer_env: LOCALAI_TEST_REGISTRY_TOKEN\n"), 0o600)).To(Succeed())

			Expect(LoadCredentials(p)).To(Succeed())
			Expect(credentials.Default().Len()).To(Equal(1))
			c, ok := credentials.Default().Match("https://ghcr.io/acme/img")
			Expect(ok).To(BeTrue())
			h := http.Header{}
			Expect(c.ApplyHeaders(h)).To(Succeed())
			Expect(h.Get("Authorization")).To(Equal("Bearer from-env"))
		})

		It("fails on an invalid file and names it", func() {
			p := filepath.Join(GinkgoT().TempDir(), "credentials.yaml")
			Expect(os.WriteFile(p, []byte("- match: ghcr.io\n"), 0o600)).To(Succeed())
			Expect(LoadCredentials(p)).To(MatchError(ContainSubstring(p)))
			Expect(credentials.Default()).To(BeNil())
		})

		It("does nothing for an empty path", func() {
			Expect(LoadCredentials("")).To(Succeed())
			Expect(credentials.Default()).To(BeNil())
		})
	})
})

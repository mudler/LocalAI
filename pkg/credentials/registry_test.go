package credentials_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/mudler/LocalAI/pkg/credentials"
)

func writeDockerConfig(dir, registry, user, pass string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	doc := fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, registry, encoded)
	Expect(os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600)).To(Succeed())
}

func authConfigFor(target authn.Resource) *authn.AuthConfig {
	a, err := credentials.Keychain().Resolve(target)
	Expect(err).NotTo(HaveOccurred())
	cfg, err := a.Authorization()
	Expect(err).NotTo(HaveOccurred())
	return cfg
}

var _ = Describe("Keychain", Serial, func() {
	var dockerDir string

	BeforeEach(func() {
		dockerDir = isolateDockerConfig()
	})

	repo := func(s string) name.Repository {
		r, err := name.NewRepository(s)
		Expect(err).NotTo(HaveOccurred())
		return r
	}

	It("resolves basic auth for a matching repository", func() {
		useStore("- match: ghcr.io/acme\n  username: bot\n  password: pw\n")
		cfg := authConfigFor(repo("ghcr.io/acme/backend"))
		Expect(cfg.Username).To(Equal("bot"))
		Expect(cfg.Password).To(Equal("pw"))
	})

	It("resolves a bearer rule to a registry token", func() {
		useStore("- match: ghcr.io/acme\n  bearer: tok\n")
		Expect(authConfigFor(repo("ghcr.io/acme/backend")).RegistryToken).To(Equal("tok"))
	})

	It("does not apply a repository-scoped rule to the bare registry", func() {
		useStore("- match: ghcr.io/acme\n  bearer: tok\n")
		reg, err := name.NewRegistry("ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		a, err := credentials.Keychain().Resolve(reg)
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(authn.Anonymous))
	})

	It("ignores header rules, which registries cannot use", func() {
		useStore("- match: ghcr.io/acme\n  header:\n    name: X-Key\n    value: v\n")
		a, err := credentials.Keychain().Resolve(repo("ghcr.io/acme/backend"))
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(authn.Anonymous))
	})

	It("falls back to docker config when no rule matches", func() {
		writeDockerConfig(dockerDir, "ghcr.io", "docker-user", "docker-pw")
		useStore("- match: ghcr.io/acme\n  bearer: tok\n")
		Expect(authConfigFor(repo("ghcr.io/other/backend")).Username).To(Equal("docker-user"))
	})

	It("prefers the store over docker config", func() {
		writeDockerConfig(dockerDir, "ghcr.io", "docker-user", "docker-pw")
		useStore("- match: ghcr.io/acme\n  username: bot\n  password: pw\n")
		Expect(authConfigFor(repo("ghcr.io/acme/backend")).Username).To(Equal("bot"))
	})

	It("builds match URLs with the registry's scheme", func() {
		Expect(credentials.RegistryURL(repo("ghcr.io/acme/backend"))).To(Equal("https://ghcr.io/acme/backend"))
		Expect(credentials.RegistryURL(repo("localhost:5000/acme/backend"))).To(Equal("http://localhost:5000/acme/backend"))
	})
})

var _ = Describe("OrasCredential", Serial, func() {
	var dockerDir string
	ctx := context.Background()

	BeforeEach(func() {
		dockerDir = isolateDockerConfig()
	})

	It("returns basic credentials for the repository's rule", func() {
		useStore("- match: ghcr.io/acme\n  username: bot\n  password: pw\n")
		cred, err := credentials.OrasCredential("ghcr.io/acme/model")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{Username: "bot", Password: "pw"}))
	})

	It("returns a bearer rule as an access token", func() {
		useStore("- match: ghcr.io/acme\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("ghcr.io/acme/model")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{AccessToken: "tok"}))
	})

	It("does not apply a rule for another repository", func() {
		useStore("- match: ghcr.io/acme\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("ghcr.io/other/model")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.EmptyCredential))
	})

	It("matches a repository rule for Docker Hub, which oras calls with registry-1.docker.io", func() {
		useStore("- match: docker.io/acme\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("docker.io/acme/m")(ctx, "registry-1.docker.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{AccessToken: "tok"}))
	})

	It("does not apply a Docker Hub rule for another repository", func() {
		useStore("- match: docker.io/acme\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("docker.io/other/m")(ctx, "registry-1.docker.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.EmptyCredential))
	})

	It("matches a repository given without a tag or digest", func() {
		useStore("- match: registry.ollama.ai/library\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("registry.ollama.ai/library/gemma")(ctx, "registry.ollama.ai")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{AccessToken: "tok"}))
	})

	It("ignores the tag when matching a repository rule", func() {
		useStore("- match: ghcr.io/acme/model\n  bearer: tok\n")
		cred, err := credentials.OrasCredential("ghcr.io/acme/model:v1")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{AccessToken: "tok"}))
	})

	It("stays anonymous when docker config names a credential helper that is missing", func() {
		Expect(os.WriteFile(filepath.Join(dockerDir, "config.json"), []byte(`{"credsStore":"does-not-exist"}`), 0o600)).To(Succeed())
		useStore("")
		cred, err := credentials.OrasCredential("ghcr.io/other/model")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred).To(Equal(auth.EmptyCredential))
	})

	It("falls back to docker config", func() {
		writeDockerConfig(dockerDir, "ghcr.io", "docker-user", "docker-pw")
		useStore("")
		cred, err := credentials.OrasCredential("ghcr.io/other/model")(ctx, "ghcr.io")
		Expect(err).NotTo(HaveOccurred())
		Expect(cred.Username).To(Equal("docker-user"))
	})
})

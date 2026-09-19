package oci

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

const (
	registryUser = "bot"
	registryPass = "s3cret"
)

// requireBasicAuth fronts a registry the way a private registry behaves: every
// request without the right credentials gets a basic-auth challenge.
func requireBasicAuth(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != registryUser || p != registryPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="localai-test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func useCredentials(doc string) {
	s, err := credentials.Parse([]byte(doc), func(string) (string, bool) { return "", false })
	Expect(err).NotTo(HaveOccurred())
	prev := credentials.SetDefault(s)
	DeferCleanup(func() { credentials.SetDefault(prev) })
}

// isolateDockerConfig stops the test host's docker or podman login from
// satisfying a spec, and returns the directory that holds config.json.
func isolateDockerConfig() string {
	home := GinkgoT().TempDir()
	dockerDir := filepath.Join(home, ".docker")
	Expect(os.MkdirAll(dockerDir, 0o700)).To(Succeed())
	GinkgoT().Setenv("HOME", home)
	GinkgoT().Setenv("DOCKER_CONFIG", dockerDir)
	GinkgoT().Setenv("REGISTRY_AUTH_FILE", filepath.Join(home, "no-such-auth.json"))
	GinkgoT().Setenv("XDG_RUNTIME_DIR", home)
	return dockerDir
}

func pushPrivateImage(serverURL, repoPath string) string {
	imageRef := strings.TrimPrefix(serverURL, "http://") + "/" + repoPath + ":latest"
	ref, err := name.ParseReference(imageRef)
	Expect(err).NotTo(HaveOccurred())
	img, err := random.Image(4096, 1)
	Expect(err).NotTo(HaveOccurred())
	Expect(remote.Write(ref, img, remote.WithAuth(&authn.Basic{Username: registryUser, Password: registryPass}))).To(Succeed())
	return imageRef
}

var _ = Describe("registry credentials", Serial, func() {
	var (
		server    *httptest.Server
		dockerDir string
		imageRef  string
	)

	BeforeEach(func() {
		dockerDir = isolateDockerConfig()
		server = httptest.NewServer(requireBasicAuth(registry.New()))
		DeferCleanup(server.Close)
		imageRef = pushPrivateImage(server.URL, "acme/backend")
	})

	It("pulls a private image with a credentials rule", func() {
		useCredentials(fmt.Sprintf("- match: %s/acme\n  username: %s\n  password: %s\n  allow_insecure: true\n", server.URL, registryUser, registryPass))

		_, err := GetImage(imageRef, "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		digest, err := GetImageDigest(imageRef, "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(HavePrefix("sha256:"))
	})

	It("reports that no rule matches when the registry demands auth", func() {
		useCredentials("")

		_, err := GetImage(imageRef, "", nil, nil)
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue(), "got %v", err)
		Expect(authErr.Match).To(BeEmpty())
		Expect(authErr.Status).To(Equal(http.StatusUnauthorized))
	})

	It("names the rule the registry rejected", func() {
		rule := server.URL + "/acme"
		useCredentials(fmt.Sprintf("- match: %s\n  username: %s\n  password: not-it\n  allow_insecure: true\n", rule, registryUser))

		_, err := GetImageDigest(imageRef, "", nil, nil)
		var authErr *credentials.AuthError
		Expect(errors.As(err, &authErr)).To(BeTrue(), "got %v", err)
		Expect(authErr.Match).To(Equal(rule))
		Expect(err.Error()).NotTo(ContainSubstring("not-it"))
	})

	It("still authenticates from docker config when no rule matches", func() {
		host := strings.TrimPrefix(server.URL, "http://")
		encoded := base64.StdEncoding.EncodeToString([]byte(registryUser + ":" + registryPass))
		Expect(os.WriteFile(filepath.Join(dockerDir, "config.json"),
			[]byte(fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, encoded)), 0o600)).To(Succeed())
		useCredentials("")

		_, err := GetImage(imageRef, "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("authenticates the Range requests that resume an interrupted layer", func() {
		reg := &droppingBlobRegistry{inner: requireBasicAuth(registry.New())}
		dropping := httptest.NewServer(reg)
		DeferCleanup(dropping.Close)
		prev := layerRetryBackoff
		layerRetryBackoff = func(int) time.Duration { return 0 }
		DeferCleanup(func() { layerRetryBackoff = prev })

		ref := pushPrivateImage(dropping.URL, "acme/backend")
		useCredentials(fmt.Sprintf("- match: %s/acme\n  username: %s\n  password: %s\n  allow_insecure: true\n", dropping.URL, registryUser, registryPass))

		pulled, err := GetImage(ref, "", nil, nil)
		Expect(err).NotTo(HaveOccurred())
		tarPath := filepath.Join(GinkgoT().TempDir(), "image.tar")
		Expect(DownloadOCIImageTar(context.Background(), pulled, ref, tarPath, nil)).To(Succeed())

		reg.mu.Lock()
		defer reg.mu.Unlock()
		Expect(reg.rangeRequests).NotTo(BeEmpty())
	})
})

var _ = Describe("FetchImageBlob registry credentials", Serial, func() {
	var (
		server *httptest.Server
		repo   string
		digest string
	)

	BeforeEach(func() {
		isolateDockerConfig()
		// oras only speaks HTTPS unless a repository opts into plain HTTP,
		// which FetchImageBlob does not, so the registry has to serve TLS.
		server = httptest.NewTLSServer(requireBasicAuth(registry.New()))
		DeferCleanup(server.Close)

		// FetchImageBlob builds its oras client on retry.DefaultClient, whose
		// transport falls back to http.DefaultTransport on every request.
		// Swapping it for the test server's client is the only way to trust
		// the test certificate without a production seam; the spec is Serial
		// so nothing else sees the swap.
		prev := http.DefaultTransport
		http.DefaultTransport = server.Client().Transport
		DeferCleanup(func() { http.DefaultTransport = prev })

		repo = strings.TrimPrefix(server.URL, "https://") + "/acme/model"
		ref, err := name.ParseReference(repo + ":latest")
		Expect(err).NotTo(HaveOccurred())
		img, err := random.Image(4096, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(remote.Write(ref, img,
			remote.WithAuth(&authn.Basic{Username: registryUser, Password: registryPass}),
			remote.WithTransport(server.Client().Transport))).To(Succeed())
		layers, err := img.Layers()
		Expect(err).NotTo(HaveOccurred())
		d, err := layers[0].Digest()
		Expect(err).NotTo(HaveOccurred())
		digest = d.String()
	})

	It("fetches a private blob with a credentials rule", func() {
		useCredentials(fmt.Sprintf("- match: %s/acme\n  username: %s\n  password: %s\n", server.URL, registryUser, registryPass))

		dst := filepath.Join(GinkgoT().TempDir(), "blob")
		Expect(FetchImageBlob(context.Background(), repo, digest, dst, nil)).To(Succeed())
		info, err := os.Stat(dst)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">", 0))
	})

	It("fails to fetch a private blob when no rule matches", func() {
		useCredentials("")

		dst := filepath.Join(GinkgoT().TempDir(), "blob")
		err := FetchImageBlob(context.Background(), repo, digest, dst, nil)
		Expect(err).To(MatchError(ContainSubstring("basic credential not found")))
	})
})

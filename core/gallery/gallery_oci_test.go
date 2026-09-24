package gallery

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// errNoGallerySignature stands in for what the real verifier reports when an
// artifact has no signature attached.
var errNoGallerySignature = errors.New("no signature found for the gallery artifact")

// stubGalleryVerifier replaces the signature check for the duration of a spec
// and restores it afterwards.
func stubGalleryVerifier(f func(context.Context, *config.GalleryVerification, string) error) {
	GinkgoHelper()
	original := verifyGalleryArtifact
	verifyGalleryArtifact = f
	DeferCleanup(func() { verifyGalleryArtifact = original })
}

// ociGalleryFile is one layer of a test gallery artifact: the title the puller
// lays the content out by, and the content itself.
type ociGalleryFile struct {
	title string
	body  string
}

// ociRegistry starts an in-process registry and returns it together with the
// counters the specs assert on: every request, and specifically blob reads.
// Blob reads are what "nothing was unpacked" means from the registry's side,
// so a spec can prove that a refused gallery never got as far as content.
func ociRegistry() (*httptest.Server, *atomic.Int64, *atomic.Int64) {
	GinkgoHelper()
	var requests, blobs atomic.Int64
	upstream := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if strings.Contains(r.URL.Path, "/blobs/") {
			blobs.Add(1)
		}
		upstream.ServeHTTP(w, r)
	}))
	DeferCleanup(srv.Close)
	return srv, &requests, &blobs
}

// pushGalleryArtifact publishes a gallery artifact to the in-process registry
// and returns the oci:// URL a gallery entry would carry. The publish goes
// through the same counting handler the fetch does, so specs reset the
// counters afterwards with resetCounters.
func pushGalleryArtifact(serverURL, repoPath, artifactType string, files []ociGalleryFile) string {
	GinkgoHelper()
	host := strings.TrimPrefix(serverURL, "http://")
	ctx := context.Background()

	store := memory.New()
	layers := []ocispec.Descriptor{}
	for _, f := range files {
		body := []byte(f.body)
		desc := content.NewDescriptorFromBytes("application/yaml", body)
		desc.Annotations = map[string]string{ocispec.AnnotationTitle: f.title}
		Expect(store.Push(ctx, desc, bytes.NewReader(body))).To(Succeed())
		layers = append(layers, desc)
	}

	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType, oras.PackManifestOptions{
		Layers: layers,
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(store.Tag(ctx, manifestDesc, "latest")).To(Succeed())

	repo, err := remote.NewRepository(host + "/" + repoPath)
	Expect(err).ToNot(HaveOccurred())
	repo.PlainHTTP = true
	_, err = oras.Copy(ctx, store, "latest", repo, "latest", oras.DefaultCopyOptions)
	Expect(err).ToNot(HaveOccurred())

	return "oci://" + host + "/" + repoPath + ":latest"
}

// resetCounters zeroes the request counters, so a spec measures the fetch and
// not the publish that set the scene for it.
func resetCounters(counters ...*atomic.Int64) {
	for _, c := range counters {
		c.Store(0)
	}
}

// ociCacheEntries lists what the OCI gallery cache root holds, so a spec can
// assert that a refused or failed pull left nothing behind at all, staging
// directories included.
func ociCacheEntries(basePath string) []string {
	GinkgoHelper()
	root := filepath.Join(basePath, "..", "cache", "gallery", "oci")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	Expect(err).ToNot(HaveOccurred())
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

var _ = Describe("oci:// galleries", func() {
	const index = "- name: premium-model\n"

	BeforeEach(resetGalleryFailures)

	It("reads the index from an artifact in a registry", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/premium", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
			{title: "base/virtual.yaml", body: "- name: virtual\n"},
		})

		base := tempModelsDir()
		body, served, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "premium"}, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(index))
		Expect(served).To(Equal(url))

		// The whole tree is unpacked, not just the index: entry URLs resolve
		// against it.
		Expect(filepath.Join(ociGalleryCacheDir(base, url, nil), "base", "virtual.yaml")).To(BeAnExistingFile())
	})

	It("serves a second fetch from the cache instead of pulling again", func() {
		srv, requests, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/cached", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})

		base := tempModelsDir()
		g := config.Gallery{URL: url, Name: "cached"}
		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())

		resetCounters(requests)
		body, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(index))
		Expect(requests.Load()).To(BeZero(), "the registry was contacted although the artifact was already cached")
	})

	It("refuses an artifact that is not a gallery", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/wrongtype", "application/vnd.oci.image.config.v1+json", []ociGalleryFile{
			{title: "index.yaml", body: index},
		})

		base := tempModelsDir()
		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "wrongtype"}, base, false)
		Expect(err).To(HaveOccurred())
		Expect(ociCacheEntries(base)).To(BeEmpty())
	})

	Context("with a verification policy", func() {
		policy := &config.GalleryVerification{
			Issuer:        "https://token.actions.githubusercontent.com",
			IdentityRegex: "^https://github.com/localai/premium/.*$",
		}

		It("refuses a gallery whose artifact carries no signature, and caches nothing", func() {
			srv, _, blobs := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/unsigned", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			resetCounters(blobs)

			// The real verifier reaches the public Sigstore TUF mirror, which
			// a test must not depend on. The seam keeps the spec about what
			// this package decides: an artifact that does not verify is never
			// unpacked.
			stubGalleryVerifier(func(_ context.Context, _ *config.GalleryVerification, _ string) error {
				return errNoGallerySignature
			})

			base := tempModelsDir()
			_, _, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "unsigned", Verification: policy}, base, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("signature"))
			Expect(ociCacheEntries(base)).To(BeEmpty(), "unverified content landed in the cache")
			Expect(blobs.Load()).To(BeZero(), "content was fetched before the signature was verified")
		})

		It("verifies the digest, not the tag, and only then unpacks", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/signed", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})

			verified := ""
			stubGalleryVerifier(func(_ context.Context, _ *config.GalleryVerification, ref string) error {
				verified = ref
				return nil
			})

			base := tempModelsDir()
			body, _, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "signed", Verification: policy}, base, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal(index))
			Expect(verified).To(ContainSubstring("@sha256:"), "the tag was verified instead of the digest it resolved to")
			Expect(verified).ToNot(ContainSubstring(":latest"))
		})
	})

	It("refuses an unsigned gallery in strict integrity mode", func() {
		srv, _, blobs := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/strict", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		resetCounters(blobs)

		base := tempModelsDir()
		// Strict integrity is the --require-backend-integrity /
		// LOCALAI_REQUIRE_BACKEND_INTEGRITY switch, carried here on the system
		// state the listing already had.
		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "strict"}, base, true)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("verification"))
		Expect(ociCacheEntries(base)).To(BeEmpty())
		Expect(blobs.Load()).To(BeZero())
	})

	// A pull that dies halfway leaves partial files behind. Serving those as
	// if they were the gallery is worse than failing: the user gets a
	// truncated index with no sign that anything went wrong.
	It("does not serve a half-written pull to a later fetch", func() {
		var fail atomic.Bool
		fail.Store(true)
		upstream := registry.New()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail.Load() && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
				// Answer with content that does not match the descriptor, the
				// way a broken proxy or a truncated transfer would.
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = w.Write([]byte("- name: trunc"))
				return
			}
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(srv.Close)

		url := pushGalleryArtifact(srv.URL, "galleries/partial", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		base := tempModelsDir()
		g := config.Gallery{URL: url, Name: "partial"}

		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).To(HaveOccurred())
		Expect(ociCacheEntries(base)).To(BeEmpty(), "a failed pull left a directory a later fetch would serve")

		// With the registry healthy again the gallery must come back with the
		// real index, not with whatever the failed attempt left on disk.
		fail.Store(false)
		resetGalleryFailures()
		body, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(index))
	})
})

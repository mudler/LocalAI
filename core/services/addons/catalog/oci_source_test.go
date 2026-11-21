// SPDX-License-Identifier: MIT
package catalog

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	oraslib "oras.land/oras-go/v2/registry/remote"
)

// ociLibraryRegistry starts an in-process registry. The returned handler
// switch lets a spec answer every request with one status, as a registry that
// refuses the credentials does.
func ociLibraryRegistry() (*httptest.Server, *int) {
	GinkgoHelper()
	status := new(int)
	upstream := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *status != 0 {
			w.WriteHeader(*status)
			return
		}
		upstream.ServeHTTP(w, r)
	}))
	DeferCleanup(srv.Close)
	return srv, status
}

// pushLibrary publishes a one-file artifact and returns its oci:// address.
func pushLibrary(serverURL, repoPath, artifactType, body string) string {
	GinkgoHelper()
	host := strings.TrimPrefix(serverURL, "http://")
	store := memory.New()
	b := []byte(body)
	desc := content.NewDescriptorFromBytes("application/yaml", b)
	desc.Annotations = map[string]string{ocispec.AnnotationTitle: "index.yaml"}
	Expect(store.Push(ctx, desc, bytes.NewReader(b))).To(Succeed())
	manifest, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType,
		oras.PackManifestOptions{Layers: []ocispec.Descriptor{desc}})
	Expect(err).NotTo(HaveOccurred())
	Expect(store.Tag(ctx, manifest, "latest")).To(Succeed())
	repo, err := oraslib.NewRepository(host + "/" + repoPath)
	Expect(err).NotTo(HaveOccurred())
	repo.PlainHTTP = true
	_, err = oras.Copy(ctx, store, "latest", repo, "latest", oras.DefaultCopyOptions)
	Expect(err).NotTo(HaveOccurred())
	return "oci://" + host + "/" + repoPath + ":latest"
}

func libraryIndexBody() string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(WriteIndex(&buf, []Release{release()})).To(Succeed())
	return buf.String()
}

// expireLibraryCache makes the unpacked copy older than its TTL.
func expireLibraryCache(s *Sources, src Source) {
	GinkgoHelper()
	old := time.Now().Add(-2 * libraryCacheTTL)
	Expect(os.Chtimes(s.libraryCacheDir(src), old, old)).To(Succeed())
}

func stubLibraryVerifier(f func(context.Context, VerificationPolicy, string) error) {
	GinkgoHelper()
	original := verifyLibraryArtifact
	verifyLibraryArtifact = f
	DeferCleanup(func() { verifyLibraryArtifact = original })
}

var signedPolicy = VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", Identity: "signer@registry.example"}

var _ = Describe("oci:// libraries", func() {
	var (
		srv    *httptest.Server
		status *int
		s      *Sources
	)
	saveOCI := func(url string, policy VerificationPolicy) Source {
		GinkgoHelper()
		src := Source{ID: "lib", Name: "lib", URL: url, Enabled: true, Policy: policy}
		Expect(s.Save(ctx, admin, src)).To(Succeed())
		return src
	}
	BeforeEach(func() {
		srv, status = ociLibraryRegistry()
		s = setup()
	})

	It("lists the entries of the artifact and stamps the source on them", func() {
		url := pushLibrary(srv.URL, "libs/fixture-addon", LibraryArtifactType, libraryIndexBody())
		saveOCI(url, VerificationPolicy{})
		result, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Count).To(Equal(1))
		r := s.cache["lib"].releases[0]
		Expect(r.SourceID).To(Equal("lib"))
		Expect(r.SourceURL).To(Equal(url))
		Expect(r.Official).To(BeFalse())
		Expect(r.Policy).To(Equal(VerificationPolicy{}))
	})

	It("stamps the policy of a signed source on its entries", func() {
		url := pushLibrary(srv.URL, "libs/signed", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(_ context.Context, p VerificationPolicy, ref string) error {
			Expect(p).To(Equal(signedPolicy))
			Expect(ref).To(ContainSubstring("@sha256:"), "the tag was verified instead of the digest")
			return nil
		})
		saveOCI(url, signedPolicy)
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.cache["lib"].releases[0].Policy).To(Equal(signedPolicy))
	})

	It("refuses a bad signature, caches nothing and serves no older copy afterwards", func() {
		url := pushLibrary(srv.URL, "libs/bad", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error { return nil })
		src := saveOCI(url, signedPolicy)
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())

		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error {
			return fmt.Errorf("no signature: %w", cosignverify.ErrPolicyRejected)
		})
		expireLibraryCache(s, src)
		_, err = s.Refresh(ctx, "lib")
		Expect(err).To(MatchError(ErrRefresh))
		Expect(s.cache["lib"].failed).To(BeTrue())
		Expect(s.cache["lib"].releases).To(BeEmpty(), "an older copy was served after a refusal")
		Expect(s.cache["lib"].err).To(ContainSubstring("signature policy"))
	})

	It("refuses a bad signature on a first fetch and leaves nothing on disk", func() {
		url := pushLibrary(srv.URL, "libs/never", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error {
			return fmt.Errorf("no signature: %w", cosignverify.ErrPolicyRejected)
		})
		src := saveOCI(url, signedPolicy)
		_, err := s.Refresh(ctx, "lib")
		Expect(err).To(MatchError(ErrRefresh))
		Expect(s.libraryCacheDir(src)).NotTo(BeADirectory())
	})

	It("serves the last verified copy when the registry is down", func() {
		url := pushLibrary(srv.URL, "libs/outage", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error { return nil })
		src := saveOCI(url, signedPolicy)
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())

		srv.Close()
		expireLibraryCache(s, src)
		result, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Count).To(Equal(1))
		Expect(s.cache["lib"].failed).To(BeFalse())
	})

	It("serves the last verified copy when the verifier cannot be reached", func() {
		url := pushLibrary(srv.URL, "libs/verifier-down", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error { return nil })
		src := saveOCI(url, signedPolicy)
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())

		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error { return fmt.Errorf("trust root unreachable") })
		expireLibraryCache(s, src)
		_, err = s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.cache["lib"].releases).To(HaveLen(1))
	})

	It("refuses an artifact of another type", func() {
		url := pushLibrary(srv.URL, "libs/image", "application/vnd.oci.image.config.v1+json", libraryIndexBody())
		src := saveOCI(url, VerificationPolicy{})
		_, err := s.Refresh(ctx, "lib")
		Expect(err).To(MatchError(ErrRefresh))
		Expect(s.libraryCacheDir(src)).NotTo(BeADirectory())
	})

	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		It(fmt.Sprintf("reports a registry answer of %d as a credential failure", code), func() {
			url := pushLibrary(srv.URL, "libs/private", LibraryArtifactType, libraryIndexBody())
			saveOCI(url, VerificationPolicy{})
			*status = code
			_, err := s.Refresh(ctx, "lib")
			Expect(err).To(MatchError(ErrCredential))
		})
	}

	It("does not reuse a copy admitted under another policy", func() {
		url := pushLibrary(srv.URL, "libs/policy", LibraryArtifactType, libraryIndexBody())
		stubLibraryVerifier(func(context.Context, VerificationPolicy, string) error { return nil })
		loose := saveOCI(url, VerificationPolicy{})
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())

		strict := saveOCI(url, signedPolicy)
		Expect(s.libraryCacheDir(strict)).NotTo(Equal(s.libraryCacheDir(loose)))
		// A registry that is gone must not let the looser copy answer for the
		// tighter policy, fresh or stale.
		srv.Close()
		_, err = s.Refresh(ctx, "lib")
		Expect(err).To(MatchError(ErrRefresh))
	})

	It("keeps the previous good copy when a pull returns an empty or non-YAML index", func() {
		url := pushLibrary(srv.URL, "libs/junk", LibraryArtifactType, libraryIndexBody())
		src := saveOCI(url, VerificationPolicy{})
		_, err := s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())

		for _, junk := range []string{"", "not: [an index", "releases: []\n"} {
			pushLibrary(srv.URL, "libs/junk", LibraryArtifactType, junk)
			expireLibraryCache(s, src)
			result, err := s.Refresh(ctx, "lib")
			Expect(err).NotTo(HaveOccurred(), "junk %q", junk)
			Expect(result.Count).To(Equal(1), "junk %q", junk)
			good, err := os.ReadFile(filepath.Join(s.libraryCacheDir(src), "index.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(good)).To(Equal(libraryIndexBody()), "the cache was replaced by %q", junk)
		}
	})

	It("fails a first fetch of a junk index", func() {
		url := pushLibrary(srv.URL, "libs/junk-first", LibraryArtifactType, "")
		src := saveOCI(url, VerificationPolicy{})
		_, err := s.Refresh(ctx, "lib")
		Expect(err).To(MatchError(ErrRefresh))
		Expect(s.libraryCacheDir(src)).NotTo(BeADirectory())
	})
})

var _ = DescribeTable("oci:// source addresses",
	func(src Source, ok bool) {
		src.ID, src.Name, src.Enabled = "lib", "lib", true
		Expect(validSource(src)).To(Equal(ok))
	},
	Entry("an add-on library", Source{URL: "oci://registry.example/libs/fixture-addon:latest"}, true),
	Entry("an explicit add-on kind", Source{URL: "oci://registry.example/libs/fixture-addon", Kind: KindAddon}, true),
	Entry("a digest reference", Source{URL: "oci://registry.example/libs/fixture-addon@sha256:" + strings.Repeat("a", 64)}, true),
	Entry("an exact signer identity", Source{URL: "oci://registry.example/libs/x", Policy: signedPolicy}, true),
	Entry("an identity pattern", Source{URL: "oci://registry.example/libs/x", Policy: VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", IdentityRegex: "^signer@.*$"}}, false),
	Entry("credentials in the address", Source{URL: "oci://user:secret@registry.example/libs/x"}, false),
	Entry("a space", Source{URL: "oci://registry.example/libs/x y"}, false),
	Entry("no repository", Source{URL: "oci://"}, false),
	Entry("a backend kind", Source{URL: "oci://registry.example/libs/x", Kind: KindBackend}, false),
	Entry("a gallery-format add-on source", Source{URL: "oci://registry.example/libs/x", Kind: KindAddon, Format: FormatGallery}, false),
)

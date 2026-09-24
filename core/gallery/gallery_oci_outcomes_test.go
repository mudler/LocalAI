package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// verifierVerdict is what a stubbed verifier answers, changeable mid-spec.
// atomic.Value would do, except that it cannot hold a nil error.
type verifierVerdict struct {
	mu  sync.Mutex
	err error
}

func (v *verifierVerdict) Store(err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.err = err
}

func (v *verifierVerdict) Load() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

var _ = Describe("oci:// gallery verification outcomes", func() {
	const index = "- name: acme-model\n"

	policy := &config.GalleryVerification{
		Issuer:        "https://token.actions.githubusercontent.com",
		IdentityRegex: "^https://github.com/acme/.*$",
	}

	BeforeEach(resetGalleryFailures)

	// signedGallery publishes a gallery, fetches it once under the policy so
	// a verified copy is on disk, and expires the unpacked cache so the next
	// fetch goes back to the registry.
	signedGallery := func() (config.Gallery, string, *verifierVerdict) {
		GinkgoHelper()
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})
		verdict := &verifierVerdict{}
		stubGalleryVerifier(func(context.Context, *config.GalleryVerification, string) error {
			return verdict.Load()
		})
		base := tempModelsDir()
		g := config.Gallery{URL: url, Name: "acme", Verification: policy}
		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		expireOCIGalleryCache()
		return g, base, verdict
	}

	// The verifier fetches the Sigstore trusted root and the signature from
	// the network. When that fails it has decided nothing about the gallery,
	// so the copy verified under the same policy is served, as it is when
	// the registry itself is down.
	DescribeTable("falls back to the verified copy when verification cannot reach its sources",
		func(failure error) {
			g, base, verdict := signedGallery()
			verdict.Store(failure)

			body, served, err := fetchGalleryIndex(context.Background(), g, base, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal(index))
			Expect(served).To(Equal(galleryCachePath(base, g.URL, policy)))
		},
		Entry("a timeout", fmt.Errorf("cosignverify: fetching trusted_root.json: %w", context.DeadlineExceeded)),
		Entry("a registry 5xx", fmt.Errorf("cosignverify: querying referrers: %w",
			&transport.Error{StatusCode: http.StatusServiceUnavailable})),
		Entry("a connection error", fmt.Errorf("cosignverify: querying referrers: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})),
	)

	It("does not fall back when the policy rejects the signature", func() {
		g, base, verdict := signedGallery()
		verdict.Store(fmt.Errorf("cosignverify: verification failed: %w", cosignverify.ErrPolicyRejected))

		body, served, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
		var refused *galleryVerificationError
		Expect(errors.As(err, &refused)).To(BeTrue(), "not reported as a refusal: %v", err)
		Expect(strings.Count(err.Error(), `"acme"`)).To(Equal(1), "gallery name repeated: %v", err)
	})

	Context("with a mirror that is not an OCI artifact", func() {
		It("does not let the mirror answer for a refused primary", func() {
			g, base, verdict := signedGallery()
			mirror, hits := countingServer(http.StatusOK, "- name: evil\n")
			g.Mirrors = []string{mirror.URL}
			Expect(galleryCachePath(base, g.URL, policy)).To(BeARegularFile())
			verdict.Store(fmt.Errorf("cosignverify: %w", cosignverify.ErrPolicyRejected))

			body, served, err := fetchGalleryIndex(context.Background(), g, base, false)
			Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
			Expect(hits.Load()).To(BeZero(), "an unsigned mirror was asked for a signed gallery")
			onDisk, readErr := os.ReadFile(galleryCachePath(base, g.URL, policy))
			Expect(readErr).ToNot(HaveOccurred())
			Expect(string(onDisk)).To(Equal(index), "the mirror's body replaced the verified copy")
		})

		It("does not let the mirror answer for an unreachable primary", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			stubGalleryVerifier(func(context.Context, *config.GalleryVerification, string) error { return nil })
			srv.Close()
			mirror, hits := countingServer(http.StatusOK, "- name: evil\n")
			base := tempModelsDir()

			body, served, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "acme", Mirrors: []string{mirror.URL}, Verification: policy}, base, false)
			Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
			Expect(hits.Load()).To(BeZero())
			Expect(galleryCachePath(base, url, policy)).ToNot(BeAnExistingFile())
		})

		It("does not let the mirror answer for a primary refused by strict integrity", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			mirror, hits := countingServer(http.StatusOK, "- name: evil\n")

			body, served, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "acme", Mirrors: []string{mirror.URL}}, tempModelsDir(), true)
			Expect(err).To(HaveOccurred(), "served %q from %q", string(body), served)
			Expect(err.Error()).To(ContainSubstring("strict integrity"))
			Expect(hits.Load()).To(BeZero())
		})

		It("still uses the mirror when the gallery has no policy", func() {
			srv, _, _ := ociRegistry()
			url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
				{title: "index.yaml", body: index},
			})
			srv.Close()
			mirror, _ := countingServer(http.StatusOK, "- name: mirrored\n")

			body, served, err := fetchGalleryIndex(context.Background(),
				config.Gallery{URL: url, Name: "acme", Mirrors: []string{mirror.URL}}, tempModelsDir(), false)
			Expect(err).ToNot(HaveOccurred())
			Expect(served).To(Equal(mirror.URL))
			Expect(string(body)).To(Equal("- name: mirrored\n"))
		})
	})

	// A backend gallery served over HTTPS carries a verification block for
	// the backend images it lists. Nothing checks its index, so the index
	// must not be stored under a name that claims a policy admitted it.
	It("keeps the URL-only cache name for an HTTP gallery whose policy covers its backends", func() {
		srv, _ := countingServer(http.StatusOK, index)
		base := tempModelsDir()
		g := config.Gallery{URL: srv.URL, Name: "backends", Verification: policy}

		_, _, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(galleryCachePath(base, srv.URL, policy)).ToNot(BeAnExistingFile())
		Expect(galleryCachePath(base, srv.URL, nil)).To(BeARegularFile())

		srv.Close()
		_, served, err := fetchGalleryIndex(context.Background(), g, base, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(served).To(Equal(galleryCachePath(base, srv.URL, nil)))
	})

	It("says strict integrity refused the gallery, not its verification policy", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/acme", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})

		_, _, err := fetchGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "acme"}, tempModelsDir(), true)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("strict integrity"))
		Expect(err.Error()).ToNot(ContainSubstring("refused by its verification policy"))
		Expect(strings.Count(err.Error(), `"acme"`)).To(Equal(1), "gallery name repeated: %v", err)
	})

	// The listing cache sits in front of the on-disk one. Keyed without the
	// policy, it kept listing what the old policy admitted after a runtime
	// settings change, and a relative entry then pointed at an unpacked tree
	// that the new policy had not produced.
	It("lists and installs again after the policy changes", func() {
		srv, _, _ := ociRegistry()
		url := pushGalleryArtifact(srv.URL, "galleries/relative-policy", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: "- name: relative-entry\n  url: base/virtual.yaml\n"},
			{title: "base/virtual.yaml", body: "name: virtual\nconfig_file: |\n  backend: llama\n"},
		})
		var calls atomic.Int64
		stubGalleryVerifier(func(context.Context, *config.GalleryVerification, string) error {
			calls.Add(1)
			return nil
		})
		base := tempModelsDir()
		systemState, err := system.GetSystemState(system.WithModelPath(base))
		Expect(err).ToNot(HaveOccurred())

		install := func(v *config.GalleryVerification) {
			GinkgoHelper()
			galleries := []config.Gallery{{Name: "relative-policy", URL: url, Verification: v}}
			models, err := AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(galleryModelNames(models)).To(ConsistOf("relative-entry"))
			Expect(InstallModelFromGallery(context.Background(), galleries, nil, systemState, nil,
				"relative-policy@relative-entry", GalleryModel{}, noProgress, false, false, false)).To(Succeed())
		}

		install(policy)
		Expect(calls.Load()).To(Equal(int64(1)))

		tightened := *policy
		tightened.SourceRepository = "https://github.com/acme/gallery"
		install(&tightened)
		Expect(calls.Load()).To(Equal(int64(2)), "the listing verified under the old policy was reused")
		Expect(filepath.Join(base, "relative-entry.yaml")).To(BeAnExistingFile())
	})
})

// pinnedPolicyCacheName is galleryCacheName of the fixed policy below.
const pinnedPolicyCacheName = "fe2f2092cdd99f3979c185bd3590873721e2c9394fd04411962d2e5d5b84333f"

var _ = Describe("galleryCacheName", func() {
	const url = "oci://registry.example.com/acme/gallery:latest"
	full := config.GalleryVerification{
		Issuer:           "https://token.actions.githubusercontent.com",
		IssuerRegex:      "^https://token\\.actions\\.githubusercontent\\.com$",
		Identity:         "https://github.com/acme/gallery/.github/workflows/publish.yml@refs/heads/main",
		IdentityRegex:    "^https://github\\.com/acme/.*$",
		SourceRepository: "https://github.com/acme/gallery",
		NotBefore:        "2026-01-01T00:00:00Z",
	}

	// Copies cached before policies became part of the name must stay usable
	// after an upgrade, so a gallery without a policy keeps the old name.
	It("keeps the URL-only name for a gallery without a policy", func() {
		sum := sha256.Sum256([]byte(url))
		Expect(galleryCacheName(url, nil)).To(Equal(hex.EncodeToString(sum[:])))
	})

	// Pinned so a reordered struct or a field that loses omitempty, either
	// of which renames every policy's cache and drops its offline copy on
	// upgrade, is a visible change rather than a silent one.
	It("keeps a stable name for a fixed policy", func() {
		Expect(galleryCacheName(url, &full)).To(Equal(pinnedPolicyCacheName))
	})

	// A field left out of the key would let a copy admitted by one value of
	// it be served under another. Walking the struct makes a newly added
	// field fail here until it is part of the key.
	It("changes the name when any policy field changes", func() {
		base := galleryCacheName(url, &full)
		v := reflect.ValueOf(&full).Elem()
		for i := range v.NumField() {
			changed := full
			field := reflect.ValueOf(&changed).Elem().Field(i)
			Expect(field.Kind()).To(Equal(reflect.String),
				"GalleryVerification.%s is not a string: extend this spec and galleryCacheName for it", v.Type().Field(i).Name)
			field.SetString(field.String() + "-changed")
			Expect(galleryCacheName(url, &changed)).ToNot(Equal(base),
				"GalleryVerification.%s is not part of the cache name", v.Type().Field(i).Name)
		}
	})
})

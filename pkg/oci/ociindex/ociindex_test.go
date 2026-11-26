package ociindex_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/pkg/oci/ociindex"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	oraslib "oras.land/oras-go/v2/registry/remote"
)

const (
	testType  = "application/vnd.example.index.v1"
	testIndex = "- name: entry\n"
)

// push publishes a one-file artifact to a registry listener and returns the
// reference a fetch uses.
func push(serverURL, repoPath, artifactType, title, body string) string {
	GinkgoHelper()
	host := strings.TrimPrefix(serverURL, "http://")
	ctx := context.Background()

	store := memory.New()
	b := []byte(body)
	desc := content.NewDescriptorFromBytes("application/yaml", b)
	desc.Annotations = map[string]string{ocispec.AnnotationTitle: title}
	Expect(store.Push(ctx, desc, bytes.NewReader(b))).To(Succeed())
	manifest, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType,
		oras.PackManifestOptions{Layers: []ocispec.Descriptor{desc}})
	Expect(err).ToNot(HaveOccurred())
	Expect(store.Tag(ctx, manifest, "latest")).To(Succeed())

	repo, err := oraslib.NewRepository(host + "/" + repoPath)
	Expect(err).ToNot(HaveOccurred())
	repo.PlainHTTP = true
	_, err = oras.Copy(ctx, store, "latest", repo, "latest", oras.DefaultCopyOptions)
	Expect(err).ToNot(HaveOccurred())
	return host + "/" + repoPath + ":latest"
}

// countingRegistry is an in-process registry that counts every request.
func countingRegistry() (*httptest.Server, *atomic.Int64) {
	GinkgoHelper()
	var requests atomic.Int64
	upstream := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		upstream.ServeHTTP(w, r)
	}))
	DeferCleanup(srv.Close)
	return srv, &requests
}

func options() ociindex.Options {
	GinkgoHelper()
	return ociindex.Options{
		ArtifactType: testType,
		IndexFile:    "index.yaml",
		CacheDir:     filepath.Join(GinkgoT().TempDir(), "cache", "entry"),
		CacheTTL:     time.Hour,
		MaxLayers:    16,
		MaxBytes:     1 << 20,
		Usable:       func(b []byte) bool { return strings.HasPrefix(string(b), "- name:") },
	}
}

var _ = Describe("Fetch", func() {
	It("pulls the index, keeps the tree and serves it from the cache within the TTL", func() {
		srv, requests := countingRegistry()
		ref := push(srv.URL, "libs/one", testType, "index.yaml", testIndex)
		o := options()

		body, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(testIndex))
		Expect(filepath.Join(o.CacheDir, "index.yaml")).To(BeAnExistingFile())

		requests.Store(0)
		body, err = ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(testIndex))
		Expect(requests.Load()).To(BeZero(), "the registry was contacted although the cache was fresh")
	})

	It("asks the registry on every fetch when the TTL is zero", func() {
		srv, requests := countingRegistry()
		ref := push(srv.URL, "libs/zero", testType, "index.yaml", testIndex)
		o := options()
		o.CacheTTL = 0
		_, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())

		// On a coarse clock the copy can be zero seconds old at the next
		// fetch. A modification time a little ahead of the clock makes that
		// case certain here, so only the TTL can send the fetch on.
		soon := time.Now().Add(time.Second)
		Expect(os.Chtimes(o.CacheDir, soon, soon)).To(Succeed())
		requests.Store(0)
		body, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(testIndex))
		Expect(requests.Load()).ToNot(BeZero(), "a copy was served without asking the registry although the TTL is zero")
	})

	It("refreshes a stale cache entry", func() {
		srv, requests := countingRegistry()
		ref := push(srv.URL, "libs/stale", testType, "index.yaml", testIndex)
		o := options()
		_, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())

		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(o.CacheDir, old, old)).To(Succeed())
		requests.Store(0)
		_, err = ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())
		Expect(requests.Load()).ToNot(BeZero())
	})

	It("refuses pulled bytes that are not usable and writes no cache", func() {
		srv, _ := countingRegistry()
		ref := push(srv.URL, "libs/junk", testType, "index.yaml", "not an index")
		o := options()

		_, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).To(HaveOccurred())
		Expect(filepath.Dir(o.CacheDir)).To(BeADirectory())
		entries, err := os.ReadDir(filepath.Dir(o.CacheDir))
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(BeEmpty(), "a refused pull left something in the cache root")
	})

	It("refuses an artifact whose manifest declares another type", func() {
		srv, _ := countingRegistry()
		ref := push(srv.URL, "libs/wrongtype", "application/vnd.oci.image.config.v1+json", "index.yaml", testIndex)
		o := options()

		_, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).To(HaveOccurred())
		Expect(o.CacheDir).ToNot(BeADirectory())
	})

	It("returns a VerifyError, wrapping the verifier error, and pulls nothing", func() {
		srv, _ := countingRegistry()
		ref := push(srv.URL, "libs/verify", testType, "index.yaml", testIndex)
		o := options()
		refused := errors.New("signature refused")
		var pinned string
		o.Verify = func(_ context.Context, digestRef string) error {
			pinned = digestRef
			return refused
		}

		_, err := ociindex.Fetch(context.Background(), ref, o)
		var verr *ociindex.VerifyError
		Expect(errors.As(err, &verr)).To(BeTrue(), "got %v", err)
		Expect(verr.Err).To(BeIdenticalTo(refused))
		Expect(errors.Is(err, refused)).To(BeTrue())
		Expect(pinned).To(ContainSubstring("@sha256:"))
		Expect(o.CacheDir).ToNot(BeADirectory())
	})

	It("pulls the digest the verifier approved", func() {
		srv, _ := countingRegistry()
		ref := push(srv.URL, "libs/pinned", testType, "index.yaml", testIndex)
		o := options()
		called := false
		o.Verify = func(_ context.Context, digestRef string) error {
			called = true
			Expect(digestRef).To(ContainSubstring("@sha256:"))
			return nil
		}

		body, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(string(body)).To(Equal(testIndex))
	})

	It("keeps a good cache entry when a refresh fails", func() {
		srv, _ := countingRegistry()
		ref := push(srv.URL, "libs/keep", testType, "index.yaml", testIndex)
		o := options()
		_, err := ociindex.Fetch(context.Background(), ref, o)
		Expect(err).ToNot(HaveOccurred())

		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(o.CacheDir, old, old)).To(Succeed())
		srv.Close()
		_, err = ociindex.Fetch(context.Background(), ref, o)
		Expect(err).To(HaveOccurred())
		// The caller may answer the outage with this copy; Fetch must not
		// have thrown it away.
		Expect(filepath.Join(o.CacheDir, "index.yaml")).To(BeAnExistingFile())
	})
})

var _ = Describe("Fetch credentials", Serial, func() {
	const (
		user = "bot"
		pass = "s3cret"
	)

	It("does not send the registry credential to a host the registry redirects a blob to", func() {
		upstream := registry.New()
		open := httptest.NewServer(upstream)
		DeferCleanup(open.Close)
		push(open.URL, "private/lib", testType, "index.yaml", testIndex)

		var mu sync.Mutex
		var seen []http.Header
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Clone())
			mu.Unlock()
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(other.Close)
		// A different host name, not just a different port, so the test does
		// not depend on how the client compares ports.
		otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)

		private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if strings.Contains(r.URL.Path, "/blobs/") {
				http.Redirect(w, r, otherURL+r.URL.Path, http.StatusTemporaryRedirect)
				return
			}
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(private.Close)

		// The registry client always matches rules against an https URL.
		store, err := credentials.Parse([]byte(fmt.Sprintf(
			"- match: %s/private\n  username: %s\n  password: %s\n  allow_insecure: true\n",
			strings.Replace(private.URL, "http://", "https://", 1), user, pass)),
			func(string) (string, bool) { return "", false })
		Expect(err).ToNot(HaveOccurred())
		prev := credentials.SetDefault(store)
		DeferCleanup(func() { credentials.SetDefault(prev) })

		body, err := ociindex.Fetch(context.Background(),
			strings.TrimPrefix(private.URL, "http://")+"/private/lib:latest", options())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(testIndex))

		mu.Lock()
		defer mu.Unlock()
		Expect(seen).ToNot(BeEmpty(), "the blob was never read from the redirect target")
		for _, h := range seen {
			Expect(h.Get("Authorization")).To(BeEmpty(), "the registry credential reached the redirect target")
		}
	})
})

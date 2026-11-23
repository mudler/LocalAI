package oci_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
	localoci "github.com/mudler/LocalAI/pkg/oci"
)

var _ = Describe("PullArtifact redirects", Serial, func() {
	const (
		user = "bot"
		pass = "s3cret"
	)

	var (
		upstream http.Handler
		open     *httptest.Server
		dest     string
	)

	BeforeEach(func() {
		upstream = registry.New()
		open = httptest.NewServer(upstream)
		DeferCleanup(open.Close)
		dest = filepath.Join(GinkgoT().TempDir(), "tree")
		Expect(os.MkdirAll(dest, 0o750)).To(Succeed())
	})

	// privateRegistry demands basic auth and sends blob reads to wherever
	// redirect points them.
	privateRegistry := func(redirect func(r *http.Request) string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/moved/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/moved")
			} else if strings.Contains(r.URL.Path, "/blobs/") {
				if u, p, ok := r.BasicAuth(); ok && u == user && p == pass {
					http.Redirect(w, r, redirect(r), http.StatusTemporaryRedirect)
					return
				}
			}
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(s.Close)
		useRule := func() {
			// The registry client always matches rules against an https URL.
			store, err := credentials.Parse([]byte(fmt.Sprintf(
				"- match: %s/private\n  username: %s\n  password: %s\n  allow_insecure: true\n",
				strings.Replace(s.URL, "http://", "https://", 1), user, pass)),
				func(string) (string, bool) { return "", false })
			Expect(err).ToNot(HaveOccurred())
			prev := credentials.SetDefault(store)
			DeferCleanup(func() { credentials.SetDefault(prev) })
		}
		useRule()
		return s
	}

	It("does not send the credential to the same host name on another port", func() {
		pushTestArtifact(open.URL, "private/lib", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
		})

		var mu sync.Mutex
		var seen []string
		// The open registry's host name is the private one's (both 127.0.0.1);
		// only the port differs, which is what the client's own redirect
		// rule ignores.
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Get("Authorization"))
			mu.Unlock()
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(target.Close)

		private := privateRegistry(func(r *http.Request) string { return target.URL + r.URL.Path })
		ref := strings.TrimPrefix(private.URL, "http://") + "/private/lib:latest"

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(filepath.Join(dest, "index.yaml"))).To(BeEquivalentTo("- name: one\n"))

		mu.Lock()
		defer mu.Unlock()
		Expect(seen).NotTo(BeEmpty(), "the blob was never read from the redirect target")
		for _, h := range seen {
			Expect(h).To(BeEmpty(), "the registry credential reached the redirect target")
		}
	})

	It("keeps the credential on a redirect to the same host and port", func() {
		pushTestArtifact(open.URL, "private/lib", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
		})

		var private *httptest.Server
		private = privateRegistry(func(r *http.Request) string { return private.URL + "/moved" + r.URL.Path })
		ref := strings.TrimPrefix(private.URL, "http://") + "/private/lib:latest"

		// The /moved/ path still demands the credential, so the pull only
		// succeeds if the client keeps it across a same-origin redirect.
		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(filepath.Join(dest, "index.yaml"))).To(BeEquivalentTo("- name: one\n"))
	})
})

package gallery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Characterization of the credentialed OCI gallery fetch: a registry may
// answer a blob read with a redirect to another host (object storage is the
// usual case), and the registry credential must stay with the registry.
var _ = Describe("oci:// gallery credentials", Serial, func() {
	const (
		user  = "bot"
		pass  = "s3cret"
		index = "- name: private-model\n"
	)

	It("does not send the registry credential to a host a blob is redirected to", func() {
		upstream := registry.New()

		// The artifact is published through an open listener on the same
		// store, so the fetch under test is the only credentialed client.
		open := httptest.NewServer(upstream)
		DeferCleanup(open.Close)
		pushGalleryArtifact(open.URL, "private/gallery", galleryArtifactType, []ociGalleryFile{
			{title: "index.yaml", body: index},
		})

		var mu sync.Mutex
		var seen []http.Header
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Clone())
			mu.Unlock()
			upstream.ServeHTTP(w, r)
		}))
		DeferCleanup(other.Close)
		// A different host name, not just a different port, so the test
		// does not depend on how the client compares ports.
		otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)

		private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="localai-test"`)
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

		store, err := credentials.Parse([]byte(fmt.Sprintf(
			"- match: %s/private\n  username: %s\n  password: %s\n  allow_insecure: true\n", strings.Replace(private.URL, "http://", "https://", 1), user, pass)),
			func(string) (string, bool) { return "", false })
		Expect(err).ToNot(HaveOccurred())
		prev := credentials.SetDefault(store)
		DeferCleanup(func() { credentials.SetDefault(prev) })

		url := "oci://" + strings.TrimPrefix(private.URL, "http://") + "/private/gallery:latest"
		body, err := fetchOCIGalleryIndex(context.Background(),
			config.Gallery{URL: url, Name: "private"}, url, tempModelsDir(), false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal(index))

		mu.Lock()
		defer mu.Unlock()
		Expect(seen).ToNot(BeEmpty(), "the blob was never read from the redirect target")
		for _, h := range seen {
			Expect(h.Get("Authorization")).To(BeEmpty(), "the registry credential reached the redirect target")
		}
	})
})

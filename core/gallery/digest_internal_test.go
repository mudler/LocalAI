package gallery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// localFetcher returns a fetcher that may reach a loopback test server.
func localFetcher() *BaseConfigFetcher {
	f := NewBaseConfigFetcher()
	f.validate = func(string) error { return nil }
	f.client = httpclient.NewWithTimeout(5*time.Second, httpclient.WithFollowRedirects())
	return f
}

var _ = Describe("BaseConfigFetcher", func() {
	It("asks again with the ETag and reuses the body on a 304", func() {
		var full, notModified atomic.Int32
		body := "name: tiny\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("If-None-Match") == `"v1"` {
				notModified.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			full.Add(1)
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(body))
		}))
		DeferCleanup(srv.Close)

		f := localFetcher()
		got1, sha1, err := f.Fetch(context.TODO(), srv.URL+"/tiny.yaml", GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		got2, sha2, err := f.Fetch(context.TODO(), srv.URL+"/tiny.yaml", GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got1)).To(Equal(body))
		Expect(got2).To(Equal(got1))
		Expect(sha2).To(Equal(sha1))
		Expect(full.Load()).To(Equal(int32(1)))
		Expect(notModified.Load()).To(Equal(int32(1)))
	})

	It("refuses a config over the size limit instead of cutting it", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(make([]byte, maxGalleryConfigBytes+1))
		}))
		DeferCleanup(srv.Close)
		_, _, err := localFetcher().Fetch(context.TODO(), srv.URL+"/big.yaml", GinkgoT().TempDir())
		Expect(err).To(MatchError(ContainSubstring("larger than")))
	})

	It("hashes no overrides as nothing, and fails on a value it cannot marshal", func() {
		sum, err := canonicalSHA256(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(sum).To(BeEmpty())
		sum, err = canonicalSHA256(map[string]any{"a": 1})
		Expect(err).ToNot(HaveOccurred())
		Expect(sum).ToNot(BeEmpty())
		_, err = canonicalSHA256(map[string]any{"a": make(chan int)})
		Expect(err).To(HaveOccurred())
	})

	It("refuses a private address unless the guard is replaced", func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
		DeferCleanup(srv.Close)
		_, _, err := NewBaseConfigFetcher().Fetch(context.TODO(), srv.URL+"/x.yaml", GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeZero())
	})

	It("validates the resolved http url of a github: url, not only the raw form", func() {
		var seen string
		f := NewBaseConfigFetcher()
		f.validate = func(u string) error {
			seen = u
			return errors.New("refused")
		}
		_, _, err := f.Fetch(context.TODO(), "github:mudler/LocalAI/gallery/x.yaml@master", GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(seen).To(HavePrefix("https://"))
	})

	It("does not dial a loopback host even when the url guard passes", func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
		DeferCleanup(srv.Close)
		f := NewBaseConfigFetcher()
		f.validate = func(string) error { return nil }
		_, _, err := f.Fetch(context.TODO(), srv.URL+"/x.yaml", GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeZero())
	})

	It("refuses a redirect to a link-local address", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		}))
		DeferCleanup(srv.Close)
		f := NewBaseConfigFetcher()
		f.validate = func(string) error { return nil }
		// The transport lets the test reach the loopback origin; the redirect
		// policy of the client is what must stop the second hop.
		f.client = newConfigHTTPClient(httpclient.WithTransport(&http.Transport{}))
		_, _, err := f.Fetch(context.TODO(), srv.URL+"/x.yaml", GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("169.254.169.254"))
	})

	It("reports a failed status", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		DeferCleanup(srv.Close)
		f := localFetcher()
		_, _, err := f.Fetch(context.TODO(), srv.URL+"/gone.yaml", GinkgoT().TempDir())
		Expect(err).To(MatchError(ContainSubstring("404")))
	})

	It("forgets the cached gallery indexes", func() {
		galleryCache.Set("k", galleryCacheEntry{yamlEntry: []byte("[]")})
		ForgetGalleryIndexes()
		Expect(galleryCache.Exists("k")).To(BeFalse())
	})
})

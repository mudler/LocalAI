package gallery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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

		f := NewBaseConfigFetcher()
		f.validate = func(string) error { return nil }
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

	It("refuses a private address unless the guard is replaced", func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
		DeferCleanup(srv.Close)
		_, _, err := NewBaseConfigFetcher().Fetch(context.TODO(), srv.URL+"/x.yaml", GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeZero())
	})

	It("reports a failed status", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		DeferCleanup(srv.Close)
		f := NewBaseConfigFetcher()
		f.validate = func(string) error { return nil }
		_, _, err := f.Fetch(context.TODO(), srv.URL+"/gone.yaml", GinkgoT().TempDir())
		Expect(err).To(MatchError(ContainSubstring("404")))
	})

	It("forgets the cached gallery indexes", func() {
		galleryCache.Set("k", galleryCacheEntry{yamlEntry: []byte("[]")})
		ForgetGalleryIndexes()
		Expect(galleryCache.Exists("k")).To(BeFalse())
	})
})

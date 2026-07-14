package gallery_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/mudler/LocalAI/core/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gallery API tests", func() {
	Context("requests", func() {
		It("parses github with a branch", func() {
			req := GalleryModel{
				Metadata: Metadata{
					URL: "github:go-skynet/model-gallery/gpt4all-j.yaml@main",
				},
			}
			e, err := GetGalleryConfigFromURL[ModelConfig](req.URL, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(e.Name).To(Equal("gpt4all-j"))
		})
	})

	// SSRF guard: a user-supplied gallery config URL (e.g. POST /models/apply
	// with an empty id) must not be able to reach internal network addresses.
	// See https://github.com/mudler/LocalAI/issues/10665
	Context("SSRF protection on config URLs", func() {
		var server *httptest.Server

		BeforeEach(func() {
			// A reachable internal server that would happily serve a valid
			// gallery config. Without the SSRF guard the fetch succeeds; the
			// guard must block it before the request ever leaves the process.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("name: internal-ssrf\nfiles: []\n"))
			}))
		})

		AfterEach(func() {
			server.Close()
		})

		It("blocks fetching a config from a loopback address", func() {
			_, err := GetGalleryConfigFromURL[ModelConfig](server.URL, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not allowed"))
		})

		It("blocks fetching a config from a loopback address (context variant)", func() {
			_, err := GetGalleryConfigFromURLWithContext[ModelConfig](context.Background(), server.URL, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not allowed"))
		})

		It("blocks well-known internal hostnames and metadata endpoints", func() {
			for _, u := range []string{
				"http://localhost/secret",
				"http://10.0.0.1/config.yaml",
				"http://192.168.1.1/config.yaml",
				"http://169.254.169.254/latest/meta-data/",
			} {
				_, err := GetGalleryConfigFromURL[ModelConfig](u, "")
				Expect(err).To(HaveOccurred(), "expected %s to be rejected", u)
			}
		})
	})
})

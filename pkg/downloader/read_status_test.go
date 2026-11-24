package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ReadWithCallback", func() {
	// ReadWithCallback used to hand the body of an error response to the
	// callback with a nil error, so a 404 page was indistinguishable from an
	// empty gallery index and callers had no way to notice the source was down.
	DescribeTable("fails on an HTTP error status",
		func(status int) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "nope", status)
			}))
			DeferCleanup(srv.Close)

			called := false
			err := downloader.URI(srv.URL).ReadWithCallback(specTempDir(), func(string, []byte) error {
				called = true
				return nil
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("status code"),
				"the error does not mention the status code")
			Expect(called).To(BeFalse(), "the error body was passed to the callback as content")
		},
		Entry("404", http.StatusNotFound),
		Entry("500", http.StatusInternalServerError),
		Entry("502", http.StatusBadGateway),
	)

	It("succeeds on 200", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("- name: a\n"))
		}))
		DeferCleanup(srv.Close)

		var got string
		Expect(downloader.URI(srv.URL).ReadWithAuthorizationAndCallback(context.Background(), specTempDir(), "",
			func(_ string, d []byte) error {
				got = string(d)
				return nil
			})).To(Succeed())
		Expect(got).To(Equal("- name: a\n"))
	})
})

// SPDX-License-Identifier: MIT
package downloader

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("config redirect validation", func() {
	It("rejects a private redirect before fetching or delivering its body", func() {
		var hits atomic.Int32
		sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
		}))
		DeferCleanup(sink.Close)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, sink.URL, http.StatusFound)
		}))
		DeferCleanup(origin.Close)
		called := false
		err := URI(origin.URL).ReadWithCallback("", func(string, []byte) error {
			called = true
			return nil
		})
		Expect(err).To(MatchError(ContainSubstring("internal network addresses")))
		Expect(called).To(BeFalse())
		Expect(hits.Load()).To(BeZero())

		// File downloads retain their existing redirect policy.
		response, err := downloadHTTPClient().Get(origin.URL)
		Expect(err).NotTo(HaveOccurred())
		response.Body.Close()
		Expect(hits.Load()).To(Equal(int32(1)))
	})
	It("shares the cached transport between config reads", func() {
		Expect(redirectValidatedHTTPClient().Transport).To(Equal(downloadHTTPClient().Transport))
		Expect(redirectValidatedHTTPClient().Transport).To(Equal(redirectValidatedHTTPClient().Transport))
	})
})

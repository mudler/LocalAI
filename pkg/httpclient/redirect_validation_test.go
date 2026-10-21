// SPDX-License-Identifier: MIT
package httpclient_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("redirect URL validation", func() {
	It("validates every hop and stops before sending to a rejected target", func() {
		var hits atomic.Int32
		sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
		DeferCleanup(sink.Close)
		intermediate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, sink.URL, http.StatusFound)
		}))
		DeferCleanup(intermediate.Close)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, intermediate.URL, http.StatusFound)
		}))
		DeferCleanup(origin.Close)
		rejected := errors.New("rejected destination")
		var targets []string
		client := httpclient.New(httpclient.WithFollowRedirects(), httpclient.WithRedirectURLValidation(func(u *url.URL) error {
			targets = append(targets, u.String())
			if u.String() == sink.URL {
				return rejected
			}
			return nil
		}))
		_, err := client.Get(origin.URL)
		Expect(errors.Is(err, rejected)).To(BeTrue())
		Expect(targets).To(Equal([]string{intermediate.URL, sink.URL}))
		Expect(hits.Load()).To(BeZero())
	})
	It("keeps credential stripping and the redirect limit when validation succeeds", func() {
		client := httpclient.New(httpclient.WithFollowRedirects(), httpclient.WithRedirectURLValidation(func(*url.URL) error { return nil }))
		origin, err := http.NewRequest(http.MethodGet, "https://first.example/start", nil)
		Expect(err).NotTo(HaveOccurred())
		next, err := http.NewRequest(http.MethodGet, "https://second.example/end", nil)
		Expect(err).NotTo(HaveOccurred())
		next.Header.Set("Authorization", "Bearer secret")
		next.Header.Set("X-Api-Key", "secret")
		Expect(client.CheckRedirect(next, []*http.Request{origin})).To(Succeed())
		Expect(next.Header.Get("Authorization")).To(BeEmpty())
		Expect(next.Header.Get("X-Api-Key")).To(BeEmpty())
		Expect(client.CheckRedirect(next, make([]*http.Request, 10))).To(MatchError(ContainSubstring("stopped after 10 redirects")))
	})
	It("does not enable redirects by itself", func() {
		called := false
		client := httpclient.New(httpclient.WithRedirectURLValidation(func(*url.URL) error { called = true; return nil }))
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(errors.Is(client.CheckRedirect(req, []*http.Request{req}), httpclient.ErrRedirectBlocked)).To(BeTrue())
		Expect(called).To(BeFalse())
	})
})

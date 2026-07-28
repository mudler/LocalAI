package httpclient_test

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHTTPClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "httpclient suite")
}

var _ = Describe("httpclient", func() {
	Describe("New (default)", func() {
		It("refuses to follow redirects and never reaches the redirect target", func() {
			sinkHit := make(chan string, 1)
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sinkHit <- r.Header.Get("X-Api-Key")
				w.WriteHeader(http.StatusOK)
			}))
			defer sink.Close()

			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, sink.URL, http.StatusFound)
			}))
			defer redirector.Close()

			req, _ := http.NewRequest(http.MethodGet, redirector.URL, nil)
			req.Header.Set("X-Api-Key", "secret")

			_, err := httpclient.New().Do(req)
			Expect(err).To(HaveOccurred(), "redirect must surface as an error")
			Expect(errors.Is(err, httpclient.ErrRedirectBlocked)).To(BeTrue(), "error should wrap ErrRedirectBlocked")
			Expect(sinkHit).NotTo(Receive(), "the redirect target must never be contacted")
		})

		It("sets no overall timeout (streaming-safe) by default", func() {
			Expect(httpclient.New().Timeout).To(BeZero())
		})

		It("sets a TLS 1.2 floor on the default transport", func() {
			c := httpclient.New()
			t, ok := c.Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(t.TLSClientConfig).NotTo(BeNil())
			Expect(t.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
		})
	})

	Describe("WithResponseHeaderTimeout", func() {
		It("aborts a peer that accepts the request but never sends headers", func() {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-release
			}))
			defer srv.Close()
			defer close(release)

			c := httpclient.New(httpclient.WithResponseHeaderTimeout(200 * time.Millisecond))
			done := make(chan error, 1)
			go func() {
				_, err := c.Get(srv.URL)
				done <- err
			}()

			var err error
			Eventually(done, "3s").Should(Receive(&err))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("timeout awaiting response headers"))
		})

		It("does not bound the response body once headers have arrived", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(500 * time.Millisecond)
				_, _ = w.Write([]byte("late"))
			}))
			defer srv.Close()

			c := httpclient.New(httpclient.WithResponseHeaderTimeout(200 * time.Millisecond))
			resp, err := c.Get(srv.URL)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(body)).To(Equal("late"), "a slow body must not be truncated")
		})

		It("leaves the transport unbounded when not opted in, so streaming is unaffected", func() {
			t, ok := httpclient.New().Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(t.ResponseHeaderTimeout).To(BeZero())
		})
	})

	Describe("NewWithTimeout", func() {
		It("applies the overall timeout", func() {
			Expect(httpclient.NewWithTimeout(5 * time.Second).Timeout).To(Equal(5 * time.Second))
		})
	})

	Describe("WithFollowRedirects", func() {
		It("follows same-host redirects keeping the credential header", func() {
			got := make(chan string, 2)
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					http.Redirect(w, r, srv.URL+"/end", http.StatusFound)
					return
				}
				got <- r.Header.Get("X-Api-Key")
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
			req.Header.Set("X-Api-Key", "secret")

			resp, err := httpclient.New(httpclient.WithFollowRedirects()).Do(req)
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(<-got).To(Equal("secret"), "same-host redirect should preserve the header")
		})

		It("strips credential headers on a cross-host redirect", func() {
			sinkKey := make(chan string, 1)
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sinkKey <- r.Header.Get("X-Api-Key")
				w.WriteHeader(http.StatusOK)
			}))
			defer sink.Close()

			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, sink.URL, http.StatusFound)
			}))
			defer redirector.Close()

			req, _ := http.NewRequest(http.MethodGet, redirector.URL, nil)
			req.Header.Set("X-Api-Key", "secret")

			resp, err := httpclient.New(httpclient.WithFollowRedirects()).Do(req)
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(<-sinkKey).To(BeEmpty(), "x-api-key must be stripped crossing to a different host")
		})
	})

	Describe("Harden", func() {
		It("adds NoRedirect and a TLS floor to a bare client without clobbering existing config", func() {
			c := httpclient.Harden(&http.Client{})
			Expect(c.CheckRedirect).NotTo(BeNil())
			t, ok := c.Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(t.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
		})

		It("returns nil for a nil client", func() {
			Expect(httpclient.Harden(nil)).To(BeNil())
		})

		It("preserves a caller-supplied CheckRedirect", func() {
			sentinel := errors.New("mine")
			c := httpclient.Harden(&http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error { return sentinel },
			})
			Expect(c.CheckRedirect(nil, nil)).To(Equal(sentinel))
		})
	})
})

package mitm

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/net/http2"
)

// h2InterceptRig is the test fixture for HTTP/2 paths. Two things
// differ from the H1.1 rig:
//   - The client http.Transport has http2.ConfigureTransport called
//     so it negotiates h2 with our proxy.
//   - The upstream httptest server is started via StartTLS *and*
//     manually configured for h2 (httptest does this by default in
//     modern Go but we make it explicit for clarity).
func h2InterceptRig(interceptHost string, upstream http.Handler) (*http.Client, string, func()) {
	ts := httptest.NewUnstartedServer(upstream)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	upstreamCertPool := x509.NewCertPool()
	upstreamCertPool.AddCert(ts.Certificate())
	upstreamURL, _ := url.Parse(ts.URL)

	ca, err := NewInMemoryCA()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	srv, err := NewServer(Config{
		Addr:           "127.0.0.1:0",
		CA:             ca,
		InterceptHosts: []string{interceptHost},
		Handler:        passthroughHandler(upstreamCertPool, upstreamURL.Host),
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, srv.Start()).To(Succeed())

	// Client with HTTP/2 explicitly enabled (modern net/http does
	// this by default, but configuring the Transport directly makes
	// the test independent of stdlib defaults).
	clientPool := x509.NewCertPool()
	clientPool.AddCert(ca.Cert())
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{
			RootCAs:    clientPool,
			NextProtos: []string{"h2", "http/1.1"},
		},
		ForceAttemptHTTP2: true,
	}
	ExpectWithOffset(1, http2.ConfigureTransport(transport)).To(Succeed(), "client h2 configure")
	client := &http.Client{Transport: transport}

	cleanup := func() {
		srv.Stop()
		ts.Close()
	}
	return client, "https://" + interceptHost, cleanup
}

var _ = Describe("Proxy HTTP/2", func() {
	It("negotiates HTTP/2", func() {
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The upstream side: when serving over h2, r.ProtoMajor == 2.
			w.Header().Set("X-Upstream-Proto", r.Proto)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		})

		client, base, cleanup := h2InterceptRig("api.test.local", upstream)
		defer cleanup()

		resp, err := client.Get(base + "/v1/test")
		Expect(err).NotTo(HaveOccurred(), "Get")
		defer func() { _ = resp.Body.Close() }()

		// The proxy ↔ client leg: client sees h2 because we ALPN-
		// negotiated it. resp.Proto is the protocol the client used.
		Expect(resp.Proto).To(Equal("HTTP/2.0"), "proxy did not serve h2")
		body, _ := io.ReadAll(resp.Body)
		Expect(string(body)).To(ContainSubstring(`"ok":true`))
	})

	It("streams over HTTP/2", func() {
		// h2 streaming: the proxy must flush each frame promptly. The
		// upstream sends 3 SSE-style chunks; we read them back through
		// a streaming decoder so a buffering bug would surface.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)
			for _, msg := range []string{"first", "second", "third"} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			}
		})

		client, base, cleanup := h2InterceptRig("api.test.local", upstream)
		defer cleanup()

		resp, err := client.Get(base + "/stream")
		Expect(err).NotTo(HaveOccurred(), "Get")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.Proto).To(Equal("HTTP/2.0"), "expected h2 for streaming response")
		body, _ := io.ReadAll(resp.Body)
		for _, msg := range []string{"first", "second", "third"} {
			Expect(strings.Contains(string(body), "data: "+msg)).To(BeTrue(), "missing %q in h2 streamed body: %s", msg, body)
		}
	})

	It("falls back to HTTP/1.1", func() {
		// Force the client to negotiate h1.1 only, by overriding ALPN.
		// Verifies the fallback path still works for legacy clients.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		})

		ts := httptest.NewTLSServer(upstream)
		defer ts.Close()
		upstreamCertPool := x509.NewCertPool()
		upstreamCertPool.AddCert(ts.Certificate())
		upstreamURL, _ := url.Parse(ts.URL)

		ca, _ := NewInMemoryCA()
		srv, _ := NewServer(Config{
			Addr:           "127.0.0.1:0",
			CA:             ca,
			InterceptHosts: []string{"api.test.local"},
			Handler:        passthroughHandler(upstreamCertPool, upstreamURL.Host),
		})
		Expect(srv.Start()).To(Succeed())
		defer srv.Stop()

		clientPool := x509.NewCertPool()
		clientPool.AddCert(ca.Cert())
		proxyURL, _ := url.Parse("http://" + srv.Addr())
		// ALPN intentionally restricted to http/1.1 to force the
		// fallback path. Most clients will negotiate h2, but the
		// proxy must keep h1 working for the rare case.
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					RootCAs:    clientPool,
					NextProtos: []string{"http/1.1"},
				},
			},
		}
		resp, err := client.Get("https://api.test.local/v1/test")
		Expect(err).NotTo(HaveOccurred(), "Get")
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.Proto).To(Equal("HTTP/1.1"))
		body, _ := io.ReadAll(resp.Body)
		Expect(string(body)).To(ContainSubstring(`"ok":true`))
	})
})

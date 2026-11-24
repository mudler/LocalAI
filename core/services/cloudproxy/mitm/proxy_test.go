package mitm

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// passthroughHandler is the test fixture: forward the parsed
// request to the upstream and stream the response back. Mirrors
// what a production handler would do without any PII rewriting,
// so the proxy core's CONNECT/TLS/req-loop semantics are testable
// in isolation from the redaction logic.
func passthroughHandler(upstreamRoots *x509.CertPool, upstreamAddr string) InterceptHandler {
	return func(w http.ResponseWriter, r *http.Request, host string) {
		// Build the upstream URL — host is what the client thought
		// it was talking to (api.anthropic.com); upstreamAddr is
		// where the test fake actually lives. We use upstreamAddr
		// directly because the test fake's cert is self-signed
		// against an arbitrary CA we control.
		u := *r.URL
		u.Scheme = "https"
		u.Host = upstreamAddr

		body := r.Body
		req, err := http.NewRequest(r.Method, u.String(), body)
		if err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Header = r.Header.Clone()
		req.Header.Set("Host", host)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: upstreamRoots,
					// httptest.NewTLSServer issues a cert for
					// example.com / *.example.com regardless of the
					// listener's actual hostname. Trust that name
					// rather than the SNI the client used —
					// production code would set ServerName=host.
					ServerName: "example.com",
				},
			},
			Timeout: 10 * time.Second,
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// startMITMTestRig spins up:
//   - A fake "upstream" HTTPS server with a self-signed cert
//   - A MITM proxy that intercepts the upstream's hostname
//
// Returns a client http.Client whose Transport points at the proxy
// and trusts the MITM CA, plus the upstream URL the client should
// use. Callers tear down with the returned cleanup.
func startMITMTestRig(interceptHost string, upstream http.Handler) (*http.Client, string, func()) {
	// Upstream: real TLS server with its own cert. Trust this
	// from the proxy's outbound side only.
	ts := httptest.NewTLSServer(upstream)
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

	// Client side: trust the MITM CA so the proxied TLS handshake
	// succeeds. Configure HTTPS_PROXY to the proxy listener.
	clientPool := x509.NewCertPool()
	clientPool.AddCert(ca.Cert())
	proxyURL, _ := url.Parse("http://" + srv.Addr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: clientPool},
		},
		Timeout: 10 * time.Second,
	}

	cleanup := func() {
		srv.Stop()
		ts.Close()
	}
	return client, "https://" + interceptHost, cleanup
}

var _ = Describe("Proxy", func() {
	It("intercepts allowlisted host", func() {
		captured := false
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = true
			// Upstream receives whatever Host header the proxy
			// forwarded — in production this would be the real
			// hostname; in this test it's the upstream's listener.
			// We just verify *some* request landed at the upstream.
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		})

		client, baseURL, cleanup := startMITMTestRig("api.test.local", upstream)
		defer cleanup()

		resp, err := client.Get(baseURL + "/v1/test")
		Expect(err).NotTo(HaveOccurred(), "client.Get")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(200))
		body, _ := io.ReadAll(resp.Body)
		Expect(string(body)).To(ContainSubstring(`"ok":true`))
		Expect(captured).To(BeTrue(), "upstream handler was never called — proxy did not forward")
	})

	It("tunnels non-allowlisted host", func() {
		// Set up a "different" upstream we don't put in the allowlist.
		// The proxy should tunnel CONNECTs to it without TLS termination,
		// so we need to dial through the proxy and verify the upstream
		// sees the raw TLS — the MITM CA isn't used.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `passthrough`)
		})
		ts := httptest.NewTLSServer(upstream)
		defer ts.Close()
		upstreamURL, _ := url.Parse(ts.URL)
		upstreamHost, upstreamPort, _ := net.SplitHostPort(upstreamURL.Host)

		ca, _ := NewInMemoryCA()
		srv, err := NewServer(Config{
			Addr: "127.0.0.1:0",
			CA:   ca,
			// Allowlist only "api.test.local" — upstream's host is NOT
			// on it, so CONNECT to it must tunnel.
			InterceptHosts: []string{"api.test.local"},
			Handler:        func(w http.ResponseWriter, r *http.Request, h string) { http.Error(w, "should not be called", 500) },
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Start()).To(Succeed())
		defer srv.Stop()

		// Client trusts the upstream's actual cert (NOT the MITM CA),
		// so a successful TLS handshake proves the proxy did not MITM.
		upstreamCertPool := x509.NewCertPool()
		upstreamCertPool.AddCert(ts.Certificate())
		proxyURL, _ := url.Parse("http://" + srv.Addr())
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					RootCAs:    upstreamCertPool,
					ServerName: upstreamHost,
				},
			},
			Timeout: 10 * time.Second,
		}
		_ = upstreamPort

		resp, err := client.Get(ts.URL)
		Expect(err).NotTo(HaveOccurred(), "Get through tunnel")
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		Expect(string(body)).To(Equal("passthrough"))
	})

	It("rejects non-CONNECT requests", func() {
		ca, _ := NewInMemoryCA()
		srv, err := NewServer(Config{
			Addr:    "127.0.0.1:0",
			CA:      ca,
			Handler: func(w http.ResponseWriter, r *http.Request, h string) {},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(srv.Start()).To(Succeed())
		defer srv.Stop()

		resp, err := http.Get("http://" + srv.Addr() + "/")
		Expect(err).NotTo(HaveOccurred(), "GET")
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("streams responses", func() {
		// SSE-style upstream: send three text chunks with explicit
		// flushes so the proxy's Flusher path is exercised.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			flusher := w.(http.Flusher)
			for _, msg := range []string{"a", "b", "c"} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			}
		})
		client, baseURL, cleanup := startMITMTestRig("api.test.local", upstream)
		defer cleanup()

		resp, err := client.Get(baseURL + "/stream")
		Expect(err).NotTo(HaveOccurred(), "Get")
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		for _, msg := range []string{"a", "b", "c"} {
			Expect(string(body)).To(ContainSubstring("data: " + msg))
		}
	})

	It("with no allowlist tunnels everything", func() {
		// Empty InterceptHosts means the proxy is in observability-
		// only mode: every CONNECT tunnels. Verifies the default-
		// fail-safe behaviour mentioned in shouldIntercept.
		upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "tunneled")
		})
		ts := httptest.NewTLSServer(upstream)
		defer ts.Close()
		upstreamURL, _ := url.Parse(ts.URL)
		upstreamHost, _, _ := net.SplitHostPort(upstreamURL.Host)

		ca, _ := NewInMemoryCA()
		srv, _ := NewServer(Config{
			Addr:    "127.0.0.1:0",
			CA:      ca,
			Handler: func(w http.ResponseWriter, r *http.Request, h string) { Fail("intercept handler called with empty allowlist") },
			// InterceptHosts intentionally empty.
		})
		Expect(srv.Start()).To(Succeed())
		defer srv.Stop()

		upstreamCertPool := x509.NewCertPool()
		upstreamCertPool.AddCert(ts.Certificate())
		proxyURL, _ := url.Parse("http://" + srv.Addr())
		client := &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				TLSClientConfig: &tls.Config{
					RootCAs:    upstreamCertPool,
					ServerName: upstreamHost,
				},
			},
		}
		resp, err := client.Get(ts.URL)
		Expect(err).NotTo(HaveOccurred(), "Get")
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		Expect(string(body)).To(Equal("tunneled"))
	})
})

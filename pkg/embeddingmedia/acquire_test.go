// SPDX-License-Identifier: MIT
package embeddingmedia_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const securityGroup = "Multimodal acquisition security"

var publicV4 = netip.MustParseAddr("93.184.216.34")

func pngBytes(n int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, n)...)
}

var samples = map[string][]byte{
	"png":  pngBytes(16),
	"jpeg": append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{1}, 16)...),
	"wav":  append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), bytes.Repeat([]byte{2}, 16)...),
	"mp3":  append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), bytes.Repeat([]byte{3}, 16)...),
	"flac": append([]byte("fLaC\x00\x00\x00\x22"), bytes.Repeat([]byte{4}, 16)...),
	"mp4":  append([]byte("\x00\x00\x00\x18ftypisom"), bytes.Repeat([]byte{5}, 16)...),
	"webm": append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x82, 0x84}, append([]byte("webm"), bytes.Repeat([]byte{6}, 16)...)...),
}

func expectError(err error, status int, code string) {
	GinkgoHelper()
	var e *embeddingmedia.Error
	ExpectWithOffset(1, errors.As(err, &e)).To(BeTrue(), "error %v", err)
	ExpectWithOffset(1, e.Status).To(Equal(status))
	ExpectWithOffset(1, e.Code).To(Equal(code))
}

const (
	codeFetch   = "embedding_fetch_failed"
	codeTimeout = "embedding_fetch_timeout"
	codeFormat  = "embedding_unsupported_format"
	codeLimit   = "embedding_resource_limit"
)

// harness routes every dial to an owned loopback server through the injected
// hooks. The production address policy still runs on the resolved addresses.
type harness struct {
	server   *httptest.Server
	mu       sync.Mutex
	dialed   []netip.AddrPort
	resolve  func(host string) []netip.Addr
	resolved int32
	timeout  time.Duration
	acq      *embeddingmedia.Acquirer
}

func (h *harness) dials() []netip.AddrPort {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]netip.AddrPort(nil), h.dialed...)
}

func (h *harness) port() string {
	_, p, _ := net.SplitHostPort(h.server.Listener.Addr().String())
	return p
}

func (h *harness) url(path string) string { return "http://media.example:" + h.port() + path }

func newHarness(handler http.Handler, tls bool, timeout time.Duration) *harness {
	h := &harness{timeout: timeout}
	if tls {
		h.server = httptest.NewTLSServer(handler)
	} else {
		h.server = httptest.NewServer(handler)
	}
	DeferCleanup(h.server.Close)
	h.resolve = func(string) []netip.Addr { return []netip.Addr{publicV4} }
	hooks := embeddingmedia.TestHooks{
		FetchTimeout: timeout,
		Resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			atomic.AddInt32(&h.resolved, 1)
			return h.resolve(host), nil
		},
		Dial: func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
			h.mu.Lock()
			h.dialed = append(h.dialed, addr)
			h.mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, "tcp", h.server.Listener.Addr().String())
		},
	}
	if tls {
		pool := x509.NewCertPool()
		pool.AddCert(h.server.Certificate())
		hooks.RootCAs = pool
	}
	h.acq = embeddingmedia.NewTestAcquirer(hooks)
	return h
}

func bodyHandler(body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) })
}

func fetch(h *harness, kind, path string, budget *embeddingmedia.Budget) ([]byte, error) {
	return h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h.url(path), Kind: kind}, budget)
}

func fresh() *embeddingmedia.Budget { b := embeddingmedia.NewBudget(); return &b }

// stdLookup answers like net.Resolver.LookupNetIP: IPv4 answers come back in
// 4-in-6 form for every network, and the caller's family selects the answers.
func stdLookup(v4, v6 []netip.Addr) func(context.Context, string, string) ([]netip.Addr, error) {
	return func(_ context.Context, network, _ string) ([]netip.Addr, error) {
		var out []netip.Addr
		if network == "ip" || network == "ip4" {
			out = append(out, v4...)
		}
		if network == "ip" || network == "ip6" {
			out = append(out, v6...)
		}
		if len(out) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: "media.example", IsNotFound: true}
		}
		return out, nil
	}
}

// mapped returns s in the 16-byte form the standard resolver produces.
func mapped(s string) netip.Addr { return netip.AddrFrom16(netip.MustParseAddr(s).As16()) }

// useLookup routes the harness through the production resolver closure, with
// only the stdlib boundary replaced.
func useLookup(h *harness, lookup func(context.Context, string, string) ([]netip.Addr, error)) {
	h.acq = embeddingmedia.NewTestAcquirer(embeddingmedia.TestHooks{
		Lookup: lookup,
		Dial: func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
			h.mu.Lock()
			h.dialed = append(h.dialed, addr)
			h.mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, "tcp", h.server.Listener.Addr().String())
		},
	})
}

var _ = Describe(securityGroup, func() {
	Describe("network destinations", func() {
		It("refuses redirects without following them or forwarding credentials", func() {
			var hits int32
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final" {
					atomic.AddInt32(&hits, 1)
				}
				http.Redirect(w, r, "/final", http.StatusFound)
			}), false, 0)
			_, err := fetch(h, "image", "/start", fresh())
			expectError(err, 502, codeFetch)
			Expect(atomic.LoadInt32(&hits)).To(BeZero())
			Expect(h.dials()).To(HaveLen(1))
		})

		It("rechecks resolution on every dial and rejects rebinding to a denied address", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			var calls int32
			h.resolve = func(string) []netip.Addr {
				if atomic.AddInt32(&calls, 1) == 1 {
					return []netip.Addr{publicV4}
				}
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
			}
			_, err := fetch(h, "image", "/a", fresh())
			Expect(err).NotTo(HaveOccurred())
			_, err = fetch(h, "image", "/b", fresh())
			expectError(err, 502, codeFetch)
			Expect(h.dials()).To(HaveLen(1))
			Expect(h.dials()[0].Addr()).To(Equal(publicV4))
		})

		It("rejects a name resolving to any denied address", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			h.resolve = func(string) []netip.Addr {
				return []netip.Addr{publicV4, netip.MustParseAddr("10.1.2.3")}
			}
			_, err := fetch(h, "image", "/", fresh())
			expectError(err, 502, codeFetch)
			Expect(h.dials()).To(BeEmpty())
		})

		It("denies every reserved IPv4 and IPv6 range", func() {
			denied := []string{
				"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
				"172.16.0.1", "192.0.0.1", "192.0.2.1", "192.168.1.1", "198.18.0.1", "198.51.100.1",
				"203.0.113.1", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
				"::", "::1", "fe80::1", "fec0::1", "fc00::1", "fd12::1", "ff02::1", "ff0e::1",
				"2001:db8::1", "2001::1", "2002:7f00:1::", "64:ff9b::7f00:1", "100::1", "::7f00:1",
			}
			for _, s := range denied {
				Expect(embeddingmedia.IsApprovedAddress(netip.MustParseAddr(s))).To(BeFalse(), s)
			}
			for _, s := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "2606:2800:220:1::1", "2a00:1450:4001::1"} {
				Expect(embeddingmedia.IsApprovedAddress(netip.MustParseAddr(s))).To(BeTrue(), s)
			}
		})

		It("denies IPv4-mapped IPv6 destinations", func() {
			for _, s := range []string{"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:93.184.216.34", "::ffff:169.254.169.254"} {
				Expect(embeddingmedia.IsApprovedAddress(netip.MustParseAddr(s))).To(BeFalse(), s)
			}
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			h.resolve = func(string) []netip.Addr { return []netip.Addr{netip.MustParseAddr("::ffff:93.184.216.34")} }
			_, err := fetch(h, "image", "/", fresh())
			expectError(err, 502, codeFetch)
			Expect(h.dials()).To(BeEmpty())
		})

		It("approves public IPv4 answers returned in 4-in-6 form by the production resolver", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			useLookup(h, stdLookup([]netip.Addr{mapped("93.184.216.34")}, nil))
			data, err := fetch(h, "image", "/", fresh())
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(pngBytes(8)))
			Expect(h.dials()).To(HaveLen(1))
			Expect(h.dials()[0].Addr()).To(Equal(publicV4))
			Expect(h.dials()[0].Addr().Is4()).To(BeTrue())
		})

		It("denies private and loopback IPv4 answers in 4-in-6 form from the production resolver", func() {
			for _, s := range []string{"127.0.0.1", "10.0.0.1"} {
				h := newHarness(bodyHandler(pngBytes(8)), false, 0)
				useLookup(h, stdLookup([]netip.Addr{mapped(s)}, nil))
				_, err := fetch(h, "image", "/", fresh())
				expectError(err, 502, codeFetch)
				Expect(h.dials()).To(BeEmpty(), s)
			}
		})

		It("denies an IPv4-mapped AAAA answer from the production resolver", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			useLookup(h, stdLookup(nil, []netip.Addr{mapped("93.184.216.34")}))
			_, err := fetch(h, "image", "/", fresh())
			expectError(err, 502, codeFetch)
			Expect(h.dials()).To(BeEmpty())
		})

		It("denies IP literal and internal-name URLs without dialing", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			for _, u := range []string{
				"http://127.0.0.1/a.png", "http://10.0.0.5:8080/", "http://[::1]/", "http://[::ffff:7f00:1]/",
				"http://[fe80::1%25eth0]/", "http://localhost/a", "http://svc.local/a", "http://metadata.google.internal/a",
				"http://169.254.169.254/latest/meta-data",
			} {
				_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: u, Kind: "image"}, fresh())
				Expect(err).To(HaveOccurred(), u)
				var e *embeddingmedia.Error
				Expect(errors.As(err, &e)).To(BeTrue())
				Expect(e.Status).To(Or(Equal(502), Equal(400)), u)
			}
			Expect(h.dials()).To(BeEmpty())
			Expect(atomic.LoadInt32(&h.resolved)).To(BeZero())
		})

		It("denies the reserved ranges missing from the first table", func() {
			for _, s := range []string{
				"::ffff:0:7f00:1", "2001:1::1", "2001:3::1", "2001:4:112::1", "2001:10::1",
				"2001:20::1", "2001:30::1", "2620:4f:8000::1", "192.31.196.1", "192.52.193.1", "192.175.48.1",
			} {
				Expect(embeddingmedia.IsApprovedAddress(netip.MustParseAddr(s))).To(BeFalse(), s)
			}
		})

		It("pins the dial to the validated address, never the hostname", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			h.resolve = func(string) []netip.Addr { return []netip.Addr{netip.MustParseAddr("2606:2800:220:1::1")} }
			_, err := fetch(h, "image", "/", fresh())
			Expect(err).NotTo(HaveOccurred())
			Expect(h.dials()).To(HaveLen(1))
			Expect(h.dials()[0].Addr().String()).To(Equal("2606:2800:220:1::1"))
			Expect(h.dials()[0].Port()).To(BeEquivalentTo(mustPort(h.port())))
		})

		It("keeps the original hostname for TLS server name verification", func() {
			var sni atomic.Value
			handler := bodyHandler(pngBytes(8))
			h := &harness{}
			h.server = httptest.NewUnstartedServer(handler)
			h.server.TLS = &tls.Config{GetConfigForClient: func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
				sni.Store(hi.ServerName)
				return nil, nil
			}}
			h.server.StartTLS()
			DeferCleanup(h.server.Close)
			pool := x509.NewCertPool()
			pool.AddCert(h.server.Certificate())
			acq := embeddingmedia.NewTestAcquirer(embeddingmedia.TestHooks{
				RootCAs: pool,
				Resolve: func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{publicV4}, nil },
				Dial: func(ctx context.Context, _ string, addr netip.AddrPort) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", h.server.Listener.Addr().String())
				},
			})
			// The test certificate is issued for example.com.
			u := "https://example.com:" + h.port() + "/x.png"
			data, err := acq.Acquire(context.Background(), embeddingmedia.Source{URL: u, Kind: "image"}, fresh())
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(pngBytes(8)))
			Expect(sni.Load()).To(Equal("example.com"))
			// A certificate for another name must fail verification.
			u = "https://other.example:" + h.port() + "/x.png"
			_, err = acq.Acquire(context.Background(), embeddingmedia.Source{URL: u, Kind: "image"}, fresh())
			expectError(err, 502, codeFetch)
		})
	})

	Describe("request hygiene", func() {
		It("rejects URL credentials before any resolution or dial", func() {
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{
				URL: "http://user:pw-secret@media.example:" + h.port() + "/x", Kind: "image"}, fresh())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("pw-secret"))
			Expect(h.dials()).To(BeEmpty())
			Expect(atomic.LoadInt32(&h.resolved)).To(BeZero())
		})

		It("redacts credentials, query and fragments from every error", func() {
			secret := "tok-9f3a"
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom "+secret, 500)
			}), false, 0)
			cases := []string{
				"/p?token=" + secret + "#frag-" + secret,
				"/p/" + secret + "/x",
			}
			for _, p := range cases {
				_, err := fetch(h, "image", p, fresh())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).NotTo(ContainSubstring(secret))
				Expect(fmt.Sprintf("%#v %+v", err, err)).NotTo(ContainSubstring(secret))
				Expect(err.Error()).NotTo(ContainSubstring("media.example"))
			}
			// Dial failure, TLS failure and resolver failure stay sanitized too.
			for _, tc := range []struct{ u string }{
				{"https://" + secret + ".example:1/x?token=" + secret},
				{"http://" + secret + ".example/x?token=" + secret},
				{"gopher://x/" + secret},
				{"ftp://x/" + secret},
				{"file:///etc/passwd?" + secret},
			} {
				_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: tc.u, Kind: "image"}, fresh())
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).NotTo(ContainSubstring(secret))
				Expect(fmt.Sprintf("%#v", err)).NotTo(ContainSubstring(secret))
			}
		})

		It("ignores proxy environment variables", func() {
			var proxied int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&proxied, 1)
				http.Error(w, "proxy", 500)
			}))
			DeferCleanup(proxy.Close)
			for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY"} {
				GinkgoT().Setenv(k, proxy.URL)
			}
			GinkgoT().Setenv("NO_PROXY", "")
			h := newHarness(bodyHandler(pngBytes(8)), false, 0)
			Expect(embeddingmedia.ProxyDisabled(h.acq)).To(BeTrue())
			Expect(embeddingmedia.ProxyDisabled(embeddingmedia.NewAcquirer())).To(BeTrue())
			data, err := fetch(h, "image", "/", fresh())
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(pngBytes(8)))
			Expect(atomic.LoadInt32(&proxied)).To(BeZero())
			Expect(h.dials()).To(HaveLen(1))
		})

		It("sends no forwarded authentication or cookie headers", func() {
			var got http.Header
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				_, _ = w.Write(pngBytes(8))
			}), false, 0)
			ctx := context.WithValue(context.Background(), struct{}{}, "Bearer leaked")
			_, err := h.acq.Acquire(ctx, embeddingmedia.Source{URL: h.url("/x"), Kind: "image"}, fresh())
			Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key", "Referer", "Origin"} {
				Expect(got.Values(name)).To(BeEmpty(), name)
			}
		})

		It("classifies unsuccessful HTTP status as fetch failure", func() {
			for _, status := range []int{204, 206, 301, 304, 401, 403, 404, 429, 500, 503} {
				status := status
				h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					if status != 204 && status != 304 {
						_, _ = w.Write(pngBytes(8))
					}
				}), false, 0)
				_, err := fetch(h, "image", "/", fresh())
				expectError(err, 502, codeFetch)
			}
		})

		It("rejects encoded response bodies it did not request", func() {
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get("Accept-Encoding")).To(Equal("identity"))
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = w.Write(pngBytes(8))
			}), false, 0)
			_, err := fetch(h, "image", "/", fresh())
			expectError(err, 502, codeFetch)
		})
	})

	Describe("transfer bounds", func() {
		It("rejects declared length above the budget before reading the body", func() {
			release := make(chan struct{})
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100000")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}), false, 10*time.Second)
			DeferCleanup(func() { close(release) })
			b := fresh()
			b.MediaBytes = 64
			start := time.Now()
			_, err := fetch(h, "image", "/", b)
			expectError(err, 413, codeLimit)
			Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
			Expect(b.MediaBytes).To(BeEquivalentTo(64))
		})

		It("rejects unknown length bodies that exceed the budget at limit plus one", func() {
			var written int64
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chunk := bytes.Repeat([]byte{9}, 1024)
				_, _ = w.Write(pngBytes(0))
				for i := 0; i < 1<<20; i++ { // far beyond the limit
					n, err := w.Write(chunk)
					atomic.AddInt64(&written, int64(n))
					if err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
			}), false, 10*time.Second)
			b := fresh()
			b.MediaBytes = 4096
			_, err := fetch(h, "image", "/", b)
			expectError(err, 413, codeLimit)
			Expect(b.MediaBytes).To(BeEquivalentTo(4096))
			Expect(atomic.LoadInt64(&written)).To(BeNumerically("<", 64<<20))
		})

		It("accepts exactly the remaining media budget and charges actual bytes", func() {
			body := pngBytes(100)
			h := newHarness(bodyHandler(body), false, 0)
			b := fresh()
			b.MediaBytes = uint64(len(body))
			data, err := fetch(h, "image", "/", b)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(body))
			Expect(b.MediaBytes).To(BeZero())

			b = fresh()
			b.MediaBytes = uint64(len(body)) - 1
			_, err = fetch(h, "image", "/", b)
			expectError(err, 413, codeLimit)
			Expect(b.MediaBytes).To(BeEquivalentTo(len(body) - 1))

			b = fresh()
			_, err = fetch(h, "image", "/", b)
			Expect(err).NotTo(HaveOccurred())
			Expect(b.MediaBytes).To(BeEquivalentTo(embeddingmedia.MaxMediaBytes - uint64(len(body))))
		})

		It("does not allocate the declared length before the body arrives", func() {
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", strconv.FormatUint(embeddingmedia.MaxMediaBytes, 10))
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}), false, 2*time.Second)
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, err := fetch(h, "image", "/", fresh())
			runtime.ReadMemStats(&after)
			expectError(err, 504, codeTimeout)
			Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 1<<20))
		})

		It("reads a full-size declared body exactly at the limit", func() {
			body := pngBytes(3<<20 - 8)
			h := newHarness(bodyHandler(body), false, 0)
			b := fresh()
			b.MediaBytes = uint64(len(body))
			data, err := fetch(h, "image", "/", b)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(body))
			Expect(b.MediaBytes).To(BeZero())
		})

		It("rejects a body shorter than its declared length as fetch failure", func() {
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "500")
				_, _ = w.Write(pngBytes(8))
			}), false, 10*time.Second)
			b := fresh()
			_, err := fetch(h, "image", "/", b)
			expectError(err, 502, codeFetch)
			Expect(*b).To(Equal(embeddingmedia.NewBudget()))
		})

		It("maps a stalled response to 504 at the fetch deadline", func() {
			release := make(chan struct{})
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(pngBytes(8))
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}), false, 250*time.Millisecond)
			DeferCleanup(func() { close(release) })
			// Unknown length: the read must wait for more data and time out.
			start := time.Now()
			_, err := fetch(h, "image", "/", fresh())
			expectError(err, 504, codeTimeout)
			Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		})

		It("returns cancellation promptly when the caller cancels a stalled read", func() {
			release := make(chan struct{})
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(pngBytes(8))
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}), false, 20*time.Second)
			DeferCleanup(func() { close(release) })
			ctx, cancel := context.WithCancel(context.Background())
			go func() { time.Sleep(150 * time.Millisecond); cancel() }()
			start := time.Now()
			_, err := h.acq.Acquire(ctx, embeddingmedia.Source{URL: h.url("/"), Kind: "image"}, fresh())
			Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "%v", err)
			Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		})
	})

	Describe("inline data", func() {
		It("bounds base64 output before allocation", func() {
			encoded := strings.Repeat("QUJD", 2<<20) // 8 MiB of text, 6 MiB decoded
			b := fresh()
			b.MediaBytes = 12
			h := newHarness(bodyHandler(nil), false, 0)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{Data: encoded, Kind: "audio"}, b)
			runtime.ReadMemStats(&after)
			expectError(err, 413, codeLimit)
			Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 1<<20))
			Expect(b.MediaBytes).To(BeEquivalentTo(12))
			// The same bound applies to data URIs.
			_, err = h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: "data:audio/wav;base64," + encoded, Kind: "audio"}, b)
			expectError(err, 413, codeLimit)
		})

		It("decodes inline data into an owned exact-size buffer and charges its length", func() {
			h := newHarness(bodyHandler(nil), false, 0)
			raw := samples["wav"]
			enc := base64.StdEncoding.EncodeToString(raw)
			b := fresh()
			out, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{Data: enc, Kind: "audio", Format: "wav"}, b)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(raw))
			Expect(cap(out)).To(Equal(len(out)))
			Expect(b.MediaBytes).To(BeEquivalentTo(embeddingmedia.MaxMediaBytes - uint64(len(raw))))
			out[0] = 'X'
			out2, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{Data: enc, Kind: "audio"}, fresh())
			Expect(err).NotTo(HaveOccurred())
			Expect(out2[0]).To(Equal(byte('R')))
		})

		It("rejects malformed base64 data and data URIs", func() {
			h := newHarness(bodyHandler(nil), false, 0)
			good := base64.StdEncoding.EncodeToString(samples["png"])
			for name, src := range map[string]embeddingmedia.Source{
				"empty":        {Data: "", Kind: "image"},
				"both":         {Data: good, URL: "data:image/png;base64," + good, Kind: "image"},
				"neither":      {Kind: "image"},
				"badchars":     {Data: "!!!!", Kind: "image"},
				"unpadded":     {Data: strings.TrimRight(base64.StdEncoding.EncodeToString(samples["png"][:23]), "="), Kind: "image"},
				"newline":      {Data: good[:4] + "\n" + good[4:], Kind: "image"},
				"trailing":     {Data: good + "=", Kind: "image"},
				"uri nob64":    {URL: "data:image/png," + good, Kind: "image"},
				"uri nocomma":  {URL: "data:image/png;base64", Kind: "image"},
				"uri empty":    {URL: "data:image/png;base64,", Kind: "image"},
				"unknown kind": {Data: good, Kind: "text"},
			} {
				b := fresh()
				_, err := h.acq.Acquire(context.Background(), src, b)
				Expect(err).To(HaveOccurred(), name)
				var e *embeddingmedia.Error
				Expect(errors.As(err, &e)).To(BeTrue(), name)
				Expect(e.Status).To(Equal(400), name)
				Expect(*b).To(Equal(embeddingmedia.NewBudget()), name)
			}
		})

		It("validates data URI media types against the source kind", func() {
			h := newHarness(bodyHandler(nil), false, 0)
			enc := func(f string) string { return base64.StdEncoding.EncodeToString(samples[f]) }
			ok := []struct{ mime, format, kind string }{
				{"image/png", "png", "image"}, {"image/jpeg", "jpeg", "image"}, {"", "png", "image"},
				{"audio/wav", "wav", "audio"}, {"audio/x-wav", "wav", "audio"}, {"audio/mpeg", "mp3", "audio"},
				{"audio/flac", "flac", "audio"}, {"video/mp4", "mp4", "video"}, {"video/webm", "webm", "video"},
			}
			for _, c := range ok {
				_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{
					URL: "data:" + c.mime + ";base64," + enc(c.format), Kind: c.kind}, fresh())
				Expect(err).NotTo(HaveOccurred(), c.mime+" "+c.format)
			}
			for _, c := range []struct{ mime, format, kind string }{
				{"image/gif", "png", "image"}, {"text/plain", "png", "image"}, {"audio/wav", "png", "image"},
				{"image/png", "wav", "audio"}, {"audio/ogg", "wav", "audio"}, {"video/mp4", "webm", "video"},
			} {
				_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{
					URL: "data:" + c.mime + ";base64," + enc(c.format), Kind: c.kind}, fresh())
				expectError(err, 415, codeFormat)
			}
		})
	})

	Describe("format signatures", func() {
		It("accepts supported signatures per kind and rejects others with 415", func() {
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(samples[strings.TrimPrefix(r.URL.Path, "/")])
			}), false, 0)
			allowed := map[string][]string{
				"image": {"png", "jpeg"}, "audio": {"wav", "mp3", "flac"}, "video": {"mp4", "webm"},
			}
			for kind, names := range allowed {
				for name := range samples {
					_, err := fetch(h, kind, "/"+name, fresh())
					if contains(names, name) {
						Expect(err).NotTo(HaveOccurred(), kind+" "+name)
					} else {
						expectError(err, 415, codeFormat)
					}
				}
			}
			// Frame-sync MP3 without an ID3 tag, and garbage.
			samples["mp3sync"] = []byte{0xff, 0xfb, 0x90, 0x00, 1, 2, 3, 4}
			_, err := fetch(h, "audio", "/mp3sync", fresh())
			Expect(err).NotTo(HaveOccurred())
			for name, junk := range map[string][]byte{
				"tiny": {0x89}, "text": []byte("<html>not media</html>"), "riffavi": []byte("RIFF\x00\x00\x00\x00AVI LIST"),
				"mkv": {0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x82, 0x88, 'm', 'a', 't', 'r', 'o', 's', 'k', 'a'},
			} {
				samples["x-"+name] = junk
				kind := "audio"
				if name == "mkv" {
					kind = "video"
				}
				_, err := fetch(h, kind, "/x-"+name, fresh())
				expectError(err, 415, codeFormat)
			}
		})

		It("accepts only allow-listed ISO-BMFF major brands for video", func() {
			brands := map[string]bool{
				"isom": true, "mp42": true, "iso2": true, "iso4": true, "iso5": true, "iso6": true,
				"mp41": true, "avc1": true, "M4V ": true, "dash": true,
				"avif": false, "heic": false, "M4A ": false, "3gp4": false,
			}
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				brand := strings.TrimPrefix(r.URL.Path, "/")
				_, _ = w.Write(append([]byte("\x00\x00\x00\x18ftyp"+brand), bytes.Repeat([]byte{5}, 16)...))
			}), false, 0)
			for brand, ok := range brands {
				_, err := fetch(h, "video", "/"+url.PathEscape(brand), fresh())
				if ok {
					Expect(err).NotTo(HaveOccurred(), brand)
				} else {
					expectError(err, 415, codeFormat)
				}
			}
		})

		It("rejects a format hint that contradicts the detected signature", func() {
			h := newHarness(bodyHandler(samples["wav"]), false, 0)
			b := fresh()
			_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h.url("/"), Kind: "audio", Format: "mp3"}, b)
			expectError(err, 415, codeFormat)
			Expect(*b).To(Equal(embeddingmedia.NewBudget()))
			_, err = h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h.url("/"), Kind: "audio", Format: "ogg"}, fresh())
			expectError(err, 415, codeFormat)
			_, err = h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h.url("/"), Kind: "audio", Format: "WAV"}, fresh())
			Expect(err).NotTo(HaveOccurred())
			h2 := newHarness(bodyHandler(samples["jpeg"]), false, 0)
			_, err = h2.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h2.url("/"), Kind: "image", Format: "jpg"}, fresh())
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects unsupported schemes, file paths and shell-like sources", func() {
			h := newHarness(bodyHandler(samples["png"]), false, 0)
			for _, u := range []string{
				"file:///etc/passwd", "/etc/passwd", "../x.png", "~/x.png", "ftp://example.com/a.png",
				"gopher://example.com/", "$(touch /tmp/x)", "|cat /etc/passwd", "http://", "https:///a", "",
				"data:image/png;base64", "javascript:alert(1)",
			} {
				_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: u, Kind: "image"}, fresh())
				Expect(err).To(HaveOccurred(), u)
				var e *embeddingmedia.Error
				Expect(errors.As(err, &e)).To(BeTrue(), u)
			}
			Expect(h.dials()).To(BeEmpty())
		})
	})

	Describe("budget atomicity", func() {
		It("leaves the budget unchanged on every failure", func() {
			cases := map[string]http.Handler{
				"404":  http.NotFoundHandler(),
				"text": bodyHandler([]byte("plain text, not media")),
			}
			for name, handler := range cases {
				h := newHarness(handler, false, 0)
				b := fresh()
				_, err := fetch(h, "image", "/", b)
				Expect(err).To(HaveOccurred(), name)
				Expect(*b).To(Equal(embeddingmedia.NewBudget()), name)
			}
			var nilBudget *embeddingmedia.Budget
			h := newHarness(bodyHandler(samples["png"]), false, 0)
			_, err := h.acq.Acquire(context.Background(), embeddingmedia.Source{URL: h.url("/"), Kind: "image"}, nilBudget)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("preparation limiter", func() {
		It("admits exactly capacity holders and returns 429 beyond it", func() {
			l := embeddingmedia.NewLimiter(2)
			r1, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			r2, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			_, err = l.TryAcquire()
			expectError(err, 429, "embedding_capacity_exhausted")
			r1()
			r3, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			r2()
			r3()
			zero := embeddingmedia.NewLimiter(0)
			_, err = zero.TryAcquire()
			expectError(err, 429, "embedding_capacity_exhausted")
		})

		It("releases idempotently and frees exactly one slot", func() {
			l := embeddingmedia.NewLimiter(1)
			r, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			r()
			r()
			r()
			a, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			_, err = l.TryAcquire()
			expectError(err, 429, "embedding_capacity_exhausted")
			a()
		})

		It("never admits more than capacity under concurrent holders", func() {
			l := embeddingmedia.NewLimiter(2)
			var current, peak, admitted, rejected int32
			var wg sync.WaitGroup
			for i := 0; i < 64; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 50; j++ {
						release, err := l.TryAcquire()
						if err != nil {
							atomic.AddInt32(&rejected, 1)
							continue
						}
						n := atomic.AddInt32(&current, 1)
						for {
							p := atomic.LoadInt32(&peak)
							if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
								break
							}
						}
						atomic.AddInt32(&admitted, 1)
						runtime.Gosched()
						atomic.AddInt32(&current, -1)
						release()
						release()
					}
				}()
			}
			wg.Wait()
			Expect(atomic.LoadInt32(&peak)).To(BeNumerically("<=", 2))
			Expect(atomic.LoadInt32(&admitted)).To(BeNumerically(">", 0))
			r1, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			r2, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			r1()
			r2()
		})

		It("has a fixed production capacity of two per frontend and independent state", func() {
			a, b := embeddingmedia.NewPreparationLimiter(), embeddingmedia.NewPreparationLimiter()
			var rel []func()
			for i := 0; i < 2; i++ {
				r, err := a.TryAcquire()
				Expect(err).NotTo(HaveOccurred())
				rel = append(rel, r)
			}
			_, err := a.TryAcquire()
			expectError(err, 429, "embedding_capacity_exhausted")
			rb, err := b.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			rb()
			for _, r := range rel {
				r()
			}
			Expect(embeddingmedia.MaxConcurrentPreparations).To(BeEquivalentTo(2))
		})

		It("frees the slot after a cancelled acquisition releases it", func() {
			release := make(chan struct{})
			h := newHarness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(pngBytes(8))
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}), false, 20*time.Second)
			DeferCleanup(func() { close(release) })
			l := embeddingmedia.NewLimiter(1)
			rel, err := l.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				defer rel()
				_, err := h.acq.Acquire(ctx, embeddingmedia.Source{URL: h.url("/"), Kind: "image"}, fresh())
				done <- err
			}()
			_, err = l.TryAcquire()
			expectError(err, 429, "embedding_capacity_exhausted")
			cancel()
			Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
			Eventually(func() error {
				r, err := l.TryAcquire()
				if err == nil {
					r()
				}
				return err
			}, 2*time.Second).Should(Succeed())
		})
	})
})

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func mustPort(s string) uint16 {
	u, err := url.Parse("http://x:" + s)
	Expect(err).NotTo(HaveOccurred())
	var p uint16
	_, err = fmt.Sscanf(u.Port(), "%d", &p)
	Expect(err).NotTo(HaveOccurred())
	return p
}

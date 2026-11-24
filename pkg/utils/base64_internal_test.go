package utils

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box test for the http(s) download branch of GetContentURIAsBase64.
//
// We can't point it at a loopback httptest server directly: ValidateExternalURL
// runs its own net.LookupHost and rejects any host that resolves to a
// loopback/private/link-local IP (SSRF protection). So we use a TEST-NET-3
// literal (203.0.113.1, RFC 5737 — reserved for documentation, never globally
// routed). LookupHost short-circuits for IP literals (no DNS), and IsPublicIP
// accepts it, so validation passes offline. We then reassign the package
// download client's transport (an unexported var, reachable from this
// same-package test) so the connection is actually served by our in-process
// httptest server regardless of the address it was asked to dial.
var _ = Describe("utils/base64 http download", func() {
	It("downloads external content and returns its base64 encoding", func() {
		payload := []byte("\x89PNG\r\n\x1a\n not really a png, just some bytes")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		DeferCleanup(srv.Close)

		// Redirect every dial from the package download client to the test
		// server, then restore the original transport afterwards.
		orig := base64DownloadClient.Transport
		base64DownloadClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			},
		}
		DeferCleanup(func() { base64DownloadClient.Transport = orig })

		// 203.0.113.1 passes ValidateExternalURL without touching DNS.
		b64, err := GetContentURIAsBase64("http://203.0.113.1/whatever.png")
		Expect(err).To(BeNil())
		Expect(b64).To(Equal(base64.StdEncoding.EncodeToString(payload)))
	})

	It("rejects URLs that resolve to internal addresses", func() {
		// Sanity check that the SSRF guard is actually in the path: a loopback
		// literal must be refused before any download is attempted.
		b64, err := GetContentURIAsBase64("http://127.0.0.1/secret")
		Expect(err).To(HaveOccurred())
		Expect(b64).To(Equal(""))
	})
})

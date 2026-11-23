// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// assetHost stands in for the object store a GitHub release download
// redirects to. A TEST-NET literal needs no DNS and passes the public address
// check; the test transport sends its connections to an in-process server.
const assetHost = "203.0.113.2"

var _ = Describe("library index behind a release download redirect", func() {
	var (
		assetHits atomic.Int32
		assetAuth atomic.Value
	)

	BeforeEach(func() {
		assetHits.Store(0)
		assetAuth.Store("")
		asset := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			assetHits.Add(1)
			assetAuth.Store(r.Header.Get("Authorization"))
			_ = WriteIndex(w, []Release{release()})
		}))
		DeferCleanup(asset.Close)
		indexTransport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server certificate
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr == assetHost+":443" {
					addr = asset.Listener.Addr().String()
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		}
		DeferCleanup(func() { indexTransport = nil })
	})

	// releaseDownload answers like github.com/<owner>/<repo>/releases/download:
	// a 302 to a signed URL on another host.
	releaseDownload := func(target string) *httptest.Server {
		return library(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		})
	}
	allowAssetHost := func() {
		orig := ReleaseAssetHosts
		ReleaseAssetHosts = append(append([]string{}, orig...), assetHost)
		DeferCleanup(func() { ReleaseAssetHosts = orig })
	}

	It("follows the redirect to an allowed https host and reads the index", func() {
		allowAssetHost()
		server := releaseDownload("https://" + assetHost + "/release-asset/1?X-Amz-Signature=abc")
		s := setup()
		save(s, "lib", server.URL)
		result, e := s.Refresh(ctx, "lib")
		Expect(e).NotTo(HaveOccurred())
		Expect(result.Count).To(Equal(1))
		Expect(assetHits.Load()).To(Equal(int32(1)))
	})

	It("does not send the library credential to the redirect host", func() {
		allowAssetHost()
		server := library(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer library-secret"))
			http.Redirect(w, r, "https://"+assetHost+"/release-asset/1", http.StatusFound)
		})
		store, e := credentials.Parse([]byte("- match: "+server.URL+"\n  allow_insecure: true\n  bearer: library-secret"), nil)
		Expect(e).NotTo(HaveOccurred())
		s := setup()
		s.credentials = CredentialResolver{References: map[string]CredentialBinding{"lib": {Origin: server.URL, Store: store}}}
		Expect(s.Save(ctx, admin, Source{ID: "lib", Name: "lib", URL: server.URL, Enabled: true, AuthReference: "lib"})).To(Succeed())
		_, e = s.Refresh(ctx, "lib")
		Expect(e).NotTo(HaveOccurred())
		Expect(assetHits.Load()).To(Equal(int32(1)))
		Expect(assetAuth.Load()).To(BeEmpty())
	})

	DescribeTable("refuses a redirect the allow-list does not cover",
		func(target string, allow bool) {
			if allow {
				allowAssetHost()
			}
			server := releaseDownload(target)
			s := setup()
			save(s, "lib", server.URL)
			_, e := s.Refresh(ctx, "lib")
			Expect(e).To(MatchError(ErrRefresh))
			Expect(assetHits.Load()).To(BeZero())
		},
		// Before the fix every redirect was refused: this is how a 5.0.0
		// release build lost its official library.
		Entry("a host not on the list", "https://"+assetHost+"/release-asset/1", false),
		Entry("plain http to an allowed host", "http://"+assetHost+"/release-asset/1", true),
		Entry("a loopback address", "https://127.0.0.1/release-asset/1", false),
	)

	It("lists the object-store host GitHub redirects release downloads to", func() {
		Expect(ReleaseAssetHosts).To(ContainElements("github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"))
	})
})

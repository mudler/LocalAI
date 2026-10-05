package credentials_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

var _ = Describe("Transport", Serial, func() {
	var client *http.Client

	BeforeEach(func() {
		client = httpclient.New(
			httpclient.WithFollowRedirects(),
			httpclient.WithTransport(credentials.Transport(httpclient.HardenedTransport())),
		)
	})

	get := func(rawURL string, header http.Header) {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		Expect(err).NotTo(HaveOccurred())
		for k, v := range header {
			req.Header[k] = v
		}
		resp, err := client.Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Body.Close()).To(Succeed())
	}

	It("authenticates each redirect hop with its own rule and sends nothing where no rule matches", func() {
		var originAuth, originKey, mirrorAuth, mirrorKey, cdnAuth, cdnKey string
		cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cdnAuth = r.Header.Get("Authorization")
			cdnKey = r.Header.Get("X-Key")
		}))
		DeferCleanup(cdn.Close)
		mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorAuth = r.Header.Get("Authorization")
			mirrorKey = r.Header.Get("X-Key")
			http.Redirect(w, r, cdn.URL+"/blob", http.StatusFound)
		}))
		DeferCleanup(mirror.Close)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originAuth = r.Header.Get("Authorization")
			originKey = r.Header.Get("X-Key")
			http.Redirect(w, r, mirror.URL+"/file", http.StatusFound)
		}))
		DeferCleanup(origin.Close)

		// The origin uses a header rule because net/http strips Authorization
		// on a cross-host redirect by itself but knows nothing of X-Key, so
		// only a custom header proves the transport does not copy credentials
		// to the next hop.
		useStore(fmt.Sprintf("- match: %s\n  header:\n    name: X-Key\n    value: origin-key\n  allow_insecure: true\n- match: %s\n  bearer: mirror-token\n  allow_insecure: true\n", origin.URL, mirror.URL))
		get(origin.URL+"/start", nil)

		Expect(originKey).To(Equal("origin-key"))
		Expect(originAuth).To(BeEmpty())
		Expect(mirrorAuth).To(Equal("Bearer mirror-token"))
		Expect(mirrorKey).To(BeEmpty())
		Expect(cdnAuth).To(BeEmpty())
		Expect(cdnKey).To(BeEmpty())
	})

	It("leaves an explicit Authorization header to the caller", func() {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
		}))
		DeferCleanup(srv.Close)
		useStore(fmt.Sprintf("- match: %s\n  bearer: store-token\n  allow_insecure: true\n", srv.URL))

		get(srv.URL, http.Header{"Authorization": {"Bearer explicit"}})
		Expect(seen).To(Equal("Bearer explicit"))
	})

	It("fails the request when the matching rule cannot be resolved", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		DeferCleanup(srv.Close)
		useStore(fmt.Sprintf("- match: %s\n  bearer_env: MISSING_TOKEN\n  allow_insecure: true\n", srv.URL))

		req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Do(req)
		Expect(err).To(MatchError(ContainSubstring("MISSING_TOKEN")))
	})

	It("closes the request body when the matching rule cannot be resolved", func() {
		useStore("- match: https://files.example.com\n  bearer_env: MISSING_TOKEN\n")
		body := &closeRecorder{Reader: strings.NewReader("payload")}
		req, err := http.NewRequest(http.MethodPost, "https://files.example.com/upload", body)
		Expect(err).NotTo(HaveOccurred())

		_, err = credentials.Transport(httpclient.HardenedTransport()).RoundTrip(req)
		Expect(err).To(HaveOccurred())
		Expect(body.closed).To(BeTrue())
	})

	It("sends nothing when no store is installed", func() {
		var seen string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Header.Get("Authorization")
		}))
		DeferCleanup(srv.Close)
		prev := credentials.SetDefault(nil)
		DeferCleanup(func() { credentials.SetDefault(prev) })

		get(srv.URL, nil)
		Expect(seen).To(BeEmpty())
	})
})

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

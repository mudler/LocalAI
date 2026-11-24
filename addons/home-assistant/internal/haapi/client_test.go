// SPDX-License-Identifier: MIT
package haapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/haapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const token = "ha-token-very-secret-0123456789"

var _ = Describe("the address of a connection", func() {
	DescribeTable("accepted",
		func(raw, base, hostport string) {
			t, err := haapi.ParseBase(raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Base).To(Equal(base))
			Expect(t.HostPort).To(Equal(hostport))
		},
		Entry("https with a port", "https://Home.Example.org:8123/", "https://home.example.org:8123", "home.example.org:8123"),
		Entry("https default port", "https://ha.example.org", "https://ha.example.org:443", "ha.example.org:443"),
		Entry("a path prefix is kept", "https://ha.example.org/ha/", "https://ha.example.org:443/ha", "ha.example.org:443"),
		Entry("http on a private address", "http://192.168.1.20:8123", "http://192.168.1.20:8123", "192.168.1.20:8123"),
		Entry("http on a loopback address", "http://127.0.0.1:8123", "http://127.0.0.1:8123", "127.0.0.1:8123"),
		Entry("http on a single label", "http://homeassistant:8123", "http://homeassistant:8123", "homeassistant:8123"),
		Entry("http on a .local name", "http://homeassistant.local:8123", "http://homeassistant.local:8123", "homeassistant.local:8123"),
	)
	DescribeTable("refused",
		func(raw string) {
			_, err := haapi.ParseBase(raw)
			Expect(err).To(MatchError(haapi.ErrBadAddress))
		},
		Entry("empty", ""),
		Entry("no scheme", "ha.example.org:8123"),
		Entry("ftp", "ftp://ha.example.org"),
		Entry("javascript", "javascript:alert(1)"),
		Entry("credentials in the address", "https://user:pass@ha.example.org"),
		Entry("a query", "https://ha.example.org/?x=1"),
		Entry("a fragment", "https://ha.example.org/#x"),
		Entry("http on a public name", "http://ha.example.org:8123"),
		Entry("http on a public address", "http://8.8.8.8:8123"),
		Entry("link-local metadata address", "https://169.254.169.254"),
		Entry("unspecified address", "http://0.0.0.0:8123"),
		Entry("parent path", "https://ha.example.org/../x"),
	)
})

var _ = Describe("a call", func() {
	var (
		srv *hatest.Server
		cl  *haapi.Client
		tg  haapi.Target
		ctx = context.Background()
	)
	BeforeEach(func() {
		srv = hatest.New(token)
		DeferCleanup(srv.Close)
		cl = haapi.New()
		var err error
		tg, err = haapi.ParseBase(srv.URL)
		Expect(err).NotTo(HaveOccurred())
	})

	It("sends the token in the Authorization header only", func() {
		var cfg map[string]any
		Expect(cl.GetJSON(ctx, tg, token, "/api/config", nil, &cfg)).To(Succeed())
		c := srv.Calls()[0]
		Expect(c.Token).To(Equal(token))
		Expect(c.Path + "?" + c.Query).NotTo(ContainSubstring(token))
		Expect(c.Body).NotTo(ContainSubstring(token))
	})

	It("maps the status codes without the token or the body in the error", func() {
		_, err := cl.Get(ctx, tg, "wrong-token", "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrUnauthorized))
		_, err = cl.Get(ctx, tg, token, "/api/states/light.none", nil)
		Expect(err).To(MatchError(haapi.ErrNotFound))
		srv.Fail("/api/config", 500)
		_, err = cl.Get(ctx, tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrUnavailable))
		srv.Fail("/api/config", 403)
		_, err = cl.Get(ctx, tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrForbidden))
		srv.Fail("/api/config", 429)
		_, err = cl.Get(ctx, tg, token, "/api/config", nil)
		Expect(err.Error()).To(ContainSubstring("wait"))
		for _, e := range []error{haapi.ErrUnauthorized, haapi.ErrUnavailable, haapi.ErrCertificate} {
			Expect(e.Error()).NotTo(ContainSubstring(token))
			Expect(haapi.Describe(e)).NotTo(ContainSubstring("127.0.0.1"))
		}
	})

	It("refuses a redirect, even to another host, and never sends the token there", func() {
		other := hatest.New(token)
		DeferCleanup(other.Close)
		srv.RedirectTo(other.URL + "/api/config")
		_, err := cl.Get(ctx, tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrRedirect))
		Expect(other.Calls()).To(BeEmpty())
	})

	It("refuses a path outside /api and an empty token", func() {
		_, err := cl.Get(ctx, tg, token, "/auth/token", nil)
		Expect(err).To(MatchError(haapi.ErrBadAddress))
		_, err = cl.Get(ctx, tg, "", "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrUnauthorized))
	})

	It("reports an answer above the limit", func() {
		big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			chunk := []byte(strings.Repeat("x", 1<<20))
			for range 17 {
				_, _ = w.Write(chunk)
			}
		}))
		DeferCleanup(big.Close)
		bt, _ := haapi.ParseBase(big.URL)
		_, err := cl.Get(ctx, bt, token, "/api/states", nil)
		Expect(err).To(MatchError(haapi.ErrTooLarge))
	})

	It("passes a cancelled context through", func() {
		c, cancel := context.WithCancel(ctx)
		cancel()
		_, err := cl.Get(c, tg, token, "/api/config", nil)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
	})
})

var _ = Describe("a self-signed certificate", func() {
	var srv *hatest.Server
	BeforeEach(func() {
		srv = hatest.NewTLS(token)
		DeferCleanup(srv.Close)
	})
	pinOf := func() string { return haapi.Fingerprint(srv.Certificate().Raw) }

	It("is refused without a pin, with a message that names the pin", func() {
		tg, _ := haapi.ParseBase(srv.URL)
		_, err := haapi.New().Get(context.Background(), tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrCertificate))
		Expect(srv.Calls()).To(BeEmpty(), "the token was not sent")
	})

	It("is accepted with the pin of its certificate", func() {
		tg, _ := haapi.ParseBase(srv.URL)
		cl := haapi.New()
		cl.Pins = func() map[string]string { return map[string]string{tg.HostPort: pinOf()} }
		var cfg map[string]any
		Expect(cl.GetJSON(context.Background(), tg, token, "/api/config", nil, &cfg)).To(Succeed())
	})

	It("is refused with the pin of another certificate, and the token is not sent", func() {
		tg, _ := haapi.ParseBase(srv.URL)
		cl := haapi.New()
		cl.Pins = func() map[string]string { return map[string]string{tg.HostPort: strings.Repeat("0", 64)} }
		_, err := cl.Get(context.Background(), tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrCertificate))
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("does not use the pin of one host for another", func() {
		tg, _ := haapi.ParseBase(srv.URL)
		cl := haapi.New()
		cl.Pins = func() map[string]string { return map[string]string{"other.example:443": pinOf()} }
		_, err := cl.Get(context.Background(), tg, token, "/api/config", nil)
		Expect(err).To(MatchError(haapi.ErrCertificate))
	})
})

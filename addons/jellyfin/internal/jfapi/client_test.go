// SPDX-License-Identifier: MIT
package jfapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/jellyfin/internal/jfapi"
	"github.com/mudler/LocalAI/addons/jellyfin/jellyfintest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJfapi(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Jellyfin add-on client")
}

const key = "KEY-SECRET-123"

func fingerprint(s *jellyfintest.Server) string {
	sum := sha256.Sum256(s.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

var _ = Describe("the server address", func() {
	DescribeTable("accepts",
		func(in, base, pin string) {
			s, err := jfapi.ParseServer(in)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Base.String()).To(Equal(base))
			Expect(s.Pin).To(Equal(pin))
		},
		Entry("a plain address", "http://192.0.2.10:8096", "http://192.0.2.10:8096", ""),
		Entry("a trailing slash", "https://media.example/", "https://media.example", ""),
		Entry("a path prefix", "https://media.example/jellyfin/", "https://media.example/jellyfin", ""),
		Entry("a pinned certificate", "https://media.example:8920#sha256="+strings.Repeat("AB", 32), "https://media.example:8920", strings.Repeat("ab", 32)),
		Entry("a pin written with colons", "https://m.example#sha256="+strings.Repeat("ab:", 31)+"ab", "https://m.example", strings.Repeat("ab", 32)),
	)
	DescribeTable("refuses",
		func(in string) {
			_, err := jfapi.ParseServer(in)
			Expect(err).To(HaveOccurred())
		},
		Entry("an empty address", ""),
		Entry("a file", "file:///etc/passwd"),
		Entry("ftp", "ftp://media.example"),
		Entry("no scheme", "media.example"),
		Entry("no host", "http://"),
		Entry("a user name and password", "http://u:p@media.example"),
		Entry("a query", "http://media.example/?api_key=x"),
		Entry("a fragment that is not a pin", "http://media.example#x"),
		Entry("a pin on plain http", "http://media.example#sha256="+strings.Repeat("ab", 32)),
		Entry("a short pin", "https://media.example#sha256=abcd"),
		Entry("a pin that is not hex", "https://media.example#sha256="+strings.Repeat("zz", 32)),
		Entry("a dot-dot path", "http://media.example/a/../b"),
		Entry("a backslash", `http://media.example\@evil`),
		Entry("a space", "http://media.example /x"),
	)
})

var _ = Describe("the client", func() {
	var c *jfapi.Client
	BeforeEach(func() { c = jfapi.New() })

	It("sends the key in the Authorization header and nowhere else", func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		srv.AddKey(key)
		s, _ := jfapi.ParseServer(srv.URL)
		var out []any
		Expect(c.GetJSON(context.Background(), s, key, []string{"Users"}, url.Values{"x": {"1"}}, &out)).To(Succeed())
		call := srv.Calls()[0]
		Expect(call.Auth).To(Equal(`MediaBrowser Token="` + key + `"`))
		Expect(call.Query).To(Equal("x=1"))
	})
	It("refuses a path segment that could change the route", func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		srv.AddKey(key)
		s, _ := jfapi.ParseServer(srv.URL)
		for _, seg := range []string{"", "..", ".", "a/b", "a?x=1", "a#b", `a\b`, "%2e%2e", "a\nb"} {
			_, err := c.Do(context.Background(), s, key, jfapi.Request{Path: []string{"Items", seg}})
			Expect(err).To(MatchError(jfapi.ErrInvalid), seg)
		}
		Expect(srv.Calls()).To(BeEmpty())
	})
	It("refuses a key that could break out of the header", func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		s, _ := jfapi.ParseServer(srv.URL)
		for _, k := range []string{"", `a"b`, "a b", "a\nb", `a\b`} {
			_, err := c.Do(context.Background(), s, k, jfapi.Request{Path: []string{"Users"}})
			Expect(err).To(MatchError(jfapi.ErrUnauthorized))
		}
		Expect(srv.Calls()).To(BeEmpty())
	})
	It("does not follow a redirect, even to the same host", func() {
		srv := jellyfintest.New()
		DeferCleanup(srv.Close)
		srv.AddKey(key)
		srv.RedirectTo(srv.URL + "/Users")
		s, _ := jfapi.ParseServer(srv.URL)
		_, err := c.Do(context.Background(), s, key, jfapi.Request{Path: []string{"Users"}})
		Expect(err).To(MatchError(jfapi.ErrRedirect))
		Expect(srv.Calls()).To(HaveLen(1))
	})
	It("keeps the error free of the key and of the address", func() {
		s, _ := jfapi.ParseServer("http://127.0.0.1:1")
		_, err := c.Do(context.Background(), s, key, jfapi.Request{Path: []string{"Users"}})
		Expect(err).To(MatchError(jfapi.ErrUnavailable))
		Expect(err.Error()).NotTo(ContainSubstring("127.0.0.1"))
		Expect(err.Error()).NotTo(ContainSubstring(key))
	})

	Describe("a self-signed certificate", func() {
		var srv *jellyfintest.Server
		BeforeEach(func() {
			srv = jellyfintest.NewTLS()
			DeferCleanup(srv.Close)
			srv.AddKey(key)
		})
		It("is refused without a pin, with a sentence that says how to pin it", func() {
			s, _ := jfapi.ParseServer(srv.URL)
			_, err := c.Do(context.Background(), s, key, jfapi.Request{Path: []string{"Users"}})
			Expect(err).To(MatchError(jfapi.ErrCertificate))
			Expect(jfapi.Describe(err)).To(ContainSubstring("fingerprint"))
			Expect(srv.Calls()).To(BeEmpty())
		})
		It("is accepted with the right pin", func() {
			s, err := jfapi.ParseServer(srv.URL + "#sha256=" + fingerprint(srv))
			Expect(err).NotTo(HaveOccurred())
			var out []any
			Expect(c.GetJSON(context.Background(), s, key, []string{"Users"}, nil, &out)).To(Succeed())
		})
		It("is refused with the pin of another certificate, before any request is sent", func() {
			s, _ := jfapi.ParseServer(srv.URL + "#sha256=" + strings.Repeat("00", 32))
			_, err := c.Do(context.Background(), s, key, jfapi.Request{Path: []string{"Users"}})
			Expect(err).To(MatchError(jfapi.ErrPin))
			Expect(srv.Calls()).To(BeEmpty())
		})
	})
})

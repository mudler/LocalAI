// SPDX-License-Identifier: MIT
package kvmd_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/addons/pikvm/pikvmtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	user = "kvmuser"
	// The password holds colons: only the first colon splits the credential.
	pass = "pa:ss:wo:rd-SECRET-0123"
	cred = user + ":" + pass
)

func fingerprint(s *httptest.Server) string {
	sum := sha256.Sum256(s.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

var _ = Describe("the address of a connection", func() {
	DescribeTable("accepted",
		func(raw, key, pin string) {
			d, err := kvmd.ParseDevice(raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(d.Key()).To(Equal(key))
			Expect(d.Pin).To(Equal(pin))
		},
		Entry("https", "https://Kvm.Example.org/", "https://kvm.example.org", ""),
		Entry("https with a port and path", "https://kvm.example.org:8443/a/", "https://kvm.example.org:8443/a", ""),
		Entry("http on a private address", "http://192.168.1.50", "http://192.168.1.50", ""),
		Entry("http on a .local name", "http://pikvm.local", "http://pikvm.local", ""),
		Entry("a pinned fingerprint", "https://pikvm.local#sha256="+hex64("ab"), "https://pikvm.local", hex64("ab")),
		Entry("a fingerprint with colons", "https://pikvm.local#sha256=AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB:AB", "https://pikvm.local", hex64("ab")),
	)
	DescribeTable("refused",
		func(raw string) {
			_, err := kvmd.ParseDevice(raw)
			Expect(err).To(MatchError(kvmd.ErrBadAddress))
		},
		Entry("empty", ""),
		Entry("no scheme", "pikvm.local"),
		Entry("ftp", "ftp://pikvm.local"),
		Entry("user information", "https://u:p@pikvm.local"),
		Entry("a query", "https://pikvm.local/?x=1"),
		Entry("http on a public name", "http://kvm.example.org"),
		Entry("http on a public address", "http://8.8.8.8"),
		Entry("link-local metadata address", "https://169.254.169.254"),
		Entry("another fragment", "https://pikvm.local#x"),
		Entry("a short fingerprint", "https://pikvm.local#sha256=abcd"),
		Entry("a fingerprint on http", "http://pikvm.local#sha256="+hex64("ab")),
		Entry("a parent path", "https://pikvm.local/../x"),
	)
})

func hex64(b string) string {
	out := ""
	for range 32 {
		out += b
	}
	return out
}

var _ = Describe("the credential", func() {
	It("splits at the first colon", func() {
		u, p, err := kvmd.SplitCredential(cred)
		Expect(err).NotTo(HaveOccurred())
		Expect(u).To(Equal(user))
		Expect(p).To(Equal(pass))
	})
	DescribeTable("refused", func(c string) {
		_, _, err := kvmd.SplitCredential(c)
		Expect(err).To(MatchError(kvmd.ErrBadCredential))
	}, Entry("no colon", "justonestring"), Entry("no user", ":pass"), Entry("no password", "user:"), Entry("a newline", "user:pa\nss"))
})

var _ = Describe("a call", func() {
	var (
		srv *pikvmtest.Server
		cl  = kvmd.New()
		ctx = context.Background()
	)
	AfterEach(func() { srv.Close() })

	get := func(d kvmd.Device, c string) error {
		return cl.GetJSON(ctx, d, c, []string{"api", "atx"}, nil, &map[string]any{})
	}

	It("sends the user and the password in the two headers and in nothing else", func() {
		srv = pikvmtest.New(user, pass)
		d, _ := kvmd.ParseDevice(srv.URL)
		Expect(get(d, cred)).To(Succeed())
		c := srv.Calls()[0]
		Expect(c.User).To(Equal(user))
		Expect(c.Passwd).To(Equal(pass))
		Expect(c.Query).To(BeEmpty())
		Expect(c.Path).To(Equal("/api/atx"))
	})

	It("reports a wrong password with a fixed sentence that holds no credential", func() {
		srv = pikvmtest.New(user, pass)
		d, _ := kvmd.ParseDevice(srv.URL)
		err := get(d, user+":wrong-password-XYZ")
		Expect(err).To(MatchError(kvmd.ErrUnauthorized))
		Expect(kvmd.Describe(err)).NotTo(ContainSubstring("wrong-password"))
		Expect(kvmd.Describe(err)).To(ContainSubstring("one-time code is not supported"))
	})

	It("refuses a redirect and never follows it", func() {
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Fail("the redirect was followed with " + r.Header.Get("X-KVMD-Passwd"))
		}))
		defer other.Close()
		srv = pikvmtest.New(user, pass)
		srv.RedirectTo(other.URL)
		d, _ := kvmd.ParseDevice(srv.URL)
		Expect(get(d, cred)).To(MatchError(kvmd.ErrRedirect))
	})

	It("maps a busy device and a server error", func() {
		srv = pikvmtest.New(user, pass)
		d, _ := kvmd.ParseDevice(srv.URL)
		srv.SetATX(true, true)
		_, err := cl.Do(ctx, d, cred, kvmd.Request{Method: http.MethodPost, Path: []string{"api", "atx", "power"}, Query: url.Values{"action": {"on"}}})
		Expect(err).To(MatchError(kvmd.ErrBusy))
		srv.Fail("/api/atx", 500)
		Expect(get(d, cred)).To(MatchError(kvmd.ErrUnavailable))
	})

	Describe("over TLS with a self-signed certificate", func() {
		BeforeEach(func() { srv = pikvmtest.NewTLS(user, pass) })

		It("is refused without a pin and never verified away", func() {
			d, _ := kvmd.ParseDevice(srv.URL)
			Expect(get(d, cred)).To(MatchError(kvmd.ErrCertificate))
			Expect(srv.Calls()).To(BeEmpty())
		})
		It("is accepted with the pinned fingerprint", func() {
			d, err := kvmd.ParseDevice(srv.URL + "#sha256=" + fingerprint(srv.Server))
			Expect(err).NotTo(HaveOccurred())
			Expect(get(d, cred)).To(Succeed())
		})
		It("is refused with another fingerprint, and the credential is not sent", func() {
			d, _ := kvmd.ParseDevice(srv.URL + "#sha256=" + hex64("cd"))
			Expect(get(d, cred)).To(MatchError(kvmd.ErrPin))
			Expect(srv.Calls()).To(BeEmpty())
		})
	})
})

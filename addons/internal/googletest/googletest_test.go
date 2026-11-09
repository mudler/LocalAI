// SPDX-License-Identifier: MIT
package googletest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/googletest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGoogletest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Googletest suite")
}

type fake struct{ googletest.Core }

var _ = Describe("Core", func() {
	var (
		f   *fake
		srv *httptest.Server
	)
	BeforeEach(func() {
		f = &fake{}
		f.Init(func(m string) bool { return m == http.MethodPost })
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.Serve(w, r, func(w http.ResponseWriter, _ *http.Request, p *googletest.Principal) {
				googletest.Reply(w, http.StatusOK, map[string]string{"who": p.Email})
			})
		}))
		DeferCleanup(srv.Close)
	})

	do := func(method, token string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+"/x", strings.NewReader("body"))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	It("answers a known token, refuses an unknown one and records both", func() {
		f.AddToken("good", "a@example.test", "scope")
		code, body := do(http.MethodGet, "good")
		Expect(code).To(Equal(200))
		Expect(body).To(ContainSubstring("a@example.test"))
		code, _ = do(http.MethodGet, "bad")
		Expect(code).To(Equal(401))
		Expect(f.Calls()).To(HaveLen(2))
		Expect(f.Calls()[0].Body).To(Equal("body"))
	})

	It("accepts what the authenticator accepts and not a revoked token", func() {
		f.SetAuthenticator(func(t string) (string, []string, bool) { return "x@example.test", nil, t == "dynamic" })
		code, _ := do(http.MethodGet, "dynamic")
		Expect(code).To(Equal(200))
		f.AddToken("t", "a@example.test")
		f.RevokeToken("t")
		code, _ = do(http.MethodGet, "t")
		Expect(code).To(Equal(401))
	})

	It("injects a failure a number of times, for one method", func() {
		f.AddToken("t", "a@example.test")
		f.InjectMethod(http.MethodPost, "/x", http.StatusForbidden, "forbidden", 1)
		code, _ := do(http.MethodGet, "t")
		Expect(code).To(Equal(200), "a read is not touched")
		code, _ = do(http.MethodPost, "t")
		Expect(code).To(Equal(403))
		code, _ = do(http.MethodPost, "t")
		Expect(code).To(Equal(200), "the failure was used up")
	})

	It("closes the connection after a change when writes are dropped", func() {
		f.AddToken("t", "a@example.test")
		f.DropWrites(true)
		code, _ := do(http.MethodPost, "t")
		Expect(code).To(BeZero())
		code, _ = do(http.MethodGet, "t")
		Expect(code).To(Equal(200))
	})

	It("forgets everything on reset", func() {
		f.AddToken("t", "a@example.test")
		f.Inject("", 500, "x", "", 0)
		f.Mu.Lock()
		f.ResetCore()
		f.Mu.Unlock()
		code, _ := do(http.MethodGet, "t")
		Expect(code).To(Equal(401))
		Expect(f.Calls()).To(HaveLen(1))
	})
})

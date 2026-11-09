// SPDX-License-Identifier: MIT
package gmailapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/addons/gmail/internal/gmailapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGmailAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gmail API client")
}

var _ = Describe("the client", func() {
	var (
		srv    *httptest.Server
		status int
		header http.Header
		body   string
		seen   *http.Request
		c      *gmailapi.Client
	)
	BeforeEach(func() {
		status, header, body, seen = 200, http.Header{}, `{"ok":true}`, nil
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r
			for k, v := range header {
				w.Header()[k] = v
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		DeferCleanup(srv.Close)
		c = gmailapi.New()
		c.Base = srv.URL
	})
	get := func() error {
		var v map[string]any
		return c.GetJSON(context.Background(), "tok", "/gmail/v1/users/me/messages", nil, &v)
	}

	It("sends the token only in the Authorization header", func() {
		Expect(get()).To(Succeed())
		Expect(seen.Header.Get("Authorization")).To(Equal("Bearer tok"))
		Expect(seen.URL.RawQuery).To(BeEmpty())
		Expect(c.GetJSON(context.Background(), "", "/x", nil, new(any))).To(MatchError(gmailapi.ErrUnauthorized), "no token, no call")
	})

	DescribeTable("maps an answer to an error that holds no text of the answer", func(code int, reason, retry string, check func(error) bool) {
		status, body = code, `{"error":{"errors":[{"reason":"`+reason+`","message":"file secret-name.txt"}],"details":[{"reason":"`+reason+`"}]}}`
		if retry != "" {
			header.Set("Retry-After", retry)
		}
		err := get()
		Expect(err).To(HaveOccurred())
		Expect(check(err)).To(BeTrue(), "%v", err)
		Expect(err.Error()).NotTo(ContainSubstring("secret-name"))
		Expect(gmailapi.Describe(err)).NotTo(ContainSubstring("secret-name"))
	},
		Entry("401", 401, "authError", "", func(e error) bool { return errors.Is(e, gmailapi.ErrUnauthorized) }),
		Entry("403 forbidden", 403, "forbidden", "", func(e error) bool { return errors.Is(e, gmailapi.ErrForbidden) }),
		Entry("403 scope", 403, "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "", func(e error) bool { return errors.Is(e, gmailapi.ErrScope) }),
		Entry("403 insufficient permissions", 403, "insufficientPermissions", "", func(e error) bool { return errors.Is(e, gmailapi.ErrScope) }),
		Entry("404", 404, "notFound", "", func(e error) bool { return errors.Is(e, gmailapi.ErrNotFound) }),
		Entry("410", 410, "gone", "", func(e error) bool { return errors.Is(e, gmailapi.ErrNotFound) }),
		Entry("400", 400, "badRequest", "", func(e error) bool { return errors.Is(e, gmailapi.ErrInvalid) }),
		Entry("403 rate limit with a wait", 403, "rateLimitExceeded", "4", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 4
		}),
		Entry("403 userRateLimitExceeded", 403, "userRateLimitExceeded", "", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
		Entry("429", 429, "", "", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
		Entry("503 with a wait", 503, "backendError", "6", func(e error) bool {
			var s *gmailapi.ServerError
			return errors.As(e, &s) && s.RetryAfter == 6
		}),
		Entry("500", 500, "", "", func(e error) bool {
			var s *gmailapi.ServerError
			return errors.As(e, &s) && s.RetryAfter == 5
		}),
		Entry("a wait that is not a number", 429, "", "tomorrow", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
	)

	It("refuses an answer above the bound and an answer it cannot read", func() {
		body = `{"a":"` + string(make([]byte, 100)) + `"}`
		_, err := c.Do(context.Background(), "tok", gmailapi.Request{Path: "/x"}, 10)
		Expect(err).To(MatchError(gmailapi.ErrTooLarge))
		body = "not json"
		Expect(get()).To(MatchError(gmailapi.ErrDecode))
	})

	It("reports a change whose answer cannot be read apart from one that failed", func() {
		body = "not json"
		err := c.SendJSON(context.Background(), "PATCH", "tok", "/x", nil, map[string]any{"a": 1}, new(map[string]any))
		Expect(err).To(MatchError(gmailapi.ErrUnreadable))
		Expect(seen.Header.Get("Content-Type")).To(Equal("application/json"))
	})

	It("reports an unreachable service as such, and a cancelled call as cancelled", func() {
		srv.Close()
		Expect(get()).To(MatchError(gmailapi.ErrUnavailable))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(c.GetJSON(ctx, "tok", "/x", nil, new(any))).To(MatchError(context.Canceled))
	})

	It("does not follow a redirect", func() {
		status, header = 302, http.Header{"Location": {"https://elsewhere.example.test/"}}
		Expect(get()).To(HaveOccurred())
	})
})

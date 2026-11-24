// SPDX-License-Identifier: MIT
package driveapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDriveAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Google Drive API client")
}

var _ = Describe("the client", func() {
	var (
		srv    *httptest.Server
		status int
		header http.Header
		body   string
		seen   *http.Request
		c      *driveapi.Client
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
		c = driveapi.New()
		c.Base = srv.URL
	})
	get := func() error {
		var v map[string]any
		return c.GetJSON(context.Background(), "tok", "/drive/v3/files", nil, &v)
	}

	It("sends the token only in the Authorization header", func() {
		Expect(get()).To(Succeed())
		Expect(seen.Header.Get("Authorization")).To(Equal("Bearer tok"))
		Expect(seen.URL.RawQuery).To(BeEmpty())
		Expect(c.GetJSON(context.Background(), "", "/x", nil, new(any))).To(MatchError(driveapi.ErrUnauthorized), "no token, no call")
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
		Expect(driveapi.Describe(err)).NotTo(ContainSubstring("secret-name"))
	},
		Entry("401", 401, "authError", "", func(e error) bool { return errors.Is(e, driveapi.ErrUnauthorized) }),
		Entry("403 forbidden", 403, "forbidden", "", func(e error) bool { return errors.Is(e, driveapi.ErrForbidden) }),
		Entry("403 scope", 403, "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "", func(e error) bool { return errors.Is(e, driveapi.ErrScope) }),
		Entry("403 insufficient permissions", 403, "insufficientPermissions", "", func(e error) bool { return errors.Is(e, driveapi.ErrScope) }),
		Entry("404", 404, "notFound", "", func(e error) bool { return errors.Is(e, driveapi.ErrNotFound) }),
		Entry("410", 410, "gone", "", func(e error) bool { return errors.Is(e, driveapi.ErrNotFound) }),
		Entry("416", 416, "range", "", func(e error) bool { return errors.Is(e, driveapi.ErrRange) }),
		Entry("400", 400, "badRequest", "", func(e error) bool { return errors.Is(e, driveapi.ErrInvalid) }),
		Entry("403 rate limit with a wait", 403, "rateLimitExceeded", "4", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 4
		}),
		Entry("403 user rate limit", 403, "userRateLimitExceeded", "", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
		Entry("429", 429, "", "", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
		Entry("503 with a wait", 503, "backendError", "6", func(e error) bool {
			var s *driveapi.ServerError
			return errors.As(e, &s) && s.RetryAfter == 6
		}),
		Entry("500", 500, "", "", func(e error) bool {
			var s *driveapi.ServerError
			return errors.As(e, &s) && s.RetryAfter == 5
		}),
		Entry("a wait that is not a number", 429, "", "tomorrow", func(e error) bool {
			var l *contract.ErrRateLimited
			return errors.As(e, &l) && l.Seconds() == 30
		}),
	)

	It("refuses an answer above the bound and an answer it cannot read", func() {
		body = `{"a":"` + string(make([]byte, 100)) + `"}`
		_, err := c.Do(context.Background(), "tok", driveapi.Request{Path: "/x"}, 10)
		Expect(err).To(MatchError(driveapi.ErrTooLarge))
		body = "not json"
		Expect(get()).To(MatchError(driveapi.ErrDecode))
	})

	It("reports a change whose answer cannot be read apart from one that failed", func() {
		body = "not json"
		err := c.SendJSON(context.Background(), "PATCH", "tok", "/x", nil, map[string]any{"a": 1}, new(map[string]any))
		Expect(err).To(MatchError(driveapi.ErrUnreadable))
		Expect(seen.Header.Get("Content-Type")).To(Equal("application/json"))
	})

	It("reports an unreachable service as such, and a cancelled call as cancelled", func() {
		srv.Close()
		Expect(get()).To(MatchError(driveapi.ErrUnavailable))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(c.GetJSON(ctx, "tok", "/x", nil, new(any))).To(MatchError(context.Canceled))
	})

	It("opens a stream and maps a failure of the open", func() {
		body = "streamed"
		rc, err := c.Open(context.Background(), "tok", driveapi.Request{Path: "/x"})
		Expect(err).NotTo(HaveOccurred())
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		Expect(string(data)).To(Equal("streamed"))
		status = 401
		_, err = c.Open(context.Background(), "tok", driveapi.Request{Path: "/x"})
		Expect(err).To(MatchError(driveapi.ErrUnauthorized))
	})

	It("does not follow a redirect", func() {
		status, header = 302, http.Header{"Location": {"https://elsewhere.example.test/"}}
		Expect(get()).To(HaveOccurred())
	})
})

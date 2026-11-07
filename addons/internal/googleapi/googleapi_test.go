// SPDX-License-Identifier: MIT
package googleapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/LocalAI/addons/internal/googleapi"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGoogleapi(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Google API client suite")
}

var errTeapot = errors.New("teapot")

var _ = Describe("Client", func() {
	var (
		status int
		header http.Header
		c      *googleapi.Client
	)

	BeforeEach(func() {
		status, header = http.StatusOK, http.Header{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range header {
				w.Header()[k] = v
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`))
		}))
		DeferCleanup(srv.Close)
		c = googleapi.New(srv.URL, "test-agent")
		c.StatusErrors = map[int]error{http.StatusTeapot: errTeapot}
	})

	get := func() error {
		_, err := c.Do(context.Background(), "tok", googleapi.Request{Path: "/x"}, 1<<10)
		return err
	}

	It("gives a status of the service its own error", func() {
		status = http.StatusTeapot
		Expect(get()).To(MatchError(errTeapot))
	})

	It("reads a rate limit reason from a 403 and the wait from the header", func() {
		status = http.StatusForbidden
		header.Set("Retry-After", "7")
		var limited *contract.ErrRateLimited
		Expect(errors.As(get(), &limited)).To(BeTrue())
		Expect(limited.Seconds()).To(Equal(7))
	})

	It("reports a server failure with its wait", func() {
		status = http.StatusBadGateway
		var server *googleapi.ServerError
		Expect(errors.As(get(), &server)).To(BeTrue())
		Expect(server.RetryAfter).To(Equal(5))
	})

	It("words a refusal with the texts of the service and falls back to the neutral one", func() {
		texts := googleapi.Texts{googleapi.ErrNotFound: "no such mail", errTeapot: "short and stout"}
		Expect(googleapi.Describe(googleapi.ErrNotFound, texts)).To(Equal("no such mail"))
		Expect(googleapi.Describe(googleapi.ErrScope, texts)).To(Equal(googleapi.ErrScope.Error()))
		Expect(googleapi.Describe(errTeapot, texts)).To(Equal("short and stout"))
		Expect(googleapi.Describe(errors.New("dial tcp"), nil)).To(Equal(googleapi.ErrUnavailable.Error()))
	})

	It("refuses an empty token without a call", func() {
		_, err := c.Do(context.Background(), "", googleapi.Request{Path: "/x"}, 1)
		Expect(err).To(MatchError(googleapi.ErrUnauthorized))
	})
})

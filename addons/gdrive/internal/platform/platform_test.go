// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/gdrive/gdrivetest"
	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/gdrive/internal/platform"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlatform(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Google Drive add-on connector")
}

var _ = Describe("the connector", func() {
	var (
		srv *gdrivetest.Server
		p   platform.Platform
	)
	BeforeEach(func() {
		srv = gdrivetest.New()
		DeferCleanup(srv.Close)
		srv.AddToken("tok", "person@example.test", gdrivetest.ScopeReadonly)
		api := driveapi.New()
		api.Base = srv.URL
		p = platform.Platform{API: api}
	})

	It("names the connection by the account of the token", func() {
		bot, err := p.Me(context.Background(), "tok")
		Expect(err).NotTo(HaveOccurred())
		Expect(bot.Username).To(Equal("person@example.test"))
		Expect(bot.ID).NotTo(BeEmpty())
	})

	It("reports a refused token as unauthorized", func() {
		_, err := p.Me(context.Background(), "other")
		Expect(err).To(MatchError(connector.ErrUnauthorized))
	})

	It("passes the wait of a limit on", func() {
		srv.Inject("/drive/v3/about", 429, "rateLimitExceeded", "8", 1)
		_, err := p.Me(context.Background(), "tok")
		var ra *connector.RetryAfter
		Expect(err).To(HaveOccurred())
		Expect(err).To(BeAssignableToTypeOf(ra))
		Expect(err.(*connector.RetryAfter).Seconds).To(Equal(8))
	})

	It("carries no messages and does not spin", func() {
		Expect(p.Kind()).To(Equal("gdrive"))
		msgs, err := p.Poll(context.Background(), "tok", 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err = p.Poll(ctx, "tok", 0, 5)
		Expect(err).To(MatchError(context.DeadlineExceeded))
		_, err = p.Send(context.Background(), contract.ConnectorRequest{})
		Expect(err).To(MatchError(platform.ErrNoMessages))
	})
})

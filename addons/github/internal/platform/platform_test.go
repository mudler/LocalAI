// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/github/githubtest"
	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
	"github.com/mudler/LocalAI/addons/github/internal/platform"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlatform(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GitHub add-on connector route")
}

var _ = Describe("the connector route", func() {
	var (
		srv *githubtest.Server
		p   platform.Platform
	)
	BeforeEach(func() {
		srv = githubtest.New()
		DeferCleanup(srv.Close)
		srv.AddToken("good", "octo")
		p = platform.Platform{API: &ghapi.Client{Base: srv.URL, HTTP: srv.Client(), Now: time.Now}}
	})
	serve := func(token, op string) contract.ConnectorResponse {
		out, err := connector.Serve(context.Background(), p, contract.ConnectorRequest{Op: op, Kind: "github", Token: token})
		Expect(err).NotTo(HaveOccurred())
		return out
	}

	It("answers me with the login of the token", func() {
		out := serve("good", contract.ConnectorOpMe)
		Expect(out.BotUsername).To(Equal("octo"))
		Expect(out.BotID).To(Equal("4242"))
	})
	It("types a refused token as unauthorized", func() {
		out := serve("bad", contract.ConnectorOpMe)
		Expect(out.Error).NotTo(BeNil())
		Expect(out.Error.Code).To(Equal(contract.ConnectorUnauthorized))
	})
	It("types a rate limit with its wait", func() {
		srv.LimitSecondary(33)
		out := serve("good", contract.ConnectorOpMe)
		Expect(out.Error.Code).To(Equal(contract.ConnectorRateLimited))
		Expect(out.Error.RetryAfter).To(Equal(33))
	})
	It("polls without messages and sends nothing", func() {
		out := serve("good", contract.ConnectorOpPoll)
		Expect(out.Updates).To(BeEmpty())
		out, err := connector.Serve(context.Background(), p, contract.ConnectorRequest{Op: contract.ConnectorOpSend, Kind: "github", Token: "good", Chat: "C1", Text: "hi"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).NotTo(BeNil())
	})
})

// SPDX-License-Identifier: MIT
package platform_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/platform"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the connector route", func() {
	p := platform.Platform{}
	ctx := context.Background()

	It("serves the one kind and carries no messages", func() {
		Expect(p.Kind()).To(Equal("home-assistant"))
		_, err := p.Send(ctx, contract.ConnectorRequest{})
		Expect(err).To(MatchError(platform.ErrNoMessages))
		Expect(p.Edit(ctx, contract.ConnectorRequest{})).To(MatchError(platform.ErrNoMessages))
	})

	It("answers a poll after the wait and returns nothing", func() {
		msgs, err := p.Poll(ctx, "t", 0, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
		c, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err = p.Poll(c, "t", 0, 30)
		Expect(err).To(HaveOccurred())
	})

	It("accepts a token that is present and refuses an empty one", func() {
		b, err := p.Me(ctx, "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(b.ID).NotTo(BeEmpty())
		_, err = p.Me(ctx, "")
		Expect(err).To(MatchError(connector.ErrUnauthorized))
	})
})

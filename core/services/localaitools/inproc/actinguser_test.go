package inproc

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/routing/billing"
)

var _ = Describe("inproc.Client acting user", func() {
	var (
		c     *Client
		admin = &auth.User{ID: "u-admin", Name: "Ada", Role: auth.RoleAdmin}
		bob   = &auth.User{ID: "u-bob", Name: "Bob", Role: auth.RoleUser}
		fall  = &auth.User{ID: "u-fallback", Name: "Local", Role: auth.RoleAdmin}
	)

	BeforeEach(func() {
		rec := billing.NewRecorder(billing.NewMemoryBackend(100))
		for _, u := range []*auth.User{admin, bob} {
			Expect(rec.Record(context.Background(), &auth.UsageRecord{
				UserID: u.ID, UserName: u.Name, Source: auth.UsageSourceWeb, Model: "m", Endpoint: "/v1/chat/completions",
				PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2, CreatedAt: time.Now(),
			})).To(Succeed())
		}
		c = &Client{StatsRecorder: rec, FallbackUser: fall, SingleUser: true}
	})

	It("answers for the user of the context, and two users get different results", func() {
		a, err := c.GetUsageStats(localaitools.WithUser(context.Background(), admin), localaitools.UsageStatsQuery{})
		Expect(err).ToNot(HaveOccurred())
		b, err := c.GetUsageStats(localaitools.WithUser(context.Background(), bob), localaitools.UsageStatsQuery{})
		Expect(err).ToNot(HaveOccurred())
		Expect(a.Viewer.ID).To(Equal("u-admin"))
		Expect(b.Viewer.ID).To(Equal("u-bob"))
		Expect(b.Viewer.Role).To(Equal(auth.RoleUser))
	})

	It("lets an administrator read everyone and refuses an ordinary user", func() {
		all, err := c.GetUsageStats(localaitools.WithUser(context.Background(), admin), localaitools.UsageStatsQuery{All: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(all.Totals.RequestCount).To(BeEquivalentTo(2))

		_, err = c.GetUsageStats(localaitools.WithUser(context.Background(), bob), localaitools.UsageStatsQuery{All: true})
		Expect(err).To(HaveOccurred())
		_, err = c.GetUsageStats(localaitools.WithUser(context.Background(), bob), localaitools.UsageStatsQuery{UserID: admin.ID})
		Expect(err).To(HaveOccurred())
	})

	It("falls back to FallbackUser when the context has no user", func() {
		s, err := c.GetUsageStats(context.Background(), localaitools.UsageStatsQuery{})
		Expect(err).ToNot(HaveOccurred())
		Expect(s.Viewer.ID).To(Equal("u-fallback"))
	})

	It("never falls back to FallbackUser when authentication is on", func() {
		c.SingleUser = false
		_, err := c.GetUsageStats(context.Background(), localaitools.UsageStatsQuery{})
		Expect(err).To(HaveOccurred())
		_, err = c.requireActingUser(context.Background())
		Expect(err).To(MatchError(ContainSubstring("no acting user")))
		// The user of the context still wins.
		u, err := c.requireActingUser(localaitools.WithUser(context.Background(), bob))
		Expect(err).ToNot(HaveOccurred())
		Expect(u.ID).To(Equal("u-bob"))
	})

	It("does not leak a user from one call to the next", func() {
		_, err := c.GetUsageStats(localaitools.WithUser(context.Background(), bob), localaitools.UsageStatsQuery{})
		Expect(err).ToNot(HaveOccurred())
		s, err := c.GetUsageStats(context.Background(), localaitools.UsageStatsQuery{})
		Expect(err).ToNot(HaveOccurred())
		Expect(s.Viewer.ID).To(Equal("u-fallback"))

		c.FallbackUser = nil
		_, err = c.GetUsageStats(context.Background(), localaitools.UsageStatsQuery{})
		Expect(err).To(HaveOccurred())
	})
})

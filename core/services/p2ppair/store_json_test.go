package p2ppair_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
)

var _ = Describe("the JSON store", func() {
	var s p2ppair.Store
	ctx := context.Background()

	BeforeEach(func() {
		var err error
		s, err = p2ppair.NewJSONStore(GinkgoT().TempDir(), "secret")
		Expect(err).NotTo(HaveOccurred())
	})

	It("forgets invites that ended long ago when it stores a new one", func() {
		Expect(s.CreateInvite(ctx, invite("old", time.Hour), "AAAAA22222")).To(Succeed())
		fresh := invite("fresh", time.Hour)
		fresh.CreatedAt = t0.Add(45 * 24 * time.Hour)
		fresh.ExpiresAt = fresh.CreatedAt.Add(time.Hour)
		Expect(s.CreateInvite(ctx, fresh, "BBBBB33333")).To(Succeed())
		_, err := s.GetInvite(ctx, "old")
		Expect(err).To(MatchError(p2ppair.ErrNotFound))
		_, err = s.GetInvite(ctx, "fresh")
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps a recent invite that is no longer pending", func() {
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), "AAAAA22222")).To(Succeed())
		b := invite("b", time.Hour)
		b.CreatedAt = t0.Add(24 * time.Hour)
		Expect(s.CreateInvite(ctx, b, "BBBBB33333")).To(Succeed())
		_, err := s.GetInvite(ctx, "a")
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not rewrite the file for a device seen again within a minute", func() {
		p := newPeer()
		Expect(s.CreateInvite(ctx, invite("a", time.Hour), testCode)).To(Succeed())
		rp := redeemParams(testCode, "d1")
		rp.Peer = p
		_, _, err := s.Redeem(ctx, rp)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.TouchDevice(ctx, p, t0.Add(time.Hour), "relay")).To(Succeed())
		Expect(s.TouchDevice(ctx, p, t0.Add(time.Hour+10*time.Second), "relay")).To(Succeed())
		d, _ := s.GetDevice(ctx, "d1")
		Expect(d.LastSeenAt.Equal(t0.Add(time.Hour))).To(BeTrue())
		Expect(s.TouchDevice(ctx, p, t0.Add(time.Hour+2*time.Minute), "relay")).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.LastSeenAt.Equal(t0.Add(time.Hour + 2*time.Minute))).To(BeTrue())
		Expect(s.TouchDevice(ctx, p, t0.Add(time.Hour+2*time.Minute+time.Second), "direct")).To(Succeed())
		d, _ = s.GetDevice(ctx, "d1")
		Expect(d.LastPath).To(Equal("direct"))
	})
})

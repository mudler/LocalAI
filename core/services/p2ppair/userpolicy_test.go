package p2ppair_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
)

var _ = Describe("user pairing policy", func() {
	ctx := context.Background()

	It("has the defaults of the design", func() {
		p := p2ppair.DefaultUserPairingPolicy()
		Expect(p).To(Equal(p2ppair.UserPairingPolicy{Relays: p2ppair.RelayAuto, MaxDevices: 5, MaxPending: 2, MaxTTLSeconds: 3600}))
		Expect(p.Validate()).To(Succeed())
	})

	It("reads the defaults from a database that has none, and keeps what was saved", func() {
		db := openSQLite()
		Expect(db.AutoMigrate(p2ppair.Models()...)).To(Succeed())
		p, err := p2ppair.LoadUserPairingPolicy(ctx, db)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(p2ppair.DefaultUserPairingPolicy()))

		want := p2ppair.UserPairingPolicy{Relays: p2ppair.RelayNone, AllowOpenRelay: true, OmitLANAddrs: true, MaxDevices: 3, MaxPending: 1, MaxTTLSeconds: 600}
		Expect(p2ppair.SaveUserPairingPolicy(ctx, db, want, "root")).To(Succeed())
		p, err = p2ppair.LoadUserPairingPolicy(ctx, db)
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(want))

		By("replacing the stored policy on a second save")
		want.MaxDevices = 7
		Expect(p2ppair.SaveUserPairingPolicy(ctx, db, want, "root")).To(Succeed())
		p, err = p2ppair.LoadUserPairingPolicy(ctx, db)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.MaxDevices).To(Equal(7))
	})

	DescribeTable("refuses values out of range",
		func(change func(*p2ppair.UserPairingPolicy)) {
			p := p2ppair.DefaultUserPairingPolicy()
			change(&p)
			Expect(p.Validate()).To(MatchError(p2ppair.ErrMalformed))
			Expect(p2ppair.SaveUserPairingPolicy(ctx, openSQLite(), p, "root")).To(MatchError(p2ppair.ErrMalformed))
		},
		Entry("an explicit relay mode", func(p *p2ppair.UserPairingPolicy) { p.Relays = p2ppair.RelayExplicit }),
		Entry("no devices", func(p *p2ppair.UserPairingPolicy) { p.MaxDevices = 0 }),
		Entry("too many devices", func(p *p2ppair.UserPairingPolicy) { p.MaxDevices = 101 }),
		Entry("no pending invites", func(p *p2ppair.UserPairingPolicy) { p.MaxPending = 0 }),
		Entry("more pending invites than the instance allows", func(p *p2ppair.UserPairingPolicy) { p.MaxPending = p2ppair.MaxUserPendingInstance + 1 }),
		Entry("a TTL under a minute", func(p *p2ppair.UserPairingPolicy) { p.MaxTTLSeconds = 59 }),
		Entry("a TTL above the one of admins", func(p *p2ppair.UserPairingPolicy) { p.MaxTTLSeconds = int(p2ppair.MaxTTL.Seconds()) + 1 }),
	)
})

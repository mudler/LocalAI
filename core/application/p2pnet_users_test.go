//go:build auth

package application

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }
func boolp(v bool) *bool    { return &v }

var _ = Describe("P2P pairing by users", func() {
	ctx := context.Background()

	var (
		a      *Application
		server *p2pRuntime
		svc    p2pService
	)
	BeforeEach(func() {
		a, server = shareServer()
		svc = p2pService{n: a.p2p()}
		signInUser(a, "alice", auth.RoleUser, true)
		signInUser(a, "bob", auth.RoleUser, true)
	})

	It("refuses an identity without a user account", func() {
		for _, id := range []string{"", "legacy-api-key"} {
			_, err := svc.MyPairing(ctx, id)
			Expect(err).To(MatchError(p2papi.ErrPairNeedsAccount))
			_, err = svc.MintMyInvite(ctx, id, p2papi.MyMintRequest{})
			Expect(err).To(MatchError(p2papi.ErrPairNeedsAccount))
			_, err = svc.ListMyDevices(ctx, id)
			Expect(err).To(MatchError(p2papi.ErrPairNeedsAccount))
		}
		By("and a disabled one")
		Expect(a.AuthDB().Model(&auth.User{}).Where("id = ?", "bob").Update("status", auth.StatusDisabled).Error).To(Succeed())
		_, err := svc.MintMyInvite(ctx, "bob", p2papi.MyMintRequest{})
		Expect(err).To(MatchError(p2papi.ErrPairNeedsAccount))
	})

	It("mints a sign-in device invite whose other fields come from the policy", func() {
		a.p2p().isLANAddr = func(p2pnet.Multiaddr) bool { return true }
		_, err := svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{Relays: strp("none"), OmitLANAddrs: boolp(true)})
		Expect(err).ToNot(HaveOccurred())

		m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{Label: "phone", TTLSeconds: 600})
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Kind).To(Equal("device"))
		Expect(m.QR).To(BeTrue())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		Expect(link.Addrs).To(BeEmpty(), "omit_lan_addrs of the policy")
		Expect(link.Relays).To(BeEmpty())

		inv, err := server.pair.GetInvite(ctx, m.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(inv.SignIn).To(BeTrue())
		Expect(inv.Kind).To(Equal(p2ppair.KindDevice))
		Expect(inv.OwnerID).To(Equal("alice"))
		Expect(inv.CreatedBy).To(Equal("alice"))
		Expect(inv.Scope).To(Equal(p2ppair.Scope{}))
		Expect(inv.OmitLANAddrs).To(BeTrue())
		Expect(inv.ExpiresAt.Sub(inv.CreatedAt)).To(BeNumerically("~", 600*time.Second, 2*time.Second))

		By("opening pairing at once")
		Expect(server.pending.Load()).To(BeEquivalentTo(1))

		By("listing it as a pending invite of alice only")
		invs, err := svc.ListMyInvites(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(invs).To(HaveLen(1))
		Expect(invs[0].ID).To(Equal(m.ID))
		invs, err = svc.ListMyInvites(ctx, "bob")
		Expect(err).ToNot(HaveOccurred())
		Expect(invs).To(BeEmpty())
	})

	It("caps the life of an invite", func() {
		_, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{TTLSeconds: 3601})
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		_, err = svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{TTLSeconds: -1})
		Expect(err).To(MatchError(p2papi.ErrInvalid))

		m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
		inv, err := server.pair.GetInvite(ctx, m.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(inv.ExpiresAt.Sub(inv.CreatedAt)).To(BeNumerically("~", p2ppair.DefaultTTL, 2*time.Second))

		By("using the cap as the default when it is shorter")
		_, err = svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{MaxTTLSeconds: intp(300)})
		Expect(err).ToNot(HaveOccurred())
		m, err = svc.MintMyInvite(ctx, "bob", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
		inv, err = server.pair.GetInvite(ctx, m.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(inv.ExpiresAt.Sub(inv.CreatedAt)).To(BeNumerically("~", 300*time.Second, 2*time.Second))
	})

	It("limits the pending invites of a user", func() {
		var ids []string
		for range 2 {
			m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
			Expect(err).ToNot(HaveOccurred())
			ids = append(ids, m.ID)
		}
		_, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).To(MatchError(p2papi.ErrLimitReached))
		_, err = svc.MintMyInvite(ctx, "bob", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred(), "the limit is per user")

		Expect(svc.CancelMyInvite(ctx, "alice", ids[0])).To(Succeed())
		_, err = svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
	})

	It("limits the mints of a user in an hour", func() {
		_, err := svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{MaxPending: intp(20)})
		Expect(err).ToNot(HaveOccurred())
		for range p2ppair.MaxUserMintsPerHour {
			m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(svc.CancelMyInvite(ctx, "alice", m.ID)).To(Succeed())
		}
		_, err = svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).To(MatchError(p2papi.ErrLimitReached))
		Expect(err.Error()).To(ContainSubstring("last hour"))
	})

	It("limits the pending sign-in invites of the whole instance", func() {
		for i := range p2ppair.MaxUserPendingInstance / 2 {
			id := fmt.Sprintf("user%d", i)
			signInUser(a, id, auth.RoleUser, true)
			for range 2 {
				_, err := svc.MintMyInvite(ctx, id, p2papi.MyMintRequest{})
				Expect(err).ToNot(HaveOccurred())
			}
		}
		_, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).To(MatchError(p2papi.ErrLimitReached))
		Expect(err.Error()).To(ContainSubstring("this server"))
	})

	It("limits the devices of a user when minting and when redeeming", func() {
		_, err := svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{MaxDevices: intp(1)})
		Expect(err).ToNot(HaveOccurred())
		first, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
		second, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())

		_, c1 := device(server)
		status, _ := redeemCode(c1, server, codeOf(first.Link))
		Expect(status).To(Equal(http.StatusOK))

		By("refusing the redeem of an invite minted before the limit was reached")
		_, c2 := device(server)
		status, out := redeemCode(c2, server, codeOf(second.Link))
		Expect(status).To(Equal(http.StatusConflict), fmt.Sprint(out))

		By("refusing a new mint")
		Expect(svc.CancelMyInvite(ctx, "alice", second.ID)).To(Succeed())
		_, err = svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).To(MatchError(p2papi.ErrLimitReached))
		Expect(err.Error()).To(ContainSubstring("devices"))

		st, err := svc.MyPairing(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Available).To(BeTrue())
		Expect(st.Devices).To(Equal(1))
		Expect(st.PendingInvites).To(Equal(0))
		Expect(st.Limits).To(Equal(p2papi.UserPairingLimits{MaxDevices: 1, MaxPending: 2, MaxTTLSeconds: 3600, MaxMintsPerHour: 10}))
	})

	It("answers 404 for the invites and devices of another user", func() {
		m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
		_, c := device(server)
		status, _ := redeemCode(c, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		devs, err := svc.ListMyDevices(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		Expect(devs[0].SignIn).To(BeTrue())
		Expect(devs[0].OwnerName).To(Equal("alice"))
		devID := devs[0].ID

		other, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())

		Expect(svc.CancelMyInvite(ctx, "bob", other.ID)).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.CancelMyInvite(ctx, "bob", "nope")).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.RenameMyDevice(ctx, "bob", devID, "mine")).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.RevokeMyDevice(ctx, "bob", devID)).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.RevokeMyDevice(ctx, "bob", "nope")).To(MatchError(p2papi.ErrNotFound))
		devs, err = svc.ListMyDevices(ctx, "bob")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(BeEmpty())

		By("letting the owner rename and revoke it")
		Expect(svc.RenameMyDevice(ctx, "alice", devID, "tablet")).To(Succeed())
		Expect(svc.RevokeMyDevice(ctx, "alice", devID)).To(Succeed())
		devs, err = svc.ListMyDevices(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs[0].Label).To(Equal("tablet"))
		Expect(devs[0].Status).To(Equal("revoked"))
	})

	It("acts only on sign-in rows, not on the instance devices a user minted as an admin", func() {
		signInUser(a, "carol", auth.RoleAdmin, true)
		// carol mints a worker-like device and a pending invite without
		// sign-in as an admin, and is later demoted.
		pairPlain(server, "carol")
		plain, err := server.pair.Mint(ctx, p2ppair.MintRequest{OwnerID: "carol", CreatedBy: "carol", Kind: p2ppair.KindDevice, Relays: p2ppair.RelayNone})
		Expect(err).ToNot(HaveOccurred())
		Expect(a.AuthDB().Model(&auth.User{}).Where("id = ?", "carol").Update("role", auth.RoleUser).Error).To(Succeed())
		devs, err := server.pair.ListDevicesByOwner(ctx, "carol")
		Expect(err).ToNot(HaveOccurred())
		Expect(devs).To(HaveLen(1))
		devID := devs[0].ID

		mine, err := svc.ListMyDevices(ctx, "carol")
		Expect(err).ToNot(HaveOccurred())
		Expect(mine).To(BeEmpty())
		invs, err := svc.ListMyInvites(ctx, "carol")
		Expect(err).ToNot(HaveOccurred())
		Expect(invs).To(BeEmpty())
		Expect(svc.RenameMyDevice(ctx, "carol", devID, "mine")).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.RevokeMyDevice(ctx, "carol", devID)).To(MatchError(p2papi.ErrNotFound))
		Expect(svc.CancelMyInvite(ctx, "carol", plain.Invite.ID)).To(MatchError(p2papi.ErrNotFound))
		st, err := svc.MyPairing(ctx, "carol")
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Devices).To(BeZero())
		Expect(st.PendingInvites).To(BeZero())

		d, err := server.pair.GetDevice(ctx, devID)
		Expect(err).ToNot(HaveOccurred())
		Expect(d.Status()).To(Equal(p2ppair.DeviceActive), "the instance device stays")
	})

	It("shows the owner of each device to the admin and filters by owner", func() {
		m, err := svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		Expect(err).ToNot(HaveOccurred())
		_, c := device(server)
		status, _ := redeemCode(c, server, codeOf(m.Link))
		Expect(status).To(Equal(http.StatusOK))
		pairPlain(server, "")

		all, err := svc.ListDevices(ctx, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(all).To(HaveLen(2))
		mine, err := svc.ListDevices(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(mine).To(HaveLen(1))
		Expect(mine[0].OwnerID).To(Equal("alice"))
		Expect(mine[0].OwnerName).To(Equal("alice"))
		Expect(mine[0].SignIn).To(BeTrue())
		none, err := svc.ListDevices(ctx, "bob")
		Expect(err).ToNot(HaveOccurred())
		Expect(none).To(BeEmpty())
	})

	It("says when pairing is not available", func() {
		a.StopP2PNet()
		st, err := svc.MyPairing(ctx, "alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Available).To(BeFalse())
		Expect(st.Reason).To(Equal("not_share"))
		Expect(st.Mode).To(Equal("off"))
		_, err = svc.MintMyInvite(ctx, "alice", p2papi.MyMintRequest{})
		var me *p2papi.ModeError
		Expect(err).To(BeAssignableToTypeOf(me))
	})

	It("keeps the setting in the auth database and checks it", func() {
		st, err := svc.UserPairing(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(st).To(Equal(p2papi.UserPairingSettings{Relays: "auto", MaxDevices: 5, MaxPending: 2, MaxTTLSeconds: 3600}))

		st, err = svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{AllowOpenRelay: boolp(true), MaxDevices: intp(3)})
		Expect(err).ToNot(HaveOccurred())
		Expect(st).To(Equal(p2papi.UserPairingSettings{Relays: "auto", AllowOpenRelay: true, MaxDevices: 3, MaxPending: 2, MaxTTLSeconds: 3600}))
		stored, err := p2ppair.LoadUserPairingPolicy(ctx, a.AuthDB())
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.MaxDevices).To(Equal(3))
		Expect(a.p2pDeviceLimit(ctx)).To(Equal(3))

		_, err = svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{Relays: strp("r1")})
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		_, err = svc.SetUserPairing(ctx, "root", p2papi.SetUserPairingRequest{MaxPending: intp(0)})
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		st, err = svc.UserPairing(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.MaxPending).To(Equal(2), "a refused change leaves the setting as it was")
	})
})

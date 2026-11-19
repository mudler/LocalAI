package p2ppair_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
)

type fakeRegistrar struct {
	mu        sync.Mutex
	fail      map[string]error
	registers []string
	cancels   []string
	cancelErr error
}

func (f *fakeRegistrar) RegisterPass(_ context.Context, req p2ppair.PassRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers = append(f.registers, req.Relay.ID)
	if err := f.fail[req.Relay.ID]; err != nil {
		return "", err
	}
	return "pass-" + req.Relay.ID, nil
}

func (f *fakeRegistrar) CancelPass(_ context.Context, r p2ppair.Relay, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, r.ID)
	return f.cancelErr
}

type fakeRelays []p2ppair.Relay

func (f fakeRelays) Relays(context.Context) []p2ppair.Relay { return f }

type fakeIssuer struct {
	issued  atomic.Int32
	revoked []string
	fail    error
}

func (f *fakeIssuer) Issue(_ context.Context, _ p2ppair.Tx, inv p2ppair.Invite, _ p2ppair.Device) (p2ppair.IssuedKey, error) {
	if f.fail != nil {
		return p2ppair.IssuedKey{}, f.fail
	}
	n := f.issued.Add(1)
	return p2ppair.IssuedKey{ID: "key-" + inv.ID, Secret: "secret-" + string(rune('0'+n))}, nil
}

func (f *fakeIssuer) Revoke(_ context.Context, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func mustRelay(id string, addr string, open bool) p2ppair.Relay {
	m, err := ma.NewMultiaddr(addr + "/p2p/" + newPeer().String())
	Expect(err).NotTo(HaveOccurred())
	return p2ppair.Relay{ID: id, Label: "relay " + id, Addr: m, Open: open}
}

// serviceSpecs runs the service tests on one kind of store.
func serviceSpecs(newStore func() p2ppair.Store) {
	var (
		svc    *p2ppair.Service
		reg    *fakeRegistrar
		iss    *fakeIssuer
		clk    *clock
		server peer.ID
		relays fakeRelays
		addrs  []ma.Multiaddr
	)
	ctx := context.Background()

	BeforeEach(func() {
		server = newPeer()
		reg = &fakeRegistrar{fail: map[string]error{}}
		iss = &fakeIssuer{}
		clk = &clock{t: t0}
		relays = fakeRelays{
			mustRelay("g1", "/ip4/198.51.100.1/tcp/4001", false),
			mustRelay("g2", "/ip4/198.51.100.2/tcp/4001", false),
			mustRelay("open", "/ip4/198.51.100.3/tcp/4001", true),
		}
		a, _ := ma.NewMultiaddr("/ip4/203.0.113.9/tcp/4001")
		addrs = []ma.Multiaddr{a}
		build := func() {
			var err error
			svc, err = p2ppair.NewService(newStore(), p2ppair.Config{
				ServerPeer: server, Addrs: func() []ma.Multiaddr { return addrs },
				Relays: relays, Registrar: reg, Issuer: iss, Now: clk.Now,
			})
			Expect(err).NotTo(HaveOccurred())
		}
		build()
	})

	mint := func(req p2ppair.MintRequest) *p2ppair.Minted {
		if req.Kind == "" {
			req.Kind = p2ppair.KindDevice
		}
		m, err := svc.Mint(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		return m
	}
	redeem := func(m *p2ppair.Minted, from peer.ID) (*p2ppair.RedeemResult, error) {
		return svc.Redeem(ctx, from, p2ppair.RedeemRequest{V: 1, Code: m.Link.Code, Name: "my phone"})
	}

	Describe("Mint", func() {
		It("defaults the life to 600 seconds and builds a link that parses", func() {
			m := mint(p2ppair.MintRequest{Label: "phone"})
			Expect(m.Invite.ExpiresAt.Sub(t0)).To(Equal(600 * time.Second))
			parsed, err := p2ppair.ParseLink(m.Link.String())
			Expect(err).NotTo(HaveOccurred())
			Expect(parsed.Peer).To(Equal(server))
			Expect(parsed.Code).To(HaveLen(p2ppair.CodeLen))
			Expect(parsed.Addrs).To(HaveLen(1))
			Expect(m.Link.Expires.Equal(m.Invite.ExpiresAt)).To(BeTrue())
		})

		It("refuses a bad kind, label and life", func() {
			for _, req := range []p2ppair.MintRequest{
				{Kind: "bogus"},
				{Kind: "federated"},
				{Kind: p2ppair.KindDevice, Label: "bad\nlabel"},
				{Kind: p2ppair.KindDevice, TTL: 25 * time.Hour},
				{Kind: p2ppair.KindDevice, TTL: -time.Second},
			} {
				_, err := svc.Mint(ctx, req)
				Expect(err).To(MatchError(p2ppair.ErrMalformed))
			}
		})

		It("registers a pass on the first reachable gated relay (auto)", func() {
			reg.fail["g1"] = errors.New("down")
			m := mint(p2ppair.MintRequest{})
			Expect(reg.registers).To(Equal([]string{"g1", "g2"}))
			Expect(m.Link.Pass).To(Equal("pass-g2"))
			Expect(m.Invite.PassRelayID).To(Equal("g2"))
			Expect(m.Pass).To(And(HaveField("RelayID", "g2"), HaveField("Registered", true)))
			Expect(m.Link.Relays).To(HaveLen(2), "the open relay is left out unless allowed")
			Expect(m.Warnings).To(BeEmpty())
		})

		It("mints without a pass, and says why, when every relay fails", func() {
			reg.fail["g1"] = errors.New("unreachable")
			reg.fail["g2"] = errors.New("timeout")
			m := mint(p2ppair.MintRequest{})
			Expect(m.Link.Pass).To(BeEmpty())
			Expect(m.Invite.PassRelayID).To(BeEmpty())
			Expect(m.Pass.Registered).To(BeFalse())
			Expect(m.Pass.Reason).To(Equal("relay_unreachable"))
			Expect(m.Warnings).To(ContainElement(p2ppair.WarnRelayUnreachable))
			Expect(m.Link.Relays).To(HaveLen(2), "the relays are still named")
			Expect(redeem(m, newPeer())).NotTo(BeNil(), "the invite works")
		})

		It("names open relays only when allowed, last, with the flag and a warning", func() {
			m := mint(p2ppair.MintRequest{AllowOpenRelay: true})
			Expect(m.Link.Relays).To(HaveLen(3))
			Expect(m.Link.Open).To(Equal([]int{2}))
			Expect(m.Warnings).To(ContainElement(p2ppair.WarnOpenRelay))
			Expect(reg.registers).NotTo(ContainElement("open"), "no pass is registered on an open relay")
		})

		It("never registers a pass when only open relays are named", func() {
			m := mint(p2ppair.MintRequest{Relays: p2ppair.RelayExplicit, RelayIDs: []string{"open"}, AllowOpenRelay: true})
			Expect(reg.registers).To(BeEmpty())
			Expect(m.Link.Pass).To(BeEmpty())
			Expect(m.Warnings).To(ContainElement(p2ppair.WarnOpenRelay))
		})

		It("refuses an open relay that is named but not allowed, and unknown relays", func() {
			_, err := svc.Mint(ctx, p2ppair.MintRequest{Kind: p2ppair.KindDevice, Relays: p2ppair.RelayExplicit, RelayIDs: []string{"open"}})
			Expect(err).To(MatchError(p2ppair.ErrMalformed))
			_, err = svc.Mint(ctx, p2ppair.MintRequest{Kind: p2ppair.KindDevice, Relays: p2ppair.RelayExplicit, RelayIDs: []string{"nope"}})
			Expect(err).To(MatchError(p2ppair.ErrMalformed))
		})

		It("uses an explicit list in the order asked", func() {
			m := mint(p2ppair.MintRequest{Relays: p2ppair.RelayExplicit, RelayIDs: []string{"g2"}})
			Expect(m.Invite.RelayIDs).To(Equal([]string{"g2"}))
			Expect(m.Link.Pass).To(Equal("pass-g2"))
		})

		It("warns when there is no relay, and no public address", func() {
			m := mint(p2ppair.MintRequest{Relays: p2ppair.RelayNone})
			Expect(m.Warnings).To(Equal([]p2ppair.Warning{p2ppair.WarnNoRelay}))
			addrs = nil
			m = mint(p2ppair.MintRequest{Relays: p2ppair.RelayNone})
			Expect(m.Warnings).To(ConsistOf(p2ppair.WarnNoRelay, p2ppair.WarnNoPublicAddr))
		})

		It("refuses the remote kind with no key issuer", func() {
			s, err := p2ppair.NewService(newStore(), p2ppair.Config{ServerPeer: server})
			Expect(err).NotTo(HaveOccurred())
			_, err = s.Mint(ctx, p2ppair.MintRequest{Kind: p2ppair.KindRemote})
			Expect(err).To(MatchError(p2ppair.ErrNoKeyIssuer))
		})
	})

	Describe("Redeem", func() {
		It("creates the device and returns the phrase both sides can compute", func() {
			m := mint(p2ppair.MintRequest{Label: "phone"})
			from := newPeer()
			res, err := redeem(m, from)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Device.PeerID).To(Equal(from.String()))
			Expect(res.Device.Label).To(Equal("my phone"))
			Expect(res.Phrase).To(Equal(p2ppair.Phrase(server, from, m.Invite.ID)))
			Expect(res.ClientKey).To(BeNil(), "a device invite issues no key")
			peers, err := svc.PairedPeers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(peers).To(ConsistOf(from))
		})

		It("answers a wrong, used and expired code with the same error", func() {
			m := mint(p2ppair.MintRequest{})
			_, err := svc.Redeem(ctx, newPeer(), p2ppair.RedeemRequest{V: 1, Code: "ZZZZZZZZZ2"})
			wrong := err
			_, err = redeem(m, newPeer())
			Expect(err).NotTo(HaveOccurred())
			_, used := redeem(m, newPeer())
			m2 := mint(p2ppair.MintRequest{TTL: time.Minute})
			clk.Advance(time.Minute)
			_, expired := redeem(m2, newPeer())
			for _, e := range []error{wrong, used, expired} {
				Expect(e).To(MatchError(p2ppair.ErrInviteInvalid))
				Expect(e.Error()).To(Equal(wrong.Error()))
			}
		})

		It("refuses a malformed request", func() {
			for _, req := range []p2ppair.RedeemRequest{
				{V: 2, Code: "ABCDE23456"},
				{V: 1, Code: "ABCDE23456", Name: "bad\nname"},
			} {
				_, err := svc.Redeem(ctx, newPeer(), req)
				Expect(err).To(MatchError(p2ppair.ErrMalformed))
			}
		})

		It("lets one of many concurrent redeems win", func() {
			m := mint(p2ppair.MintRequest{})
			var wins atomic.Int32
			var wg sync.WaitGroup
			for range 12 {
				wg.Add(1)
				go func(p peer.ID) {
					defer GinkgoRecover()
					defer wg.Done()
					if _, err := redeem(m, p); err == nil {
						wins.Add(1)
					}
				}(newPeer())
			}
			wg.Wait()
			Expect(wins.Load()).To(Equal(int32(1)))
		})

		It("limits a peer to five wrong codes an hour", func() {
			from := newPeer()
			m := mint(p2ppair.MintRequest{})
			for range 5 {
				_, err := svc.Redeem(ctx, from, p2ppair.RedeemRequest{V: 1, Code: "ZZZZZZZZZ2"})
				Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
			}
			_, err := redeem(m, from)
			Expect(err).To(MatchError(p2ppair.ErrRateLimited), "even the right code is refused now")
			_, err = redeem(m, newPeer())
			Expect(err).NotTo(HaveOccurred(), "other peers are not affected")

			from2 := newPeer()
			for range 5 {
				_, _ = svc.Redeem(ctx, from2, p2ppair.RedeemRequest{V: 1, Code: "ZZZZZZZZZ2"})
			}
			clk.Advance(time.Hour + time.Second)
			m2 := mint(p2ppair.MintRequest{})
			_, err = redeem(m2, from2)
			Expect(err).NotTo(HaveOccurred(), "the window passed")
		})

		It("limits a source to ten wrong codes a minute across peers", func() {
			for range 10 {
				_, err := svc.Redeem(ctx, newPeer(), p2ppair.RedeemRequest{V: 1, Code: "ZZZZZZZZZ2", Source: "192.0.2.5"})
				Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
			}
			m := mint(p2ppair.MintRequest{})
			_, err := svc.Redeem(ctx, newPeer(), p2ppair.RedeemRequest{V: 1, Code: m.Link.Code, Source: "192.0.2.5"})
			Expect(err).To(MatchError(p2ppair.ErrRateLimited))
			_, err = svc.Redeem(ctx, newPeer(), p2ppair.RedeemRequest{V: 1, Code: m.Link.Code, Source: "192.0.2.6"})
			Expect(err).NotTo(HaveOccurred())
			clk.Advance(time.Minute)
			m = mint(p2ppair.MintRequest{})
			_, err = svc.Redeem(ctx, newPeer(), p2ppair.RedeemRequest{V: 1, Code: m.Link.Code, Source: "192.0.2.5"})
			Expect(err).NotTo(HaveOccurred())
		})

		It("counts a malformed code as a wrong one", func() {
			from := newPeer()
			for range 5 {
				_, err := svc.Redeem(ctx, from, p2ppair.RedeemRequest{V: 1, Code: "short"})
				Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
			}
			_, err := svc.Redeem(ctx, from, p2ppair.RedeemRequest{V: 1, Code: "short"})
			Expect(err).To(MatchError(p2ppair.ErrRateLimited))
		})

		It("refuses a second pairing of the same peer and keeps the other invite", func() {
			from := newPeer()
			_, err := redeem(mint(p2ppair.MintRequest{}), from)
			Expect(err).NotTo(HaveOccurred())
			m := mint(p2ppair.MintRequest{})
			_, err = redeem(m, from)
			Expect(err).To(MatchError(p2ppair.ErrPeerPaired))
			_, err = redeem(m, newPeer())
			Expect(err).NotTo(HaveOccurred())
		})

		Describe("of the remote kind", func() {
			It("issues a key inside the redeem and returns it once", func() {
				m := mint(p2ppair.MintRequest{Kind: p2ppair.KindRemote, Scope: p2ppair.Scope{Models: []string{"m"}}})
				res, err := redeem(m, newPeer())
				Expect(err).NotTo(HaveOccurred())
				Expect(res.ClientKey).NotTo(BeNil())
				Expect(res.ClientKey.ID).To(Equal("key-" + m.Invite.ID))
				Expect(res.Device.ClientKeyID).To(Equal(res.ClientKey.ID))
			})

			It("leaves the invite usable when the issuer fails", func() {
				m := mint(p2ppair.MintRequest{Kind: p2ppair.KindRemote})
				iss.fail = errors.New("no more keys")
				_, err := redeem(m, newPeer())
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, p2ppair.ErrInviteInvalid)).To(BeFalse())
				iss.fail = nil
				_, err = redeem(m, newPeer())
				Expect(err).NotTo(HaveOccurred())
			})

			It("revokes the key with the device", func() {
				m := mint(p2ppair.MintRequest{Kind: p2ppair.KindRemote})
				res, err := redeem(m, newPeer())
				Expect(err).NotTo(HaveOccurred())
				Expect(svc.Revoke(ctx, res.Device.ID, "admin")).To(Succeed())
				Expect(iss.revoked).To(Equal([]string{res.ClientKey.ID}))
			})
		})
	})

	Describe("Revoke", func() {
		It("removes the peer from the allowlist source", func() {
			from := newPeer()
			res, err := redeem(mint(p2ppair.MintRequest{}), from)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Revoke(ctx, res.Device.ID, "admin")).To(Succeed())
			peers, _ := svc.PairedPeers(ctx)
			Expect(peers).To(BeEmpty())
			devs, _ := svc.ListDevices(ctx)
			Expect(devs).To(HaveLen(1))
			Expect(devs[0].RevokedBy).To(Equal("admin"))
			Expect(svc.Revoke(ctx, "nope", "admin")).To(MatchError(p2ppair.ErrNotFound))
		})
	})

	Describe("Cancel", func() {
		It("cancels the invite and the pass", func() {
			m := mint(p2ppair.MintRequest{})
			res, err := svc.Cancel(ctx, m.Invite.ID, "admin")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.PassCanceled).To(BeTrue())
			Expect(reg.cancels).To(Equal([]string{"g1"}))
			_, err = redeem(m, newPeer())
			Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
			list, _ := svc.ListInvites(ctx)
			Expect(list[0].Status(clk.Now())).To(Equal(p2ppair.InviteCanceled))
		})

		It("still cancels when the relay is down, and says so", func() {
			m := mint(p2ppair.MintRequest{})
			reg.cancelErr = errors.New("down")
			res, err := svc.Cancel(ctx, m.Invite.ID, "admin")
			Expect(err).NotTo(HaveOccurred())
			Expect(res.PassCanceled).To(BeFalse())
			Expect(res.PassErr).To(HaveOccurred())
			_, err = redeem(m, newPeer())
			Expect(err).To(MatchError(p2ppair.ErrInviteInvalid))
		})

		It("does not call the relay for an invite with no pass", func() {
			m := mint(p2ppair.MintRequest{Relays: p2ppair.RelayNone})
			_, err := svc.Cancel(ctx, m.Invite.ID, "admin")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.cancels).To(BeEmpty())
		})

		It("refuses an invite that was used, and one that does not exist", func() {
			m := mint(p2ppair.MintRequest{})
			_, err := redeem(m, newPeer())
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Cancel(ctx, m.Invite.ID, "admin")
			Expect(err).To(MatchError(p2ppair.ErrNotPending))
			_, err = svc.Cancel(ctx, "nope", "admin")
			Expect(err).To(MatchError(p2ppair.ErrNotFound))
		})
	})

	Describe("lists and touch", func() {
		It("lists invites with their status and records the last path", func() {
			a := mint(p2ppair.MintRequest{TTL: time.Minute})
			from := newPeer()
			_, err := redeem(mint(p2ppair.MintRequest{}), from)
			Expect(err).NotTo(HaveOccurred())
			clk.Advance(2 * time.Minute)
			list, err := svc.ListInvites(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			statuses := map[string]p2ppair.InviteStatus{}
			for _, i := range list {
				statuses[i.ID] = i.Status(clk.Now())
			}
			Expect(statuses[a.Invite.ID]).To(Equal(p2ppair.InviteExpired))

			Expect(svc.Touch(ctx, from, "hole-punched")).To(Succeed())
			devs, _ := svc.ListDevices(ctx)
			Expect(devs[0].LastPath).To(Equal("hole-punched"))
			Expect(devs[0].LastSeenAt.Equal(clk.Now())).To(BeTrue())
			Expect(svc.RenameDevice(ctx, devs[0].ID, "laptop")).To(Succeed())
		})
	})
}

var _ = Describe("Service", func() {
	Describe("on the JSON store", func() {
		serviceSpecs(func() p2ppair.Store {
			s, err := p2ppair.NewJSONStore(filepath.Join(GinkgoT().TempDir(), "pair"), "")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("on the GORM store", func() {
		serviceSpecs(func() p2ppair.Store {
			s, err := p2ppair.NewGormStore(openSQLite(), "secret")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
})

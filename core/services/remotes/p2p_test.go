package remotes_test

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/remotes"
)

const serverPeer = "12D3KooWServerPeerForTheRemotesSpecs"

// fakeTunnels pairs with any invite and "forwards" to a fake remote: the
// port it opens is the port the fake remote listens on.
type fakeTunnels struct {
	mu      sync.Mutex
	port    int
	off     bool
	pairs   int
	pairErr error
	opened  map[string]remotes.Tunnel
	closed  []string
	allowed []bool
}

func newFakeTunnels(r *fakeRemote) *fakeTunnels {
	u, err := url.Parse(r.URL)
	Expect(err).NotTo(HaveOccurred())
	port, err := strconv.Atoi(u.Port())
	Expect(err).NotTo(HaveOccurred())
	return &fakeTunnels{port: port, opened: map[string]remotes.Tunnel{}}
}

func (f *fakeTunnels) Pair(_ context.Context, link string, allowOpen bool) (remotes.Pairing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairs++
	f.allowed = append(f.allowed, allowOpen)
	if f.pairErr != nil {
		return remotes.Pairing{}, f.pairErr
	}
	return remotes.Pairing{Peer: serverPeer, ServerName: "Studio", Phrase: "apple river stone cloud", Key: secret,
		Relays: []string{"/ip4/192.0.2.1/tcp/4001/p2p/12D3KooWRelay"}, Hints: []string{"/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWHint"}}, nil
}

func (f *fakeTunnels) Open(t remotes.Tunnel) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Port == 0 {
		t.Port = f.port
	}
	f.opened[t.ID] = t
	return t.Port, nil
}

func (f *fakeTunnels) Close(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.opened, id)
	f.closed = append(f.closed, id)
}

func (f *fakeTunnels) Status(id string) remotes.P2PInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.off {
		return remotes.P2PInfo{State: remotes.TunnelOff}
	}
	if _, ok := f.opened[id]; !ok {
		return remotes.P2PInfo{State: remotes.TunnelError, Error: "not open"}
	}
	return remotes.P2PInfo{State: remotes.TunnelUp, Path: "direct"}
}

func (f *fakeTunnels) setOff(off bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.off = off
}

func (f *fakeTunnels) pairCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pairs
}

var _ = Describe("Remotes over P2P", func() {
	var (
		ctx    = context.Background()
		remote *fakeRemote
		tun    *fakeTunnels
		store  remotes.Store
		e      *env
	)
	const invite = "localai://pair?v=1&k=remote&p=x&c=ABCDEFGHJK&e=1&n=studio"

	newSvc := func(t remotes.Tunnels) remotes.Service {
		svc, err := remotes.New(remotes.Options{Store: store, Admin: e.admin, Configs: e.loader, Unloader: e.unloader, ModelsPath: e.dir, Tunnels: t})
		Expect(err).NotTo(HaveOccurred())
		return svc
	}

	BeforeEach(func() {
		remote = newFakeRemote("alpha", "beta")
		DeferCleanup(remote.Close)
		tun = newFakeTunnels(remote)
		store = memStore()
		e = newEnv(store)
		e.svc = newSvc(tun)
	})

	It("adds a remote by invite, keeps the issued key and builds entries on the loopback port", func() {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite, Models: chat("alpha")})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phrase).To(Equal("apple river stone cloud"))
		Expect(res.Created).To(Equal([]string{"alpha"}))

		r := res.Remote
		Expect(r.Name).To(Equal("Studio"))
		Expect(r.Kind).To(Equal(remotes.KindLocalAI))
		Expect(r.URL).To(Equal("p2p://" + serverPeer))
		Expect(r.Transport).To(Equal(remotes.TransportP2P))
		Expect(r.KeySet).To(BeTrue())
		Expect(r.Status).To(Equal(remotes.StatusOK), r.LastError)
		Expect(r.New).To(ConsistOf(remotes.UpstreamModel{ID: "beta", Usecases: []string{"chat"}}))
		Expect(r.P2P).NotTo(BeNil())
		Expect(*r.P2P).To(Equal(remotes.P2PInfo{PeerID: serverPeer, LocalPort: tun.port, State: remotes.TunnelUp, Path: "direct"}))

		cfg := e.config("alpha")
		Expect(cfg.Proxy.UpstreamURL).To(Equal("http://127.0.0.1:" + strconv.Itoa(tun.port)))
		Expect(cfg.Proxy.Remote).To(Equal(r.ID))
		Expect(e.read("alpha")).NotTo(ContainSubstring(secret))

		key, err := e.svc.(config.ProxyKeyResolver).ResolveKey(ctx, r.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(key.Key).To(Equal(secret))
		Expect(config.UpstreamWithinBase(cfg.Proxy.UpstreamURL, key.Base)).To(BeTrue())
		Expect(tun.allowed).To(Equal([]bool{false}))
	})

	It("passes the open relay choice to the pairing", func() {
		_, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite, AllowOpenRelay: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(tun.allowed).To(Equal([]bool{true}))
	})

	It("checks an explicit name before the invite is spent", func() {
		_, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Studio", URL: remote.URL, APIKey: secret})
		Expect(err).NotTo(HaveOccurred())
		_, err = e.svc.Create(ctx, remotes.CreateRequest{Invite: invite, Name: "studio"})
		Expect(errors.Is(err, remotes.ErrConflict)).To(BeTrue())
		Expect(tun.pairCount()).To(BeZero())

		By("numbering a name taken from the server")
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Remote.Name).To(Equal("Studio (2)"))
	})

	It("refuses an invite together with an address or a key, or another kind", func() {
		for _, req := range []remotes.CreateRequest{
			{Invite: invite, URL: remote.URL},
			{Invite: invite, APIKey: secret},
			{Invite: invite, Kind: remotes.KindOpenAI},
		} {
			_, err := e.svc.Create(ctx, req)
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue(), "%+v", req)
		}
		Expect(tun.pairCount()).To(BeZero())
	})

	It("refuses an invite on a server without tunnels and passes a pairing error on", func() {
		_, err := newSvc(nil).Create(ctx, remotes.CreateRequest{Invite: invite})
		var inv *remotes.InvalidError
		Expect(errors.As(err, &inv)).To(BeTrue())
		Expect(inv.Message).To(ContainSubstring("distributed mode"))

		tun.pairErr = &remotes.InvalidError{Message: "The invite has expired. Ask for a new one.", Reason: remotes.ErrInviteGone}
		_, err = e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(errors.Is(err, remotes.ErrInviteGone)).To(BeTrue())
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		Expect(errors.As(err, &inv)).To(BeTrue())
		Expect(inv.Message).To(HavePrefix("The invite has expired"))
		all, _ := e.svc.List(ctx)
		Expect(all).To(BeEmpty())
	})

	It("tells to use an invite when a p2p address is tested", func() {
		res, err := e.svc.Test(ctx, remotes.KindLocalAI, "p2p://"+serverPeer, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Step).To(Equal(remotes.StepInvalidURL))
		Expect(res.Message).To(ContainSubstring("invite link"))
	})

	It("withholds the key and records P2P off while the tunnel does not run", func() {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(err).NotTo(HaveOccurred())
		id := res.Remote.ID

		tun.setOff(true)
		_, err = e.svc.(config.ProxyKeyResolver).ResolveKey(ctx, id)
		Expect(err).To(MatchError(ContainSubstring("P2P is off")))
		r, err := e.svc.Refresh(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(remotes.StatusUnreachable))
		Expect(r.LastError).To(ContainSubstring("P2P is off"))
		Expect(r.P2P.State).To(Equal(remotes.TunnelOff))
	})

	It("keeps the address of a remote over P2P and closes the tunnel on delete", func() {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(err).NotTo(HaveOccurred())
		id := res.Remote.ID

		other := "http://192.0.2.10:8080"
		_, err = e.svc.Update(ctx, id, remotes.UpdateRequest{URL: &other})
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		same, name := res.Remote.URL, "Renamed"
		r, err := e.svc.Update(ctx, id, remotes.UpdateRequest{URL: &same, Name: &name})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Name).To(Equal("Renamed"))
		Expect(r.URL).To(Equal(same))

		Expect(e.svc.Delete(ctx, id, true)).To(Succeed())
		Expect(tun.closed).To(ContainElement(id))
	})

	It("opens the stored tunnels again at startup on the same port", func() {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(err).NotTo(HaveOccurred())

		fresh := newFakeTunnels(remote)
		fresh.port = 1 // a new pick would differ; the stored port must win
		remotes.StartTunnels(ctx, newSvc(fresh))
		Expect(fresh.opened).To(HaveKey(res.Remote.ID))
		t := fresh.opened[res.Remote.ID]
		Expect(t.Port).To(Equal(tun.port))
		Expect(t.Peer).To(Equal(serverPeer))
		Expect(t.Relays).To(Equal([]string{"/ip4/192.0.2.1/tcp/4001/p2p/12D3KooWRelay"}))
		Expect(t.Hints).To(Equal([]string{"/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWHint"}), "the hints of the invite are saved too")
	})

	It("keeps the newest address that worked, first and without repeats", func() {
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Invite: invite})
		Expect(err).NotTo(HaveOccurred())
		id := res.Remote.ID
		stored := func() []string {
			rec, err := store.Get(ctx, id)
			Expect(err).NotTo(HaveOccurred())
			return rec.P2P.Addrs
		}
		Expect(stored()).To(BeEmpty())

		got, changed, err := remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.10/tcp/4001"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(got).To(Equal([]string{"/ip4/192.0.2.10/tcp/4001"}))

		// The address of a new IP goes first; the old one stays behind it.
		got, changed, err = remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.20/tcp/4001"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(got).To(Equal([]string{"/ip4/192.0.2.20/tcp/4001", "/ip4/192.0.2.10/tcp/4001"}))
		Expect(stored()).To(Equal(got))

		// Several at once keep their order, in front of the older ones, up to the limit.
		got, changed, err = remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.30/tcp/4001", "/ip4/192.0.2.30/udp/4001/quic-v1", "/ip4/192.0.2.31/tcp/4001"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(got).To(Equal([]string{"/ip4/192.0.2.30/tcp/4001", "/ip4/192.0.2.30/udp/4001/quic-v1", "/ip4/192.0.2.31/tcp/4001", "/ip4/192.0.2.20/tcp/4001"}))
		got, changed, err = remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.20/tcp/4001"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(got[0]).To(Equal("/ip4/192.0.2.20/tcp/4001"))

		// The same address again writes nothing.
		got, changed, err = remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.20/tcp/4001"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
		Expect(got).To(Equal(stored()))

		// A circuit address is never stored.
		_, changed, err = remotes.LearnAddrs(ctx, e.svc, id, []string{"/ip4/192.0.2.1/tcp/4001/p2p/12D3KooWRelay/p2p-circuit"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())

		// The next start of the tunnel uses the stored address.
		fresh := newFakeTunnels(remote)
		remotes.StartTunnels(ctx, newSvc(fresh))
		Expect(fresh.opened[id].Addrs).To(Equal(stored()))
		Expect(fresh.opened[id].Relays).To(Equal([]string{"/ip4/192.0.2.1/tcp/4001/p2p/12D3KooWRelay"}))
		Expect(fresh.opened[id].Hints).To(Equal([]string{"/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWHint"}))
	})

	It("does not learn an address for a remote that is not over P2P or is gone", func() {
		_, _, err := remotes.LearnAddrs(ctx, e.svc, "missing", []string{"/ip4/192.0.2.10/tcp/4001"})
		Expect(errors.Is(err, remotes.ErrNotFound)).To(BeTrue())
	})

	DescribeTable("merges an address into the stored ones",
		func(stored []string, addr string, max int, want []string) {
			Expect(remotes.MergeAddr(stored, addr, max)).To(Equal(want))
		},
		Entry("into nothing", nil, "a", 4, []string{"a"}),
		Entry("in front", []string{"a", "b"}, "c", 4, []string{"c", "a", "b"}),
		Entry("moves a known one to the front", []string{"a", "b", "c"}, "c", 4, []string{"c", "a", "b"}),
		Entry("drops the oldest at the limit", []string{"a", "b", "c", "d"}, "e", 4, []string{"e", "a", "b", "c"}),
		Entry("refuses a circuit address", []string{"a"}, "/ip4/192.0.2.1/tcp/1/p2p/X/p2p-circuit", 4, []string{"a"}),
		Entry("refuses an empty address", []string{"a"}, "", 4, []string{"a"}),
	)
})

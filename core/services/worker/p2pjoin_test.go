package worker

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func loopbackHostFor(gate *p2pnet.Allowlist) *p2pnet.Host {
	GinkgoHelper()
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         gate,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

var _ = Describe("A worker that joined over P2P", func() {
	var (
		frontend *p2pnet.Host
		j        *p2pJoin
		hellos   chan tunnel.Hello
		answer   int
	)

	BeforeEach(func() {
		gate := p2pnet.NewAllowlist()
		frontend = loopbackHostFor(gate)
		host := loopbackHostFor(p2pnet.NewAllowlist(frontend.ID()))
		gate.Allow(host.ID())
		j = &p2pJoin{host: host, server: frontend.ID(), dial: []p2pnet.DialOption{p2pnet.WithAddrs(frontend.Addrs()...)}}
		hellos = make(chan tunnel.Hello, 4)
		answer = http.StatusOK
		for _, proto := range []p2pnet.Protocol{p2pnet.ProtoTunnelInference, p2pnet.ProtoTunnelBulk} {
			ln, err := frontend.Listen(proto, nil)
			Expect(err).ToNot(HaveOccurred())
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					h, err := tunnel.ReadHello(c)
					if err != nil {
						_ = c.Close()
						continue
					}
					hellos <- h
					_ = tunnel.WriteHelloReply(c, tunnel.HelloReply{Status: answer, Error: "no"})
				}
			}()
		}
	})

	It("opens a lane with a hello that names the node, the lane and the credential", func() {
		conn, err := j.DialTunnel(context.Background(), tunnel.LaneBulk, "node-1", "secret")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)
		var h tunnel.Hello
		Eventually(hellos).Should(Receive(&h))
		Expect(h).To(Equal(tunnel.Hello{V: tunnel.HelloVersion, NodeID: "node-1", Lane: tunnel.LaneBulk, Token: "secret"}))
	})

	It("reports a refusal with its status, so that 401 and 403 register the worker again", func() {
		answer = http.StatusUnauthorized
		_, err := j.DialTunnel(context.Background(), tunnel.LaneInference, "node-1", "stale")
		Expect(err).To(HaveOccurred())
		Expect(refusedCredential(err)).To(BeTrue())
		answer = http.StatusConflict
		_, err = j.DialTunnel(context.Background(), tunnel.LaneBulk, "node-1", "t")
		Expect(isWrongReplica(err)).To(BeTrue())
	})

	It("names the frontend by peer ID and reports the path of its tunnel", func() {
		Expect(j.FrontendURL()).To(Equal("http://" + frontend.ID().String()))
		_, err := j.DialTunnel(context.Background(), tunnel.LaneInference, "node-1", "t")
		Expect(err).ToNot(HaveOccurred())
		Expect(j.Route()).To(Equal(nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "direct"}))
	})

	It("holds the tunnel only, over P2P, and reports its P2P path", func() {
		cfg := &Config{Join: "localai://pair?x", NatsURL: "nats://example:4222", join: j}
		routes, err := cfg.routes()
		Expect(err).ToNot(HaveOccurred())
		Expect(routes).To(Equal([]Route{RouteTunnel}), "a P2P worker never talks to NATS, even with a NATS URL")

		cfg.Routes = []string{"nats", "tunnel"}
		_, err = cfg.routes()
		Expect(err).To(MatchError(ContainSubstring("tunnel only")))

		_, err = j.DialTunnel(context.Background(), tunnel.LaneInference, "node-1", "t")
		Expect(err).ToNot(HaveOccurred())
		plane := NewBackendPlane(&Config{RegisterTo: j.FrontendURL(), join: j}, "node-1", "127.0.0.1:0", NATSLocal{})
		Expect(tunnelAttachment{plane.tunnel}.TunnelRoute()).To(Equal(nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "direct"}))
	})
})

var _ = Describe("The flags of the frontend of a worker", func() {
	It("wants one of --register-to and --join", func() {
		Expect(Config{}.validateFrontend()).To(MatchError(ContainSubstring("missing --register-to")))
		Expect(Config{RegisterTo: "http://f", Join: "localai://pair"}.validateFrontend()).To(HaveOccurred())
		Expect(Config{RegisterTo: "http://f"}.validateFrontend()).To(Succeed())
		Expect(Config{Join: "localai://pair"}.validateFrontend()).To(Succeed())
	})

	It("refuses an invite of another kind before it starts anything", func() {
		server := loopbackHostFor(p2pnet.NewAllowlist())
		link := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: server.ID(), Code: "K7QF29MXAB", Expires: time.Now().Add(time.Hour)}
		dir := GinkgoT().TempDir()
		_, err := joinP2P(context.Background(), &Config{Join: link.String(), P2PIdentity: filepath.Join(dir, "id.key")})
		Expect(err).To(MatchError(ContainSubstring("device invite")))
		_, statErr := os.Stat(filepath.Join(dir, "id.key"))
		Expect(errors.Is(statErr, os.ErrNotExist)).To(BeTrue(), "nothing is written for an invite that cannot be used")
	})

	It("refuses an expired worker invite that it never redeemed", func() {
		server := loopbackHostFor(p2pnet.NewAllowlist())
		link := &p2ppair.Link{V: 1, Kind: p2ppair.KindWorker, Peer: server.ID(), Code: "K7QF29MXAB", Expires: time.Now().Add(-time.Minute)}
		_, err := joinP2P(context.Background(), &Config{Join: link.String(), P2PIdentity: filepath.Join(GinkgoT().TempDir(), "id.key")})
		Expect(err).To(HaveOccurred())
	})

	Describe("with a record of the server", func() {
		var (
			dir, recPath string
			server       *p2pnet.Host
			old          *p2ppair.Link
		)
		BeforeEach(func() {
			dir = GinkgoT().TempDir()
			server = loopbackHostFor(p2pnet.NewAllowlist())
			old = &p2ppair.Link{V: 1, Kind: p2ppair.KindWorker, Peer: server.ID(), Code: "K7QF29MXAB", Expires: time.Now().Add(time.Hour)}
			recPath = filepath.Join(dir, "pairs", server.ID().String()+".json")
		})

		join := func(link *p2ppair.Link) (*p2pJoin, error) {
			return joinP2P(context.Background(), &Config{Join: link.String(), P2PIdentity: filepath.Join(dir, "id.key")})
		}

		It("redeems a new invite of the same server again", func() {
			Expect(writeJoinRecord(recPath, &joinRecord{ServerPeer: server.ID().String(), Invite: inviteHash(old)})).To(Succeed())
			fresh := *old
			fresh.Code, fresh.Expires = "N3QW57RTYZ", time.Now().Add(-time.Minute)
			_, err := join(&fresh)
			// The expiry check runs only on the way to a redeem.
			Expect(err).To(MatchError(pairclient.ErrExpired))
		})

		It("does not redeem the invite of the record again", func() {
			Expect(writeJoinRecord(recPath, &joinRecord{ServerPeer: server.ID().String(), Invite: inviteHash(old)})).To(Succeed())
			expired := *old
			expired.Expires = time.Now().Add(-time.Minute)
			j, err := join(&expired)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(j.Close)
		})

		It("keeps using a record that has no invite hash", func() {
			Expect(writeJoinRecord(recPath, &joinRecord{ServerPeer: server.ID().String()})).To(Succeed())
			fresh := *old
			fresh.Code, fresh.Expires = "N3QW57RTYZ", time.Now().Add(-time.Minute)
			j, err := join(&fresh)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(j.Close)
		})
	})
})

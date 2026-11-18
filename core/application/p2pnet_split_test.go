//go:build auth

package application

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/p2psplit"
)

// tunnelList records what the llama.cpp tunnel callback received last.
type tunnelList struct {
	mu   sync.Mutex
	last []string
}

func (l *tunnelList) set(v []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = v
}

func (l *tunnelList) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

// rpcEcho is a stand-in for llama.cpp's rpc-server: it answers "rpc:" and
// what it read.
func rpcEcho() string {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(l.Close)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = c.Write(append([]byte("rpc:"), buf[:n]...))
			}()
		}
	}()
	return l.Addr().String()
}

var _ = Describe("model-split workers in mode share", func() {
	It("pairs a llama.cpp worker by invite and hands its address to llama.cpp", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		a := newP2PTestApp("share", GinkgoT().TempDir(), db)
		var tunnels tunnelList
		a.ApplicationConfig().LlamaCPPTunnelCallback = tunnels.set
		redeem := echo.New()
		redeem.POST(redeemPath, p2papi.RedeemEndpoint(a.P2P()))
		a.ServeP2PHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "main") }), redeem)
		Expect(a.StartP2PNet()).To(Succeed())
		server, _ := a.p2p().runtime()
		svc, split := a.P2P(), a.P2PSplit()
		ctx := context.Background()

		By("refusing an unknown engine")
		_, err = svc.MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "split", Labels: map[string]string{"engine": "cuda"}, Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(errors.Is(err, p2papi.ErrInvalid)).To(BeTrue())

		By("minting a split invite with no QR code")
		m, err := svc.MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "split", Label: "attic", Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(m.QR).To(BeFalse())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		link.Addrs = server.host.Addrs()

		By("redeeming it on the worker")
		worker, err := p2pnet.New(ctx, p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			Gate:         p2pnet.NewAllowlist(server.host.ID()),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(worker.Close)
		res, err := pairclient.Redeem(ctx, worker, link, pairclient.Request{Name: "attic", Platform: "linux"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Kind).To(Equal("split"))

		By("connecting the worker agent")
		agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.host.ID(), Dial: pairclient.DialOptions(link),
			Engine: p2psplit.EngineLlama, MemoryBytes: 12 << 30, RPCAddr: rpcEcho(),
		})
		Expect(err).ToNot(HaveOccurred())
		actx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = agent.Run(actx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 10*time.Second).Should(BeClosed())
		})

		Eventually(tunnels.get, 20*time.Second).Should(HaveLen(1))
		c, err := net.Dial("tcp", tunnels.get()[0])
		Expect(err).ToNot(HaveOccurred())
		_, _ = c.Write([]byte("ping"))
		b, _ := io.ReadAll(c)
		_ = c.Close()
		Expect(string(b)).To(Equal("rpc:ping"))

		list, err := split.ListSplitWorkers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Workers).To(HaveLen(1))
		w := list.Workers[0]
		Expect(w.State).To(Equal("online"))
		Expect(w.Engine).To(Equal("llama"))
		Expect(w.Path.Kind).To(Equal("direct"))
		Expect(w.Addr).To(Equal(tunnels.get()[0]))
		Expect(list.MemoryInUse).To(Equal(uint64(12 << 30)))

		By("recording Use anyway")
		allow := true
		v, err := split.SetSplitWorker(ctx, w.ID, p2papi.SplitWorkerUpdate{AllowSlowPath: &allow})
		Expect(err).ToNot(HaveOccurred())
		Expect(v.AllowSlowPath).To(BeTrue())
		_, err = split.SetSplitWorker(ctx, "nope", p2papi.SplitWorkerUpdate{AllowSlowPath: &allow})
		Expect(err).To(MatchError(p2papi.ErrNotFound))

		By("dropping the worker when it is revoked")
		Expect(svc.RevokeDevice(ctx, w.ID, "admin")).To(Succeed())
		Eventually(tunnels.get, 10*time.Second).Should(BeEmpty())
		list, err = split.ListSplitWorkers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Workers).To(BeEmpty())
	})

	It("answers that the model-split workers need mode share", func() {
		a := newP2PTestApp("connect", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(Succeed())
		_, err := a.P2PSplit().ListSplitWorkers(context.Background())
		var me *p2papi.ModeError
		Expect(errors.As(err, &me)).To(BeTrue())
	})
})

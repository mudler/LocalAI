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

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
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

	It("sends a worker attached to a backend node to that node, and takes it back", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		ndb, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		reg, err := nodes.NewNodeRegistry(ndb)
		Expect(err).ToNot(HaveOccurred())
		ctx := context.Background()
		gpu := &nodes.BackendNode{Name: "gpu-1", NodeType: nodes.NodeTypeBackend, SplitServer: true}
		Expect(reg.Register(ctx, gpu, true)).To(Succeed())
		agentNode := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent}
		Expect(reg.Register(ctx, agentNode, true)).To(Succeed())

		a := newP2PTestApp("share", GinkgoT().TempDir(), db)
		a.p2p().nodeRegistry = reg
		var serverTunnels tunnelList
		a.ApplicationConfig().LlamaCPPTunnelCallback = serverTunnels.set
		redeem := echo.New()
		redeem.POST(redeemPath, p2papi.RedeemEndpoint(a.P2P()))
		a.ServeP2PHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "main") }), redeem)
		Expect(a.StartP2PNet()).To(Succeed())
		server, _ := a.p2p().runtime()
		svc, split := a.P2P(), a.P2PSplit()

		By("refusing an invite for a node that is not a backend node")
		_, err = svc.MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "split", Labels: map[string]string{"node": agentNode.ID}, Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(errors.Is(err, p2papi.ErrInvalid)).To(BeTrue())

		By("pairing a worker with an invite that names the node")
		m, err := svc.MintInvite(ctx, "admin", p2papi.MintRequest{Kind: "split", Label: "attic", Labels: map[string]string{"node": gpu.ID}, Relays: p2papi.RelayChoice{Mode: "none"}})
		Expect(err).ToNot(HaveOccurred())
		link, err := p2ppair.ParseLink(m.Link)
		Expect(err).ToNot(HaveOccurred())
		link.Addrs = server.host.Addrs()
		workerGate := p2pnet.NewAllowlist(server.host.ID())
		worker, err := p2pnet.New(ctx, p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "id.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			Gate:         workerGate,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(worker.Close)
		_, err = pairclient.Redeem(ctx, worker, link, pairclient.Request{Name: "attic", Platform: "linux"})
		Expect(err).ToNot(HaveOccurred())

		By("serving the node: its manager reads the workers the frontend attached to it")
		nodeHost, err := p2pnet.New(ctx, p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "node.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			Gate:         p2pnet.NewAllowlist(worker.ID()),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(nodeHost.Close)
		var nodeTunnels tunnelList
		nm, err := p2psplit.NewManager(ctx, p2psplit.Config{
			Host: nodeHost, OnLlama: nodeTunnels.set,
			Devices: func(ctx context.Context) ([]p2psplit.Device, error) {
				as, err := a.SplitAssignment(ctx, gpu.ID)
				if err != nil {
					return nil, err
				}
				var out []p2psplit.Device
				for _, w := range as.Workers {
					p, _ := p2pnet.DecodePeerID(w.PeerID)
					out = append(out, p2psplit.Device{ID: w.DeviceID, Label: w.Label, Peer: p, Engine: p2psplit.Engine(w.Engine), CreatedAt: w.CreatedAt})
				}
				return out, nil
			},
			Elsewhere: func(context.Context, p2pnet.ID) (p2psplit.Move, bool) { return p2psplit.Move{Wait: "not yet"}, true },
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(nm.Close)
		report := func() {
			rep := workerctl.SplitReport{PeerID: nodeHost.ID().String()}
			for _, ad := range nodeHost.Addrs() {
				rep.Addrs = append(rep.Addrs, ad.String())
			}
			for _, w := range nm.Workers() {
				rep.Workers = append(rep.Workers, workerctl.SplitWorkerReport{
					DeviceID: w.DeviceID, State: string(w.State), MemoryBytes: w.MemoryBytes,
					Addr: w.Addr, Engine: string(w.Engine), Path: workerctl.SplitPath{Kind: string(w.Path.Kind)},
				})
			}
			Expect(reg.SetSplitReport(ctx, gpu.ID, rep)).To(Succeed())
		}
		report()

		agent, err := p2psplit.NewAgent(p2psplit.AgentConfig{
			Host: worker, Server: server.host.ID(), Dial: pairclient.DialOptions(link),
			Engine: p2psplit.EngineLlama, MemoryBytes: 6 << 30, RPCAddr: rpcEcho(),
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

		By("forwarding the worker on the node, not on the frontend")
		Eventually(nodeTunnels.get, 20*time.Second).Should(HaveLen(1))
		c, err := net.Dial("tcp", nodeTunnels.get()[0])
		Expect(err).ToNot(HaveOccurred())
		_, _ = c.Write([]byte("ping"))
		b, _ := io.ReadAll(c)
		_ = c.Close()
		Expect(string(b)).To(Equal("rpc:ping"))
		Expect(serverTunnels.get()).To(BeEmpty())

		By("showing the state the node reported")
		report()
		list, err := split.ListSplitWorkers(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Distributed).To(BeTrue())
		Expect(list.Workers).To(HaveLen(1))
		w := list.Workers[0]
		Expect(w.NodeID).To(Equal(gpu.ID))
		Expect(w.NodeName).To(Equal("gpu-1"))
		Expect(w.State).To(Equal("online"))
		Expect(w.Addr).To(Equal(nodeTunnels.get()[0]))
		Expect(list.MemoryInUse).To(Equal(uint64(6 << 30)))

		By("refusing a node that does not exist")
		bad := "nope"
		_, err = split.SetSplitWorker(ctx, w.ID, p2papi.SplitWorkerUpdate{NodeID: &bad})
		Expect(errors.Is(err, p2papi.ErrInvalid)).To(BeTrue())

		By("attaching the worker to the frontend again")
		none := ""
		v, err := split.SetSplitWorker(ctx, w.ID, p2papi.SplitWorkerUpdate{NodeID: &none})
		Expect(err).ToNot(HaveOccurred())
		Expect(v.NodeID).To(BeEmpty())
		as, err := a.SplitAssignment(ctx, gpu.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(as.Workers).To(BeEmpty())
		nm.Refresh()
		Eventually(nodeTunnels.get, 10*time.Second).Should(BeEmpty())
		Eventually(serverTunnels.get, 30*time.Second).Should(HaveLen(1))
	})
})

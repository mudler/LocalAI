package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/tunnel/slowlink"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs run the worker client against the real connect endpoint, the real
// registry of tunnels and a real PostgreSQL, over real sockets.
var _ = Describe("Worker tunnel against the connect endpoint", func() {
	const (
		nodeToken = "own-tunnel-token"
		linkRate  = 125e6 // 1 Gbit/s
		oneWay    = 10 * time.Millisecond
	)

	var (
		ctx      context.Context
		cancel   context.CancelFunc
		clusterR *cluster.Registry
		registry *tunnel.Registry
		frontend *httptest.Server
		nodeID   string

		hijackedMu sync.Mutex
		hijacked   []net.Conn
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		nodeReg, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		registry = tunnel.NewRegistry(clusterR, "replica-a")

		node := &nodes.BackendNode{Name: "w1", NodeType: nodes.NodeTypeBackend}
		Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
		nodeID = node.ID
		sum := sha256.Sum256([]byte(nodeToken))
		Expect(nodeReg.SetTunnelTokenHash(ctx, nodeID, hex.EncodeToString(sum[:]))).To(Succeed())

		e := echo.New()
		e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(nodeReg, registry))
		frontend = httptest.NewUnstartedServer(e)
		// The connections that were upgraded leave the server, so the server does
		// not close them. The spec closes them to cut the tunnel.
		frontend.Config.ConnState = func(c net.Conn, state http.ConnState) {
			if state == http.StateHijacked {
				hijackedMu.Lock()
				hijacked = append(hijacked, c)
				hijackedMu.Unlock()
			}
		}
		frontend.Start()
		DeferCleanup(frontend.Close)
	})

	startWorker := func(frontendURL string, services map[string]LocalService) *Tunnel {
		GinkgoHelper()
		t, err := StartTunnel(ctx, TunnelConfig{
			FrontendURL: frontendURL,
			NodeID:      nodeID,
			Token:       func() string { return nodeToken },
			Services:    services,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(t.Close)
		return t
	}

	// tcpService starts a server and returns its address.
	tcpService := func(handle func(net.Conn)) string {
		GinkgoHelper()
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(lis.Close)
		go func() {
			for {
				c, err := lis.Accept()
				if err != nil {
					return
				}
				go handle(c)
			}
		}()
		return lis.Addr().String()
	}

	echo8 := func(c net.Conn) {
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	}
	// download sends transferSize bytes to whoever connects, and then closes.
	download := func(c net.Conn) {
		defer func() { _ = c.Close() }()
		chunk := make([]byte, 256<<10)
		for sent := 0; sent < transferBytes; sent += len(chunk) {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}

	It("holds both lanes on the replica that owns the node and carries streams on each", func() {
		echoAddr := tcpService(echo8)
		t := startWorker(frontend.URL, map[string]LocalService{
			tunnel.StreamTagGRPC: dialLocalTCP,
			tunnel.StreamTagHTTP: dialLocalTCP,
		})
		Eventually(t.Connected, "10s").Should(BeTrue())
		Eventually(t.BulkConnected, "10s").Should(BeTrue())
		Eventually(registry.Held, "10s").Should(Equal([]string{nodeID}))

		for _, lane := range []tunnel.Lane{tunnel.LaneInference, tunnel.LaneBulk} {
			st, err := registry.Open(ctx, nodeID, lane)
			Expect(err).ToNot(HaveOccurred())
			Expect(tunnel.WriteStreamRequest(st, tunnel.StreamTagHTTP, echoAddr)).To(Succeed())
			Expect(tunnel.ReadStreamReply(st)).To(Succeed())
			_, err = st.Write([]byte("hello"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 5)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred(), string(lane))
			Expect(string(got)).To(Equal("hello"))
			Expect(st.Close()).To(Succeed())
		}
	})

	It("connects both lanes again, with a new claim, after the connections to the frontend were cut", func() {
		t := startWorker(frontend.URL, map[string]LocalService{})
		Eventually(registry.Held, "10s").Should(Equal([]string{nodeID}))
		Eventually(t.BulkConnected, "10s").Should(BeTrue())
		_, first, err := clusterR.Owner(ctx, nodeID)
		Expect(err).ToNot(HaveOccurred())

		// The sockets are cut with no close frame, as they are when a replica is
		// killed.
		hijackedMu.Lock()
		for _, c := range hijacked {
			_ = c.Close()
		}
		hijackedMu.Unlock()
		Eventually(t.Connected, "10s").Should(BeFalse())

		Eventually(t.Connected, "30s").Should(BeTrue())
		Eventually(t.BulkConnected, "30s").Should(BeTrue())
		Eventually(registry.Held, "10s").Should(Equal([]string{nodeID}))
		_, second, err := clusterR.Owner(ctx, nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(second).ToNot(Equal(first), "a new session is a new claim")
	})

	// The delay of a small call during a large transfer, on the real code: the
	// connect endpoint, the registry, and the client of the worker, all behind a
	// link with a rate and a delay. The numbers are the same kind as the ones in
	// the spec of the tunnel package, and the lane is chosen by the caller of
	// Open, as the file stager will do.
	Describe("the delay of a small call during a transfer", Label("benchmark"), Ordered, func() {
		const directName = "direct: transfer and probe on two TCP connections"
		var results = map[string][]time.Duration{}

		// run starts a worker behind the link, and runs a download of
		// transferBytes on transferLane while a probe runs on the inference lane.
		run := func(name string, transferLane tunnel.Lane) {
			GinkgoHelper()
			echoAddr := tcpService(echo8)
			downloadAddr := tcpService(download)
			link, err := slowlink.New(frontend.Listener.Addr().String(), linkRate, oneWay)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(link.Close)

			t := startWorker("http://"+link.Addr(), map[string]LocalService{
				tunnel.StreamTagGRPC: dialLocalTCP,
				tunnel.StreamTagHTTP: dialLocalTCP,
			})
			Eventually(t.Connected, "10s").Should(BeTrue())
			Eventually(t.BulkConnected, "10s").Should(BeTrue())
			Eventually(registry.Held, "10s").Should(Equal([]string{nodeID}))

			// The probe sends the request frame and the 8 bytes together, so a
			// call costs one round trip, as a call on an open connection does.
			probe := func() (time.Duration, error) {
				start := time.Now()
				st, err := registry.Open(ctx, nodeID, tunnel.LaneInference)
				if err != nil {
					return 0, err
				}
				defer func() { _ = st.Close() }()
				if err := tunnel.WriteStreamRequest(st, tunnel.StreamTagGRPC, echoAddr); err != nil {
					return 0, err
				}
				if _, err := st.Write(make([]byte, 8)); err != nil {
					return 0, err
				}
				if err := tunnel.ReadStreamReply(st); err != nil {
					return 0, err
				}
				if _, err := io.ReadFull(st, make([]byte, 8)); err != nil {
					return 0, err
				}
				return time.Since(start), nil
			}

			stop := make(chan struct{})
			sampled := make(chan []time.Duration, 1)
			go func() {
				var samples []time.Duration
				for {
					select {
					case <-stop:
						sampled <- samples
						return
					case <-time.After(5 * time.Millisecond):
					}
					d, err := probe()
					if err != nil {
						sampled <- samples
						return
					}
					samples = append(samples, d)
				}
			}()

			start := time.Now()
			st, err := registry.Open(ctx, nodeID, transferLane)
			Expect(err).ToNot(HaveOccurred())
			Expect(tunnel.WriteStreamRequest(st, tunnel.StreamTagHTTP, downloadAddr)).To(Succeed())
			Expect(tunnel.ReadStreamReply(st)).To(Succeed())
			n, err := io.Copy(io.Discard, st)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeEquivalentTo(transferBytes))
			elapsed := time.Since(start)
			close(stop)

			var samples []time.Duration
			Eventually(sampled, "30s").Should(Receive(&samples))
			Expect(len(samples)).To(BeNumerically(">=", 15), "too few probes completed during the transfer")
			samples = samples[len(samples)/10:]
			results[name] = samples
			slices.Sort(samples)
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			AddReportEntry(name, map[string]any{
				"transfer MiB":   transferBytes >> 20,
				"transfer MiB/s": float64(transferBytes>>20) / elapsed.Seconds(),
				"probes":         len(samples),
				"probe p50 ms":   ms(samples[len(samples)/2]),
				"probe p99 ms":   ms(samples[min(len(samples)-1, len(samples)*99/100)]),
				"probe max ms":   ms(samples[len(samples)-1]),
			})
		}

		It("measures the baseline: the transfer and the probe on two TCP connections", func() {
			// One service behind the link. The first byte of a connection says
			// what the connection is for.
			front := tcpService(func(c net.Conn) {
				kind := make([]byte, 1)
				if _, err := io.ReadFull(c, kind); err != nil {
					_ = c.Close()
					return
				}
				if kind[0] == 'e' {
					echo8(c)
				} else {
					download(c)
				}
			})
			link, err := slowlink.New(front, linkRate, oneWay)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(link.Close)
			dial := func(kind byte) net.Conn {
				c, err := net.Dial("tcp", link.Addr())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(c.Close)
				_, err = c.Write([]byte{kind})
				Expect(err).ToNot(HaveOccurred())
				return c
			}
			bulk, probeConn := dial('d'), dial('e')

			stop := make(chan struct{})
			sampled := make(chan []time.Duration, 1)
			go func() {
				var samples []time.Duration
				buf := make([]byte, 8)
				for {
					select {
					case <-stop:
						sampled <- samples
						return
					case <-time.After(5 * time.Millisecond):
					}
					start := time.Now()
					if _, err := probeConn.Write(buf); err != nil {
						sampled <- samples
						return
					}
					if _, err := io.ReadFull(probeConn, buf); err != nil {
						sampled <- samples
						return
					}
					samples = append(samples, time.Since(start))
				}
			}()
			n, err := io.Copy(io.Discard, bulk)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeEquivalentTo(transferBytes))
			close(stop)

			var samples []time.Duration
			Eventually(sampled, "30s").Should(Receive(&samples))
			Expect(len(samples)).To(BeNumerically(">=", 15))
			samples = samples[len(samples)/10:]
			slices.Sort(samples)
			results[directName] = samples
			ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
			AddReportEntry(directName, map[string]any{
				"probes":       len(samples),
				"probe p50 ms": ms(samples[len(samples)/2]),
				"probe p99 ms": ms(samples[min(len(samples)-1, len(samples)*99/100)]),
			})
		})

		It("measures the probe with the transfer on the same lane", func() {
			run("shared: transfer and probe on the inference lane", tunnel.LaneInference)
		})

		It("measures the probe with the transfer on the bulk lane", func() {
			run("bulk lane: transfer on the bulk lane, probe on the inference lane", tunnel.LaneBulk)
		})

		It("delays the probe less when the transfer uses the bulk lane", func() {
			shared := results["shared: transfer and probe on the inference lane"]
			bulk := results["bulk lane: transfer on the bulk lane, probe on the inference lane"]
			direct := results[directName]
			Expect(shared).ToNot(BeEmpty())
			Expect(bulk).ToNot(BeEmpty())
			Expect(direct).ToNot(BeEmpty())
			p99 := func(s []time.Duration) time.Duration { return s[min(len(s)-1, len(s)*99/100)] }
			// The acceptance of the bulk lane: the probe stays within 10 ms of a
			// probe on its own TCP connection, at the 99th percentile, behind
			// the same link and the same queue. The same transfer on the lane of
			// the probe delays it by tens of milliseconds more.
			Expect(p99(bulk)).To(BeNumerically("<=", p99(direct)+10*time.Millisecond),
				"bulk lane p99 %v, direct p99 %v", p99(bulk), p99(direct))
			Expect(p99(shared)).To(BeNumerically(">", p99(bulk)),
				"shared p99 %v, bulk p99 %v", p99(shared), p99(bulk))
		})
	})
})

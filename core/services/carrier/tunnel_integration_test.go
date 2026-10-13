package carrier_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	ggrpc "google.golang.org/grpc"
	"gorm.io/gorm"

	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/tunnel/slowlink"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeBackend is a gRPC backend that a spec can make load, fail to load, or hang
// while it loads.
type fakeBackend struct {
	pb.UnimplementedBackendServer

	mu        sync.Mutex
	loads     []string
	cancelled []string
	loading   chan string
	// gate holds a load of the model "slow" until a spec closes it.
	gate chan struct{}
}

func (b *fakeBackend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (b *fakeBackend) LoadModel(ctx context.Context, in *pb.ModelOptions) (*pb.Result, error) {
	b.mu.Lock()
	b.loads = append(b.loads, in.Model)
	b.mu.Unlock()
	switch in.Model {
	case "fails":
		return &pb.Result{Success: false, Message: "out of memory"}, nil
	case "slow":
		select {
		case b.loading <- in.Model:
		default:
		}
		select {
		case <-b.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case "hangs":
		select {
		case b.loading <- in.Model:
		default:
		}
		<-ctx.Done()
		b.mu.Lock()
		b.cancelled = append(b.cancelled, in.Model)
		b.mu.Unlock()
		return nil, ctx.Err()
	}
	return &pb.Result{Success: true, Message: "loaded " + in.Model}, nil
}

func (b *fakeBackend) Predict(_ context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("echo: " + in.Prompt)}, nil
}

func (b *fakeBackend) cancelledModels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.cancelled)
}

// controlPlane is the control plane of the fake worker. It answers the verbs of a
// load and counts what it saw.
type controlPlane struct {
	mu       sync.Mutex
	address  string
	installs []workerctl.BackendInstallRequest
	stops    []workerctl.ModelStopRequest
	renewals int
	ensures  []workerctl.FileEnsureRequest
}

func (c *controlPlane) handler() http.Handler {
	mux := http.NewServeMux()
	body := func(r *http.Request, into any) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, into)
	}
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbBackendInstall), func(w http.ResponseWriter, r *http.Request) {
		var req workerctl.BackendInstallRequest
		body(r, &req)
		c.mu.Lock()
		c.installs = append(c.installs, req)
		address := c.address
		c.mu.Unlock()
		w.Header().Set("Content-Type", workerctl.ContentTypeStream)
		ev, _ := json.Marshal(workerctl.BackendInstallProgressEvent{OpID: req.OpID, Percentage: 50})
		_, _ = fmt.Fprintf(w, "{\"progress\":%s}\n", ev)
		reply, _ := json.Marshal(workerctl.BackendInstallReply{Success: true, Address: address, ProcessInstance: "i1", ReportsOperations: true})
		_, _ = fmt.Fprintf(w, "{\"reply\":%s}\n", reply)
	})
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbModelStop), func(w http.ResponseWriter, r *http.Request) {
		var req workerctl.ModelStopRequest
		body(r, &req)
		c.mu.Lock()
		c.stops = append(c.stops, req)
		c.mu.Unlock()
		// A repeat answers like the first: Terminated, never an error.
		_ = json.NewEncoder(w).Encode(workerctl.ModelStopReply{Matched: true, Terminated: true})
	})
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbModelOp), func(w http.ResponseWriter, r *http.Request) {
		var req workerctl.OperationRequest
		body(r, &req)
		c.mu.Lock()
		c.renewals++
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(workerctl.OperationReply{Renewed: req.Renew, Completed: req.Complete})
	})
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbFilesEnsure), func(w http.ResponseWriter, r *http.Request) {
		var req workerctl.FileEnsureRequest
		body(r, &req)
		c.mu.Lock()
		c.ensures = append(c.ensures, req)
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(workerctl.FileEnsureReply{LocalPath: "/cache/" + req.Key})
	})
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbBackendList), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"backends":[]}`)
	})
	mux.HandleFunc(workerctl.PathOf(workerctl.VerbModelsRunning), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[]}`)
	})
	return mux
}

func (c *controlPlane) seenStops() []workerctl.ModelStopRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.stops)
}

func (c *controlPlane) seenEnsures() []workerctl.FileEnsureRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ensures)
}

func (c *controlPlane) seenRenewals() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renewals
}

// replica is one frontend: its own tunnel registry, peer link, relay and carrier
// set, behind its own HTTP server, over the database that all replicas share.
type replica struct {
	id       string
	url      string
	tunnels  *tunnel.Registry
	sessions *tunnel.PeerSessions
	pool     *tunnel.PeerPool
	set      *carrier.Set
	active   *atomic.Pointer[carrier.Set]

	commands *carrier.Commands
	files    *carrier.Files
	clients  *carrier.Clients
	agents   *carrier.Agents
	queue    *carrier.WorkQueue
	bus      *carrier.Broadcaster

	// selector picks the agent workers, as the set does for the agent control.
	selector *nodes.AgentSelector
	// stopHeartbeat ends the heartbeat of the row of this replica, as the death
	// of its process does.
	stopHeartbeat context.CancelFunc
}

// startReplica starts one frontend: its own tunnel registry, peer link, relay and
// carrier set, behind its own HTTP server, over the database that all replicas
// share.
func startReplica(ctx context.Context, id string, db *gorm.DB, dsn string, clusterR *cluster.Registry, nodeReg *nodes.NodeRegistry, tweak ...func(*carrier.TunnelOptions)) *replica {
	GinkgoHelper()
	r := &replica{id: id}
	cred := cluster.NewPeerCredential()
	r.tunnels = tunnel.NewRegistry(clusterR, id)
	r.sessions = tunnel.NewPeerSessions(tunnel.NewRelay(r.tunnels).Stream)
	DeferCleanup(r.sessions.Close)
	r.pool = tunnel.NewPeerPool(id, cred, clusterR)
	DeferCleanup(r.pool.Close)

	e := echo.New()
	e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(nodeReg, r.tunnels))
	e.GET(tunnel.PeerPath, clusterapi.PeerHandler(clusterR, r.sessions.Accept))
	srv := httptest.NewServer(e)
	DeferCleanup(srv.Close)
	r.url = srv.URL
	Expect(clusterR.RegisterPeer(ctx, id, "test", 0, "", strings.TrimPrefix(srv.URL, "http://"), cred.Hash())).To(Succeed())

	// The row of the replica stays live while its process lives.
	beat, stop := context.WithCancel(ctx)
	r.stopHeartbeat = stop
	DeferCleanup(stop)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-beat.Done():
				return
			case <-t.C:
				_ = clusterR.Register(beat, id, "test", 0, "")
			}
		}
	}()

	fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: db, DSN: dsn})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(fan.Close)
	r.selector = nodes.NewAgentSelector(nodeReg, clusterR, id)
	options := carrier.TunnelOptions{
		Epoch:          1,
		Dialer:         tunnel.NewWorkerDialer(r.tunnels, r.pool),
		Fanout:         fan,
		WorkQueue:      jobs.NewClaimQueue(db, fan.Broadcaster),
		AgentSelector:  r.selector,
		Registry:       nodeReg,
		InstallTimeout: 30 * time.Second,
		UpgradeTimeout: 30 * time.Second,
		Token:          registrationToken,
	}
	for _, t := range tweak {
		t(&options)
	}
	set, err := carrier.NewTunnelSet(options)
	Expect(err).ToNot(HaveOccurred())
	r.set = set
	r.active = &atomic.Pointer[carrier.Set]{}
	r.active.Store(set)
	r.commands = carrier.NewCommands(r.active)
	r.files = carrier.NewFiles(r.active)
	r.clients = carrier.NewClients(r.active)
	r.agents = carrier.NewAgents(r.active)
	r.queue = carrier.NewWorkQueue(r.active)
	r.bus = carrier.NewBroadcaster(r.active)
	return r
}

const (
	registrationToken = "shared-registration-token"
	workerToken       = "own-tunnel-credential-of-w1"
)

func hashOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

var _ = Describe("The tunnel carrier through the holders, end to end", func() {
	var (
		ctx    context.Context
		db     *gorm.DB
		dsn    string
		nodeID string

		clusterR *cluster.Registry
		nodeReg  *nodes.NodeRegistry
		a, b     *replica

		backend  *fakeBackend
		control  *controlPlane
		grpcAddr string

		httpAddr   string
		stagingDir string
	)

	newReplica := func(id string) *replica {
		GinkgoHelper()
		return startReplica(ctx, id, db, dsn, clusterR, nodeReg)
	}

	// attach starts a worker tunnel to url. Its services reach the fake backend
	// and the HTTP server of the fake worker.
	attach := func(url string) *worker.Tunnel {
		GinkgoHelper()
		t, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: url,
			NodeID:      nodeID,
			Token:       func() string { return workerToken },
			Services: map[string]worker.LocalService{
				tunnel.StreamTagGRPC: func(ctx context.Context, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "tcp", grpcAddr)
				},
				tunnel.StreamTagHTTP: func(ctx context.Context, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "tcp", httpAddr)
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(t.Close)
		return t
	}

	ownerIs := func(r *replica) func() bool {
		return func() bool {
			owner, _, err := clusterR.Owner(ctx, nodeID)
			return err == nil && owner == r.id
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, dsn = testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		node := &nodes.BackendNode{Name: "w1", NodeType: nodes.NodeTypeBackend, TokenHash: hashOf(registrationToken)}
		Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
		nodeID = node.ID
		Expect(nodeReg.SetTunnelTokenHash(ctx, nodeID, hashOf(workerToken))).To(Succeed())

		// The fake worker: a gRPC backend and an HTTP server that serves the
		// control plane and the file transfer of the real worker.
		backend = &fakeBackend{loading: make(chan string, 1)}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		srv := ggrpc.NewServer()
		pb.RegisterBackendServer(srv, backend)
		go func() { _ = srv.Serve(lis) }()
		DeferCleanup(srv.Stop)
		grpcAddr = lis.Addr().String()

		control = &controlPlane{address: grpcAddr}
		stagingDir = GinkgoT().TempDir()
		hl, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		httpAddr = hl.Addr().String()
		Expect(hl.Close()).To(Succeed())
		httpSrv, err := nodes.StartFileTransferServerWithControl(httpAddr, stagingDir, stagingDir, stagingDir, registrationToken, 1<<30, nil, nil, control.handler())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { nodes.ShutdownFileTransferServer(httpSrv) })

		a, b = newReplica("replica-a"), newReplica("replica-b")
	})

	It("loads a model through the replica that holds the tunnel", func() {
		attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())

		var progress []float64
		reply, err := a.commands.InstallBackendOp(nodeID, "llama-cpp", "m", "", 0, "op-1", "load-1", time.Minute, func(ev workerctl.BackendInstallProgressEvent) {
			progress = append(progress, ev.Percentage)
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
		Expect(reply.Address).To(Equal(grpcAddr))
		Expect(progress).To(Equal([]float64{50}))

		client := a.clients.NewClient(nodeID, reply.Address, false)
		res, err := client.LoadModel(ctx, &pb.ModelOptions{Model: "ok"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Success).To(BeTrue())
		out, err := client.Predict(ctx, &pb.PredictOptions{Prompt: "hi"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out.Message)).To(Equal("echo: hi"))

		renewed, err := a.commands.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{"load-1"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(renewed.Renewed).To(ConsistOf("load-1"))
		Expect(a.commands.PingNode(nodeID)).To(Succeed())
	})

	It("loads a model through a replica that does not hold the tunnel, over the peer link", func() {
		attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())

		reply, err := b.commands.InstallBackendOp(nodeID, "llama-cpp", "m", "", 0, "op-2", "load-2", time.Minute, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())

		client := b.clients.NewClient(nodeID, reply.Address, false)
		res, err := client.LoadModel(ctx, &pb.ModelOptions{Model: "ok"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Success).To(BeTrue())
		Expect(a.sessions.Holds("replica-b")).To(BeTrue(), "the request crossed a link from the replica that does not hold the tunnel to the one that does")
		Expect(b.tunnels.Holds(nodeID)).To(BeFalse())
	})

	It("reports a failing load as the answer of the backend, and never as a missing route", func() {
		attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())

		for _, r := range []*replica{a, b} {
			client := r.clients.NewClient(nodeID, grpcAddr, false)
			res, err := client.LoadModel(ctx, &pb.ModelOptions{Model: "fails"})
			Expect(err).ToNot(HaveOccurred(), r.id)
			Expect(res.Success).To(BeFalse(), r.id)
			Expect(res.Message).To(Equal("out of memory"), r.id)
		}
	})

	It("cancels a load that hangs, and stops its operation twice without an error", func() {
		attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())

		loadCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			client := b.clients.NewClient(nodeID, grpcAddr, false)
			_, err := client.LoadModel(loadCtx, &pb.ModelOptions{Model: "hangs"})
			done <- err
		}()
		Eventually(backend.loading, "10s").Should(Receive(Equal("hangs")))

		cancel()
		var err error
		Eventually(done, "10s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Eventually(backend.cancelledModels, "10s").Should(ConsistOf("hangs"), "the cancellation must reach the backend of the worker through the relay and the tunnel")

		for range 2 {
			stop, err := b.commands.StopLoadOperation(ctx, nodeID, workerctl.ModelStopRequest{
				ModelName: "m", ProcessKey: "m#0", ExpectedAddress: grpcAddr, OperationID: "load-3", Force: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(stop.Terminated).To(BeTrue())
		}
		Expect(control.seenStops()).To(HaveLen(2))
	})

	It("keeps renewing across a worker that reconnects to the other replica", func() {
		first := attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())
		_, err := a.commands.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{"load-4"}})
		Expect(err).ToNot(HaveOccurred())

		// The worker loses replica A and dials replica B.
		Expect(first.Close()).To(Succeed())
		attach(b.url)
		Eventually(ownerIs(b), "20s", "50ms").Should(BeTrue())

		// Replica A holds nothing now, and reaches the worker over the link.
		Eventually(func() error {
			_, err := a.commands.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{"load-4"}})
			return err
		}, "20s", "100ms").Should(Succeed())
		Expect(a.tunnels.Holds(nodeID)).To(BeFalse())
		Expect(b.sessions.Holds("replica-a")).To(BeTrue())
		Expect(control.seenRenewals()).To(BeNumerically(">=", 2))
	})

	It("reports a worker that is connected nowhere as ErrNoRoute, within the cadence of a renewal", func() {
		begun := time.Now()
		_, err := a.commands.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue(), "%v", err)
		Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse(), "absence must not reach the caller")
		Expect(time.Since(begun)).To(BeNumerically("<", 5*time.Second))
		Expect(errors.Is(a.commands.PingNode(nodeID), nodes.ErrNoRoute)).To(BeTrue())
	})

	It("stages a file over the bulk lane, from either replica", func() {
		attach(a.url)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())
		Eventually(func() bool { return a.tunnels.Holds(nodeID) }, "10s").Should(BeTrue())

		src := filepath.Join(GinkgoT().TempDir(), "weights.bin")
		payload := make([]byte, 6<<20)
		for i := range payload {
			payload[i] = byte(i * 7)
		}
		Expect(os.WriteFile(src, payload, 0o600)).To(Succeed())

		for _, r := range []*replica{a, b} {
			var remote string
			Eventually(func() error {
				var err error
				remote, err = r.files.EnsureRemote(ctx, nodeID, src, "models/"+r.id+"/weights.bin")
				return err
			}, "30s", "200ms").Should(Succeed(), r.id)
			got, err := os.ReadFile(remote)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(payload), r.id)

			// A node that is forgotten loses its cached clients, and the next
			// transfer builds new ones.
			Expect(r.set.ForgetNode).ToNot(BeNil())
			r.set.ForgetNode(nodeID)
			_, err = r.files.EnsureRemote(ctx, nodeID, src, "models/"+r.id+"/again.bin")
			Expect(err).ToNot(HaveOccurred(), r.id)
		}
	})

	It("keeps a small call fast while a large transfer runs on the same worker", func() {
		// The worker reaches the frontend through a link with a rate and a delay,
		// as a worker in another network does.
		link, err := slowlink.New(strings.TrimPrefix(a.url, "http://"), 8e6, 10*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(link.Close)
		attach("http://" + link.Addr())
		Eventually(ownerIs(a), "20s", "50ms").Should(BeTrue())
		Eventually(func() bool { return a.tunnels.Holds(nodeID) }, "10s").Should(BeTrue())
		// The bulk lane joins a moment after the inference lane.
		time.Sleep(time.Second)

		src := filepath.Join(GinkgoT().TempDir(), "big.bin")
		Expect(os.WriteFile(src, make([]byte, 32<<20), 0o600)).To(Succeed())

		// percentile is the 95th percentile of a set of samples. The 99th of a few
		// dozen samples is the largest one, which a single scheduling hiccup of a
		// loaded machine decides.
		percentile := func(samples []time.Duration) time.Duration {
			sorted := slices.Clone(samples)
			slices.Sort(sorted)
			return sorted[min(len(sorted)-1, len(sorted)*95/100)]
		}

		// probe is one call on the inference lane.
		probe := func() (time.Duration, error) {
			begun := time.Now()
			_, err := a.commands.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
			return time.Since(begun), err
		}

		// The baseline of the lane: the same probe while nothing else runs. Both
		// runs below are judged against it, so that a slow machine moves the bound
		// with it.
		var idle []time.Duration
		for range 60 {
			took, err := probe()
			Expect(err).ToNot(HaveOccurred())
			idle = append(idle, took)
			time.Sleep(10 * time.Millisecond)
		}
		idleP95 := percentile(idle)

		// probeDuring probes the inference lane while transfer runs, and returns
		// the 95th percentile of the probes and the time of the transfer.
		probeDuring := func(transfer func() error) (time.Duration, time.Duration) {
			GinkgoHelper()
			var (
				samples []time.Duration
				stop    = make(chan struct{})
				wg      sync.WaitGroup
			)
			wg.Go(func() {
				for {
					select {
					case <-stop:
						return
					case <-time.After(10 * time.Millisecond):
					}
					took, err := probe()
					if err != nil {
						return
					}
					samples = append(samples, took)
				}
			})
			begun := time.Now()
			err := transfer()
			elapsed := time.Since(begun)
			close(stop)
			wg.Wait()
			Expect(err).ToNot(HaveOccurred())
			Expect(elapsed).To(BeNumerically(">", 500*time.Millisecond), "the transfer must last long enough to be measured")
			Expect(len(samples)).To(BeNumerically(">=", 30), "probes completed during the transfer: %d in %v", len(samples), elapsed)
			return percentile(samples), elapsed
		}

		// The transfer on the bulk lane, as the set does it.
		bulkP95, bulkTime := probeDuring(func() error {
			_, err := a.files.EnsureRemote(ctx, nodeID, src, "models/big.bin")
			return err
		})
		// The same transfer on the lane of the probe, which is what the set
		// would do if it fell back. The comparison is between two runs on the
		// same machine, so a loaded runner moves both.
		shared := nodes.NewHTTPFileStager(func(id string) (string, error) { return nodes.WorkerHTTPHost(id, ""), nil }, registrationToken, a.set.Dialer)
		sharedP95, sharedTime := probeDuring(func() error {
			_, err := shared.EnsureRemote(ctx, nodeID, src, "models/big-shared.bin")
			return err
		})
		AddReportEntry("probe during a transfer", map[string]any{
			"idle lane p95 ms": idleP95.Milliseconds(),
			"bulk lane p95 ms": bulkP95.Milliseconds(), "bulk transfer s": bulkTime.Seconds(),
			"shared lane p95 ms": sharedP95.Milliseconds(), "shared transfer s": sharedTime.Seconds(),
		})
		Expect(bulkP95).To(BeNumerically("<", sharedP95*8/10),
			"a probe must wait less behind a transfer on the bulk lane than behind one on its own lane: bulk %v, shared %v", bulkP95, sharedP95)
		Expect(bulkP95).To(BeNumerically("<", idleP95*5+100*time.Millisecond),
			"a transfer on the bulk lane must not slow a probe much beyond the idle lane: idle %v, bulk %v", idleP95, bulkP95)
	})

	It("refuses a transfer when the worker has no bulk session, and sends nothing on the other lane", func() {
		// A worker that dials the inference lane only: the case of a worker that
		// predates the bulk lane, or one whose bulk session is down.
		inferenceOnly := dialInferenceOnly(a.url, nodeID, workerToken, httpAddr)
		DeferCleanup(inferenceOnly)
		Eventually(ownerIs(a), "15s", "50ms").Should(BeTrue())

		src := filepath.Join(GinkgoT().TempDir(), "x.bin")
		Expect(os.WriteFile(src, []byte("bytes"), 0o600)).To(Succeed())
		for _, r := range []*replica{a, b} {
			_, err := r.files.EnsureRemote(ctx, nodeID, src, "models/x.bin")
			Expect(err).To(HaveOccurred(), r.id)
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeFalse(), "%s: a lane that is down must not demote a worker whose model calls work: %v", r.id, err)
		}
		Expect(filepath.Join(stagingDir, "models", "x.bin")).ToNot(BeAnExistingFile())
		// The inference lane still carries a control call.
		Expect(a.commands.PingNode(nodeID)).To(Succeed())
	})

	It("stages a file through shared object storage, with only the verbs on the tunnel, when the set is built for it", func() {
		store, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		fm, err := storage.NewFileManager(store, GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		s3 := startReplica(ctx, "replica-s3", db, dsn, clusterR, nodeReg, func(o *carrier.TunnelOptions) {
			o.S3Staging, o.FileManager = true, fm
		})
		attach(s3.url)
		Eventually(ownerIs(s3), "15s", "50ms").Should(BeTrue())

		src := filepath.Join(GinkgoT().TempDir(), "x.bin")
		Expect(os.WriteFile(src, []byte("shared bytes"), 0o600)).To(Succeed())
		remote, err := s3.files.EnsureRemote(ctx, nodeID, src, "models/s3/x.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(remote).To(Equal("/cache/models/s3/x.bin"))

		exists, err := store.Exists(ctx, "models/s3/x.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue(), "the frontend put the file in the shared store")
		Expect(control.seenEnsures()).To(Equal([]workerctl.FileEnsureRequest{{Key: "models/s3/x.bin"}}))
		Expect(filepath.Join(stagingDir, "models", "s3", "x.bin")).ToNot(BeAnExistingFile(), "no bytes went over the tunnel")
	})

	It("refuses to build a set that lacks a member of this carrier", func() {
		whole := carrier.TunnelOptions{
			Dialer: tunnel.NewWorkerDialer(a.tunnels, a.pool), Fanout: &carrier.Fanout{},
			WorkQueue: a.queue, AgentSelector: a.selector,
		}
		for name, mutate := range map[string]func(*carrier.TunnelOptions){
			"no dialer":                  func(o *carrier.TunnelOptions) { o.Dialer = nil },
			"no fan-out":                 func(o *carrier.TunnelOptions) { o.Fanout = nil },
			"no queue":                   func(o *carrier.TunnelOptions) { o.WorkQueue = nil },
			"no agent selector":          func(o *carrier.TunnelOptions) { o.AgentSelector = nil },
			"S3 staging with no manager": func(o *carrier.TunnelOptions) { o.S3Staging = true },
		} {
			o := whole
			mutate(&o)
			_, err := carrier.NewTunnelSet(o)
			Expect(err).To(HaveOccurred(), name)
		}
	})

	It("carries broadcasts between the replicas through the holders", func() {
		subject := messagingtest.BroadcastRootSubjects["state"]
		var got atomic.Int32
		_, err := carrier.NewBroadcaster(b.active).Subscribe(subject, func([]byte) { got.Add(1) })
		Expect(err).ToNot(HaveOccurred())
		Expect(carrier.NewBroadcaster(a.active).Publish(subject, map[string]string{"k": "v"})).To(Succeed())
		Eventually(got.Load, "10s").Should(Equal(int32(1)))
	})
})

// dialInferenceOnly connects a fake worker that holds the inference lane and no
// bulk lane. It serves the http tag from httpAddr. The returned function closes
// it.
func dialInferenceOnly(frontend, nodeID, token, httpAddr string) func() {
	GinkgoHelper()
	url := "ws" + strings.TrimPrefix(frontend, "http") + tunnel.ConnectPath + "?id=" + nodeID
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	ws, _, err := tunnel.NewDialer(5*time.Second).Dial(url, header)
	Expect(err).ToNot(HaveOccurred())
	sess, err := tunnel.ClientSession(ws, tunnel.LaneInference)
	Expect(err).ToNot(HaveOccurred())
	go func() {
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				tag, _, err := tunnel.ReadStreamRequest(st)
				if err != nil || tag != tunnel.StreamTagHTTP {
					_ = tunnel.WriteStreamRefusal(st, tunnel.ErrStreamTagUnknown)
					_ = st.Close()
					return
				}
				local, err := net.Dial("tcp", httpAddr)
				if err != nil {
					_ = tunnel.WriteStreamRefusal(st, tunnel.ErrStreamTargetUnavailable)
					_ = st.Close()
					return
				}
				_ = tunnel.WriteStreamAccepted(st)
				_ = tunnel.Splice(st, local)
			}()
		}
	}()
	return func() { _ = sess.Close() }
}

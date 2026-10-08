package carrier_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	ggrpc "google.golang.org/grpc"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	switchNATSOnce sync.Once
	switchNATSURL  string
	switchNATSCtr  testcontainers.Container
	switchNATSErr  error
)

// startSwitchNATS starts one NATS server for the process.
func startSwitchNATS() (string, error) {
	switchNATSOnce.Do(func() {
		ctx := context.Background()
		var ctr testcontainers.Container
		ctr, switchNATSErr = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "nats:2.10-alpine",
				ExposedPorts: []string{"4222/tcp"},
				WaitingFor:   wait.ForListeningPort("4222/tcp"),
			},
			Started: true,
		})
		if switchNATSErr != nil {
			return
		}
		switchNATSCtr = ctr
		host, err := ctr.Host(ctx)
		if err != nil {
			switchNATSErr = err
			return
		}
		port, err := ctr.MappedPort(ctx, "4222/tcp")
		if err != nil {
			switchNATSErr = err
			return
		}
		switchNATSURL = fmt.Sprintf("nats://%s:%s", host, port.Port())
	})
	return switchNATSURL, switchNATSErr
}

var _ = AfterSuite(func() {
	if switchNATSCtr != nil {
		_ = switchNATSCtr.Terminate(context.Background())
	}
})

// runLog records every run of a job and can hold a run until a spec lets it go.
type runLog struct {
	mu    sync.Mutex
	runs  map[string][]string
	gates map[string]chan struct{}
}

func newRunLog() *runLog {
	return &runLog{runs: map[string][]string{}, gates: map[string]chan struct{}{}}
}

func (r *runLog) hold(jobID string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := make(chan struct{})
	r.gates[jobID] = g
	return g
}

func (r *runLog) ranOn(jobID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.runs[jobID])
}

func (r *runLog) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.runs {
		n += len(v)
	}
	return n
}

func (r *runLog) handler(label string) messaging.WorkHandler {
	return func(ctx context.Context, payload []byte, events messaging.Publisher) error {
		var evt jobs.JobEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			return err
		}
		r.mu.Lock()
		r.runs[evt.JobID] = append(r.runs[evt.JobID], label)
		gate := r.gates[evt.JobID]
		r.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		jobs.PublishJobResult(events, evt.JobID, "completed", label, "")
		return nil
	}
}

// tally counts what a subscriber heard, by message.
type tally struct {
	mu sync.Mutex
	by map[string]int
}

func newTally() *tally { return &tally{by: map[string]int{}} }

func (t *tally) handler(raw []byte) {
	var msg string
	if err := json.Unmarshal(raw, &msg); err != nil {
		msg = string(raw)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.by[msg]++
}

func (t *tally) get(msg string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.by[msg]
}

func (t *tally) copy() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int, len(t.by))
	for k, v := range t.by {
		out[k] = v
	}
	return out
}

const chatterSubject = "state.switch-spec"

// rigWork counts the jobs that run, which is all the specs read of the work in
// flight.
type rigWork struct{ db *gorm.DB }

func (w rigWork) InFlight(ctx context.Context) (cluster.InFlight, error) {
	var n int64
	err := w.db.WithContext(ctx).Model(&jobs.JobRecord{}).Where("status = ?", "running").Count(&n).Error
	return cluster.InFlight{Jobs: int(n)}, err
}

// rigNATS is what the frontends of the rig hand over about NATS.
type rigNATS struct{ url string }

func (r rigNATS) WorkerURL(context.Context) (string, error) { return r.url, nil }
func (r rigNATS) CAPEM() (string, error)                    { return "", nil }
func (r rigNATS) ClientTLS() bool                           { return false }

// switchRig is a cluster in one process: a database, a NATS server, workers that
// run the code of a real worker to attach to either carrier, agent workers that
// do the same, and frontend replicas that are built the way the application
// builds them.
type switchRig struct {
	ctx      context.Context
	db       *gorm.DB
	dsn      string
	natsURL  string
	store    *cluster.CarrierStore
	clusterR *cluster.Registry
	nodeReg  *nodes.NodeRegistry
	jobStore *jobs.JobStore
	runs     *runLog

	timings atomic.Pointer[cluster.Timings]

	replicas []*switchReplica

	// onMove, when set, is called by the replica whose switch made a move of the
	// row. A spec uses it to kill the leader between two moves.
	onMove atomic.Pointer[func(r *switchReplica, row cluster.CarrierRow)]
}

// workerProc is a backend worker: the follower and the planes of a real worker
// around a fake backend that serves gRPC. The backend outlives the worker, as a
// backend process does when its worker restarts.
type workerProc struct {
	rig      *switchRig
	name     string
	nodeID   string
	backend  *fakeBackend
	grpcAddr string
	httpAddr string
	dir      string
	httpSrv  *http.Server
	fol      *worker.Follower
	cancel   context.CancelFunc

	mu          sync.Mutex
	installs    map[cluster.Carrier]int
	renewals    map[cluster.Carrier]int
	installGate chan struct{}
	installDone chan error
}

type workerOpts struct {
	// addr starts the worker with an address, which makes it dual-capable.
	addr bool
	// reuse starts a worker around the backend and the ports of one that stopped.
	reuse *workerProc
	// maxDelay is the longest random wait before the worker attaches.
	maxDelay time.Duration
}

func (w *workerProc) count(m map[cluster.Carrier]int, c cluster.Carrier) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return m[c]
}
func (w *workerProc) installsOn(c cluster.Carrier) int { return w.count(w.installs, c) }
func (w *workerProc) renewalsOn(c cluster.Carrier) int { return w.count(w.renewals, c) }

// verbs serves the verbs of a load on the carrier it is given, and counts them.
func (w *workerProc) verbs(v worker.VerbServer) error {
	c := v.Carrier()
	if err := v.HandleWithProgress(workerctl.VerbBackendInstall, func(_ context.Context, body []byte, progress func(workerctl.BackendInstallProgressEvent)) (any, error) {
		var req workerctl.BackendInstallRequest
		_ = json.Unmarshal(body, &req)
		w.mu.Lock()
		w.installs[c]++
		w.mu.Unlock()
		progress(workerctl.BackendInstallProgressEvent{OpID: req.OpID, Percentage: 50})
		if req.OperationID == "load-slow" {
			<-w.installGate
		}
		return workerctl.BackendInstallReply{Success: true, Address: w.grpcAddr, ProcessInstance: "i1", ReportsOperations: true}, nil
	}); err != nil {
		return err
	}
	if err := v.Handle(workerctl.VerbModelOp, func(_ context.Context, body []byte) (any, error) {
		var req workerctl.OperationRequest
		_ = json.Unmarshal(body, &req)
		w.mu.Lock()
		w.renewals[c]++
		w.mu.Unlock()
		return workerctl.OperationReply{Renewed: req.Renew, Completed: req.Complete}, nil
	}); err != nil {
		return err
	}
	if err := v.Handle(workerctl.VerbModelStop, func(context.Context, []byte) (any, error) {
		return workerctl.ModelStopReply{Matched: true, Terminated: true}, nil
	}); err != nil {
		return err
	}
	if err := v.Handle(workerctl.VerbBackendList, func(context.Context, []byte) (any, error) {
		return workerctl.BackendListReply{}, nil
	}); err != nil {
		return err
	}
	return v.Handle(workerctl.VerbModelsRunning, func(context.Context, []byte) (any, error) {
		return workerctl.ModelsRunningReply{}, nil
	})
}

// stop ends the worker process: its follower and its HTTP server. The backend
// keeps serving.
func (w *workerProc) stop() {
	w.cancel()
	w.fol.Close()
	if w.httpSrv != nil {
		nodes.ShutdownFileTransferServer(w.httpSrv)
		w.httpSrv = nil
	}
}

// startWorker registers a worker with a frontend and runs it the way Run does:
// the plane, the verbs, the attachment to the carrier the frontend names, and the
// follower.
func (rig *switchRig) startWorker(name string, o workerOpts) *workerProc {
	GinkgoHelper()
	w := &workerProc{rig: rig, name: name, installs: map[cluster.Carrier]int{}, renewals: map[cluster.Carrier]int{},
		installGate: make(chan struct{}), installDone: make(chan error, 1)}
	if o.reuse != nil {
		w.backend, w.grpcAddr, w.httpAddr, w.dir = o.reuse.backend, o.reuse.grpcAddr, o.reuse.httpAddr, o.reuse.dir
		w.installGate, w.installDone = o.reuse.installGate, o.reuse.installDone
	} else {
		w.backend = &fakeBackend{loading: make(chan string, 1), gate: make(chan struct{})}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		gs := ggrpc.NewServer()
		pb.RegisterBackendServer(gs, w.backend)
		go func() { _ = gs.Serve(lis) }()
		DeferCleanup(gs.Stop)
		w.grpcAddr = lis.Addr().String()
		hl, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		w.httpAddr = hl.Addr().String()
		Expect(hl.Close()).To(Succeed())
		w.dir = GinkgoT().TempDir()
	}
	ctx, cancel := context.WithCancel(rig.ctx)
	w.cancel = cancel
	front := rig.replicas[0].url
	regClient := &workerregistry.RegistrationClient{FrontendURL: front, RegistrationToken: registrationToken}

	var routable atomic.Bool
	routable.Store(o.addr)
	body := func() map[string]any {
		b := map[string]any{"name": name, "token": registrationToken, "routable": routable.Load(), "version": "switch-spec"}
		if routable.Load() || rig.row().Active == cluster.CarrierNATS {
			b["address"], b["http_address"] = w.grpcAddr, w.httpAddr
		}
		return b
	}
	credMgr := workerregistry.NewCredentialManager(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
		return regClient.RegisterFull(ctx, body())
	}, false)
	res, err := credMgr.Acquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	w.nodeID = res.ID
	boot := cluster.CarrierNATS
	if res.Carrier == "tunnel" {
		boot = cluster.CarrierTunnel
	} else {
		routable.Store(true)
	}

	cfg := &worker.Config{RegisterTo: front, RegistrationToken: registrationToken, ServeAddr: w.grpcAddr}
	if o.addr {
		cfg.Addr = w.grpcAddr
	}
	plane := worker.NewBackendPlane(cfg, w.nodeID, w.httpAddr, worker.NATSLocal{})
	plane.Serve(w.verbs)
	w.httpSrv, err = nodes.StartFileTransferServerWithControl(w.httpAddr, w.dir, w.dir, w.dir, registrationToken, 1<<30, nil, nil, plane.Control())
	Expect(err).ToNot(HaveOccurred())

	delay := cmp.Or(o.maxDelay, 300*time.Millisecond)
	w.fol = worker.NewFollower(worker.FollowerOptions{
		Attachers: plane.Attachers(),
		Credentials: func(c cluster.Carrier) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
				b := body()
				b["carrier"] = string(c)
				return regClient.RegisterFull(ctx, b)
			}, false, string(c))
		},
		Heartbeat: func(ctx context.Context, b map[string]any) (*workerregistry.HeartbeatReply, error) {
			return regClient.HeartbeatFull(ctx, w.nodeID, b)
		},
		Cannot:   plane.Cannot(routable.Load()),
		Interval: 300 * time.Millisecond,
		MaxDelay: delay,
	})
	_, err = w.fol.Attach(ctx, boot, credMgr, res)
	Expect(err).ToNot(HaveOccurred())
	go w.fol.Run(ctx)
	DeferCleanup(w.stop)
	return w
}

// agentProc is an agent worker around the follower of a real one.
type agentProc struct {
	nodeID string
	fol    *worker.Follower
	cancel context.CancelFunc
}

func (a *agentProc) stop() {
	a.cancel()
	a.fol.Close()
}

// startAgent registers an agent worker and runs it the way the agent worker
// command does. Every job it runs is recorded with the carrier it ran on.
func (rig *switchRig) startAgent(name string) *agentProc {
	GinkgoHelper()
	ctx, cancel := context.WithCancel(rig.ctx)
	a := &agentProc{cancel: cancel}
	front := rig.replicas[0].url
	regClient := &workerregistry.RegistrationClient{FrontendURL: front, RegistrationToken: registrationToken}
	body := func() map[string]any {
		return map[string]any{"name": name, "node_type": "agent", "token": registrationToken, "version": "switch-spec"}
	}
	credMgr := workerregistry.NewCredentialManager(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
		return regClient.RegisterFull(ctx, body())
	}, false)
	res, err := credMgr.Acquire(ctx)
	Expect(err).ToNot(HaveOccurred())
	a.nodeID = res.ID
	boot := cluster.CarrierNATS
	if res.Carrier == "tunnel" {
		boot = cluster.CarrierTunnel
	}
	serve := agentworker.FollowConfig{
		NodeID: a.nodeID, FrontendURL: front, ControlToken: registrationToken,
		Subject: "agent.execute", Queue: "agent-workers", APIURL: "http://127.0.0.1:1",
		Tool: func(context.Context, mcpRemote.MCPToolRequest) mcpRemote.MCPToolResponse {
			return mcpRemote.MCPToolResponse{}
		},
		Discovery: func(context.Context, mcpRemote.MCPDiscoveryRequest) mcpRemote.MCPDiscoveryResponse {
			return mcpRemote.MCPDiscoveryResponse{}
		},
		BackendStop: func(string) {},
		Jobs: func(ctx context.Context, on cluster.Carrier, consumer messaging.WorkConsumer) error {
			_, err := consumer.Consume(ctx, messaging.WorkMCPCI, 0, rig.runs.handler(string(on)))
			return err
		},
	}
	a.fol = worker.NewFollower(worker.FollowerOptions{
		Attachers: serve.Attachers(),
		Credentials: func(c cluster.Carrier) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
				b := body()
				b["carrier"] = string(c)
				return regClient.RegisterFull(ctx, b)
			}, false, string(c))
		},
		Heartbeat: func(ctx context.Context, b map[string]any) (*workerregistry.HeartbeatReply, error) {
			return regClient.HeartbeatFull(ctx, a.nodeID, b)
		},
		Cannot:   serve.Cannot(),
		Interval: 300 * time.Millisecond,
		MaxDelay: 300 * time.Millisecond,
	})
	_, err = a.fol.Attach(ctx, boot, credMgr, res)
	Expect(err).ToNot(HaveOccurred())
	go a.fol.Run(ctx)
	DeferCleanup(a.stop)
	return a
}

// attachedOf says where the frontends believe a node is attached, which is what
// the node reported in its last heartbeat.
func (rig *switchRig) attachedOf(nodeID string) []cluster.Carrier {
	GinkgoHelper()
	got, err := nodes.NewSwitchWorkers(rig.nodeReg, time.Hour).AttachedCarriers(rig.ctx, nodeID)
	Expect(err).ToNot(HaveOccurred())
	return got
}

// reports waits until a node has reported what it can follow.
func (rig *switchRig) reports(nodeID string) {
	GinkgoHelper()
	Eventually(func() string {
		n, err := rig.nodeReg.Get(rig.ctx, nodeID)
		if err != nil {
			return ""
		}
		return n.Follow
	}, "20s", "50ms").ShouldNot(BeEmpty(), "the node reports what it can follow")
}

type switchReplica struct {
	rig       *switchRig
	id        string
	url       string
	tunnels   *tunnel.Registry
	sessions  *tunnel.PeerSessions
	pool      *tunnel.PeerPool
	cur       atomic.Pointer[carrier.Set]
	bus       *carrier.Broadcaster
	commands  *carrier.Commands
	clients   *carrier.Clients
	queue     *carrier.WorkQueue
	window    *carrier.Window
	swapper   *carrier.Swapper
	sw        *cluster.Switch
	member    *cluster.Membership
	cancel    context.CancelFunc
	chatter   *tally
	dispatch  *jobs.Dispatcher
	mu        sync.Mutex
	builds    map[cluster.Carrier]int
	natsConns []*messaging.Client
	failBuild map[cluster.Carrier]error
	holdBuild map[cluster.Carrier]chan struct{}
}

func (r *switchReplica) active() cluster.Carrier { return r.cur.Load().Name }

func (r *switchReplica) buildCount(c cluster.Carrier) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builds[c]
}

func (r *switchReplica) build(ctx context.Context, row cluster.CarrierRow, target cluster.Carrier) (*carrier.Set, error) {
	rig := r.rig
	r.mu.Lock()
	err, hold := r.failBuild[target], r.holdBuild[target]
	r.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.builds[target]++
	r.mu.Unlock()
	switch target {
	case cluster.CarrierNATS:
		client, err := messaging.New(rig.natsURL)
		if err != nil {
			return nil, err
		}
		for deadline := time.Now().Add(10 * time.Second); !client.IsConnected(); {
			if time.Now().After(deadline) {
				client.Close()
				return nil, errors.New("the NATS server did not answer")
			}
			time.Sleep(20 * time.Millisecond)
		}
		r.mu.Lock()
		r.natsConns = append(r.natsConns, client)
		r.mu.Unlock()
		return carrier.NewNATSSet(carrier.NATSOptions{
			Client: client, Epoch: row.Epoch, Registry: rig.nodeReg,
			InstallTimeout: 30 * time.Second, UpgradeTimeout: 30 * time.Second, Token: registrationToken,
			NodeHasAddress: func(nodeID string) (bool, error) {
				n, err := rig.nodeReg.Get(context.Background(), nodeID)
				if err != nil {
					return false, err
				}
				return n.Address != "", nil
			},
		})
	case cluster.CarrierTunnel:
		fan, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: rig.db, DSN: rig.dsn})
		if err != nil {
			return nil, err
		}
		return carrier.NewTunnelSet(carrier.TunnelOptions{
			Epoch:          row.Epoch,
			Dialer:         tunnel.NewWorkerDialer(r.tunnels, r.pool),
			Fanout:         fan,
			WorkQueue:      jobs.NewClaimQueue(rig.db, fan.Broadcaster),
			AgentSelector:  nodes.NewAgentSelector(rig.nodeReg, rig.clusterR, r.id),
			Registry:       rig.nodeReg,
			InstallTimeout: 30 * time.Second, UpgradeTimeout: 30 * time.Second, Token: registrationToken,
			Claims: &carrier.ClaimWork{
				DB: rig.db, Owner: r.id, Store: rig.jobStore,
				Bus:      func() messaging.Broadcaster { return r.bus },
				Interval: 100 * time.Millisecond,
			},
		})
	}
	return nil, fmt.Errorf("unknown carrier %q", target)
}

// kill stops everything the process of the replica was doing, without telling
// anyone: it does not leave the instances table and does not close its carrier.
func (r *switchReplica) kill() {
	r.cancel()
	Expect(r.rig.db.Exec(`UPDATE instances SET last_seen = now() - interval '1 hour' WHERE id = ?`, r.id).Error).To(Succeed())
}

func (rig *switchRig) startReplica(id string) *switchReplica {
	GinkgoHelper()
	ctx, cancel := context.WithCancel(rig.ctx)
	r := &switchReplica{rig: rig, id: id, cancel: cancel, chatter: newTally(),
		builds: map[cluster.Carrier]int{}, failBuild: map[cluster.Carrier]error{}, holdBuild: map[cluster.Carrier]chan struct{}{}}
	DeferCleanup(cancel)

	cred := cluster.NewPeerCredential()
	r.tunnels = tunnel.NewRegistry(rig.clusterR, id)
	r.sessions = tunnel.NewPeerSessions(tunnel.NewRelay(r.tunnels).Stream)
	DeferCleanup(r.sessions.Close)
	r.pool = tunnel.NewPeerPool(id, cred, rig.clusterR)
	DeferCleanup(r.pool.Close)
	e := echo.New()
	e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(rig.nodeReg, r.tunnels))
	e.GET(tunnel.PeerPath, clusterapi.PeerHandler(rig.clusterR, r.sessions.Accept))
	// The routes a worker registers and heartbeats on, as the application mounts
	// them.
	nodeOpts := []localai.RegisterOption{
		localai.WithCarrierReader(rig.store), localai.WithTunnelDisconnector(r.tunnels),
		localai.WithNATSHandover(rigNATS{url: rig.natsURL}),
	}
	e.POST("/api/node/register", localai.RegisterNodeEndpoint(rig.nodeReg, registrationToken, true, nil, "", natsauth.Config{}, nodeOpts...))
	e.POST("/api/node/:id/heartbeat", localai.HeartbeatEndpoint(rig.nodeReg, nodeOpts...))
	srv := httptest.NewServer(e)
	DeferCleanup(srv.Close)
	r.url = srv.URL

	r.member = cluster.NewMembership(rig.clusterR, id, "switch-spec")
	r.member.SetPeer(strings.TrimPrefix(srv.URL, "http://"), cred)
	r.member.SetReclaimer(r.tunnels)
	Expect(r.member.Start(ctx)).To(Succeed())

	// The replica starts on the carrier the row names.
	row, err := rig.store.Get(ctx)
	Expect(err).ToNot(HaveOccurred())
	first, err := r.build(ctx, row, row.Active)
	Expect(err).ToNot(HaveOccurred())
	r.cur.Store(first)
	r.bus = carrier.NewBroadcaster(&r.cur)

	workers := nodes.NewSwitchWorkers(rig.nodeReg, time.Hour)
	r.window = carrier.NewWindow(func(ctx context.Context, nodeID string) (carrier.Attachment, error) {
		cs, err := workers.AttachedCarriers(ctx, nodeID)
		if err != nil {
			return carrier.Attachment{}, err
		}
		var a carrier.Attachment
		for _, c := range cs {
			a.NATS = a.NATS || c == cluster.CarrierNATS
			a.Tunnel = a.Tunnel || c == cluster.CarrierTunnel
		}
		return a, nil
	}, workers.AgentsAttached)
	r.commands = carrier.NewCommands(&r.cur)
	r.commands.UseWindow(r.window)
	r.clients = carrier.NewClients(&r.cur)
	r.clients.UseWindow(r.window)
	r.queue = carrier.NewWorkQueue(&r.cur)

	var err2 error
	r.sw, err2 = cluster.NewSwitch(cluster.SwitchOptions{
		Store: rig.store, Registry: rig.clusterR, Workers: workers, Work: rigWork{db: rig.db},
		Timings:            func() cluster.Timings { return *rig.timings.Load() },
		AvailabilityMaxAge: time.Minute,
		OnChange: func(row cluster.CarrierRow) {
			go func() { _ = r.bus.Publish(messaging.SubjectCarrierChanged, row) }()
			if hook := rig.onMove.Load(); hook != nil {
				(*hook)(r, row)
			}
		},
	})
	Expect(err2).ToNot(HaveOccurred())

	r.swapper, err2 = carrier.NewSwapper(carrier.SwapperOptions{
		Cur: &r.cur, Bus: r.bus, Window: r.window, Rows: rig.store, Ready: r.member, Build: r.build,
		Interval: 200 * time.Millisecond, Settle: 300 * time.Millisecond,
	})
	Expect(err2).ToNot(HaveOccurred())
	DeferCleanup(r.swapper.Close)

	// The chatter every replica listens to, and the dispatcher that records the
	// result of a job, both through the holders.
	_, err = r.bus.Subscribe(chatterSubject, r.chatter.handler)
	Expect(err).ToNot(HaveOccurred())
	_, err = r.bus.Subscribe(messaging.SubjectCarrierChanged, func([]byte) { r.swapper.Wake() })
	Expect(err).ToNot(HaveOccurred())
	r.dispatch = jobs.NewDispatcher(rig.jobStore, r.queue, r.bus, rig.db, id)
	Expect(r.dispatch.Start(ctx)).To(Succeed())
	DeferCleanup(r.dispatch.Stop)

	go r.swapper.Run(ctx)
	go advisorylock.RunLeaderLoop(ctx, rig.db, advisorylock.KeyCarrierSwitch, 200*time.Millisecond, func() { _ = r.sw.Drive(ctx) })
	rig.replicas = append(rig.replicas, r)
	return r
}

// availability reports that every live replica can build both carriers.
func (rig *switchRig) availability() {
	GinkgoHelper()
	live, err := rig.clusterR.ListLive(rig.ctx, cluster.InstanceLiveness)
	Expect(err).ToNot(HaveOccurred())
	for _, in := range live {
		Expect(rig.clusterR.ReportAvailability(rig.ctx, in.ID, map[cluster.Carrier]string{cluster.CarrierNATS: "", cluster.CarrierTunnel: ""})).To(Succeed())
	}
}

func (rig *switchRig) row() cluster.CarrierRow {
	GinkgoHelper()
	r, err := rig.store.Get(rig.ctx)
	Expect(err).ToNot(HaveOccurred())
	return r
}

func (rig *switchRig) newJob() string {
	GinkgoHelper()
	task := &jobs.TaskRecord{UserID: "u1", Name: "t", Model: "m", Enabled: true}
	Expect(rig.jobStore.CreateTask(task)).To(Succeed())
	job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "running", TriggeredBy: "manual"}
	Expect(rig.jobStore.CreateJob(job)).To(Succeed())
	return job.ID
}

func (rig *switchRig) enqueue(via *switchReplica, jobID string) {
	GinkgoHelper()
	job, err := rig.jobStore.GetJob(jobID)
	Expect(err).ToNot(HaveOccurred())
	Expect(via.queue.Enqueue(rig.ctx, messaging.WorkMCPCI, jobs.JobEvent{JobID: jobID, TaskID: job.TaskID, UserID: "u1"})).To(Succeed())
}

func (rig *switchRig) status(jobID string) string {
	job, err := rig.jobStore.GetJob(jobID)
	if err != nil {
		return err.Error()
	}
	return job.Status
}

func (rig *switchRig) completed(ids ...string) func() bool {
	return func() bool {
		for _, id := range ids {
			if rig.status(id) != "completed" {
				return false
			}
		}
		return true
	}
}

// request asks for a change as the admin API does: replicas have looked, then the
// preflight runs, then the row moves to prepare.
func (rig *switchRig) request(target cluster.Carrier, force bool) (cluster.CarrierRow, cluster.Report, error) {
	rig.availability()
	return rig.replicas[0].sw.Request(rig.ctx, cluster.Request{Target: target, By: "admin", Force: force})
}

// listeners counts the LISTEN sessions of the pgbus carrier on the database.
func (rig *switchRig) listeners() int64 {
	GinkgoHelper()
	var n int64
	Expect(rig.db.Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'localai_pgbus_%'").Scan(&n).Error).To(Succeed())
	return n
}

func (rig *switchRig) eventually(f func() bool, what string) {
	GinkgoHelper()
	Eventually(f, "60s", "50ms").Should(BeTrue(), what)
}

var _ = Describe("A change of carrier, end to end", Ordered, func() {
	var rig *switchRig

	BeforeEach(func() {
		url, err := startSwitchNATS()
		if err != nil {
			if os.Getenv("CI") != "" && runtime.GOOS != "darwin" {
				Fail("testcontainers requires Docker and CI is set: " + err.Error())
			}
			Skip("testcontainers requires Docker: " + err.Error())
		}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		rig = &switchRig{ctx: ctx, natsURL: url, runs: newRunLog()}
		rig.timings.Store(&cluster.Timings{PrepareTimeout: 20 * time.Second, TransitionWindow: 20 * time.Second, MaxDrain: 4 * time.Second})
		rig.db, rig.dsn = testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, rig.db)).To(Succeed())
		Expect(jobs.MigrateClaims(ctx, rig.db)).To(Succeed())
		rig.store, err = cluster.NewCarrierStore(rig.db)
		Expect(err).ToNot(HaveOccurred())
		rig.clusterR = cluster.NewRegistry(rig.db)
		rig.nodeReg, err = nodes.NewNodeRegistry(rig.db)
		Expect(err).ToNot(HaveOccurred())
		rig.jobStore, err = jobs.NewJobStore(rig.db)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = rig.store.Seed(ctx, cluster.CarrierNATS, "seed")
		Expect(err).ToNot(HaveOccurred())
	})

	settled := func(c cluster.Carrier) func() bool {
		return func() bool {
			r := rig.row()
			return r.State == cluster.StateStable && r.Active == c
		}
	}
	// change requests a change and waits until it has settled.
	change := func(to cluster.Carrier, force bool) {
		GinkgoHelper()
		_, _, err := rig.request(to, force)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(settled(to), "the change to "+string(to)+" settles")
	}
	drained := func() {
		GinkgoHelper()
		rig.eventually(func() bool { return rig.row().Draining == "" }, "the drain ends")
	}

	It("moves a cluster with work in flight from NATS to the tunnel and back, and loses and repeats nothing", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		Expect(a.active()).To(Equal(cluster.CarrierNATS))
		w := rig.startWorker("w1", workerOpts{addr: true})
		agent := rig.startAgent("agent-1")
		rig.reports(w.nodeID)
		rig.reports(agent.nodeID)

		// Chatter: both replicas publish a numbered message every few milliseconds
		// all through the changes, and every replica listens.
		var sent sync.Map
		stop := make(chan struct{})
		var chatter sync.WaitGroup
		for _, r := range []*switchReplica{a, b} {
			chatter.Go(func() {
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					case <-time.After(5 * time.Millisecond):
					}
					msg := fmt.Sprintf("%s-%d", r.id, i)
					if err := r.bus.Publish(chatterSubject, msg); err == nil {
						sent.Store(msg, struct{}{})
					}
				}
			})
		}

		// Renewals of a load, once every 100 ms through the holder, with the longest
		// gap between two answers.
		var (
			renewMu   sync.Mutex
			lastOK    = time.Now()
			maxGap    time.Duration
			renewErrs []error
		)
		renewStop := make(chan struct{})
		var renewing sync.WaitGroup
		renewing.Go(func() {
			for {
				select {
				case <-renewStop:
					return
				case <-time.After(100 * time.Millisecond):
				}
				_, err := a.commands.OperationControl(w.nodeID, workerctl.OperationRequest{Renew: []string{"load-1"}})
				renewMu.Lock()
				if err != nil {
					renewErrs = append(renewErrs, err)
				} else {
					maxGap = max(maxGap, time.Since(lastOK))
					lastOK = time.Now()
				}
				renewMu.Unlock()
			}
		})

		// An install that is under way on NATS, a model that is loading on the
		// backend, and a run that is held on NATS.
		go func() {
			_, err := a.commands.InstallBackendOp(w.nodeID, "llama-cpp", "m", "", 0, "op-slow", "load-slow", time.Minute, nil)
			w.installDone <- err
		}()
		Eventually(func() int { return w.installsOn(cluster.CarrierNATS) }, "10s").Should(Equal(1))
		loadDone := make(chan error, 1)
		go func() {
			res, err := a.clients.NewClient(w.nodeID, w.grpcAddr, false).LoadModel(rig.ctx, &pb.ModelOptions{Model: "slow"})
			if err == nil && !res.Success {
				err = errors.New(res.Message)
			}
			loadDone <- err
		}()
		Eventually(w.backend.loading, "10s").Should(Receive(Equal("slow")))
		heldJob := rig.newJob()
		release := rig.runs.hold(heldJob)
		rig.enqueue(a, heldJob)
		Eventually(func() int { return len(rig.runs.ranOn(heldJob)) }, "20s", "20ms").Should(Equal(1))

		var onNATS []string
		for range 8 {
			id := rig.newJob()
			onNATS = append(onNATS, id)
			rig.enqueue(a, id)
		}
		rig.eventually(rig.completed(onNATS...), "the jobs queued on NATS ran before the change")

		// Nothing was attached by hand: the workers report what they can follow, and
		// the preflight, which is a dry run, passes.
		rig.availability()
		report, err := a.sw.Preflight(rig.ctx, cluster.CarrierTunnel)
		Expect(err).ToNot(HaveOccurred())
		Expect(report.OK).To(BeTrue(), "%+v", report.Blockers)
		Expect(report.Replicas).To(HaveLen(2))
		Expect(report.Workers).To(HaveLen(2))
		Expect(report.InFlight.Jobs).To(BeNumerically(">=", 1))

		By("changing to the tunnel with the install, the load, the run and the renewals in flight")
		began := time.Now()
		_, _, err = rig.request(cluster.CarrierTunnel, false)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(settled(cluster.CarrierTunnel), "the change commits and settles")
		GinkgoWriter.Printf("change to the tunnel took %s\n", time.Since(began))
		rig.eventually(func() bool { return a.active() == cluster.CarrierTunnel && b.active() == cluster.CarrierTunnel }, "both replicas use the tunnel")
		Expect(rig.row().Draining).To(Equal(cluster.CarrierNATS))

		By("watching the workers follow, with no restart")
		followed := time.Now()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel))
		Eventually(func() []cluster.Carrier { return rig.attachedOf(agent.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel))
		GinkgoWriter.Printf("both workers reported the tunnel %s after the commit was seen\n", time.Since(followed))

		// New work goes to the claim queue, and the dispatch loop of a replica
		// drives it on the agent worker, which follows by itself.
		var onTunnel []string
		for range 8 {
			id := rig.newJob()
			onTunnel = append(onTunnel, id)
			rig.enqueue(b, id)
		}
		rig.eventually(rig.completed(onTunnel...), "the jobs queued after the change ran on the tunnel")

		By("letting the install, the load and the run finish where they began")
		close(w.installGate)
		var installErr error
		Eventually(w.installDone, "20s").Should(Receive(&installErr))
		Expect(installErr).ToNot(HaveOccurred(), "an install that began on NATS ends on NATS")
		close(w.backend.gate)
		Eventually(loadDone, "20s").Should(Receive(BeNil()), "the load of the model went on while the control moved: the backend never stopped")
		Expect(rig.status(heldJob)).To(Equal("running"))
		close(release)
		rig.eventually(rig.completed(heldJob), "the run that began on NATS reports its result, which the frontend still hears on NATS")

		By("waiting for the drain to end")
		drained()
		rig.eventually(func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			for _, c := range a.natsConns {
				if c.IsConnected() {
					return false
				}
			}
			return len(a.natsConns) > 0
		}, "the old carrier is closed on a replica when the drain is over")
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}), "the worker released NATS")
		Eventually(func() []cluster.Carrier { return rig.attachedOf(agent.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}))

		By("using the backend that never restarted, over the tunnel")
		out, err := b.clients.NewClient(w.nodeID, w.grpcAddr, false).Predict(rig.ctx, &pb.PredictOptions{Prompt: "after"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out.Message)).To(Equal("echo: after"))

		close(renewStop)
		renewing.Wait()
		close(stop)
		chatter.Wait()
		renewMu.Lock()
		Expect(renewErrs).To(BeEmpty(), "no renewal failed across the change")
		Expect(maxGap).To(BeNumerically("<", 3*time.Second), "renewals were never missed for longer than the cadence of a lease")
		renewMu.Unlock()
		Expect(w.renewalsOn(cluster.CarrierNATS)).To(BeNumerically(">", 0))
		Expect(w.renewalsOn(cluster.CarrierTunnel)).To(BeNumerically(">", 0), "renewals went to the worker over the tunnel once it was attached to it")

		// Every job ran once, on the carrier it was queued on.
		for _, id := range onNATS {
			Expect(rig.runs.ranOn(id)).To(Equal([]string{"nats"}), id)
		}
		Expect(rig.runs.ranOn(heldJob)).To(Equal([]string{"nats"}))
		for _, id := range onTunnel {
			Expect(rig.runs.ranOn(id)).To(Equal([]string{"tunnel"}), id)
		}
		// Every message was heard once by every replica.
		time.Sleep(500 * time.Millisecond)
		heardA, heardB := a.chatter.copy(), b.chatter.copy()
		type span struct{ n, min, max int }
		missing, repeated := map[string]*span{}, map[string]*span{}
		total := 0
		note := func(into map[string]*span, key string, idx int) {
			sp := into[key]
			if sp == nil {
				sp = &span{min: idx, max: idx}
				into[key] = sp
			}
			sp.n++
			sp.min, sp.max = min(sp.min, idx), max(sp.max, idx)
		}
		sent.Range(func(k, _ any) bool {
			total++
			pub, idxText, _ := strings.Cut(strings.TrimPrefix(k.(string), "replica-"), "-")
			var idx int
			_, _ = fmt.Sscanf(idxText, "%d", &idx)
			for name, h := range map[string]map[string]int{"a": heardA, "b": heardB} {
				switch n := h[k.(string)]; {
				case n == 0:
					note(missing, name+" missed "+pub, idx)
				case n > 1:
					note(repeated, name+" heard twice "+pub, idx)
				}
			}
			return true
		})
		var wrong []string
		for k, sp := range missing {
			wrong = append(wrong, fmt.Sprintf("%s: %d messages, index %d to %d", k, sp.n, sp.min, sp.max))
		}
		for k, sp := range repeated {
			wrong = append(wrong, fmt.Sprintf("%s: %d messages, index %d to %d", k, sp.n, sp.min, sp.max))
		}
		slices.Sort(wrong)
		Expect(total).To(BeNumerically(">", 100))
		Expect(wrong).To(BeEmpty(), "of %d messages", total)
	})

	It("moves a worker to the tunnel and back with a model loading and a backend running, without a restart", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(w.nodeID)
		loadDone := make(chan error, 1)
		go func() {
			res, err := a.clients.NewClient(w.nodeID, w.grpcAddr, false).LoadModel(rig.ctx, &pb.ModelOptions{Model: "slow"})
			if err == nil && !res.Success {
				err = errors.New(res.Message)
			}
			loadDone <- err
		}()
		Eventually(w.backend.loading, "10s").Should(Receive(Equal("slow")))

		followed := time.Now()
		change(cluster.CarrierTunnel, false)
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "20ms").Should(ContainElement(cluster.CarrierTunnel))
		GinkgoWriter.Printf("the worker reported the tunnel %s after the change was requested\n", time.Since(followed))
		close(w.backend.gate)
		Eventually(loadDone, "20s").Should(Receive(BeNil()))

		drained()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}))
		out, err := b.clients.NewClient(w.nodeID, w.grpcAddr, false).Predict(rig.ctx, &pb.PredictOptions{Prompt: "over the tunnel"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out.Message)).To(Equal("echo: over the tunnel"))
		reply, err := a.commands.InstallBackendOp(w.nodeID, "llama-cpp", "m", "", 0, "op-2", "load-2", time.Minute, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.Success).To(BeTrue())
		Expect(w.installsOn(cluster.CarrierTunnel)).To(Equal(1))

		By("going back to NATS")
		change(cluster.CarrierNATS, false)
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "20ms").Should(ContainElement(cluster.CarrierNATS))
		drained()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierNATS}))
		out, err = a.clients.NewClient(w.nodeID, w.grpcAddr, false).Predict(rig.ctx, &pb.PredictOptions{Prompt: "over NATS"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out.Message)).To(Equal("echo: over NATS"))
		_, err = b.commands.InstallBackendOp(w.nodeID, "llama-cpp", "m", "", 0, "op-3", "load-3", time.Minute, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(w.installsOn(cluster.CarrierNATS)).To(Equal(1))
		Expect(w.backend.cancelledModels()).To(BeEmpty(), "no load was cancelled by the changes")
	})

	It("hands over the work that is still queued on the tunnel when the cluster goes back to NATS, and runs each unit once", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		agent := rig.startAgent("agent-1")
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(agent.nodeID)
		rig.reports(w.nodeID)
		change(cluster.CarrierTunnel, false)
		drained()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(agent.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}))

		// No agent worker holds a tunnel any more, so the units wait in the table.
		agent.stop()
		var queued []string
		for range 6 {
			id := rig.newJob()
			queued = append(queued, id)
			rig.enqueue(a, id)
		}
		Consistently(rig.runs.total, "1s", "100ms").Should(BeZero())

		// A consumer that stayed on NATS runs them after the hand-over.
		stayed, err := messaging.New(rig.natsURL)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(stayed.Close)
		_, err = messaging.NewNATSWorkConsumer(stayed).Consume(rig.ctx, messaging.WorkMCPCI, 0, rig.runs.handler("nats"))
		Expect(err).ToNot(HaveOccurred())

		change(cluster.CarrierNATS, false)
		drained()

		rig.eventually(rig.completed(queued...), "the units that were queued on the tunnel run on NATS")
		for _, id := range queued {
			Expect(rig.runs.ranOn(id)).To(Equal([]string{"nats"}), id)
		}
		var migrated int64
		Expect(rig.db.Model(&jobs.WorkClaim{}).Where("state = ?", jobs.ClaimMigrated).Count(&migrated).Error).To(Succeed())
		Expect(migrated).To(BeEquivalentTo(len(queued)))
		Eventually(rig.listeners, "20s", "100ms").Should(BeZero(), "the LISTEN connection is closed when the tunnel is released")
		Expect(a.active()).To(Equal(cluster.CarrierNATS))
		Expect(b.active()).To(Equal(cluster.CarrierNATS))
	})

	It("moves two workers at once, and both serve on the new carrier", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		w1 := rig.startWorker("w1", workerOpts{addr: true})
		w2 := rig.startWorker("w2", workerOpts{addr: true})
		rig.reports(w1.nodeID)
		rig.reports(w2.nodeID)

		change(cluster.CarrierTunnel, false)
		for _, w := range []*workerProc{w1, w2} {
			Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel), w.name)
		}
		drained()
		for _, w := range []*workerProc{w1, w2} {
			Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}), w.name)
			_, err := b.commands.InstallBackendOp(w.nodeID, "llama-cpp", "m", "", 0, "op-"+w.name, "load-"+w.name, time.Minute, nil)
			Expect(err).ToNot(HaveOccurred(), w.name)
			Expect(w.installsOn(cluster.CarrierTunnel)).To(Equal(1), w.name)
		}
		_ = a
	})

	It("starts a worker that restarts in the middle of a change on the carrier the cluster has then", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		hold := make(chan struct{})
		b.mu.Lock()
		b.holdBuild[cluster.CarrierTunnel] = hold
		b.mu.Unlock()
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(w.nodeID)

		_, _, err := rig.request(cluster.CarrierTunnel, false)
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() cluster.State { return rig.row().State }, "10s").Should(Equal(cluster.StatePrepare))

		By("restarting the worker while the change is prepared")
		w.stop()
		again := rig.startWorker("w1", workerOpts{addr: true, reuse: w})
		Expect(again.nodeID).To(Equal(w.nodeID))
		Expect(rig.attachedOf(again.nodeID)).To(Equal([]cluster.Carrier{cluster.CarrierNATS}), "the cluster is still on NATS")
		Eventually(func() []cluster.Carrier { return rig.attachedOf(again.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel), "it follows the change that is being prepared")

		close(hold)
		rig.eventually(settled(cluster.CarrierTunnel), "the change settles")
		drained()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(again.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierTunnel}))

		By("restarting it once the cluster is on the tunnel")
		again.stop()
		third := rig.startWorker("w1", workerOpts{addr: true, reuse: again})
		Expect(rig.attachedOf(third.nodeID)).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}), "it starts on the carrier the cluster has, with no flag")
		Eventually(func() error {
			_, err := a.commands.InstallBackendOp(third.nodeID, "llama-cpp", "m", "", 0, "op-r", "load-r", time.Minute, nil)
			return err
		}, "20s", "100ms").Should(Succeed(), "it serves on the tunnel as soon as the tunnel is up")
		Expect(third.installsOn(cluster.CarrierTunnel)).To(Equal(1))
	})

	It("resolves a change when a replica dies in prepare, and the replica that starts again follows the row", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		c := rig.startReplica("replica-c")
		// C never becomes ready: it is stuck building the target when it dies.
		hold := make(chan struct{})
		c.mu.Lock()
		c.holdBuild[cluster.CarrierTunnel] = hold
		c.mu.Unlock()

		_, _, err := rig.request(cluster.CarrierTunnel, false)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(func() bool {
			in, err := rig.clusterR.Get(rig.ctx, "replica-a")
			return err == nil && in.ReadyEpoch == rig.row().Epoch
		}, "a replica is ready")
		Expect(rig.row().State).To(Equal(cluster.StatePrepare), "C holds the change in prepare")

		c.kill()
		close(hold)
		rig.eventually(settled(cluster.CarrierTunnel), "the change commits without the replica that died")
		Expect(a.active()).To(Equal(cluster.CarrierTunnel))
		Expect(b.active()).To(Equal(cluster.CarrierTunnel))

		// The replica comes back. It reads the row, and never its own history.
		again := rig.startReplica("replica-c")
		Expect(again.active()).To(Equal(cluster.CarrierTunnel))
		Expect(again.buildCount(cluster.CarrierNATS)).To(BeZero(), "it never builds the carrier it left")
		Expect(a.bus.Publish(chatterSubject, "after-restart")).To(Succeed())
		Eventually(func() int { return again.chatter.get("after-restart") }, "10s").Should(Equal(1), "it listens where the cluster publishes")
	})

	It("resolves a change when the replica that leads dies between the commit and the settle", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		var once sync.Once
		hook := func(r *switchReplica, row cluster.CarrierRow) {
			if row.State == cluster.StateCommit {
				once.Do(func() { go r.kill() })
			}
		}
		rig.onMove.Store(&hook)

		_, _, err := rig.request(cluster.CarrierTunnel, false)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(settled(cluster.CarrierTunnel), "the other replica settles the change from the row")
		var survivors int
		for _, r := range []*switchReplica{a, b} {
			if r.active() == cluster.CarrierTunnel {
				survivors++
			}
		}
		Expect(survivors).To(BeNumerically(">=", 1))
	})

	It("aborts a change that is stuck in prepare, drops what was built for it, and leaves the cluster on its carrier", func() {
		a, b := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(w.nodeID)
		hold := make(chan struct{})
		b.mu.Lock()
		b.holdBuild[cluster.CarrierTunnel] = hold
		b.mu.Unlock()

		_, _, err := rig.request(cluster.CarrierTunnel, false)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(func() bool { return a.buildCount(cluster.CarrierTunnel) == 1 }, "A built the target")
		Eventually(rig.listeners, "10s", "50ms").Should(Equal(int64(1)), "A listens on the database for the target")
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel), "the worker attaches to the target while it is prepared")

		row, err := a.sw.Abort(rig.ctx, "admin")
		Expect(err).ToNot(HaveOccurred())
		Expect(row.State).To(Equal(cluster.StateStable))
		Expect(row.Active).To(Equal(cluster.CarrierNATS))
		close(hold)

		Eventually(rig.listeners, "20s", "100ms").Should(BeZero(), "what was built for the change is dropped on both replicas")
		Expect(a.active()).To(Equal(cluster.CarrierNATS))
		Expect(b.active()).To(Equal(cluster.CarrierNATS))
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierNATS}), "the worker lets go of the target when the change is aborted")

		// The cluster works as it did.
		_, err = a.commands.OperationControl(w.nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(err).ToNot(HaveOccurred())
		id := rig.newJob()
		w2 := rig.startAgent("agent-1")
		rig.reports(w2.nodeID)
		rig.enqueue(a, id)
		rig.eventually(rig.completed(id), "work still runs on NATS")
		Expect(rig.runs.ranOn(id)).To(Equal([]string{"nats"}))
		heardBefore := b.chatter.get("after-abort")
		Expect(a.bus.Publish(chatterSubject, "after-abort")).To(Succeed())
		Eventually(func() int { return b.chatter.get("after-abort") }, "10s").Should(Equal(heardBefore + 1))
	})

	It("blocks a change on a worker that predates carrier switching, and a forced change leaves it on NATS, unroutable and unreaped", func() {
		a, _ := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(w.nodeID)
		// An old worker: registered, alive, and silent about carriers.
		old := &nodes.BackendNode{Name: "old", NodeType: nodes.NodeTypeBackend, TokenHash: hashOf(registrationToken), Address: "127.0.0.1:1", HTTPAddress: "127.0.0.1:2"}
		Expect(rig.nodeReg.Register(rig.ctx, old, true)).To(Succeed())
		Expect(rig.nodeReg.SetNodeModel(rig.ctx, old.ID, "keep-me", 0, "loaded", "127.0.0.1:1", 0)).To(Succeed())
		oldNATS, err := messaging.New(rig.natsURL)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(oldNATS.Close)
		_, err = oldNATS.SubscribeReply(messaging.SubjectNodeModelOp(old.ID), func(_ []byte, respond func([]byte)) {
			reply, _ := json.Marshal(workerctl.OperationReply{})
			respond(reply)
		})
		Expect(err).ToNot(HaveOccurred())

		_, _, err = rig.request(cluster.CarrierTunnel, false)
		var blocked *cluster.BlockedError
		Expect(errors.As(err, &blocked)).To(BeTrue(), "%v", err)
		Expect(blocked.Report.Blockers).To(ContainElement(SatisfyAll(
			HaveField("Kind", cluster.BlockerWorker),
			HaveField("ID", old.ID),
			HaveField("Reason", ContainSubstring("predates carrier switching")),
		)))
		Expect(blocked.Report.Blockers).To(HaveLen(1), "the worker that follows is not a blocker")
		Expect(rig.row().State).To(Equal(cluster.StateStable))
		Expect(rig.row().Epoch).To(Equal(int64(1)))

		_, _, err = rig.request(cluster.CarrierTunnel, true)
		Expect(err).ToNot(HaveOccurred())
		rig.eventually(settled(cluster.CarrierTunnel), "the forced change settles")
		Expect(rig.row().Force).To(BeTrue())
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierTunnel))

		// While NATS drains the old worker is still reached on it.
		_, err = a.commands.OperationControl(old.ID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeFalse(), "the window routes to the carrier the worker is attached to: %v", err)
		drained()
		Eventually(func() bool {
			_, err := a.commands.OperationControl(old.ID, workerctl.OperationRequest{Renew: []string{"op"}})
			return errors.Is(err, nodes.ErrNoRoute)
		}, "20s", "100ms").Should(BeTrue(), "after the drain the old worker has no route, and that is all it has")

		models, err := rig.nodeReg.GetNodeModels(rig.ctx, old.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1), "nothing was reaped")
		node, err := rig.nodeReg.Get(rig.ctx, old.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.Status).To(Equal(nodes.StatusHealthy))
	})

	It("lists a worker that has no address as unable to follow to NATS, and leaves it on the tunnel, unroutable and unreaped, after a forced change", func() {
		a, _ := rig.startReplica("replica-a"), rig.startReplica("replica-b")
		w := rig.startWorker("w1", workerOpts{addr: true})
		rig.reports(w.nodeID)
		change(cluster.CarrierTunnel, false)
		drained()

		// A worker that holds a tunnel and has no address of its own.
		only := rig.startWorker("tunnel-only", workerOpts{})
		rig.reports(only.nodeID)
		Eventually(func() string {
			n, err := rig.nodeReg.Get(rig.ctx, only.nodeID)
			if err != nil {
				return ""
			}
			return n.FollowError
		}, "20s", "50ms").Should(ContainSubstring("--addr"))
		Expect(rig.nodeReg.SetNodeModel(rig.ctx, only.nodeID, "keep-me", 0, "loaded", only.grpcAddr, 0)).To(Succeed())

		_, _, err := rig.request(cluster.CarrierNATS, false)
		var blocked *cluster.BlockedError
		Expect(errors.As(err, &blocked)).To(BeTrue(), "%v", err)
		Expect(blocked.Report.Blockers).To(ConsistOf(SatisfyAll(
			HaveField("Kind", cluster.BlockerWorker),
			HaveField("ID", only.nodeID),
			HaveField("Reason", ContainSubstring("--addr")),
			HaveField("Forceable", true),
		)))

		By("forcing the change")
		change(cluster.CarrierNATS, true)
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(ContainElement(cluster.CarrierNATS), "the worker that can follow does")
		drained()
		Eventually(func() []cluster.Carrier { return rig.attachedOf(w.nodeID) }, "20s", "50ms").Should(Equal([]cluster.Carrier{cluster.CarrierNATS}))
		Expect(rig.attachedOf(only.nodeID)).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}), "the worker that cannot follow stays where it is")

		Eventually(func() bool {
			_, err := a.commands.OperationControl(only.nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
			return errors.Is(err, nodes.ErrNoRoute)
		}, "20s", "100ms").Should(BeTrue(), "it has no route on NATS")
		_, err = a.commands.OperationControl(w.nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(err).ToNot(HaveOccurred(), "the other worker is reached on NATS")

		models, err := rig.nodeReg.GetNodeModels(rig.ctx, only.nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1), "nothing was reaped")
		node, err := rig.nodeReg.Get(rig.ctx, only.nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.Status).To(Equal(nodes.StatusHealthy))
		Expect(node.FollowError).ToNot(BeEmpty(), "it says why")
		Expect(only.fol.Attached()).To(Equal([]cluster.Carrier{cluster.CarrierTunnel}), "and it keeps its tunnel")
	})
})

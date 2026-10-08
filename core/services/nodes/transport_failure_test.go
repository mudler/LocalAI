package nodes

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A dial that fails in the transport says nothing about the backend. The tunnel
// carrier makes that the ordinary way for a probe to fail: a worker that is not
// connected to any replica right now cannot be probed, and it is well. Every
// place that reaps a row on a failed probe must tell the two apart.

// backendAnswer is the error of a dialer that reached the host of a backend and
// was told that the backend is gone.
type backendAnswer struct{ error }

func (backendAnswer) IsBackendAnswer() bool { return true }

var errNoTunnel = errors.New("no tunnel is held for that node")

var _ = Describe("A probe that could not reach the backend", func() {
	unavailable := status.Error(codes.Unavailable, "dial failed")

	DescribeTable("is classified by what failed",
		func(mk func() *fakeBackendClient, want ProbeOutcome) {
			f := &recordingFactory{next: func() grpc.Backend { return mk() }}
			Expect(grpcModelProber{clients: f}.Probe(context.Background(), "n1", "a:1")).To(Equal(want))
		},
		Entry("the transport failed: nothing was learned", func() *fakeBackendClient {
			return &fakeBackendClient{err: unavailable, dialErr: errNoTunnel}
		}, ProbeUnknown),
		Entry("the transport failed and the call timed out: still nothing learned", func() *fakeBackendClient {
			return &fakeBackendClient{err: context.DeadlineExceeded, dialErr: errNoTunnel}
		}, ProbeUnknown),
		Entry("the host answered that the backend is gone: evidence", func() *fakeBackendClient {
			return &fakeBackendClient{err: unavailable, dialErr: backendAnswer{errors.New("the worker could not reach the backend")}}
		}, ProbeUnreachable),
		Entry("the backend is well, whatever the last dial said", func() *fakeBackendClient {
			return &fakeBackendClient{healthy: true, dialErr: errNoTunnel}
		}, ProbeAlive),
		Entry("no custom dialer: a refused connection is still death", func() *fakeBackendClient {
			return &fakeBackendClient{err: unavailable}
		}, ProbeUnreachable),
	)
})

var _ = Describe("The reconciler and a probe that learned nothing", func() {
	It("never reaps a replica on it, however often it repeats", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		node := &BackendNode{Name: "tunnel-node", NodeType: NodeTypeBackend}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		Expect(db.Create(&NodeModel{
			ID: "m-1", NodeID: node.ID, ModelName: "m-1", Address: "127.0.0.1:50052",
			State: "loaded", UpdatedAt: time.Now().Add(-5 * time.Minute),
		}).Error).To(Succeed())

		prober := &fakeProber{outcomes: map[string]ProbeOutcome{"127.0.0.1:50052": ProbeUnknown}}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, Prober: prober, ProbeStaleAfter: 2 * time.Minute})
		for range probeFailuresBeforeReap * 3 {
			rc.probeLoadedModels(context.Background())
			Expect(db.Model(&NodeModel{}).Where("id = ?", "m-1").Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
		}
		var after NodeModel
		Expect(db.First(&after, "id = ?", "m-1").Error).To(Succeed(), "a transport that failed is not a dead backend")
	})

	It("keeps a streak of real failures where it was, and does not count the unknown one", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		node := &BackendNode{Name: "n", NodeType: NodeTypeBackend}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		Expect(db.Create(&NodeModel{
			ID: "m-2", NodeID: node.ID, ModelName: "m-2", Address: "127.0.0.1:50053",
			State: "loaded", UpdatedAt: time.Now().Add(-5 * time.Minute),
		}).Error).To(Succeed())
		prober := &fakeProber{outcomes: map[string]ProbeOutcome{"127.0.0.1:50053": ProbeUnreachable}}
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db, Prober: prober, ProbeStaleAfter: 2 * time.Minute})
		stale := func() {
			Expect(db.Model(&NodeModel{}).Where("id = ?", "m-2").Update("updated_at", time.Now().Add(-5*time.Minute)).Error).To(Succeed())
		}

		// One miss short of the threshold, then a pass that learns nothing, then
		// the last miss: the unknown pass neither wipes nor adds to the streak.
		for range probeFailuresBeforeReap - 1 {
			rc.probeLoadedModels(context.Background())
			stale()
		}
		prober.outcomes["127.0.0.1:50053"] = ProbeUnknown
		rc.probeLoadedModels(context.Background())
		stale()
		var mid NodeModel
		Expect(db.First(&mid, "id = ?", "m-2").Error).To(Succeed())

		prober.outcomes["127.0.0.1:50053"] = ProbeUnreachable
		rc.probeLoadedModels(context.Background())
		var after NodeModel
		Expect(db.First(&after, "id = ?", "m-2").Error).ToNot(Succeed(), "the streak was intact, so this miss reaps")
	})
})

var _ = Describe("The health monitor and a probe that learned nothing", func() {
	build := func() (*fakeNodeHealthStore, *fakeBackendClientFactory, *HealthMonitor) {
		store := newFakeNodeHealthStore()
		factory := newFakeBackendClientFactory()
		hm := newTestHealthMonitor(store, factory, true, 5*time.Minute)
		hm.perModelHealthCheck = true
		node := makeTestNode("n", "tunnel-worker", "127.0.0.1:50051", StatusHealthy, time.Now())
		store.addNode(node)
		store.addNodeModel("n", NodeModel{NodeID: "n", ModelName: "m", Address: "127.0.0.1:50053"})
		return store, factory, hm
	}

	It("does not count it as a miss, however often it repeats", func() {
		store, factory, hm := build()
		factory.setClient("127.0.0.1:50053", &fakeBackendClient{err: fmt.Errorf("unavailable"), dialErr: errNoTunnel})
		for range perModelMissThreshold * 3 {
			hm.doCheckAll(context.Background())
		}
		Expect(store.getCalls()).ToNot(ContainElement(ContainSubstring("RemoveNodeModel")))
	})

	It("still reaps after the threshold when the host answered that the backend is gone", func() {
		store, factory, hm := build()
		factory.setClient("127.0.0.1:50053", &fakeBackendClient{err: fmt.Errorf("unavailable"), dialErr: backendAnswer{errors.New("gone")}})
		for range perModelMissThreshold {
			hm.doCheckAll(context.Background())
		}
		Expect(store.getCalls()).To(ContainElement("RemoveNodeModel:n:m:0"))
	})
})

var _ = Describe("The decorators of a client", func() {
	It("let a caller see the dial error of the client under them", func() {
		raw := &fakeBackendClient{dialErr: errNoTunnel}
		staged := NewFileStagingClient(raw, nil, "n")
		tracked := NewInFlightTrackingClient(staged, nil, "n", "m", 0)

		Expect(errors.Is(grpc.LastDialErrorOf(staged), errNoTunnel)).To(BeTrue())
		Expect(errors.Is(grpc.LastDialErrorOf(tracked), errNoTunnel)).To(BeTrue())
		Expect(errors.Is(grpc.TransportFailureOf(tracked), errNoTunnel)).To(BeTrue())
	})
})

var _ = Describe("The router and a replica it cannot reach", func() {
	cached := func() (*fakeModelRouter, *BackendNode) {
		old := &BackendNode{ID: "n-old", Name: "old-node", Address: "10.0.0.70:50051"}
		fresh := &BackendNode{ID: "n-new", Name: "new-node", Address: "10.0.0.71:50051"}
		return &fakeModelRouter{
			findAndLockNode: old,
			findAndLockNM:   &NodeModel{NodeID: "n-old", ModelName: "m", Address: "10.0.0.70:9001"},
			findIdleNode:    fresh,
		}, fresh
	}
	route := func(reg *fakeModelRouter, backend *stubBackend) {
		GinkgoHelper()
		backend.loadResult = &pb.Result{Success: true}
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.71:9001"}}
		router := NewSmartRouter(reg, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})
		_, err := router.Route(context.Background(), "m", "models/m.gguf", "llama-cpp", "", nil, false)
		Expect(err).ToNot(HaveOccurred())
	}

	It("keeps the row of a replica whose probe could not get a stream, and gives its reservation back", func() {
		reg, _ := cached()
		route(reg, &stubBackend{healthResult: false, healthErr: errors.New("unavailable"), dialErr: errNoTunnel})
		Expect(reg.removeCalls).ToNot(ContainElement("n-old:m"), "a worker that cannot be reached from here is not a replica that is gone")
		Expect(reg.decrementCalls).To(ContainElement("n-old:m"))
	})

	It("removes the row of a replica whose backend does not answer on a reachable host", func() {
		reg, _ := cached()
		route(reg, &stubBackend{healthResult: false, healthErr: errors.New("unavailable")})
		Expect(reg.removeCalls).To(ContainElement("n-old:m"))
	})

	It("removes the row when the host answered that the backend is gone", func() {
		reg, _ := cached()
		route(reg, &stubBackend{healthResult: false, healthErr: errors.New("unavailable"), dialErr: backendAnswer{errors.New("gone")}})
		Expect(reg.removeCalls).To(ContainElement("n-old:m"))
	})
})

// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Three nodes of the same size, each reached over one carrier. The ids sort in
// the opposite order of the default weights, so a tie that the weight did not
// break would give the lowest weight, and the specs can tell the two apart.
func rankFixtures() (p2p, ws, nats BackendNode) {
	base := BackendNode{NodeType: NodeTypeBackend, Status: StatusHealthy, AvailableVRAM: 8 << 30, TotalVRAM: 8 << 30}
	p2p, ws, nats = base, base, base
	p2p.ID, p2p.Name = "a-p2p", "a-p2p"
	p2p.PeerID, p2p.Attached, p2p.Follow, p2p.TunnelTransport, p2p.TunnelPath = "peer", "tunnel", "tunnel", TunnelP2P, "direct"
	ws.ID, ws.Name = "b-ws", "b-ws"
	ws.Attached, ws.Follow, ws.TunnelTransport = "tunnel", "tunnel", TunnelWebSocket
	nats.ID, nats.Name = "c-nats", "c-nats"
	nats.Address, nats.Attached, nats.Follow = "10.0.0.3:50051", "nats", "nats"
	return
}

var _ = Describe("Carrier weights break ties between equally fit nodes", func() {
	var (
		ctx     context.Context
		db      *gorm.DB
		reg     *NodeRegistry
		weights cluster.CarrierWeights
		all     []string
	)

	// setWeights changes the weights as an admin does. SetCarrierWeights forgets
	// the cached ranks, which stands in for the cache time running out.
	setWeights := func(w cluster.CarrierWeights) {
		weights = w
		reg.SetCarrierWeights(func(context.Context) (cluster.CarrierWeights, error) { return weights, nil })
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		reg, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		p2p, ws, nats := rankFixtures()
		for _, n := range []BackendNode{p2p, ws, nats} {
			Expect(db.Create(&n).Error).To(Succeed())
		}
		all = []string{"a-p2p", "b-ws", "c-nats"}
		setWeights(cluster.DefaultCarrierWeights)
	})

	idle := func() string {
		GinkgoHelper()
		n, err := reg.FindIdleNodeFromSet(ctx, all)
		Expect(err).ToNot(HaveOccurred())
		return n.ID
	}

	It("ranks a node by the highest weight among its routes", func() {
		both := BackendNode{Address: "h:1", Attached: "nats,tunnel", Follow: "nats,tunnel", TunnelTransport: TunnelWebSocket}
		w := cluster.CarrierWeights{NATS: 10, WebSocket: 70, P2P: 30}
		Expect(rankOf(&both, w).weight).To(Equal(70))
		w.NATS = 90
		Expect(rankOf(&both, w).weight).To(Equal(90))
		Expect(rankOf(&BackendNode{PeerID: "p", Attached: "tunnel", Follow: "tunnel", TunnelTransport: TunnelP2P, TunnelPath: "direct"}, w).weight).To(Equal(30))
	})

	DescribeTable("picks by weight when the nodes fit equally",
		func(w cluster.CarrierWeights, want string) {
			setWeights(w)
			Expect(idle()).To(Equal(want))
		},
		Entry("default weights: NATS first", cluster.CarrierWeights{NATS: 100, WebSocket: 50, P2P: 25}, "c-nats"),
		Entry("the user puts WebSocket above NATS", cluster.CarrierWeights{NATS: 40, WebSocket: 60, P2P: 25}, "b-ws"),
		Entry("the user puts P2P on top", cluster.CarrierWeights{NATS: 40, WebSocket: 60, P2P: 900}, "a-p2p"),
		Entry("NATS set to 0 loses to a carrier with a weight", cluster.CarrierWeights{NATS: 0, WebSocket: 50, P2P: 25}, "b-ws"),
		Entry("all weights equal: the stable order of before, by id", cluster.CarrierWeights{NATS: 5, WebSocket: 5, P2P: 5}, "a-p2p"),
	)

	It("keeps the plain order when no weight source is set", func() {
		plain, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		n, err := plain.FindIdleNodeFromSet(ctx, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("a-p2p"))
	})

	It("takes a change of the weights without a restart", func() {
		Expect(idle()).To(Equal("c-nats"))
		setWeights(cluster.CarrierWeights{NATS: 1, WebSocket: 2, P2P: 3})
		Expect(idle()).To(Equal("a-p2p"))
	})

	It("never lets the weight beat fit: more free VRAM wins over a higher weight", func() {
		Expect(db.Model(&BackendNode{}).Where("id = ?", "a-p2p").Update("available_vram", 16<<30).Error).To(Succeed())
		setWeights(cluster.CarrierWeights{NATS: 1000, WebSocket: 1000, P2P: 0})
		Expect(idle()).To(Equal("a-p2p"))
	})

	It("never lets the weight beat fit: an idle node wins over a busier one with a higher weight", func() {
		busy := NodeModel{ID: "nm-1", NodeID: "c-nats", ModelName: "other", State: "loaded", InFlight: 3}
		Expect(db.Create(&busy).Error).To(Succeed())
		// Idle-first: the only idle nodes are a-p2p and b-ws, and WebSocket weighs more.
		Expect(idle()).To(Equal("b-ws"))
		// Least loaded among nodes that all hold a model: the quieter one wins.
		for i, id := range []string{"a-p2p", "b-ws"} {
			Expect(db.Create(&NodeModel{ID: "nm-x" + id, NodeID: id, ModelName: "other", State: "loaded", InFlight: i}).Error).To(Succeed())
		}
		n, err := reg.FindLeastLoadedNodeFromSet(ctx, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("a-p2p"), "in_flight 0 beats the weight of NATS and WebSocket")
	})

	It("breaks a tie of the least-loaded pass and of the VRAM pass by weight", func() {
		n, err := reg.FindLeastLoadedNodeFromSet(ctx, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("c-nats"))
		n, err = reg.FindNodeWithVRAMFromSet(ctx, 4<<30, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("c-nats"))
		setWeights(cluster.CarrierWeights{NATS: 0, WebSocket: 80, P2P: 10})
		n, err = reg.FindNodeWithVRAMFromSet(ctx, 4<<30, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("b-ws"))
		n, err = reg.FindNodeWithVRAM(ctx, 4<<30)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("b-ws"))
		n, err = reg.FindIdleNode(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("b-ws"))
		n, err = reg.FindLeastLoadedNode(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("b-ws"))
	})

	Describe("the warm path", func() {
		loaded := func(nodeID string, inFlight int) {
			GinkgoHelper()
			Expect(db.Create(&NodeModel{ID: "m-" + nodeID, NodeID: nodeID, ModelName: "m", State: "loaded", InFlight: inFlight}).Error).To(Succeed())
		}
		warm := func() string {
			GinkgoHelper()
			n, _, err := reg.FindAndLockNodeWithModel(ctx, "m", nil, nil)
			Expect(err).ToNot(HaveOccurred())
			return n.ID
		}

		BeforeEach(func() {
			for _, id := range all {
				loaded(id, 0)
			}
		})

		It("picks the node of the highest weight among equally busy replicas", func() {
			Expect(warm()).To(Equal("c-nats"))
			setWeights(cluster.CarrierWeights{NATS: 1, WebSocket: 9, P2P: 3})
			// The first call took a reservation on NATS, so give every replica the same load again.
			Expect(db.Model(&NodeModel{}).Where("1 = 1").Update("in_flight", 0).Error).To(Succeed())
			Expect(warm()).To(Equal("b-ws"))
		})

		It("keeps fit first: a less busy replica wins over a higher weight", func() {
			Expect(db.Model(&NodeModel{}).Where("node_id = ?", "c-nats").Update("in_flight", 2).Error).To(Succeed())
			Expect(db.Model(&NodeModel{}).Where("node_id = ?", "b-ws").Update("in_flight", 1).Error).To(Succeed())
			Expect(warm()).To(Equal("a-p2p"))
		})

		It("rotates among replicas of equal weight, as before", func() {
			setWeights(cluster.CarrierWeights{NATS: 7, WebSocket: 7, P2P: 7})
			first := warm()
			Expect(db.Model(&NodeModel{}).Where("1 = 1").Update("in_flight", 0).Error).To(Succeed())
			Expect(warm()).ToNot(Equal(first), "last_used still rotates when the weights tie")
		})

		It("uses a replica behind a data-limited relay only when no other replica is loaded", func() {
			Expect(db.Model(&BackendNode{}).Where("id = ?", "b-ws").Updates(map[string]any{
				"peer_id": "relay-peer", "tunnel_transport": TunnelP2P, "tunnel_path": "relayed", "tunnel_limited": true,
			}).Error).To(Succeed())
			setWeights(cluster.CarrierWeights{NATS: 0, WebSocket: 0, P2P: 1000})
			// The limited node has the top weight and the least load, and still comes last.
			Expect(db.Model(&NodeModel{}).Where("node_id = ?", "c-nats").Update("in_flight", 5).Error).To(Succeed())
			Expect(warm()).To(Equal("a-p2p"))
			Expect(db.Where("node_id IN ?", []string{"a-p2p", "c-nats"}).Delete(&NodeModel{}).Error).To(Succeed())
			Expect(warm()).To(Equal("b-ws"))
		})
	})
})

// The ORDER BY carries bound parameters in a CASE. SQLite takes them anywhere,
// so the same orders also run on PostgreSQL, which types them by position.
var _ = Describe("Carrier weights on PostgreSQL", func() {
	It("orders the cold and the warm choice by weight with bound parameters", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		reg, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		p2p, ws, nats := rankFixtures()
		for _, n := range []BackendNode{p2p, ws, nats} {
			Expect(db.Create(&n).Error).To(Succeed())
			Expect(db.Create(&NodeModel{ID: "m-" + n.ID, NodeID: n.ID, ModelName: "m", State: "loaded"}).Error).To(Succeed())
		}
		reg.SetCarrierWeights(func(context.Context) (cluster.CarrierWeights, error) {
			return cluster.CarrierWeights{NATS: 100, WebSocket: 50, P2P: 25}, nil
		})
		all := []string{"a-p2p", "b-ws", "c-nats"}
		n, err := reg.FindNodeWithVRAMFromSet(ctx, 1<<30, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("c-nats"))
		n, err = reg.FindLeastLoadedNode(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("c-nats"))
		w, _, err := reg.FindAndLockNodeWithModel(ctx, "m", nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(w.ID).To(Equal("c-nats"))
		reg.SetCarrierWeights(func(context.Context) (cluster.CarrierWeights, error) {
			return cluster.CarrierWeights{NATS: 0, WebSocket: 50, P2P: 25}, nil
		})
		n, err = reg.FindIdleNodeFromSet(ctx, all)
		Expect(err).To(HaveOccurred(), "every node holds a model, so none is idle")
		_ = n
		n, err = reg.FindLeastLoadedNodeFromSet(ctx, all)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.ID).To(Equal("b-ws"), "c-nats holds the reservation of the warm pick, so it loses on load; of the other two, WebSocket weighs more")
	})
})

// rankedFake answers FindIdleNodeFromSet from the set it is given, so that a spec
// can tell which candidates the router offered and in which order.
type rankedFake struct {
	*fakeModelRouter
	nodes map[string]*BackendNode
}

func (f *rankedFake) FindIdleNodeFromSet(_ context.Context, ids []string) (*BackendNode, error) {
	f.idleFromSetCandidates = append(f.idleFromSetCandidates, ids)
	for _, id := range ids {
		if n, ok := f.nodes[id]; ok {
			return n, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

type staticRanks map[string]nodeRank

func (s staticRanks) nodeRanks(context.Context) map[string]nodeRank { return s }

var _ = Describe("A node behind a data-limited relay is a last resort", func() {
	var (
		reg     *rankedFake
		stager  *fakeFileStager
		natsN   *BackendNode
		relayN  *BackendNode
		modelFn string
		opts    *proto.ModelOptions
	)

	newRouter := func(o SmartRouterOptions) *SmartRouter {
		o.Unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"}}
		o.ClientFactory = &stubClientFactory{client: &stubBackend{loadResult: &proto.Result{Success: true}}}
		r := NewSmartRouter(reg, o)
		r.ranker = staticRanks{
			"nats":  {weight: 100},
			"relay": {weight: 25, limitedOnly: true},
		}
		return r
	}
	route := func(r *SmartRouter) (*RouteResult, error) {
		return r.Route(context.Background(), "m", modelFn, "llama-cpp", "", opts, false)
	}

	BeforeEach(func() {
		natsN = &BackendNode{ID: "nats", Name: "nats", Address: "10.0.0.1:50051", Status: StatusHealthy}
		relayN = &BackendNode{ID: "relay", Name: "relay", Status: StatusHealthy, PeerID: "p", Attached: "tunnel", Follow: "tunnel",
			TunnelTransport: TunnelP2P, TunnelPath: "relayed", TunnelLimited: true}
		reg = &rankedFake{
			fakeModelRouter: &fakeModelRouter{
				findAndLockErr:      errors.New("not loaded"),
				findBySelectorNodes: []BackendNode{*natsN, *relayN},
			},
			nodes: map[string]*BackendNode{"nats": natsN, "relay": relayN},
		}
		stager = &fakeFileStager{}
		modelFn = filepath.Join(GinkgoT().TempDir(), "m.gguf")
		Expect(os.WriteFile(modelFn, make([]byte, 4096), 0o600)).To(Succeed())
		opts = &proto.ModelOptions{Model: "m.gguf", ModelFile: modelFn}
	})

	It("offers the other nodes first and takes the relay node only when none of them fits", func() {
		reg.nodes = map[string]*BackendNode{"relay": relayN}
		router := newRouter(SmartRouterOptions{StagingThroughStore: true})
		res, err := route(router)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Node.ID).To(Equal("relay"))
		Expect(reg.idleFromSetCandidates[0]).To(Equal([]string{"nats"}), "the first pass leaves the relay node out")
		Expect(reg.idleFromSetCandidates[len(reg.idleFromSetCandidates)-1]).To(ContainElement("relay"))
	})

	It("does not look at the relay node while another node fits", func() {
		router := newRouter(SmartRouterOptions{StagingThroughStore: true})
		res, err := route(router)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Node.ID).To(Equal("nats"))
		Expect(reg.idleFromSetCandidates).To(HaveLen(1))
		Expect(reg.idleFromSetCandidates[0]).To(Equal([]string{"nats"}))
	})

	It("never offers the relay node for a placement that pushes model files to it", func() {
		reg.nodes = map[string]*BackendNode{"relay": relayN}
		router := newRouter(SmartRouterOptions{FileStager: stager})
		_, err := route(router)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrNoAvailableNodes)).To(BeTrue())
		for _, ids := range reg.idleFromSetCandidates {
			Expect(ids).ToNot(ContainElement("relay"))
		}
		Expect(stager.ensureCalls).To(BeEmpty())
	})

	It("places a model that needs staging on the other node", func() {
		router := newRouter(SmartRouterOptions{FileStager: stager})
		res, err := route(router)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Node.ID).To(Equal("nats"))
	})

	DescribeTable("may use the relay node when staging sends no file bytes over the route",
		func(o SmartRouterOptions) {
			reg.nodes = map[string]*BackendNode{"relay": relayN}
			o.FileStager = stager
			res, err := route(newRouter(o))
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal("relay"))
		},
		Entry("the files go through S3", SmartRouterOptions{StagingThroughStore: true}),
		Entry("every node mounts the models directory", SmartRouterOptions{SharedModels: true}),
	)

	It("does not call a placement staged when the model has no local file", func() {
		reg.nodes = map[string]*BackendNode{"relay": relayN}
		opts = &proto.ModelOptions{Model: "remote-model"}
		res, err := route(newRouter(SmartRouterOptions{FileStager: stager}))
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Node.ID).To(Equal("relay"))
	})
})

package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// splitSource answers the split exchange from a fixed list, or with err, and
// records the nodes whose workers it was asked to detach.
type splitSource struct {
	workers  map[string][]workerctl.SplitAssigned
	err      error
	detached *[]string
}

func (s splitSource) DetachSplitWorkers(_ context.Context, nodeID string) (int, error) {
	if s.detached != nil {
		*s.detached = append(*s.detached, nodeID)
	}
	return 2, nil
}

func (s splitSource) SplitAssignment(_ context.Context, nodeID string) (workerctl.SplitAssignment, error) {
	if s.err != nil {
		return workerctl.SplitAssignment{}, s.err
	}
	return workerctl.SplitAssignment{Workers: s.workers[nodeID], OwnRelays: []string{"relay-1"}}, nil
}

var _ = Describe("The split exchange of a backend node", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	exchange := func(id, body string, opts ...RegisterOption) (int, workerctl.SplitAssignment) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(NodeSplitEndpoint(registry, opts...)(c)).To(Succeed())
		var out workerctl.SplitAssignment
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	register := func(n *nodes.BackendNode) string {
		GinkgoHelper()
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		return n.ID
	}

	It("stores the report and answers with the workers attached to the node", func() {
		id := register(&nodes.BackendNode{Name: "gpu-1", NodeType: nodes.NodeTypeBackend})
		src := splitSource{workers: map[string][]workerctl.SplitAssigned{id: {{DeviceID: "d1", PeerID: "peer-w", Engine: "llama"}}}}
		code, a := exchange(id, `{"peer_id":"peer-n","addrs":["/ip4/10.0.0.2/tcp/4001"],"workers":[{"device_id":"d1","state":"online"}]}`, WithSplitNodes(src))
		Expect(code).To(Equal(http.StatusOK))
		Expect(a.Workers).To(HaveLen(1))
		Expect(a.OwnRelays).To(ConsistOf("relay-1"))
		rep, ok, err := registry.SplitReport(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(rep.Report.PeerID).To(Equal("peer-n"))
		Expect(rep.Report.Workers[0].State).To(Equal("online"))
	})

	It("writes nothing for a node that runs no host, and still answers", func() {
		id := register(&nodes.BackendNode{Name: "gpu-2", NodeType: nodes.NodeTypeBackend})
		code, a := exchange(id, `{}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusOK))
		Expect(a.Workers).ToNot(BeNil(), "an empty list, so the node drops what it had")
		Expect(a.Workers).To(BeEmpty())
		_, ok, err := registry.SplitReport(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("refuses a node that joined over P2P and reports another peer", func() {
		id := register(&nodes.BackendNode{Name: "p2p-1", NodeType: nodes.NodeTypeBackend, PeerID: "peer-pinned"})
		code, _ := exchange(id, `{"peer_id":"peer-other"}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = exchange(id, `{"peer_id":"peer-pinned"}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusOK))
	})

	It("binds the split host of a node that registered with the token to its first peer", func() {
		id := register(&nodes.BackendNode{Name: "gpu-4", NodeType: nodes.NodeTypeBackend})
		code, _ := exchange(id, `{"peer_id":"peer-first"}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusOK))

		// A caller that has the registration token but reports another peer
		// for this node is refused, and the stored report keeps the first.
		code, _ = exchange(id, `{"peer_id":"peer-spoofed","workers":[{"device_id":"d1","state":"online"}]}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusConflict))
		rep, ok, err := registry.SplitReport(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(rep.Report.PeerID).To(Equal("peer-first"))
		Expect(rep.PinnedPeer).To(Equal("peer-first"))
		Expect(rep.Report.Workers).To(BeEmpty())
	})

	It("refuses an agent node and an unknown node", func() {
		id := register(&nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent})
		code, _ := exchange(id, `{}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = exchange("nope", `{}`, WithSplitNodes(splitSource{}))
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("answers 409 when this frontend does not serve split workers, so the node keeps its list", func() {
		id := register(&nodes.BackendNode{Name: "gpu-3", NodeType: nodes.NodeTypeBackend})
		code, _ := exchange(id, `{}`)
		Expect(code).To(Equal(http.StatusConflict))
		code, _ = exchange(id, `{}`, WithSplitNodes(splitSource{err: nodes.ErrSplitUnavailable}))
		Expect(code).To(Equal(http.StatusConflict))
	})
})

var _ = Describe("The split server declaration of a node", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	call := func(h echo.HandlerFunc, method, id, body string) (int, map[string]any) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(h(c)).To(Succeed())
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	It("records the start flag at registration", func() {
		code, out := call(RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{}), http.MethodPost, "",
			`{"name":"gpu-flag","address":"10.0.0.2:50051","split_server":true}`)
		Expect(code).To(Equal(http.StatusCreated), "%v", out)
		n, err := registry.GetByName(ctx, "gpu-flag")
		Expect(err).ToNot(HaveOccurred())
		Expect(n.SplitServer).To(BeTrue())
	})

	It("declares a node, and detaches its split workers when the admin clears it", func() {
		n := &nodes.BackendNode{Name: "gpu-1", NodeType: nodes.NodeTypeBackend}
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		var detached []string
		src := WithSplitNodes(splitSource{detached: &detached})

		code, out := call(SetSplitServerEndpoint(registry, src), http.MethodPut, n.ID, `{"split_server":true}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out["split_server"]).To(BeTrue())
		got, _ := registry.Get(ctx, n.ID)
		Expect(got.SplitServer).To(BeTrue())
		Expect(detached).To(BeEmpty())

		code, out = call(SetSplitServerEndpoint(registry, src), http.MethodPut, n.ID, `{"split_server":false}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out["split_server"]).To(BeFalse())
		Expect(out["detached"]).To(BeNumerically("==", 2))
		Expect(detached).To(ConsistOf(n.ID))
		got, _ = registry.Get(ctx, n.ID)
		Expect(got.SplitServer).To(BeFalse())

		code, _ = call(ResetSplitServerEndpoint(registry), http.MethodDelete, n.ID, ``)
		Expect(code).To(Equal(http.StatusOK))
		got, _ = registry.Get(ctx, n.ID)
		Expect(got.SplitServerManuallySet).To(BeFalse())
	})

	It("refuses an agent node, an unknown node and a body without the field", func() {
		agent := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent}
		Expect(registry.Register(ctx, agent, true)).To(Succeed())
		code, _ := call(SetSplitServerEndpoint(registry), http.MethodPut, agent.ID, `{"split_server":true}`)
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = call(SetSplitServerEndpoint(registry), http.MethodPut, "nope", `{"split_server":true}`)
		Expect(code).To(Equal(http.StatusNotFound))
		n := &nodes.BackendNode{Name: "gpu-2", NodeType: nodes.NodeTypeBackend}
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		code, _ = call(SetSplitServerEndpoint(registry), http.MethodPut, n.ID, `{}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})

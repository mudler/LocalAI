package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubNodeCommandSender records whether ListBackends was invoked so the test can
// assert the endpoint short-circuits (no NATS request) for agent-type nodes.
type stubNodeCommandSender struct {
	listBackendsCalled bool
}

func (s *stubNodeCommandSender) InstallBackend(_, _, _, _, _, _, _ string, _ int, _ string, _ func(messaging.BackendInstallProgressEvent)) (*messaging.BackendInstallReply, error) {
	return &messaging.BackendInstallReply{}, nil
}

func (s *stubNodeCommandSender) UpgradeBackend(_, _, _, _, _, _ string, _ int, _ string, _ func(messaging.BackendInstallProgressEvent)) (*messaging.BackendUpgradeReply, error) {
	return &messaging.BackendUpgradeReply{}, nil
}

func (s *stubNodeCommandSender) DeleteBackend(_, _ string) (*messaging.BackendDeleteReply, error) {
	return &messaging.BackendDeleteReply{Success: true}, nil
}

func (s *stubNodeCommandSender) ListBackends(_ string) (*messaging.BackendListReply, error) {
	s.listBackendsCalled = true
	return &messaging.BackendListReply{Backends: []messaging.NodeBackendInfo{{Name: "llama-cpp"}}}, nil
}

func (s *stubNodeCommandSender) StopBackend(_, _ string) error { return nil }

func (s *stubNodeCommandSender) UnloadModelOnNode(_, _ string) error { return nil }

var _ = Describe("ListBackendsOnNodeEndpoint", func() {
	var registry *nodes.NodeRegistry

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	callEndpoint := func(unloader nodes.NodeCommandSender, nodeID string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(nodeID)
		handler := ListBackendsOnNodeEndpoint(unloader, registry)
		Expect(handler(c)).To(Succeed())
		return rec
	}

	It("returns an empty list for an agent node without issuing a NATS request", func() {
		ctx := context.Background()
		node := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent}
		Expect(registry.Register(ctx, node, true)).To(Succeed())

		stub := &stubNodeCommandSender{}
		rec := callEndpoint(stub, node.ID)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(stub.listBackendsCalled).To(BeFalse(),
			"agent workers don't subscribe to backend.list; the endpoint must not issue the doomed NATS request")

		var list []messaging.NodeBackendInfo
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list).To(BeEmpty())
		// Must be `[]`, not `null`, so the UI can render it.
		Expect(rec.Body.String()).To(ContainSubstring("[]"))
	})

	It("consults the unloader (NATS) for a backend node", func() {
		ctx := context.Background()
		node := &nodes.BackendNode{Name: "backend-1", NodeType: nodes.NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())

		stub := &stubNodeCommandSender{}
		rec := callEndpoint(stub, node.ID)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(stub.listBackendsCalled).To(BeTrue(),
			"backend nodes must still be queried over NATS")

		var list []messaging.NodeBackendInfo
		Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Name).To(Equal("llama-cpp"))
	})
})

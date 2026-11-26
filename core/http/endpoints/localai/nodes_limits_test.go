package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Node limits HTTP handlers", func() {
	var registry *nodes.NodeRegistry

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	seed := func(ctx context.Context) string {
		node := &nodes.BackendNode{
			ID: "limits-node", Name: "limits-node", Address: "10.0.0.9:50051",
			Status: nodes.StatusHealthy, TotalVRAM: 16_000_000_000, AvailableVRAM: 16_000_000_000,
		}
		two := 2
		node.SetWorkerLimits(nodes.NodeLimits{MaxActiveBackends: &two})
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		return node.ID
	}

	call := func(h echo.HandlerFunc, method, id, body string) (*httptest.ResponseRecorder, map[string]any) {
		e := echo.New()
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(h(c)).To(Succeed())
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}

	It("shows the effective limits and where each comes from", func(ctx SpecContext) {
		id := seed(ctx)

		rec, out := call(GetNodeLimitsEndpoint(registry), http.MethodGet, id, "")

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(out["max_active_backends"]).To(BeNumerically("==", 2))
		Expect(out["sources"]).To(HaveKeyWithValue("max_active_backends", "worker"))
		Expect(out["sources"]).To(HaveKeyWithValue("model_swap", "cluster"))
	})

	It("sets overrides with PUT and clears them with DELETE", func(ctx SpecContext) {
		id := seed(ctx)

		rec, out := call(SetNodeLimitsEndpoint(registry), http.MethodPut, id, `{"max_active_backends": 1, "model_swap": false, "vram_budget": "50%"}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(out["max_active_backends"]).To(BeNumerically("==", 1))
		Expect(out["model_swap"]).To(BeFalse())
		Expect(out["sources"]).To(HaveKeyWithValue("vram_budget", "override"))
		node, err := registry.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.AvailableVRAM).To(Equal(uint64(8_000_000_000)))

		rec, out = call(ResetNodeLimitsEndpoint(registry), http.MethodDelete, id, "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(out["max_active_backends"]).To(BeNumerically("==", 2))
		Expect(out["model_swap"]).To(BeTrue())
	})

	It("refuses a bad value and an unknown node", func(ctx SpecContext) {
		id := seed(ctx)

		rec, _ := call(SetNodeLimitsEndpoint(registry), http.MethodPut, id, `{"memory_reclaimer_threshold": 2}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))

		rec, _ = call(SetNodeLimitsEndpoint(registry), http.MethodPut, "missing", `{"model_swap": false}`)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("returns the effective limits in the heartbeat reply when the worker asks", func(ctx SpecContext) {
		id := seed(ctx)

		rec, out := call(HeartbeatEndpoint(registry), http.MethodPost, id, `{"want_limits": true}`)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(out["limits"]).To(HaveKeyWithValue("max_active_backends", BeNumerically("==", 2)))

		_, out = call(HeartbeatEndpoint(registry), http.MethodPost, id, `{}`)
		Expect(out).ToNot(HaveKey("limits"))
	})
})

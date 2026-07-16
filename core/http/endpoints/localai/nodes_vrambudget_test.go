package localai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Node VRAM budget HTTP handlers", func() {
	var registry *nodes.NodeRegistry

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// seedHealthyNode registers a healthy node with the given total/available
	// VRAM and returns its ID.
	seedHealthyNode := func(ctx context.Context, total, available uint64) string {
		node := &nodes.BackendNode{
			ID:            "vram-node",
			Name:          "vram-node",
			Address:       "10.0.0.9:50051",
			Status:        nodes.StatusHealthy,
			TotalVRAM:     total,
			AvailableVRAM: available,
		}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		return node.ID
	}

	// doPUT drives UpdateVRAMBudgetEndpoint for the given node ID and body.
	doPUT := func(id, body string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(UpdateVRAMBudgetEndpoint(registry)(c)).To(Succeed())
		return rec
	}

	// doDELETE drives ResetVRAMBudgetEndpoint for the given node ID.
	doDELETE := func(id string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodDelete, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(ResetVRAMBudgetEndpoint(registry)(c)).To(Succeed())
		return rec
	}

	It("sets and clears a node's VRAM budget", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, 16_000_000_000, 16_000_000_000)

		rec := doPUT(id, `{"value":"50%"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		node, err := registry.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.VRAMBudget).To(Equal("50%"))
		Expect(node.AvailableVRAM).To(Equal(uint64(8_000_000_000)))

		rec = doDELETE(id)
		Expect(rec.Code).To(Equal(http.StatusOK))
		node, err = registry.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.VRAMBudget).To(Equal(""))
	})

	It("rejects a malformed budget with 400", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, 16_000_000_000, 16_000_000_000)
		rec := doPUT(id, `{"value":"120%"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 404 for an unknown node on update", func(ctx SpecContext) {
		rec := doPUT("does-not-exist", `{"value":"50%"}`)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 404 for an unknown node on reset", func(ctx SpecContext) {
		rec := doDELETE("does-not-exist")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})
})

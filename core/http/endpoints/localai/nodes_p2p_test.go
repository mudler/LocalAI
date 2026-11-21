package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// workerSet says which peers are paired workers.
type workerSet map[string]bool

func (w workerSet) IsWorkerPeer(_ context.Context, peer string) bool { return w[peer] }

var _ = Describe("Registration of a worker over P2P", func() {
	const regToken = "the-registration-token"

	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		opts     []RegisterOption
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		opts = []RegisterOption{WithNATSHandover(fixedNATS{url: "nats://nats.example:4222"}), WithWorkerPeers(workerSet{"peer-a": true, "peer-b": true})}
	})

	// register sends a registration, over P2P from peer when peer is set.
	register := func(body, peer string, autoApprove bool) (int, map[string]any) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if peer != "" {
			req = req.WithContext(p2papi.WithPeer(req.Context(), peer))
		}
		rec := httptest.NewRecorder()
		Expect(RegisterNodeEndpoint(registry, regToken, autoApprove, nil, "", natsauth.Config{}, opts...)(e.NewContext(req, rec))).To(Succeed())
		var resp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp
	}

	It("registers a paired worker without the token, pinned to its peer, pending, with a tunnel credential and never a NATS one", func() {
		code, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["tunnel","nats"]}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(resp["status"]).To(Equal(nodes.StatusPending))
		Expect(resp).To(HaveKey("tunnel_token"))
		Expect(resp).ToNot(HaveKey("nats_jwt"))
		Expect(resp).ToNot(HaveKey("nats_url"), "a worker over P2P never talks to NATS")

		node, err := registry.GetByName(ctx, "w")
		Expect(err).ToNot(HaveOccurred())
		Expect(node.PeerID).To(Equal("peer-a"))
		Expect(node.Address).To(BeEmpty(), "a worker over P2P is tunnel-only")
	})

	It("keeps the approval of a worker that registers again from its peer", func() {
		code, resp := register(`{"name":"w"}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(registry.ApproveNode(ctx, resp["id"].(string))).To(Succeed())
		code, resp = register(`{"name":"w"}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(resp["status"]).To(Equal(nodes.StatusHealthy))
	})

	It("refuses the name of a P2P worker to another peer and to the registration token", func() {
		code, _ := register(`{"name":"w"}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusCreated))
		code, _ = register(`{"name":"w"}`, "peer-b", true)
		Expect(code).To(Equal(http.StatusConflict))
		code, _ = register(`{"name":"w","address":"10.0.0.1:50051","token":"`+regToken+`"}`, "", true)
		Expect(code).To(Equal(http.StatusConflict))

		node, err := registry.GetByName(ctx, "w")
		Expect(err).ToNot(HaveOccurred())
		Expect(node.PeerID).To(Equal("peer-a"))
	})

	It("refuses the name of a node of the registration token to a P2P worker", func() {
		code, _ := register(`{"name":"t","address":"10.0.0.1:50051","token":"`+regToken+`"}`, "", true)
		Expect(code).To(Equal(http.StatusCreated))
		code, _ = register(`{"name":"t"}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusConflict))
	})

	It("asks a peer that is not a paired worker for the registration token", func() {
		code, _ := register(`{"name":"w"}`, "peer-unknown", true)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("never trusts a peer when no worker set is configured", func() {
		opts = nil
		code, _ := register(`{"name":"w"}`, "peer-a", true)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})
})

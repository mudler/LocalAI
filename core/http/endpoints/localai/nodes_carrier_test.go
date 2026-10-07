package localai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixedCarrier answers with a row that the spec chose.
type fixedCarrier struct {
	row cluster.CarrierRow
	err error
}

func (f fixedCarrier) Get(context.Context) (cluster.CarrierRow, error) { return f.row, f.err }

var _ = Describe("Registration and the active carrier", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		natsCfg  natsauth.Config
	)

	tunnelRow := cluster.CarrierRow{Active: cluster.CarrierTunnel, Epoch: 7, State: cluster.StateStable}
	natsRow := cluster.CarrierRow{Active: cluster.CarrierNATS, Epoch: 3, State: cluster.StateStable}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())

		akp, err := nkeys.CreateAccount()
		Expect(err).ToNot(HaveOccurred())
		seed, err := akp.Seed()
		Expect(err).ToNot(HaveOccurred())
		natsCfg = natsauth.Config{AccountSeed: string(seed)}
	})

	register := func(body string, autoApprove bool, opts ...RegisterOption) (int, map[string]any) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		Expect(RegisterNodeEndpoint(registry, "", autoApprove, nil, "", natsCfg, opts...)(e.NewContext(req, rec))).To(Succeed())
		var resp map[string]any
		if rec.Code == http.StatusCreated {
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		}
		return rec.Code, resp
	}

	storedHash := func(name string) string {
		GinkgoHelper()
		node, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return node.TunnelTokenHash
	}

	Describe("a deployment with no carrier reader", func() {
		It("answers as it always did: no carrier, no tunnel token", func() {
			code, resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp).ToNot(HaveKey("carrier"))
			Expect(resp).ToNot(HaveKey("carrier_epoch"))
			Expect(resp).ToNot(HaveKey("tunnel_token"))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(storedHash("w")).To(BeEmpty())
		})
	})

	Describe("with NATS active", func() {
		reader := WithCarrierReader(fixedCarrier{row: natsRow})

		It("names the carrier and its epoch, and hands over the NATS credential only", func() {
			code, resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["carrier"]).To(Equal("nats"))
			Expect(resp["carrier_epoch"]).To(BeEquivalentTo(3))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(resp).ToNot(HaveKey("tunnel_token"))
			Expect(storedHash("w")).To(BeEmpty())
		})

		It("still requires the address of a backend worker", func() {
			code, _ := register(`{"name":"w"}`, true, reader)
			Expect(code).To(Equal(http.StatusBadRequest))
		})

		It("keeps the address of a worker that sends routable false, because NATS dials it", func() {
			code, _ := register(`{"name":"w","address":"10.0.0.1:50051","routable":false}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(Equal("10.0.0.1:50051"))
		})
	})

	Describe("with the tunnel active", func() {
		reader := WithCarrierReader(fixedCarrier{row: tunnelRow})

		It("names the carrier and hands over a tunnel token, and no NATS credential", func() {
			code, resp := register(`{"name":"w"}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["carrier"]).To(Equal("tunnel"))
			Expect(resp["carrier_epoch"]).To(BeEquivalentTo(7))
			Expect(resp).ToNot(HaveKey("nats_jwt"))
			Expect(resp).ToNot(HaveKey("nats_user_seed"))
			Expect(resp["tunnel_token"]).To(BeAssignableToTypeOf(""))
		})

		It("stores only the SHA-256 of the token", func() {
			_, resp := register(`{"name":"w"}`, true, reader)
			token := resp["tunnel_token"].(string)
			Expect(len(token)).To(BeNumerically(">=", 26), "128 bits of randomness need at least 26 base32 characters")

			sum := sha256.Sum256([]byte(token))
			Expect(storedHash("w")).To(Equal(hex.EncodeToString(sum[:])))
			Expect(storedHash("w")).ToNot(ContainSubstring(token))
		})

		It("mints a new token at every registration and the old token stops matching", func() {
			_, first := register(`{"name":"w"}`, true, reader)
			firstHash := storedHash("w")
			_, second := register(`{"name":"w"}`, true, reader)

			Expect(second["tunnel_token"]).ToNot(Equal(first["tunnel_token"]))
			Expect(storedHash("w")).ToNot(Equal(firstHash))
			Expect(second["id"]).To(Equal(first["id"]), "the node keeps its identity")
		})

		It("gives a token to a node that waits for approval, and the token is inert until then", func() {
			code, resp := register(`{"name":"w"}`, false, reader)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["status"]).To(Equal(nodes.StatusPending))
			Expect(resp["tunnel_token"]).ToNot(BeEmpty())
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Status).To(Equal(nodes.StatusPending), "the connect endpoint refuses a pending node")
		})

		It("gives a token to an agent worker too", func() {
			code, resp := register(`{"name":"agent","node_type":"agent"}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["tunnel_token"]).ToNot(BeEmpty())
		})

		It("does not need an address", func() {
			code, _ := register(`{"name":"w"}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
		})

		It("keeps the address of a worker that has a routable one", func() {
			code, _ := register(`{"name":"w","address":"10.0.0.1:50051","http_address":"10.0.0.1:50050","routable":true}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(Equal("10.0.0.1:50051"))
			Expect(node.HTTPAddress).To(Equal("10.0.0.1:50050"))
		})

		It("keeps the address of a worker that predates the routable field", func() {
			code, _ := register(`{"name":"w","address":"10.0.0.1:50051"}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(Equal("10.0.0.1:50051"))
		})

		It("drops the addresses of a worker that says nothing can reach it, also from an earlier registration", func() {
			code, _ := register(`{"name":"w","address":"10.0.0.1:50051","http_address":"10.0.0.1:50050"}`, true, WithCarrierReader(fixedCarrier{row: natsRow}))
			Expect(code).To(Equal(http.StatusCreated))

			code, _ = register(`{"name":"w","address":"host:50051","http_address":"host:50050","routable":false}`, true, reader)
			Expect(code).To(Equal(http.StatusCreated))

			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(BeEmpty())
			Expect(node.HTTPAddress).To(BeEmpty())
		})
	})

	It("answers as for NATS when the carrier row cannot be read", func() {
		code, resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, true,
			WithCarrierReader(fixedCarrier{err: errors.New("database is down")}))
		Expect(code).To(Equal(http.StatusCreated))
		Expect(resp).ToNot(HaveKey("carrier"))
		Expect(resp).To(HaveKey("nats_jwt"))
		Expect(resp).ToNot(HaveKey("tunnel_token"))
	})

	Describe("approval", func() {
		approve := func(id string, opts ...RegisterOption) map[string]any {
			GinkgoHelper()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(id)
			Expect(ApproveNodeEndpoint(registry, nil, "", natsCfg, opts...)(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return resp
		}

		It("mints the NATS credential when NATS is active", func() {
			_, reg := register(`{"name":"w","address":"10.0.0.1:50051"}`, false)
			resp := approve(reg["id"].(string), WithCarrierReader(fixedCarrier{row: natsRow}))
			Expect(resp).To(HaveKey("nats_jwt"))
		})

		It("does not mint a NATS credential when the tunnel is active, and leaves the tunnel token alone", func() {
			reader := WithCarrierReader(fixedCarrier{row: tunnelRow})
			_, reg := register(`{"name":"w"}`, false, reader)
			before := storedHash("w")

			resp := approve(reg["id"].(string), reader)

			Expect(resp).ToNot(HaveKey("nats_jwt"))
			Expect(resp).ToNot(HaveKey("tunnel_token"), "the worker holds the token from its registration")
			Expect(storedHash("w")).To(Equal(before))
		})
	})

	Describe("a node type that holds no tunnel credential", func() {
		It("has the credential cleared and gets no token", func() {
			_, reg := register(`{"name":"w"}`, true, WithCarrierReader(fixedCarrier{row: tunnelRow}))
			id := reg["id"].(string)
			Expect(storedHash("w")).ToNot(BeEmpty())

			response := map[string]any{}
			attachTunnelToken(ctx, response, registry, &nodes.BackendNode{ID: id, Name: "w", NodeType: "gpu-farm"})

			Expect(response).ToNot(HaveKey("tunnel_token"))
			Expect(storedHash("w")).To(BeEmpty())
		})
	})
})

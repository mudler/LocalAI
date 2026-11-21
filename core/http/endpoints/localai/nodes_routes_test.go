package localai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixedNATS is the handover of a deployment with a NATS server.
type fixedNATS struct {
	url, ca   string
	clientTLS bool
	err       error
}

func (f fixedNATS) WorkerURL(context.Context) (string, error) { return f.url, f.err }
func (f fixedNATS) CAPEM() (string, error)                    { return f.ca, nil }
func (f fixedNATS) ClientTLS() bool                           { return f.clientTLS }

// recordingDisconnector records the nodes whose tunnel it was asked to end.
type recordingDisconnector struct {
	mu    sync.Mutex
	nodes []string
}

func (r *recordingDisconnector) Disconnect(nodeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes = append(r.nodes, nodeID)
	return true
}

func (r *recordingDisconnector) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.nodes)
}

var _ = Describe("Registration mints a credential for each route a worker asks for", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		natsCfg  natsauth.Config
		tunnels  *recordingDisconnector
	)
	handover := WithNATSHandover(fixedNATS{url: "nats://nats.example:4222", ca: "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----\n"})

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		registry, err = nodes.NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
		tunnels = &recordingDisconnector{}
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
		opts = append(opts, WithTunnelDisconnector(tunnels))
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

	It("mints nothing for a registration that asks for no route, and names no carrier", func() {
		code, resp := register(`{"name":"w","address":"10.0.0.1:50051"}`, true, handover)
		Expect(code).To(Equal(http.StatusCreated))
		Expect(resp["id"]).ToNot(BeEmpty())
		for _, k := range []string{"tunnel_token", "nats_jwt", "nats_user_seed", "nats_url", "carrier", "carrier_epoch", "credential_for"} {
			Expect(resp).ToNot(HaveKey(k))
		}
		Expect(storedHash("w")).To(BeEmpty())
		Expect(tunnels.seen()).To(BeEmpty())
	})

	Describe("the NATS route", func() {
		It("hands over the NATS credential, the address and the CA, and no tunnel token", func() {
			code, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["nats"]}`, true, handover)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(resp["nats_url"]).To(Equal("nats://nats.example:4222"))
			Expect(resp["nats_ca"]).To(ContainSubstring("BEGIN CERTIFICATE"))
			Expect(resp).ToNot(HaveKey("nats_client_tls"))
			Expect(resp).ToNot(HaveKey("tunnel_token"))
			Expect(storedHash("w")).To(BeEmpty())
		})

		It("says that the worker needs a certificate of its own when the server asks for one", func() {
			_, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["nats"]}`, true,
				WithNATSHandover(fixedNATS{url: "tls://nats.example:4222", clientTLS: true}))
			Expect(resp["nats_client_tls"]).To(BeTrue())
		})

		It("hands over no address when none is configured, and when the read fails", func() {
			_, a := register(`{"name":"a","address":"10.0.0.1:50051","routes":["nats"]}`, true, WithNATSHandover(fixedNATS{}))
			Expect(a).ToNot(HaveKey("nats_url"))
			_, b := register(`{"name":"b","address":"10.0.0.1:50051","routes":["nats"]}`, true, WithNATSHandover(fixedNATS{err: errors.New("down")}))
			Expect(b).ToNot(HaveKey("nats_url"))
		})

		It("requires the address of a backend worker, which the frontends dial", func() {
			code, _ := register(`{"name":"w","routes":["nats"]}`, true)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = register(`{"name":"w","address":"host:50051","routable":false,"routes":["nats"]}`, true)
			Expect(code).To(Equal(http.StatusBadRequest), "an address that nothing can dial is no address")
		})

		It("does not need the address of an agent worker", func() {
			code, resp := register(`{"name":"agent","node_type":"agent","routes":["nats"]}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp).To(HaveKey("nats_jwt"))
		})

		It("mints the NATS credential only for an approved node", func() {
			_, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["nats"]}`, false, handover)
			Expect(resp["status"]).To(Equal(nodes.StatusPending))
			Expect(resp).ToNot(HaveKey("nats_jwt"))
		})

		It("leaves the tunnel of the node alone, so the refresh of the NATS credential never ends its session", func() {
			_, first := register(`{"name":"w","address":"10.0.0.1:50051","routes":["tunnel"]}`, true)
			hash := storedHash("w")
			sessionsEnded := len(tunnels.seen())

			_, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["nats"]}`, true)
			Expect(resp["id"]).To(Equal(first["id"]))
			Expect(storedHash("w")).To(Equal(hash))
			Expect(tunnels.seen()).To(HaveLen(sessionsEnded))
		})
	})

	Describe("the tunnel route", func() {
		It("hands over a tunnel token and no NATS credential", func() {
			code, resp := register(`{"name":"w","routes":["tunnel"]}`, true, handover)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["tunnel_token"]).To(BeAssignableToTypeOf(""))
			Expect(resp).ToNot(HaveKey("nats_jwt"))
			Expect(resp).ToNot(HaveKey("nats_user_seed"))
			Expect(resp).ToNot(HaveKey("nats_url"))
		})

		It("stores only the SHA-256 of the token", func() {
			_, resp := register(`{"name":"w","routes":["tunnel"]}`, true)
			token := resp["tunnel_token"].(string)
			Expect(len(token)).To(BeNumerically(">=", 26), "128 bits of randomness need at least 26 base32 characters")
			sum := sha256.Sum256([]byte(token))
			Expect(storedHash("w")).To(Equal(hex.EncodeToString(sum[:])))
			Expect(storedHash("w")).ToNot(ContainSubstring(token))
		})

		It("rotates the token at every registration for the tunnel, and ends the session of the old one", func() {
			_, first := register(`{"name":"w","routes":["tunnel"]}`, true)
			firstHash := storedHash("w")
			_, second := register(`{"name":"w","routes":["tunnel"]}`, true)
			Expect(second["tunnel_token"]).ToNot(Equal(first["tunnel_token"]))
			Expect(storedHash("w")).ToNot(Equal(firstHash))
			Expect(second["id"]).To(Equal(first["id"]), "the node keeps its identity")
			Expect(tunnels.seen()).To(Equal([]string{first["id"].(string), first["id"].(string)}))
		})

		It("gives a token to a node that waits for approval, and the token is inert until then", func() {
			code, resp := register(`{"name":"w","routes":["tunnel"]}`, false)
			Expect(code).To(Equal(http.StatusCreated))
			Expect(resp["status"]).To(Equal(nodes.StatusPending))
			Expect(resp["tunnel_token"]).ToNot(BeEmpty())
		})

		It("gives a token to an agent worker too", func() {
			_, resp := register(`{"name":"agent","node_type":"agent","routes":["tunnel"]}`, true)
			Expect(resp["tunnel_token"]).ToNot(BeEmpty())
		})

		It("needs no address, and keeps a routable one", func() {
			code, _ := register(`{"name":"a","routes":["tunnel"]}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			code, _ = register(`{"name":"b","address":"10.0.0.1:50051","http_address":"10.0.0.1:50050","routable":true,"routes":["tunnel"]}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			node, err := registry.GetByName(ctx, "b")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(Equal("10.0.0.1:50051"))
			Expect(node.HTTPAddress).To(Equal("10.0.0.1:50050"))
		})

		It("drops the addresses of a worker that says nothing can reach it, also from an earlier registration", func() {
			code, _ := register(`{"name":"w","address":"10.0.0.1:50051","http_address":"10.0.0.1:50050"}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			code, _ = register(`{"name":"w","address":"host:50051","http_address":"host:50050","routable":false,"routes":["tunnel"]}`, true)
			Expect(code).To(Equal(http.StatusCreated))
			node, err := registry.GetByName(ctx, "w")
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Address).To(BeEmpty())
			Expect(node.HTTPAddress).To(BeEmpty())
		})
	})

	It("mints both credentials for a registration that asks for both routes", func() {
		_, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["nats","tunnel","carrier-pigeon"]}`, true, handover)
		Expect(resp).To(HaveKey("nats_jwt"))
		Expect(resp).To(HaveKey("nats_url"))
		Expect(resp).To(HaveKey("tunnel_token"))
	})

	Describe("approval", func() {
		approve := func(id string) map[string]any {
			GinkgoHelper()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(id)
			Expect(ApproveNodeEndpoint(registry, nil, "", natsCfg)(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return resp
		}

		It("mints the NATS credential, and leaves the tunnel token alone", func() {
			_, reg := register(`{"name":"w","address":"10.0.0.1:50051","routes":["tunnel"]}`, false)
			before := storedHash("w")
			resp := approve(reg["id"].(string))
			Expect(resp).To(HaveKey("nats_jwt"))
			Expect(resp).ToNot(HaveKey("tunnel_token"), "the worker holds the token from its registration")
			Expect(storedHash("w")).To(Equal(before))
		})
	})

	Describe("a node type that holds no tunnel credential", func() {
		It("has the credential cleared and gets no token", func() {
			_, reg := register(`{"name":"w","routes":["tunnel"]}`, true)
			id := reg["id"].(string)
			Expect(storedHash("w")).ToNot(BeEmpty())

			response := map[string]any{}
			attachTunnelToken(ctx, response, registry, &nodes.BackendNode{ID: id, Name: "w", NodeType: "gpu-farm"}, nil)

			Expect(response).ToNot(HaveKey("tunnel_token"))
			Expect(storedHash("w")).To(BeEmpty())
		})
	})

	It("ends the session when an admin removes the node", func() {
		_, reg := register(`{"name":"w","routes":["tunnel"]}`, true)
		id := reg["id"].(string)
		before := len(tunnels.seen())

		e := echo.New()
		req := httptest.NewRequest(http.MethodDelete, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		Expect(DeregisterNodeEndpoint(registry, nil, WithTunnelDisconnector(tunnels))(c)).To(Succeed())

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(tunnels.seen()).To(HaveLen(before + 1))
		_, err := registry.Get(ctx, id)
		Expect(err).To(HaveOccurred())
	})

	Describe("the heartbeat", func() {
		var id string

		BeforeEach(func() {
			_, resp := register(`{"name":"w","address":"10.0.0.1:50051","routes":["tunnel"]}`, true)
			id = resp["id"].(string)
		})

		beat := func(body string) (int, map[string]any) {
			GinkgoHelper()
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(id)
			Expect(HeartbeatEndpoint(registry)(c)).To(Succeed())
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return rec.Code, resp
		}

		It("never mints a tunnel token, and tells the worker nothing about a carrier", func() {
			hash := storedHash("w")
			ended := len(tunnels.seen())
			for range 3 {
				code, resp := beat(`{"routes":["nats","tunnel"],"attached":["nats","tunnel"]}`)
				Expect(code).To(Equal(http.StatusOK))
				Expect(resp).To(Equal(map[string]any{"message": "heartbeat received"}))
			}
			Expect(storedHash("w")).To(Equal(hash))
			Expect(tunnels.seen()).To(HaveLen(ended))
		})

		It("stores the routes the worker was given, the ones it holds, and why the others are not held", func() {
			code, _ := beat(`{"routes":["nats","tunnel"],"attached":["nats","tunnel"],"route_error":"","tunnel_transport":"websocket"}`)
			Expect(code).To(Equal(http.StatusOK))
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("nats,tunnel"))
			Expect(node.Follow).To(Equal("nats,tunnel"))
			Expect(node.TunnelTransport).To(Equal(nodes.TunnelWebSocket))
			Expect(nodes.RoutesOf(node)).To(Equal([]nodes.Route{{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket}}))

			beat(`{"routes":["nats","tunnel"],"attached":["tunnel"],"route_error":"nats: connecting","tunnel_transport":"websocket"}`)
			node, err = registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("tunnel"))
			Expect(node.FollowError).To(Equal("nats: connecting"))
			Expect(nodes.RoutesOf(node)).To(Equal([]nodes.Route{{Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket}}),
				"a route that the worker does not hold is no route of the node")
		})

		It("stores an empty attachment as a report, because the worker still names its routes", func() {
			beat(`{"routes":["nats"],"attached":["nats"]}`)
			beat(`{"routes":["nats","tunnel"],"attached":[]}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(BeEmpty())
			Expect(node.Follow).To(Equal("nats,tunnel"))
			Expect(nodes.RoutesOf(node)).To(BeEmpty())
		})

		It("stores nothing for a heartbeat that names no route, and leaves an earlier report alone", func() {
			beat(`{"routes":["tunnel"],"attached":["tunnel"]}`)
			beat(`{"available_ram":1024}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Follow).To(Equal("tunnel"))
			Expect(node.Attached).To(Equal("tunnel"))
		})

		It("keeps only routes that the cluster knows and cuts a reason that does not fit the column", func() {
			beat(`{"attached":["nats","gopher","nats"],"routes":["tunnel","nope"],"route_error":"` + strings.Repeat("é", 200) + `"}`)
			node, err := registry.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.Attached).To(Equal("nats"))
			Expect(node.Follow).To(Equal("tunnel"))
			Expect(len(node.FollowError)).To(BeNumerically("<=", 255))
			Expect(node.FollowError).To(HavePrefix("é"))
		})

		It("answers 404 for a node that is not registered and stores nothing", func() {
			id = "no-such-node"
			code, _ := beat(`{"attached":["nats"],"routes":["nats"]}`)
			Expect(code).To(Equal(http.StatusNotFound))
		})
	})
})

package localai

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hashOf is how the node registry stores a secret: hex-encoded SHA-256.
func hashOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

var _ = DescribeTable("token validation",
	func(expectedToken, providedToken string, wantMatch bool) {
		if expectedToken == "" {
			// No auth required — always matches
			Expect(wantMatch).To(BeTrue(), "no-auth should always pass")
			return
		}

		if providedToken == "" {
			Expect(wantMatch).To(BeFalse(), "empty token should be rejected")
			return
		}

		expectedHash := sha256.Sum256([]byte(expectedToken))
		providedHash := sha256.Sum256([]byte(providedToken))
		match := subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) == 1

		Expect(match).To(Equal(wantMatch))
	},
	Entry("matching tokens", "my-secret-token", "my-secret-token", true),
	Entry("mismatched tokens", "my-secret-token", "wrong-token", false),
	Entry("empty expected (no auth)", "", "any-token", true),
	Entry("empty provided when expected set", "my-secret-token", "", false),
)

var _ = Describe("Node HTTP handlers", func() {
	var (
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("RegisterNodeEndpoint", func() {
		It("registers a backend node and returns 201", func() {
			e := echo.New()
			body := `{"name":"worker-1","address":"10.0.0.1:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["name"]).To(Equal("worker-1"))
			Expect(resp["id"]).ToNot(BeEmpty())
			Expect(resp["status"]).To(Equal(nodes.StatusHealthy))
		})

		// register posts one registration and returns the decoded response.
		register := func(body string, expectedToken string, autoApprove bool) map[string]any {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, expectedToken, autoApprove, nil, "", natsauth.Config{})
			ExpectWithOffset(1, handler(c)).To(Succeed())
			ExpectWithOffset(1, rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			return resp
		}

		It("mints a per-node tunnel credential and stores only its hash", func() {
			resp := register(`{"name":"worker-tunnel","address":"10.0.0.3:50051","token":"shared-registration-token"}`,
				"shared-registration-token", true)

			plaintext, _ := resp["tunnel_token"].(string)
			Expect(plaintext).ToNot(BeEmpty())
			// Not the registration token. That is the whole point: a leaked
			// registration token plus a known node ID used to open a tunnel,
			// because the tunnel authenticated against the hash of exactly the
			// value every worker in the deployment holds.
			Expect(plaintext).ToNot(Equal("shared-registration-token"))

			node, err := registry.Get(context.Background(), resp["id"].(string))
			Expect(err).ToNot(HaveOccurred())
			// Stored as a hash, never as the secret.
			Expect(node.TunnelTokenHash).To(Equal(hashOf(plaintext)))
			Expect(node.TunnelTokenHash).ToNot(Equal(plaintext))
			// And it is a DIFFERENT column from the registration token's hash,
			// which is what the tunnel used to compare against.
			Expect(node.TunnelTokenHash).ToNot(Equal(node.TokenHash))
			Expect(node.TokenHash).To(Equal(hashOf("shared-registration-token")))

			// The security property, which none of the above actually pins: a
			// second node registering with the SAME shared token gets a
			// DIFFERENT credential. Everything above is satisfied by a secret
			// derived deterministically from the registration token, which
			// would isolate nothing; a mutation that did exactly that passed
			// every assertion before this one.
			other := register(`{"name":"worker-tunnel-2","address":"10.0.0.3:50052","token":"shared-registration-token"}`,
				"shared-registration-token", true)
			Expect(other["tunnel_token"]).ToNot(Equal(plaintext))
		})

		It("rotates the tunnel credential on every re-registration", func() {
			body := `{"name":"worker-rotate","address":"10.0.0.4:50051"}`
			first := register(body, "", true)
			second := register(body, "", true)

			Expect(second["id"]).To(Equal(first["id"]), "re-registration must keep the node identity")
			firstToken := first["tunnel_token"].(string)
			secondToken := second["tunnel_token"].(string)
			// Only the hash is stored, so a re-registering worker cannot be told
			// the secret it already holds; the alternative to rotating would be
			// storing the plaintext.
			Expect(secondToken).ToNot(Equal(firstToken))

			node, err := registry.Get(context.Background(), first["id"].(string))
			Expect(err).ToNot(HaveOccurred())
			Expect(node.TunnelTokenHash).To(Equal(hashOf(secondToken)))
			Expect(node.TunnelTokenHash).ToNot(Equal(hashOf(firstToken)))
		})

		It("issues a tunnel credential to a node still awaiting approval", func() {
			// Deliberately unlike the agent API key and the NATS JWT, which are
			// both withheld from a pending node. Those work the moment they are
			// issued; this one does not, because the tunnel endpoint re-reads
			// the node's status on every dial and refuses a pending node. A
			// worker that registers exactly once would otherwise never receive
			// one, since approval alone prompts no re-registration.
			first := register(`{"name":"worker-pending","address":"10.0.0.5:50051","token":"shared"}`, "shared", false)
			Expect(first["status"]).To(Equal(nodes.StatusPending))
			plaintext, _ := first["tunnel_token"].(string)
			Expect(plaintext).ToNot(BeEmpty())

			// Non-empty alone does not pin per-node-ness, and a review's
			// variant of the "derived from the shared token" mutation stayed
			// green on exactly that gap. A pending node's credential has to be
			// as unpredictable and as per-node as an approved one's, since it
			// becomes live the moment an admin approves.
			Expect(plaintext).ToNot(Equal("shared"))
			node, err := registry.Get(context.Background(), first["id"].(string))
			Expect(err).ToNot(HaveOccurred())
			Expect(node.TunnelTokenHash).To(Equal(hashOf(plaintext)))
			Expect(node.TunnelTokenHash).ToNot(Equal(node.TokenHash))

			second := register(`{"name":"worker-pending-2","address":"10.0.0.5:50052","token":"shared"}`, "shared", false)
			Expect(second["status"]).To(Equal(nodes.StatusPending))
			Expect(second["tunnel_token"]).ToNot(Equal(plaintext))
		})

		It("does not issue a tunnel credential to an agent node", func() {
			// An agent worker serves no gRPC backends and no file staging;
			// nothing dials into it, so a tunnel replaces nothing for it and no
			// client on its side would open one. Minting anyway would be
			// credential surface with no feature behind it.
			//
			// Enforcement is structural rather than a second check: with no
			// credential minted, the node's hash stays empty and the tunnel
			// route refuses it like any other node without one.
			resp := register(`{"name":"agent-1","node_type":"agent"}`, "", true)
			Expect(resp["node_type"]).To(Equal(nodes.NodeTypeAgent))
			Expect(resp).ToNot(HaveKey("tunnel_token"))

			node, err := registry.Get(context.Background(), resp["id"].(string))
			Expect(err).ToNot(HaveOccurred())
			Expect(node.TunnelTokenHash).To(BeEmpty())
		})

		It("clears a tunnel credential when a node stops being a backend node", func() {
			// Register upserts BY NAME, so a node can change node_type in place.
			// Skipping the mint on the way through leaves the credential the
			// node earned as a backend sitting on a row that is now an agent:
			// Register's struct Updates zero-skips the column while writing the
			// new node_type, so nothing else clears it. ConnectHandler never
			// looks at node_type, so that stale hash is a usable tunnel
			// credential for a node type that is not supposed to hold one.
			//
			// This is the same shape as the Register-upserts-by-name hazard
			// already carried forward: a name is not an identity.
			backend := register(`{"name":"shifty","address":"10.0.0.7:50051"}`, "", true)
			Expect(backend["tunnel_token"]).ToNot(BeEmpty())

			agent := register(`{"name":"shifty","node_type":"agent"}`, "", true)
			Expect(agent["id"]).To(Equal(backend["id"]), "re-registration must keep the node identity")
			Expect(agent["node_type"]).To(Equal(nodes.NodeTypeAgent))
			Expect(agent).ToNot(HaveKey("tunnel_token"))

			node, err := registry.Get(context.Background(), backend["id"].(string))
			Expect(err).ToNot(HaveOccurred())
			// The claim the gate makes is that an ineligible node HAS no
			// credential, not merely that it was not handed a new one. Only
			// then is the empty-hash refusal in ConnectHandler the enforcement.
			Expect(node.TunnelTokenHash).To(BeEmpty(),
				"the node kept the credential it earned as a backend, so the mint-site gate is not structural")
		})

		It("returns nats_jwt when account seed is configured", func() {
			akp, err := nkeys.CreateAccount()
			Expect(err).ToNot(HaveOccurred())
			seed, err := akp.Seed()
			Expect(err).ToNot(HaveOccurred())

			e := echo.New()
			body := `{"name":"worker-nats","address":"10.0.0.2:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			natsCfg := natsauth.Config{AccountSeed: string(seed)}
			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsCfg)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["nats_jwt"]).ToNot(BeEmpty())
		})

		It("returns 400 when name is missing", func() {
			e := echo.New()
			body := `{"address":"10.0.0.1:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("name is required"))
		})

		It("returns 400 when name exceeds 255 characters", func() {
			e := echo.New()
			longName := strings.Repeat("x", 256)
			body := `{"name":"` + longName + `","address":"10.0.0.1:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("exceeds 255 characters"))
		})

		It("returns 400 when address is missing for backend node type", func() {
			e := echo.New()
			body := `{"name":"worker-no-addr"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("address is required"))
		})

		It("returns 400 when node_type is invalid", func() {
			e := echo.New()
			body := `{"name":"bad-type","address":"10.0.0.1:50051","node_type":"invalid"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("invalid node_type"))
		})

		It("returns 401 when registration token is wrong", func() {
			e := echo.New()
			body := `{"name":"worker-1","address":"10.0.0.1:50051","token":"wrong-token"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "correct-token", true, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		})

		It("sets status to pending when autoApprove is false", func() {
			e := echo.New()
			body := `{"name":"pending-worker","address":"10.0.0.1:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", false, nil, "", natsauth.Config{})
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["status"]).To(Equal(nodes.StatusPending))
		})

		// Regression: a worker re-register used to wipe every UI-added label
		// because the endpoint called SetNodeLabels (replace-all) with only
		// what the worker sent. Operators reported "labels assigned to node
		// do not persist" — the labels survived until the next worker
		// restart, then disappeared.
		It("preserves UI-added labels across worker re-register", func() {
			ctx := context.Background()
			e := echo.New()

			// 1. Worker first-registers with one label.
			body1 := `{"name":"worker-merge","address":"10.0.0.50:50051","labels":{"tier":"a","gpu":"a100"}}`
			req1 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body1))
			req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec1 := httptest.NewRecorder()
			handler := RegisterNodeEndpoint(registry, "", true, nil, "", natsauth.Config{})
			Expect(handler(e.NewContext(req1, rec1))).To(Succeed())
			Expect(rec1.Code).To(Equal(http.StatusCreated))

			node, err := registry.GetByName(ctx, "worker-merge")
			Expect(err).ToNot(HaveOccurred())
			Expect(node).ToNot(BeNil())

			// 2. Operator adds a label via the UI.
			Expect(registry.SetNodeLabel(ctx, node.ID, "owner", "ettore")).To(Succeed())

			// 3. Worker restarts and re-registers, sending its own labels
			//    (different from the UI-added one).
			body2 := `{"name":"worker-merge","address":"10.0.0.50:50051","labels":{"tier":"b","gpu":"a100"}}`
			req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body2))
			req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec2 := httptest.NewRecorder()
			Expect(handler(e.NewContext(req2, rec2))).To(Succeed())
			Expect(rec2.Code).To(Equal(http.StatusCreated))

			// 4. Assert the UI-added label survived AND the worker labels updated.
			labels, err := registry.GetNodeLabels(ctx, node.ID)
			Expect(err).ToNot(HaveOccurred())
			byKey := map[string]string{}
			for _, l := range labels {
				byKey[l.Key] = l.Value
			}
			Expect(byKey).To(HaveKeyWithValue("owner", "ettore"),
				"UI-added label must survive a worker re-register")
			Expect(byKey).To(HaveKeyWithValue("tier", "b"),
				"worker label updates must apply on re-register")
			Expect(byKey).To(HaveKeyWithValue("gpu", "a100"))
		})
	})

	Describe("SetSchedulingEndpoint", func() {
		postScheduling := func(body string) *httptest.ResponseRecorder {
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			handler := SetSchedulingEndpoint(registry)
			Expect(handler(c)).To(Succeed())
			return rec
		}

		It("persists prefix-cache fields and round-trips them via GET", func() {
			ctx := context.Background()
			rec := postScheduling(`{"model_name":"pc-model","route_policy":"prefix_cache","balance_abs_threshold":3,"min_prefix_match":0.4}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			cfg, err := registry.GetModelScheduling(ctx, "pc-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg).ToNot(BeNil())
			Expect(cfg.RoutePolicy).To(Equal("prefix_cache"))
			Expect(cfg.BalanceAbsThreshold).To(Equal(3))
			Expect(cfg.MinPrefixMatch).To(BeNumerically("~", 0.4, 1e-9))

			e := echo.New()
			getReq := httptest.NewRequest(http.MethodGet, "/", nil)
			getRec := httptest.NewRecorder()
			gc := e.NewContext(getReq, getRec)
			gc.SetParamNames("model")
			gc.SetParamValues("pc-model")
			Expect(GetSchedulingEndpoint(registry)(gc)).To(Succeed())
			Expect(getRec.Code).To(Equal(http.StatusOK))

			var got nodes.ModelSchedulingConfig
			Expect(json.Unmarshal(getRec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.RoutePolicy).To(Equal("prefix_cache"))
			Expect(got.BalanceAbsThreshold).To(Equal(3))
			Expect(got.MinPrefixMatch).To(BeNumerically("~", 0.4, 1e-9))
		})

		It("returns 400 for an out-of-range min_prefix_match", func() {
			rec := postScheduling(`{"model_name":"bad-mpm","min_prefix_match":2}`)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("min_prefix_match"))
		})

		It("returns 400 for an unknown route_policy", func() {
			rec := postScheduling(`{"model_name":"bad-policy","route_policy":"bogus"}`)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("route_policy"))
		})

		It("returns 400 for a balance_rel_threshold between 0 and 1", func() {
			rec := postScheduling(`{"model_name":"bad-rel","balance_rel_threshold":0.5}`)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			errObj, ok := resp["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(errObj["message"]).To(ContainSubstring("balance_rel_threshold"))
		})

		// Regression for the partial-update footgun: a min/max-only POST used to
		// full-replace every column and silently reset the prefix-cache settings
		// to empty/zero. The pointer-merge must preserve omitted prefix fields.
		It("preserves prefix-cache settings across a min_replicas-only update", func() {
			ctx := context.Background()

			rec := postScheduling(`{"model_name":"merge-model","route_policy":"prefix_cache","min_prefix_match":0.4}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			// Update only min_replicas - omits all prefix-cache fields.
			rec = postScheduling(`{"model_name":"merge-model","min_replicas":2}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			cfg, err := registry.GetModelScheduling(ctx, "merge-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg).ToNot(BeNil())
			Expect(cfg.MinReplicas).To(Equal(2), "the provided non-prefix field must update")
			Expect(cfg.RoutePolicy).To(Equal("prefix_cache"), "omitted route_policy must be preserved")
			Expect(cfg.MinPrefixMatch).To(BeNumerically("~", 0.4, 1e-9), "omitted min_prefix_match must be preserved")
		})

		It("updates a prefix-cache field when it is explicitly provided", func() {
			ctx := context.Background()

			rec := postScheduling(`{"model_name":"update-model","route_policy":"prefix_cache","min_prefix_match":0.4}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			rec = postScheduling(`{"model_name":"update-model","route_policy":"round_robin"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))

			cfg, err := registry.GetModelScheduling(ctx, "update-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg).ToNot(BeNil())
			Expect(cfg.RoutePolicy).To(Equal("round_robin"), "explicitly provided route_policy must update")
			Expect(cfg.MinPrefixMatch).To(BeNumerically("~", 0.4, 1e-9), "omitted min_prefix_match must still be preserved")
		})
	})

	Describe("ListNodesEndpoint", func() {
		It("returns an empty list when no nodes are registered", func() {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ListNodesEndpoint(registry)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var list []nodes.BackendNode
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			Expect(list).To(BeEmpty())
		})

		It("returns registered nodes", func() {
			// Register two nodes directly via the registry
			ctx := context.Background()
			Expect(registry.Register(ctx, &nodes.BackendNode{
				Name:    "alpha",
				Address: "10.0.0.1:50051",
			}, true)).To(Succeed())
			Expect(registry.Register(ctx, &nodes.BackendNode{
				Name:    "beta",
				Address: "10.0.0.2:50051",
			}, true)).To(Succeed())

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ListNodesEndpoint(registry)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var list []nodes.BackendNode
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			Expect(list).To(HaveLen(2))
			names := []string{list[0].Name, list[1].Name}
			Expect(names).To(ConsistOf("alpha", "beta"))
		})
	})

	Describe("ListAllNodeModelsEndpoint", func() {
		It("returns an empty list when no models are loaded", func() {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ListAllNodeModelsEndpoint(registry)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var list []nodes.NodeModel
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			Expect(list).To(BeEmpty())
		})

		It("returns loaded models across healthy nodes", func() {
			ctx := context.Background()
			Expect(registry.Register(ctx, &nodes.BackendNode{
				ID: "n1", Name: "alpha", Address: "10.0.0.1:50051", Status: nodes.StatusHealthy,
			}, true)).To(Succeed())
			Expect(registry.SetNodeModel(ctx, "n1", "llama-3.3", 0, "loaded", "10.0.0.1:50051", 0)).To(Succeed())

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := ListAllNodeModelsEndpoint(registry)
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var list []nodes.NodeModel
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ModelName).To(Equal("llama-3.3"))
			Expect(list[0].NodeID).To(Equal("n1"))
		})
	})

	Describe("GetNodeModelsEndpoint", func() {
		It("returns revision and cleanup state without serialized model options", func() {
			ctx := context.Background()
			Expect(registry.Register(ctx, &nodes.BackendNode{
				ID: "n1", Name: "alpha", Address: "10.0.0.1:50051", Status: nodes.StatusHealthy,
			}, true)).To(Succeed())

			Expect(registry.EstablishModelConfigRevision(ctx, "current-model", "revision-current")).To(Succeed())
			Expect(registry.SetNodeModelRevision(ctx, "n1", "current-model", 0, "loaded", "10.0.0.1:50052", 0, "revision-current", "options-current")).To(Succeed())
			Expect(registry.SetNodeModelLoadInfoRevision(ctx, "n1", "current-model", 0, "llama-cpp", "revision-current", []byte("serialized-options"))).To(Succeed())

			Expect(registry.EstablishModelConfigRevision(ctx, "changed-model", "revision-old")).To(Succeed())
			Expect(registry.SetNodeModelRevision(ctx, "n1", "changed-model", 0, "loaded", "10.0.0.1:50053", 0, "revision-old", "options-old")).To(Succeed())
			quarantined, err := registry.AdvanceModelConfigRevision(ctx, "changed-model", "revision-new")
			Expect(err).ToNot(HaveOccurred())
			Expect(quarantined).To(HaveLen(1))
			retryAt := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
			Expect(registry.RecordModelCleanupFailure(ctx, "n1", "changed-model", 0, "worker unreachable", retryAt)).To(Succeed())

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/nodes/n1/models", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/nodes/:id/models")
			c.SetParamNames("id")
			c.SetParamValues("n1")

			Expect(GetNodeModelsEndpoint(registry)(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusOK))

			var modelsResponse []map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &modelsResponse)).To(Succeed())
			Expect(modelsResponse).To(HaveLen(2))
			byName := map[string]map[string]any{}
			for _, model := range modelsResponse {
				byName[model["model_name"].(string)] = model
				Expect(model).ToNot(HaveKey("model_opts_blob"))
			}

			Expect(byName["current-model"]).To(SatisfyAll(
				HaveKeyWithValue("state", "loaded"),
				HaveKeyWithValue("config_revision", "revision-current"),
				HaveKeyWithValue("effective_options_hash", "options-current"),
			))
			Expect(byName["changed-model"]).To(SatisfyAll(
				HaveKeyWithValue("state", "unloading"),
				HaveKeyWithValue("config_revision", "revision-old"),
				HaveKeyWithValue("effective_options_hash", "options-old"),
				HaveKeyWithValue("cleanup_error", "worker unreachable"),
				HaveKey("cleanup_next_retry_at"),
			))
		})
	})
})

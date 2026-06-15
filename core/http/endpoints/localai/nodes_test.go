package localai

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
})

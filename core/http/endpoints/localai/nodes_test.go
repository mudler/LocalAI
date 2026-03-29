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

			handler := RegisterNodeEndpoint(registry, "", true, nil, "")
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["name"]).To(Equal("worker-1"))
			Expect(resp["id"]).ToNot(BeEmpty())
			Expect(resp["status"]).To(Equal(nodes.StatusHealthy))
		})

		It("returns 400 when name is missing", func() {
			e := echo.New()
			body := `{"address":"10.0.0.1:50051"}`
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := RegisterNodeEndpoint(registry, "", true, nil, "")
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

			handler := RegisterNodeEndpoint(registry, "", true, nil, "")
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

			handler := RegisterNodeEndpoint(registry, "", true, nil, "")
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

			handler := RegisterNodeEndpoint(registry, "", true, nil, "")
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

			handler := RegisterNodeEndpoint(registry, "correct-token", true, nil, "")
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

			handler := RegisterNodeEndpoint(registry, "", false, nil, "")
			Expect(handler(c)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusCreated))

			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp["status"]).To(Equal(nodes.StatusPending))
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

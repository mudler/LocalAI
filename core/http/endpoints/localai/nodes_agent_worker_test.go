package localai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"gorm.io/gorm"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The agent worker no longer calls the HTTP API, so registering or approving
// one must not mint a user or an API key. A key with the collections grant
// would let a worker host act on any user's collections for no reason.
var _ = Describe("Agent worker registration", func() {
	var (
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	register := func(autoApprove bool) map[string]any {
		e := echo.New()
		body := `{"name":"agent-1","node_type":"agent"}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		Expect(RegisterNodeEndpoint(registry, "", autoApprove, db, "secret", natsauth.Config{})(e.NewContext(req, rec))).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusCreated))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}

	countUsers := func() int64 {
		var n int64
		Expect(db.Model(&auth.User{}).Where("provider = ?", auth.ProviderAgentWorker).Count(&n).Error).To(Succeed())
		return n
	}

	It("returns no api_token and creates no user on registration", func() {
		resp := register(true)
		Expect(resp).ToNot(HaveKey("api_token"))
		Expect(resp).To(HaveKeyWithValue("node_type", "agent"))
		Expect(countUsers()).To(BeZero())
	})

	It("returns no api_token and creates no user on approval", func() {
		register(false)
		node, err := registry.GetByName(context.Background(), "agent-1")
		Expect(err).ToNot(HaveOccurred())

		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
		c.SetParamNames("id")
		c.SetParamValues(node.ID)
		Expect(ApproveNodeEndpoint(registry, db, "secret", natsauth.Config{})(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp).ToNot(HaveKey("api_token"))
		Expect(countUsers()).To(BeZero())
	})

	It("removes the worker user an earlier release provisioned when the node registers again", func() {
		register(true)
		node, err := registry.GetByName(context.Background(), "agent-1")
		Expect(err).ToNot(HaveOccurred())
		legacy := &auth.User{ID: "legacy-worker", Name: "agent-worker:agent-1", Provider: auth.ProviderAgentWorker,
			Subject: node.ID, Role: "user", Status: "active", CreatedAt: time.Now()}
		Expect(db.Create(legacy).Error).To(Succeed())
		Expect(registry.UpdateAuthRefs(context.Background(), node.ID, legacy.ID, "legacy-key")).To(Succeed())

		register(true)

		Expect(countUsers()).To(BeZero())
		again, err := registry.GetByName(context.Background(), "agent-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(again.AuthUserID).To(BeEmpty())
		Expect(again.APIKeyID).To(BeEmpty())
	})
})

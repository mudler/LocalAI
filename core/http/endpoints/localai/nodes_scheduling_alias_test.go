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

// aliasResolverStub maps alias names to targets in place of a config loader.
type aliasResolverStub struct{ aliases map[string]string }

func (s *aliasResolverStub) ResolveAliasName(name string) (string, bool) {
	target, ok := s.aliases[name]
	if !ok {
		return name, false
	}
	return target, true
}

var _ = Describe("Scheduling endpoints with model aliases", func() {
	var (
		registry *nodes.NodeRegistry
		resolver *aliasResolverStub
	)

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		resolver = &aliasResolverStub{aliases: map[string]string{"production": "qwen3"}}
		registry.SetAliasResolver(resolver)
	})

	post := func(body string) *httptest.ResponseRecorder {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		ExpectWithOffset(1, SetSchedulingEndpoint(registry)(c)).To(Succeed())
		return rec
	}

	It("accepts a rule keyed by an alias and reports the model it governs", func() {
		rec := post(`{"model_name":"production","min_replicas":2,"node_selector":{"tier":"gpu"}}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["model_name"]).To(Equal("production"))
		Expect(resp["target_model"]).To(Equal("qwen3"))
	})

	It("rejects a second rule for a model an alias rule already governs", func() {
		Expect(post(`{"model_name":"production","min_replicas":2}`).Code).To(Equal(http.StatusOK))

		rec := post(`{"model_name":"qwen3","min_replicas":1}`)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("production"))
	})

	It("rejects an alias rule for a model that already has its own rule", func() {
		Expect(post(`{"model_name":"qwen3","min_replicas":1}`).Code).To(Equal(http.StatusOK))

		rec := post(`{"model_name":"production","min_replicas":2}`)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("qwen3"))
	})

	It("still allows editing a rule in place", func() {
		Expect(post(`{"model_name":"production","min_replicas":2}`).Code).To(Equal(http.StatusOK))

		rec := post(`{"model_name":"production","min_replicas":4}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		stored, err := registry.GetModelScheduling(context.Background(), "production")
		Expect(err).ToNot(HaveOccurred())
		Expect(stored.MinReplicas).To(Equal(4))
	})

	It("rejects a rule keyed by an alias that does not resolve", func() {
		resolver.aliases["orphan"] = "orphan"

		rec := post(`{"model_name":"orphan","min_replicas":1}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("does not resolve"))
	})

	It("still accepts a rule for a model that is not installed yet", func() {
		rec := post(`{"model_name":"not-installed-yet","min_replicas":1}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("labels a rule that another rule shadows when listing", func() {
		// A seed file or a repointed alias can leave two rules on one model,
		// which the write path above rejects but cannot retract.
		Expect(registry.SetModelScheduling(context.Background(), &nodes.ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})).To(Succeed())
		Expect(registry.SetModelScheduling(context.Background(), &nodes.ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 1})).To(Succeed())

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		Expect(ListSchedulingEndpoint(registry)(c)).To(Succeed())

		var listed []map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &listed)).To(Succeed())
		byName := map[string]map[string]any{}
		for _, item := range listed {
			byName[item["model_name"].(string)] = item
		}
		Expect(byName["qwen3"]["shadowed"]).To(BeNil())
		Expect(byName["production"]["shadowed"]).To(Equal(true))
	})
})

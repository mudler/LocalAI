package localai

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mapSource map[string]config.ModelConfig

func (s mapSource) GetModelConfig(n string) (config.ModelConfig, bool) { c, ok := s[n]; return c, ok }
func (s mapSource) GetAllModelsConfigs() []config.ModelConfig {
	var out []config.ModelConfig
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

var _ = Describe("failover endpoints", func() {
	var (
		e  *echo.Echo
		fm *failover.Manager
	)

	BeforeEach(func() {
		src := mapSource{
			"a":     {Name: "a", Backend: "cloud-proxy"},
			"b":     {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}}}},
		}
		fm = failover.New(src)
		e = echo.New()
		e.GET("/api/failover", ListFailoverChainsEndpoint(fm))
		e.GET("/api/failover/events", FailoverEventsEndpoint(fm))
		e.GET("/api/failover/:chain", GetFailoverChainEndpoint(fm))
		e.POST("/api/failover/:chain/pin", PinFailoverTargetEndpoint(fm))
		e.DELETE("/api/failover/:chain/pin", UnpinFailoverTargetEndpoint(fm))
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("lists chains", func() {
		rec := do(http.MethodGet, "/api/failover", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out FailoverChainsResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Chains).To(HaveLen(1))
		Expect(out.Chains[0].Active).To(Equal("a"))
	})

	It("gets one chain or 404", func() {
		Expect(do(http.MethodGet, "/api/failover/chain", "").Code).To(Equal(http.StatusOK))
		Expect(do(http.MethodGet, "/api/failover/nope", "").Code).To(Equal(http.StatusNotFound))
	})

	It("pins and unpins", func() {
		rec := do(http.MethodPost, "/api/failover/chain/pin", `{"target":"b"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
		Expect(do(http.MethodPost, "/api/failover/chain/pin", `{"target":"zzz"}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodPost, "/api/failover/chain/pin", `{}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodPost, "/api/failover/nope/pin", `{"target":"a"}`).Code).To(Equal(http.StatusNotFound))
		Expect(do(http.MethodDelete, "/api/failover/chain/pin", "").Code).To(Equal(http.StatusOK))
		st, _ = fm.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("streams a snapshot, then switch events", func() {
		srv := httptest.NewServer(e)
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/failover/events", nil)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		r := bufio.NewReader(resp.Body)
		next := func() string {
			for {
				line, err := r.ReadString('\n')
				Expect(err).ToNot(HaveOccurred())
				if strings.HasPrefix(line, "event: ") {
					return strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				}
			}
		}
		Expect(next()).To(Equal("snapshot"))
		Expect(fm.Pin("chain", "b")).To(Succeed())
		Expect(next()).To(Equal("chain.switched"))
	})
})

package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// listStub is a connections.Service that only lists. The other methods panic
// through the nil embedded interface, so a spec fails if the payload code
// calls anything else.
type listStub struct {
	connections.Service
	list  []connections.Connection
	err   error
	calls atomic.Int32
}

func (s *listStub) List(context.Context) ([]connections.Connection, error) {
	s.calls.Add(1)
	return s.list, s.err
}

// capabilitiesRaw is capabilitiesOf with a connections service and the
// untouched JSON objects of the answer.
func capabilitiesRaw(svc connections.Service, models map[string]string) []map[string]any {
	dir := GinkgoT().TempDir()
	st, err := system.GetSystemState(system.WithModelPath(dir))
	Expect(err).ToNot(HaveOccurred())
	cl := config.NewModelConfigLoader(dir)
	for name, body := range models {
		path := filepath.Join(dir, name+".yaml")
		Expect(os.WriteFile(path, []byte("name: "+name+"\n"+body), 0600)).To(Succeed())
		Expect(cl.ReadModelConfig(path)).To(Succeed())
	}
	ac := config.NewApplicationConfig()
	ac.SystemState = st
	ml := model.NewModelLoader(st)
	gs := galleryop.NewGalleryService(ac, ml)
	a := &application.Application{}
	if svc != nil {
		a.SetConnections(svc)
	}
	e := echo.New()
	routes.RegisterUIAPIRoutes(e, cl, ml, ac, gs, galleryop.NewOpCache(gs), a, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/capabilities", nil))
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	var response struct {
		Data []map[string]any `json:"data"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
	return response.Data
}

func byID(data []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, d := range data {
		out[d["id"].(string)] = d
	}
	return out
}

var _ = Describe("UI model capabilities of remote models", func() {
	remoteModels := map[string]string{
		"up":      "backend: localai-proxy\nknown_usecases: [chat]\nproxy:\n  upstream_url: https://example.com\n  connection: c1\n",
		"gone":    "backend: localai-proxy\nknown_usecases: [chat]\nproxy:\n  upstream_url: https://example.com\n  connection: c1\n",
		"cloud":   "backend: cloud-proxy\nproxy:\n  upstream_url: https://example.com/v1/chat/completions\n  connection: c2\n",
		"manual":  "backend: cloud-proxy\nproxy:\n  upstream_url: https://example.com/v1/chat/completions\n",
		"local-a": "backend: llama-cpp\nknown_usecases: [chat]\n",
	}

	It("reports remote, connection and upstream_state from one list call", func() {
		svc := &listStub{list: []connections.Connection{
			{ID: "c1", Entries: []string{"up", "gone"}, Unavailable: []string{"gone"}},
			{ID: "c2", Entries: []string{"cloud"}, Unavailable: []string{}},
		}}
		got := byID(capabilitiesRaw(svc, remoteModels))
		Expect(svc.calls.Load()).To(BeEquivalentTo(1))

		Expect(got["up"]).To(HaveKeyWithValue("remote", true))
		Expect(got["up"]).To(HaveKeyWithValue("connection", "c1"))
		Expect(got["up"]).To(HaveKeyWithValue("upstream_state", "ok"))

		Expect(got["gone"]).To(HaveKeyWithValue("upstream_state", "unavailable"))
		Expect(got["gone"]).To(HaveKeyWithValue("connection", "c1"))

		Expect(got["cloud"]).To(HaveKeyWithValue("connection", "c2"))
		Expect(got["cloud"]).To(HaveKeyWithValue("upstream_state", "ok"))

		// A remote entry that no connection owns: remote, no connection.
		Expect(got["manual"]).To(HaveKeyWithValue("remote", true))
		Expect(got["manual"]).NotTo(HaveKey("connection"))
		Expect(got["manual"]).To(HaveKeyWithValue("upstream_state", "ok"))
	})

	It("keeps a local model as it was, with remote false", func() {
		svc := &listStub{list: []connections.Connection{{ID: "c1", Unavailable: []string{"local-a"}}}}
		got := byID(capabilitiesRaw(svc, remoteModels))["local-a"]
		Expect(got).To(HaveKeyWithValue("remote", false))
		Expect(got).NotTo(HaveKey("connection"))
		Expect(got).NotTo(HaveKey("upstream_state"))
		// The existing shape is unchanged: only "remote" is new.
		delete(got, "remote")
		Expect(got).To(HaveKey("id"))
		Expect(got).To(HaveKey("capabilities"))
		Expect(got).To(HaveKey("backend"))
		Expect(got).To(HaveKey("disabled"))
		Expect(got).To(HaveKey("pinned"))
	})

	It("does not list connections when no model is remote", func() {
		svc := &listStub{}
		capabilitiesRaw(svc, map[string]string{"local-a": remoteModels["local-a"]})
		Expect(svc.calls.Load()).To(BeZero())
	})

	It("still reports remote without the service, and no connection state", func() {
		got := byID(capabilitiesRaw(nil, remoteModels))
		Expect(got["up"]).To(HaveKeyWithValue("remote", true))
		Expect(got["up"]).To(HaveKeyWithValue("upstream_state", "ok"))
		Expect(got["local-a"]).To(HaveKeyWithValue("remote", false))
	})

	It("answers when the list fails, as if no entry were unavailable", func() {
		svc := &listStub{err: errors.New("store down")}
		got := byID(capabilitiesRaw(svc, remoteModels))
		Expect(got["gone"]).To(HaveKeyWithValue("upstream_state", "ok"))
	})
})

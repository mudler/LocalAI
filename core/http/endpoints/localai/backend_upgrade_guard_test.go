package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// listingManager answers only ListBackends; the guard reads nothing else.
type listingManager struct {
	galleryop.BackendManager
	backends gallery.SystemBackends
}

func (m listingManager) ListBackends() (gallery.SystemBackends, error) { return m.backends, nil }

// fakeUpgrades is the upgrade checker, with the optional last-success time.
type fakeUpgrades struct {
	pending map[string]gallery.UpgradeInfo
	checked time.Time
}

func (f *fakeUpgrades) GetAvailableUpgrades() map[string]gallery.UpgradeInfo { return f.pending }
func (f *fakeUpgrades) TriggerCheck()                                        {}
func (f *fakeUpgrades) LastSuccessTime() time.Time                           { return f.checked }

// checkingUpgrades is the checker that can also run a check. Each CheckNow
// calls onCheck, which may publish a new pending list and a success time.
type checkingUpgrades struct {
	*fakeUpgrades
	checks  int
	onCheck func(f *fakeUpgrades)
}

func (c *checkingUpgrades) CheckNow(context.Context) backendupdates.Snapshot {
	c.checks++
	if c.onCheck != nil {
		c.onCheck(c.fakeUpgrades)
	}
	return backendupdates.Snapshot{}
}

var _ = Describe("backend upgrade early refusal", func() {
	var (
		e           *echo.Echo
		systemState *system.SystemState
		svc         *galleryop.GalleryService
		upgrades    *fakeUpgrades
		backends    gallery.SystemBackends
		dispatched  chan galleryop.ManagementOp[gallery.GalleryBackend, any]
		done        chan struct{}
	)

	BeforeEach(func() {
		e = echo.New()
		var err error
		systemState, err = system.GetSystemState(system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{SystemState: systemState}, nil)
		backends = gallery.SystemBackends{
			"llama-cpp": {Name: "llama-cpp", Metadata: &gallery.BackendMetadata{Name: "llama-cpp", MetaBackendFor: "cpu-llama-cpp"}},
			"cpu-llama-cpp": {Name: "cpu-llama-cpp", Metadata: &gallery.BackendMetadata{Name: "cpu-llama-cpp"},
				Nodes: []gallery.NodeBackendRef{{NodeID: "node-a"}}},
			"current":    {Name: "current", Metadata: &gallery.BackendMetadata{Name: "current"}},
			"system-one": {Name: "system-one", IsSystem: true},
		}
		upgrades = &fakeUpgrades{
			checked: time.Now(),
			pending: map[string]gallery.UpgradeInfo{
				"cpu-llama-cpp": {BackendName: "cpu-llama-cpp", AvailableVersion: "2.0.0", AvailableDigest: "sha256:abc"},
			},
		}
		svc.SetBackendManager(listingManager{backends: backends})

		// The service is not started: take the queued op off the channel.
		dispatched = make(chan galleryop.ManagementOp[gallery.GalleryBackend, any], 4)
		done = make(chan struct{})
		go func() {
			for {
				select {
				case op := <-svc.BackendGalleryChannel:
					dispatched <- op
				case <-done:
					return
				}
			}
		}()
		DeferCleanup(func() { close(done) })
	})

	post := func(handler echo.HandlerFunc, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("name")
		c.SetParamValues(strings.SplitN(target[len("/backends/upgrade/"):], "?", 2)[0])
		Expect(handler(c)).To(Succeed())
		return rec
	}
	code := func(rec *httptest.ResponseRecorder) string {
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["error"]).NotTo(BeEmpty())
		return body["code"].(string)
	}

	Describe("POST /backends/upgrade/:name", func() {
		var handler echo.HandlerFunc
		BeforeEach(func() {
			svcEndpoint := localai.CreateBackendEndpointService(nil, systemState, svc, upgrades)
			handler = svcEndpoint.UpgradeBackendEndpoint()
		})

		It("answers 404 not_installed for an unknown backend and queues nothing", func() {
			rec := post(handler, "/backends/upgrade/typo", "")
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(code(rec)).To(Equal("not_installed"))
			Consistently(dispatched, "200ms").ShouldNot(Receive())
		})

		It("answers 409 no_update for an installed backend with no pending upgrade", func() {
			rec := post(handler, "/backends/upgrade/current", "")
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(code(rec)).To(Equal("no_update"))
			Consistently(dispatched, "200ms").ShouldNot(Receive())
		})

		It("answers 409 no_update for a system backend", func() {
			rec := post(handler, "/backends/upgrade/system-one", "")
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(code(rec)).To(Equal("no_update"))
		})

		It("queues the upgrade of a backend with a pending upgrade, shape unchanged", func() {
			rec := post(handler, "/backends/upgrade/cpu-llama-cpp", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var body map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
			Expect(body).To(HaveKey("id"))
			Expect(body).To(HaveKey("status_url"))
			var op galleryop.ManagementOp[gallery.GalleryBackend, any]
			Eventually(dispatched, "2s").Should(Receive(&op))
			Expect(op.Upgrade).To(BeTrue())
		})

		It("resolves a meta backend to the concrete one that has the upgrade", func() {
			rec := post(handler, "/backends/upgrade/llama-cpp", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("accepts expected_to as the available version, the digest, in the body or in the query", func() {
			Expect(post(handler, "/backends/upgrade/cpu-llama-cpp", `{"expected_to":"2.0.0"}`).Code).To(Equal(http.StatusOK))
			Expect(post(handler, "/backends/upgrade/cpu-llama-cpp", `{"expected_to":"sha256:abc"}`).Code).To(Equal(http.StatusOK))
			Expect(post(handler, "/backends/upgrade/cpu-llama-cpp?expected_to=2.0.0", "").Code).To(Equal(http.StatusOK))
		})

		It("answers 409 stale when expected_to is no longer the available build", func() {
			rec := post(handler, "/backends/upgrade/cpu-llama-cpp", `{"expected_to":"1.5.0"}`)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(code(rec)).To(Equal("stale"))
			Expect(post(handler, "/backends/upgrade/cpu-llama-cpp?expected_to=1.5.0", "").Code).To(Equal(http.StatusConflict))
			Consistently(dispatched, "200ms").ShouldNot(Receive())
		})

		It("answers 400 for a body that is not JSON", func() {
			Expect(post(handler, "/backends/upgrade/cpu-llama-cpp", "{nope").Code).To(Equal(http.StatusBadRequest))
		})

		Describe("a check that is old or missing", func() {
			var checker *checkingUpgrades
			upgradeOf := func(name string) *httptest.ResponseRecorder {
				svcEndpoint := localai.CreateBackendEndpointService(nil, systemState, svc, checker)
				return post(svcEndpoint.UpgradeBackendEndpoint(), "/backends/upgrade/"+name, "")
			}
			found := func(f *fakeUpgrades) {
				f.checked = time.Now()
				f.pending["current"] = gallery.UpgradeInfo{BackendName: "current", AvailableVersion: "3.0.0"}
			}
			BeforeEach(func() {
				checker = &checkingUpgrades{fakeUpgrades: upgrades}
			})

			It("trusts a list that is recent and runs no check", func() {
				Expect(upgradeOf("current").Code).To(Equal(http.StatusConflict))
				Expect(checker.checks).To(Equal(0))
			})

			It("runs a check when the list is old, and lets the upgrade through when it finds one", func() {
				upgrades.checked = time.Now().Add(-time.Hour)
				checker.onCheck = found
				Expect(upgradeOf("current").Code).To(Equal(http.StatusOK))
				Expect(checker.checks).To(Equal(1))
			})

			It("refuses with no_update when the fresh check still finds nothing", func() {
				upgrades.checked = time.Now().Add(-time.Hour)
				checker.onCheck = func(f *fakeUpgrades) { f.checked = time.Now() }
				rec := upgradeOf("current")
				Expect(rec.Code).To(Equal(http.StatusConflict))
				Expect(code(rec)).To(Equal("no_update"))
				Expect(checker.checks).To(Equal(1))
			})

			It("runs a check when the list was never computed", func() {
				upgrades.checked = time.Time{}
				checker.onCheck = found
				Expect(upgradeOf("current").Code).To(Equal(http.StatusOK))
				Expect(checker.checks).To(Equal(1))
			})

			It("lets the upgrade through when the fresh check fails", func() {
				upgrades.checked = time.Now().Add(-time.Hour)
				// The check ends without a new success time.
				Expect(upgradeOf("current").Code).To(Equal(http.StatusOK))
				Expect(checker.checks).To(Equal(1))
			})

			It("runs no check for a backend that has a pending upgrade", func() {
				upgrades.checked = time.Now().Add(-time.Hour)
				Expect(upgradeOf("cpu-llama-cpp").Code).To(Equal(http.StatusOK))
				Expect(checker.checks).To(Equal(0))
			})
		})

		It("does not refuse before the first check when nothing can run one", func() {
			upgrades.checked = time.Time{}
			upgrades.pending = nil
			svcEndpoint := localai.CreateBackendEndpointService(nil, systemState, svc, upgrades)
			Expect(post(svcEndpoint.UpgradeBackendEndpoint(), "/backends/upgrade/current", "").Code).To(Equal(http.StatusOK))
		})
	})

	Describe("POST /api/nodes/:id/backends/upgrade", func() {
		upgradeOnNode := func(nodeID, body string) *httptest.ResponseRecorder {
			appCfg := &config.ApplicationConfig{}
			handler := localai.UpgradeBackendOnNodeEndpoint(svc, galleryop.NewOpCache(svc), appCfg, localai.NewBackendUpgradeGuard(svc, upgrades))
			req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/backends/upgrade", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(nodeID)
			Expect(handler(c)).To(Succeed())
			return rec
		}

		It("answers 404 not_installed for an unknown backend", func() {
			rec := upgradeOnNode("node-a", `{"backend":"typo"}`)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(code(rec)).To(Equal("not_installed"))
		})

		It("answers 404 not_installed when the node does not hold the backend", func() {
			rec := upgradeOnNode("node-b", `{"backend":"cpu-llama-cpp"}`)
			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(code(rec)).To(Equal("not_installed"))
		})

		It("answers 409 no_update and 409 stale", func() {
			Expect(code(upgradeOnNode("node-a", `{"backend":"current"}`))).To(Equal("no_update"))
			rec := upgradeOnNode("node-a", `{"backend":"cpu-llama-cpp","expected_to":"9.9.9"}`)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(code(rec)).To(Equal("stale"))
		})

		It("accepts the request with the 202 shape unchanged", func() {
			rec := upgradeOnNode("node-a", `{"backend":"cpu-llama-cpp","expected_to":"2.0.0"}`)
			Expect(rec.Code).To(Equal(http.StatusAccepted))
			var body map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
			Expect(body).To(HaveKey("jobID"))
		})
	})
})

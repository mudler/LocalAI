// SPDX-License-Identifier: MIT

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GET /api/features and the addons gate", func() {
	features := func(opts ...config.AppOption) map[string]bool {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		opts = append([]config.AppOption{config.WithContext(ctx), config.WithSystemState(state)}, opts...)
		app, err := application.New(opts...)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		e := echo.New()
		routes.RegisterFeatureRoutes(e, app.ApplicationConfig(), app)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/features", nil))
		Expect(w.Code).To(Equal(http.StatusOK))
		var got map[string]bool
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		return got
	}

	It("reports addons, fine_tuning and quantization as false with add-ons off", func() {
		got := features()
		Expect(got).To(HaveKeyWithValue("addons", false))
		Expect(got).To(HaveKeyWithValue("fine_tuning", false))
		Expect(got).To(HaveKeyWithValue("quantization", false))
	})

	It("reports chats as true when the chats service runs", func() {
		Expect(features()).To(HaveKeyWithValue("chats", true))
	})

	It("reports them as true with the owner present", func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		root, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		root = filepath.Join(root, "addons")
		transport, address, err := gateway.NewLoopbackCarrierConfig("local", root)
		Expect(err).NotTo(HaveOccurred())
		got := features(config.WithAddons(config.AddonConfig{
			Enabled:    true,
			InstanceID: "local", OwnerID: "local", OwnerAddress: transport.OwnerAddress,
			StorageID: "local", Root: root, ListenAddress: address, Transport: transport,
			OwnershipMode: "local-owner",
			Host:          contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"},
		}))
		Expect(got).To(HaveKeyWithValue("addons", true))
		Expect(got).To(HaveKeyWithValue("fine_tuning", true))
		Expect(got).To(HaveKeyWithValue("quantization", true))
	})
})

// SPDX-License-Identifier: MIT
package localai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SystemInformations memory", func() {
	It("keeps model metadata and omits VRAM for remote or stopped backends", func() {
		path, err := os.MkdirTemp("", "system-info-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, path)
		configFile := filepath.Join(path, "remote.yaml")
		Expect(os.WriteFile(configFile, []byte("name: remote\nbackend: llama-cpp\n"), 0600)).To(Succeed())
		cl := config.NewModelConfigLoader(path)
		Expect(cl.ReadModelConfig(configFile)).To(Succeed())
		ml := model.NewModelLoader(&system.SystemState{})
		store := model.NewInMemoryModelStore()
		store.Set("remote", model.NewModel("remote", "worker:50051", nil))
		store.Set("stopped", model.NewModel("stopped", "", &process.Process{}))
		ml.SetModelStore(store)
		app := echo.New()
		app.GET("/system", localai.SystemInformations(cl, ml, &config.ApplicationConfig{}, nil, nil))
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		var response struct {
			Models []map[string]any `json:"loaded_models"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Models).To(ConsistOf(
			map[string]any{"id": "remote", "backend": "llama-cpp"},
			map[string]any{"id": "stopped"},
		))
	})
})

var _ = Describe("SystemInformations distributed models", func() {
	newApp := func(remote localai.RemoteLoadedModelsFunc) *echo.Echo {
		path, err := os.MkdirTemp("", "system-info-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, path)
		configFile := filepath.Join(path, "worker.yaml")
		Expect(os.WriteFile(configFile, []byte("name: worker-only\nbackend: llama-cpp\n"), 0600)).To(Succeed())
		cl := config.NewModelConfigLoader(path)
		Expect(cl.ReadModelConfig(configFile)).To(Succeed())
		ml := model.NewModelLoader(&system.SystemState{})
		store := model.NewInMemoryModelStore()
		store.Set("local", model.NewModel("local", "", &process.Process{}))
		ml.SetModelStore(store)
		app := echo.New()
		app.GET("/system", localai.SystemInformations(cl, ml, &config.ApplicationConfig{}, nil, remote))
		return app
	}
	loadedIDs := func(app *echo.Echo) []map[string]any {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/system", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
		var response struct {
			Models []map[string]any `json:"loaded_models"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		return response.Models
	}

	It("adds loaded worker models once, skipping other states and local duplicates", func() {
		app := newApp(func(context.Context) ([]nodes.NodeModel, error) {
			return []nodes.NodeModel{
				{ModelName: "worker-only", NodeID: "a", State: "loaded"},
				{ModelName: "worker-only", NodeID: "b", ReplicaIndex: 1, State: "loaded"},
				{ModelName: "local", NodeID: "a", State: "loaded"},
				{ModelName: "loading-model", NodeID: "a", State: "loading"},
				{ModelName: "no-config", NodeID: "a", State: "loaded"},
			}, nil
		})
		Expect(loadedIDs(app)).To(ConsistOf(
			map[string]any{"id": "local"},
			map[string]any{"id": "worker-only", "backend": "llama-cpp"},
			map[string]any{"id": "no-config"},
		))
	})

	It("keeps the local list when the registry read fails", func() {
		app := newApp(func(context.Context) ([]nodes.NodeModel, error) {
			return nil, errors.New("registry down")
		})
		Expect(loadedIDs(app)).To(ConsistOf(map[string]any{"id": "local"}))
	})

	It("is unchanged when not distributed", func() {
		Expect(loadedIDs(newApp(nil))).To(ConsistOf(map[string]any{"id": "local"}))
	})
})

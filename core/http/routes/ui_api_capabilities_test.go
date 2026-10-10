package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
)

func TestUIUpscaleCapabilities(t *testing.T) {
	dir := t.TempDir()
	st, err := system.GetSystemState(system.WithModelPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	cl := config.NewModelConfigLoader(dir)
	path := filepath.Join(dir, "upscaler.yaml")
	if err := os.WriteFile(path, []byte("name: upscaler\nbackend: stablediffusion-ggml\nknown_usecases: [upscale]\nupscale_scale: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cl.ReadModelConfig(path); err != nil {
		t.Fatal(err)
	}
	ac := config.NewApplicationConfig()
	ac.SystemState = st
	ml := model.NewModelLoader(st)
	gs := galleryop.NewGalleryService(ac, ml)
	app := echo.New()
	routes.RegisterUIAPIRoutes(app, cl, ml, ac, gs, galleryop.NewOpCache(gs), &application.Application{}, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data []struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
			Scale        int      `json:"upscaleScale"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, entry := range response.Data {
		if entry.ID == "upscaler" {
			if entry.Scale != 4 || len(entry.Capabilities) != 1 || entry.Capabilities[0] != "FLAG_UPSCALE" {
				t.Fatalf("unexpected entry: %+v", entry)
			}
			return
		}
	}
	t.Fatal("upscaler missing from response")
}

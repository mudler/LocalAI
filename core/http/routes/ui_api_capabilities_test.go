package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	for name, options := range map[string]string{
		"upscaler":    "options: [upscale_scale:4]\n",
		"absent":      "",
		"malformed":   "options: [upscale_scale:bad]\n",
		"nonpositive": "options: [upscale_scale:0]\n",
		"negative":    "options: [upscale_scale:-1]\n",
		"overflow":    "options: [upscale_scale:2147483648]\n",
		"duplicate":   "options: [upscale_scale:4, upscale_scale:4]\n",
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("name: "+name+"\nbackend: stablediffusion-ggml\nknown_usecases: [upscale]\n"+options), 0600); err != nil {
			t.Fatal(err)
		}
		if err := cl.ReadModelConfig(path); err != nil {
			t.Fatal(err)
		}
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
			Scale        *int     `json:"upscaleScale"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 7 {
		t.Fatalf("unexpected model count: %d", len(response.Data))
	}
	for _, entry := range response.Data {
		if len(entry.Capabilities) != 1 || entry.Capabilities[0] != "FLAG_UPSCALE" {
			t.Fatalf("unexpected capabilities: %+v", entry)
		}
		if entry.ID == "upscaler" {
			if entry.Scale == nil || *entry.Scale != 4 {
				t.Fatalf("unexpected scale: %+v", entry)
			}
		} else if entry.Scale != nil {
			t.Fatalf("invalid scale exposed: %+v", entry)
		}
	}
}

func TestUIAliasCapabilities(t *testing.T) {
	dir := t.TempDir()
	st, err := system.GetSystemState(system.WithModelPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	cl := config.NewModelConfigLoader(dir)
	for name, body := range map[string]string{
		"target":         "backend: llama-cpp\nknown_usecases: [chat, vision]\npinned: true\n",
		"alias":          "alias: target\nknown_usecases: [embeddings]\nprofile:\n  system_prompt: Answer briefly.\n  client_tools: drop\n",
		"disabled-alias": "alias: target\ndisabled: true\n",
		"broken":         "alias: missing\nknown_usecases: [chat]\n",
		"chain":          "alias: alias\nknown_usecases: [chat]\n",
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("name: "+name+"\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := cl.ReadModelConfig(path); err != nil {
			t.Fatal(err)
		}
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
			Backend      string   `json:"backend"`
			Disabled     bool     `json:"disabled"`
			Pinned       bool     `json:"pinned"`
			MCP          bool     `json:"mcp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 5 {
		t.Fatalf("unexpected models: %+v", response.Data)
	}
	for _, entry := range response.Data {
		switch entry.ID {
		case "target", "alias", "disabled-alias":
			if len(entry.Capabilities) != 2 || !slices.Contains(entry.Capabilities, "FLAG_CHAT") || !slices.Contains(entry.Capabilities, "FLAG_VISION") {
				t.Errorf("%s must inherit chat/vision: %+v", entry.ID, entry)
			}
		case "broken", "chain":
			if len(entry.Capabilities) != 0 {
				t.Errorf("invalid alias gained capabilities: %+v", entry)
			}
		default:
			t.Errorf("unexpected ID: %s", entry.ID)
		}
		if entry.ID != "target" && (entry.Backend != "" || entry.Pinned || entry.MCP) {
			t.Errorf("alias inherited management metadata: %+v", entry)
		}
		if entry.Disabled != (entry.ID == "disabled-alias") {
			t.Errorf("disabled changed: %+v", entry)
		}
	}
}

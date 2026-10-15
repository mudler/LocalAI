// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WarmFastPath explicit requests", func() {
	var bcl *config.ModelConfigLoader
	var ac *config.ApplicationConfig
	var app *echo.Echo
	var events []diagnostics.Event
	var served string
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		ac = config.NewApplicationConfig()
		ac.Context = context.Background()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		ac.DiagnosticsRecorder = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		bcl = config.NewModelConfigLoader(dir)
		target := config.ModelConfig{Name: "target", Backend: "test"}
		target.Model = "weights.gguf"
		bcl.ReplaceModelConfigs([]config.ModelConfig{target, {Name: "default", Alias: "target"}, {Name: "literal[", Backend: "test"}})
		for i := 0; i < 128; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%03d", i)), nil, 0600)).To(Succeed())
		}
		re := NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
		app = echo.New()
		served = ""
		events = nil
		app.POST("/", func(c echo.Context) error {
			served = c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig).Name
			return c.NoContent(204)
		}, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
	})
	count := func(phase diagnostics.Phase) int {
		n := 0
		for _, e := range events {
			if e.Phase == phase && e.State == diagnostics.StateEnd {
				n++
			}
		}
		return n
	}
	It("performs zero enumerations for exact aliases, concrete and regex-metacharacter names", func() {
		for _, name := range []string{"default", "target", "literal["} {
			events = nil
			Expect(postJSON(app, "/", fmt.Sprintf(`{"model":%q}`, name)).Code).To(Equal(204))
			if name == "default" {
				Expect(served).To(Equal("target"))
			} else {
				Expect(served).To(Equal(name))
			}
			Expect(count(diagnostics.PhaseBodyLookup)).To(Equal(1))
			Expect(count(diagnostics.PhaseFSEnumeration)).To(BeZero(), name)
			Expect(count(diagnostics.PhaseExistenceFallback)).To(BeZero())
			for _, e := range events {
				Expect(e.ID).To(Equal(events[0].ID))
			}
		}
	})
	It("retains discovery on misses and does not manufacture events when diagnostics are disabled", func() {
		Expect(postJSON(app, "/", `{"model":"missing"}`).Code).To(Equal(404))
		Expect(count(diagnostics.PhaseFSEnumeration)).To(Equal(1))
		events = nil
		ac.DiagnosticsRecorder = nil
		Expect(postJSON(app, "/", `{"model":"target"}`).Code).To(Equal(204))
		Expect(events).To(BeEmpty())
	})
	It("still rejects malformed requests and aliases with missing or cyclic targets", func() {
		Expect(postJSON(app, "/", `{"model":`).Code).To(Equal(400))
		for _, alias := range []string{"missing", "default"} {
			bcl.ReplaceModelConfigs([]config.ModelConfig{{Name: "default", Alias: alias}})
			Expect(postJSON(app, "/", `{"model":"default"}`).Code).To(Equal(400))
			Expect(served).To(BeEmpty())
		}
	})
	It("still rejects disabled concrete models and alias targets", func() {
		disabled := true
		bcl.ReplaceModelConfigs([]config.ModelConfig{{Name: "target", Backend: "test", Disabled: &disabled}, {Name: "default", Alias: "target"}})
		for _, name := range []string{"target", "default"} {
			Expect(postJSON(app, "/", fmt.Sprintf(`{"model":%q}`, name)).Code).To(Equal(403))
			Expect(served).To(BeEmpty())
		}
	})
})

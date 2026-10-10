// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/google/uuid"
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

var _ = Describe("Diagnostics extraction", func() {
	var re *RequestExtractor
	var ac *config.ApplicationConfig
	var bcl *config.ModelConfigLoader
	var events []diagnostics.Event
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		ac = config.NewApplicationConfig()
		ac.Context = context.Background()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		bcl = config.NewModelConfigLoader(dir)
		a := config.ModelConfig{Name: "sentinel-model"}
		a.Model = "sentinel-model"
		bcl.ReplaceModelConfigs([]config.ModelConfig{a})
		events = nil
		ac.DiagnosticsRecorder = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		re = NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
	})
	ends := func(p diagnostics.Phase) []diagnostics.Event {
		var out []diagnostics.Event
		for _, e := range events {
			if e.Phase == p && e.State == diagnostics.StateEnd {
				out = append(out, e)
			}
		}
		return out
	}
	It("preserves anonymous default, JSON, bearer and query selection with timing off and on", func() {
		for _, tc := range []struct {
			body, query, bearer string
			listing, lookup     int
		}{
			{`{}`, "", "", 1, 0}, {`{"model":"sentinel-model","prompt":"sentinel-prompt"}`, "", "", 1, 0},
			{`{}`, "", "sentinel-model", 0, 1}, {`{}`, "?model=sentinel-model", "sentinel-credential", 0, 0},
		} {
			var previous string
			recorder := ac.DiagnosticsRecorder
			for _, enabled := range []bool{false, true} {
				events = nil
				ac.DiagnosticsRecorder = nil
				if enabled {
					ac.DiagnosticsRecorder = recorder
				}
				e := echo.New()
				e.POST("/sentinel-path", func(c echo.Context) error {
					Expect(len(ends(diagnostics.PhaseExtraction))).To(Equal(map[bool]int{false: 0, true: 2}[enabled]))
					input := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
					Expect(re.SetOpenAIRequest(c)).To(Succeed())
					defer input.Cancel()
					Expect(diagnostics.Enabled(input.Context)).To(Equal(enabled))
					diagnostics.Mark(input.Context, diagnostics.PhaseModelInit)
					return c.String(200, input.Model)
				}, re.BuildFilteredFirstAvailableDefaultModel(nil), re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
				req := httptest.NewRequest(http.MethodPost, "/sentinel-path"+tc.query, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Correlation-ID", "sentinel-correlation")
				if tc.bearer != "" {
					req.Header.Set("Authorization", "Bearer "+tc.bearer)
				}
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(200))
				Expect(rec.Body.String()).To(Equal("sentinel-model"))
				if !enabled {
					previous = rec.Body.String()
					Expect(events).To(BeEmpty())
					continue
				}
				Expect(rec.Body.String()).To(Equal(previous))
				Expect(ends(diagnostics.PhaseDefaultListing)).To(HaveLen(tc.listing))
				Expect(ends(diagnostics.PhaseBearerLookup)).To(HaveLen(tc.lookup))
				Expect(ends(diagnostics.PhaseBodyLookup)).To(HaveLen(1))
				Expect(ends(diagnostics.PhaseConfigLoadDefaults)).To(HaveLen(1))
				Expect(ends(diagnostics.PhaseConfigFilter)).NotTo(BeEmpty())
				id := events[0].ID
				_, err := uuid.Parse(id)
				Expect(err).NotTo(HaveOccurred())
				for _, event := range events {
					Expect(event.ID).To(Equal(id))
					Expect(event.Kind).To(Equal(diagnostics.KindRequest))
				}
				Expect(fmt.Sprint(events)).NotTo(ContainSubstring("sentinel"))
			}
			ac.DiagnosticsRecorder = recorder
		}
	})
	It("records bind, nil initializer result, missing, disabled and alias failures even when JSON returns nil", func() {
		disabled := true
		d := config.ModelConfig{Name: "disabled", Disabled: &disabled}
		d.Model = "disabled"
		a := config.ModelConfig{Name: "alias", Alias: "absent"}
		bcl.ReplaceModelConfigs([]config.ModelConfig{d, a})
		for _, tc := range []struct {
			body     string
			nilInput bool
			code     int
		}{
			{`{`, false, 400}, {`{}`, true, 400}, {`{"model":"missing"}`, false, 404}, {`{"model":"disabled"}`, false, 403}, {`{"model":"alias"}`, false, 400},
		} {
			events = nil
			e := echo.New()
			e.POST("/", func(c echo.Context) error { Fail("unexpected inference"); return nil }, re.SetModelAndConfig(func() schema.LocalAIRequest {
				if tc.nilInput {
					return nil
				}
				return new(schema.OpenAIRequest)
			}))
			rec := postJSON(e, "/", tc.body)
			Expect(rec.Code).To(Equal(tc.code))
			Expect(ends(diagnostics.PhaseExtraction)).To(HaveLen(1))
			Expect(ends(diagnostics.PhaseExtraction)[0].Outcome).To(Equal(diagnostics.OutcomeError))
			if strings.Contains(tc.body, "alias") {
				Expect(ends(diagnostics.PhaseAliasResolution)[0].Outcome).To(Equal(diagnostics.OutcomeError))
			}
		}
	})
	It("retains identity across repeated segments and records cancellation before downstream", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil).WithContext(ctx), httptest.NewRecorder())
		h := re.BuildConstantDefaultModelNameMiddleware("sentinel-model")(func(c echo.Context) error {
			Expect(ends(diagnostics.PhaseExtraction)).NotTo(BeEmpty())
			return echo.ErrBadGateway
		})
		Expect(h(c)).To(Equal(echo.ErrBadGateway))
		Expect(h(c)).To(Equal(echo.ErrBadGateway))
		Expect(ends(diagnostics.PhaseExtraction)).To(HaveLen(2))
		for _, e := range events {
			Expect(e.ID).To(Equal(events[0].ID))
			if e.State == diagnostics.StateEnd {
				Expect(e.Outcome).To(Equal(diagnostics.OutcomeCanceled))
			}
		}
		Expect(c.Request().Context().Err()).To(Equal(context.Canceled))
	})
	It("inherits observation and request cancellation in Responses context rebuilding", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil).WithContext(ctx), httptest.NewRecorder())
		Expect(re.BuildConstantDefaultModelNameMiddleware("sentinel-model")(func(echo.Context) error { return nil })(c)).To(Succeed())
		input := &schema.OpenResponsesRequest{Model: "sentinel-model"}
		c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		cfg := config.ModelConfig{Name: "sentinel-model"}
		cfg.Model = "sentinel-model"
		c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
		Expect(re.SetOpenResponsesRequest(c)).To(Succeed())
		defer input.Cancel()
		Expect(diagnostics.Enabled(input.Context)).To(BeTrue())
		diagnostics.Mark(input.Context, diagnostics.PhaseModelInit)
		Expect(events[len(events)-1].ID).To(Equal(events[0].ID))
		cancel()
		Eventually(input.Context.Done()).Should(BeClosed())
	})
	It("isolates concurrent requests and a background operation on the same recorder", func() {
		var mu sync.Mutex
		ac.DiagnosticsRecorder = diagnostics.NewRecorder(func(e diagnostics.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
		e := echo.New()
		e.POST("/", func(c echo.Context) error {
			diagnostics.Mark(c.Request().Context(), diagnostics.PhaseModelInit)
			return c.NoContent(204)
		}, re.BuildFilteredFirstAvailableDefaultModel(nil))
		const n = 12
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer GinkgoRecover(); defer wg.Done(); Expect(postJSON(e, "/", `{}`).Code).To(Equal(204)) }()
		}
		reload := ac.DiagnosticsRecorder.Reload(context.Background())
		diagnostics.Mark(reload, diagnostics.PhaseReloadEnumeration)
		wg.Wait()
		groups := map[string][]diagnostics.Event{}
		for _, event := range events {
			groups[event.ID] = append(groups[event.ID], event)
		}
		Expect(groups).To(HaveLen(n + 1))
		requests := 0
		for _, group := range groups {
			if group[0].Kind == diagnostics.KindConfigReload {
				Expect(group).To(HaveLen(1))
				continue
			}
			requests++
			Expect(group[0].Phase).To(Equal(diagnostics.PhaseExtraction))
			Expect(group[0].State).To(Equal(diagnostics.StateStart))
			Expect(group[len(group)-2].Phase).To(Equal(diagnostics.PhaseExtraction))
			Expect(group[len(group)-2].State).To(Equal(diagnostics.StateEnd))
			Expect(group[len(group)-1].Phase).To(Equal(diagnostics.PhaseModelInit))
		}
		Expect(requests).To(Equal(n))
	})
	It("ends an empty default listing before downstream and preserves successful alias selection", func() {
		bcl.ReplaceModelConfigs(nil)
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
		Expect(re.BuildFilteredFirstAvailableDefaultModel(nil)(func(c echo.Context) error {
			Expect(ends(diagnostics.PhaseDefaultListing)).To(HaveLen(1))
			Expect(ends(diagnostics.PhaseExtraction)).To(HaveLen(1))
			return nil
		})(c)).To(Succeed())
		target := config.ModelConfig{Name: "target"}
		target.Model = "target"
		alias := config.ModelConfig{Name: "alias", Alias: "target"}
		bcl.ReplaceModelConfigs([]config.ModelConfig{target, alias})
		events = nil
		e := echo.New()
		e.POST("/", func(c echo.Context) error {
			Expect(c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest).Model).To(Equal("alias"))
			Expect(c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig).Name).To(Equal("target"))
			Expect(ends(diagnostics.PhaseAliasResolution)).To(HaveLen(1))
			Expect(ends(diagnostics.PhaseAliasResolution)[0].Outcome).To(Equal(diagnostics.OutcomeOK))
			return c.NoContent(204)
		}, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		Expect(postJSON(e, "/", `{"model":"alias"}`).Code).To(Equal(204))
	})

	It("records direct OpenAI and Responses extraction errors", func() {
		for _, setter := range []func(echo.Context) error{re.SetOpenAIRequest, re.SetOpenResponsesRequest} {
			events = nil
			c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), httptest.NewRecorder())
			Expect(setter(c)).To(Equal(echo.ErrBadRequest))
			Expect(ends(diagnostics.PhaseExtraction)).To(HaveLen(1))
			Expect(ends(diagnostics.PhaseExtraction)[0].Outcome).To(Equal(diagnostics.OutcomeError))
		}
	})

})

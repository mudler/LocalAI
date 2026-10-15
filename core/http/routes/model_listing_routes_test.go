// SPDX-License-Identifier: MIT
package routes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Every usecase filter used by a route's default-model lookup. Each route
// passes one of these to BuildFilteredFirstAvailableDefaultModel, so a
// listing-free result for each filter covers all of them.
var routeUsecaseFilters = []struct {
	name string
	flag config.ModelConfigUsecase
}{
	{"chat", config.FLAG_CHAT},
	{"completion", config.FLAG_COMPLETION},
	{"edit", config.FLAG_EDIT},
	{"embeddings", config.FLAG_EMBEDDINGS},
	{"rerank", config.FLAG_RERANK},
	{"transcript", config.FLAG_TRANSCRIPT},
	{"diarization", config.FLAG_DIARIZATION},
	{"sound classification", config.FLAG_SOUND_CLASSIFICATION},
	{"sound generation", config.FLAG_SOUND_GENERATION},
	{"tts", config.FLAG_TTS},
	{"image", config.FLAG_IMAGE},
	{"upscale", config.FLAG_UPSCALE},
	{"detection", config.FLAG_DETECTION},
	{"depth", config.FLAG_DEPTH},
	{"face recognition", config.FLAG_FACE_RECOGNITION},
	{"speaker recognition", config.FLAG_SPEAKER_RECOGNITION},
	{"audio transform", config.FLAG_AUDIO_TRANSFORM},
	{"vad", config.FLAG_VAD},
	{"video", config.FLAG_VIDEO},
	{"3d animation", config.FLAG_3D_ANIMATION},
	{"3d", config.FLAG_3D},
	{"tokenize", config.FLAG_TOKENIZE},
}

var _ = Describe("Default-model lookup on every usecase route", func() {
	var (
		events []diagnostics.Event
		served string
	)

	// serve builds a one-route app with the same pre-binding lookup the routes
	// use, posts the body with optional headers, and returns the response code.
	serve := func(flag config.ModelConfigUsecase, body string, headers ...string) int {
		dir := GinkgoT().TempDir()
		ac := config.NewApplicationConfig()
		ac.Context = context.Background()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		ac.DiagnosticsRecorder = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		chat := config.ModelConfig{Name: "chat", Backend: "test"}
		chat.Model = "chat.gguf"
		bcl := config.NewModelConfigLoader(dir)
		bcl.ReplaceModelConfigs([]config.ModelConfig{chat})
		for i := 0; i < 64; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%03d", i)), nil, 0600)).To(Succeed())
		}
		re := middleware.NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
		app := echo.New()
		app.POST("/route", func(c echo.Context) error {
			served, _ = c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
			return c.NoContent(http.StatusNoContent)
		}, re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(flag)))

		req := httptest.NewRequest(http.MethodPost, "/route", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		events, served = nil, ""
		app.ServeHTTP(rec, req)
		return rec.Code
	}
	enumerations := func() int {
		n := 0
		for _, e := range events {
			if e.Phase == diagnostics.PhaseFSEnumeration && e.State == diagnostics.StateEnd {
				n++
			}
		}
		return n
	}

	for _, f := range routeUsecaseFilters {
		flag := f.flag

		It(fmt.Sprintf("%s: does not list the models directory when the body names a model", f.name), func() {
			serve(flag, `{"model":"chat","input":"x"}`)
			Expect(enumerations()).To(BeZero())
		})

		It(fmt.Sprintf("%s: does not list the models directory when the body names no model", f.name), func() {
			serve(flag, `{"input":"x"}`)
			Expect(enumerations()).To(BeZero())
		})

		It(fmt.Sprintf("%s: does not list the models directory when a bearer key is sent", f.name), func() {
			serve(flag, `{"input":"x"}`, "Authorization", "Bearer some-api-key")
			Expect(enumerations()).To(BeZero())
		})
	}

	It("still resolves a model named by the bearer token on the chat lookup", func() {
		code := serve(config.FLAG_CHAT, `{"input":"x"}`, "Authorization", "Bearer chat")
		Expect(code).To(Equal(http.StatusNoContent))
		Expect(served).To(Equal("chat"))
		Expect(enumerations()).To(BeZero())
	})
})

// SPDX-License-Identifier: MIT
package localai_test

import (
	"bytes"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"net/http/httptest"
	"testing"
)

func TestPortableRegisterRequiresExplicitSlot(t *testing.T) {
	e := echo.New()
	e.POST("/v1/voice/register", localai.VoiceRegisterEndpoint(nil, nil, nil, nil), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var r schema.VoiceRegisterRequest
			if err := json.NewDecoder(c.Request().Body).Decode(&r); err != nil {
				return err
			}
			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &r)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{})
			return next(c)
		}
	})
	r := httptest.NewRequest("POST", "/v1/voice/register", bytes.NewBufferString(`{"model":"test","name":"Ada","speaker_profiles":{"version":1}}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte("speaker_slot")) {
		t.Fatalf("expected explicit slot validation, got %d %s", w.Code, w.Body.String())
	}
}

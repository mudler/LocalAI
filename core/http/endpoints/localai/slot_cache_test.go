package localai_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These cases all fail before a backend is loaded, so no model loader is
// needed. They cover the checks that keep a request away from the disk.
var _ = Describe("slot cache endpoints", func() {
	call := func(h func() echo.HandlerFunc, options []string, body schema.SlotCacheRequest) int {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/v1/slots/save", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		body.Model = "m"
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &body)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Name: "m", Options: options})
		err := h()(c)
		if err == nil {
			return rec.Code
		}
		if he, ok := err.(*echo.HTTPError); ok {
			return he.Code
		}
		return http.StatusInternalServerError
	}

	handlers := map[string]func() echo.HandlerFunc{
		"save":    func() echo.HandlerFunc { return SlotCacheSaveEndpoint(nil, nil) },
		"restore": func() echo.HandlerFunc { return SlotCacheRestoreEndpoint(nil, nil) },
	}

	for name, h := range handlers {
		Describe(name, func() {
			It("is disabled when slot_save_path is not set", func() {
				Expect(call(h, []string{"cache_ram:1024"}, schema.SlotCacheRequest{Filename: "a.bin"})).To(Equal(http.StatusBadRequest))
			})

			It("rejects unsafe file names", func() {
				opts := []string{"slot_save_path:/srv/slots"}
				for _, f := range []string{"", ".", "..", "../a", "a/b", `a\b`, "/etc/passwd", "a\x00b", "a."} {
					Expect(call(h, opts, schema.SlotCacheRequest{Filename: f})).To(Equal(http.StatusBadRequest), f)
				}
			})

			It("rejects a negative slot", func() {
				Expect(call(h, []string{"slot_save_path:/srv/slots"}, schema.SlotCacheRequest{Filename: "a.bin", Slot: -1})).To(Equal(http.StatusBadRequest))
			})

			It("requires a model", func() {
				e := echo.New()
				c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
				err := h()(c)
				Expect(err).To(HaveOccurred())
			})
		})
	}
})

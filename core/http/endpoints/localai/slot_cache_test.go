package localai_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"errors"
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	It("answers 501 when the backend does not support slot cache", func() {
		err := SlotCacheErrorForTest(fmt.Errorf("wrapped: %w", backend.ErrSlotCacheUnsupported))
		var he *echo.HTTPError
		Expect(errors.As(err, &he)).To(BeTrue())
		Expect(he.Code).To(Equal(http.StatusNotImplemented))
		Expect(he.Message).To(Equal("slot save and restore is not supported by this model's backend"))
	})

	It("keeps other backend errors on the existing mapping", func() {
		plain := errors.New("boom")
		Expect(SlotCacheErrorForTest(plain)).To(Equal(plain))
	})

	It("answers 404 with a fixed text for an unknown ID", func() {
		err := SlotCacheErrorForTest(status.Error(codes.NotFound, "/srv/slots/abc: secret path detail"))
		var he *echo.HTTPError
		Expect(errors.As(err, &he)).To(BeTrue())
		Expect(he.Code).To(Equal(http.StatusNotFound))
		Expect(he.Message).To(Equal("no saved state with this id for this model"))
	})

	It("keeps the model identity mismatch off the 404 text", func() {
		err := SlotCacheErrorForTest(status.Error(codes.NotFound, "llama-cpp: model identity mismatch: loaded \"a\", requested \"b\""))
		var he *echo.HTTPError
		Expect(errors.As(err, &he)).To(BeTrue())
		Expect(he.Message).ToNot(Equal("no saved state with this id for this model"))
	})

	It("answers 412 when the saved file no longer matches its record", func() {
		err := SlotCacheErrorForTest(status.Error(codes.FailedPrecondition, "the saved file does not match its record"))
		var he *echo.HTTPError
		Expect(errors.As(err, &he)).To(BeTrue())
		Expect(he.Code).To(Equal(http.StatusPreconditionFailed))
	})

	handlers := map[string]func() echo.HandlerFunc{
		"save":    func() echo.HandlerFunc { return SlotCacheSaveEndpoint(nil, nil) },
		"restore": func() echo.HandlerFunc { return SlotCacheRestoreEndpoint(nil, nil) },
	}
	const goodID = "0123456789abcdef0123456789abcdef"

	for name, h := range handlers {
		Describe(name, func() {
			id := goodID
			if name == "save" {
				id = ""
			}

			It("is disabled when slot_save_path is not set", func() {
				Expect(call(h, []string{"cache_ram:1024"}, schema.SlotCacheRequest{ID: id})).To(Equal(http.StatusBadRequest))
			})

			It("rejects a negative slot", func() {
				Expect(call(h, []string{"slot_save_path:/srv/slots"}, schema.SlotCacheRequest{ID: id, Slot: -1})).To(Equal(http.StatusBadRequest))
			})

			It("requires a model", func() {
				e := echo.New()
				c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
				err := h()(c)
				Expect(err).To(HaveOccurred())
			})
		})
	}

	Describe("restore", func() {
		h := handlers["restore"]
		It("rejects an ID that is not 32 lowercase hex characters", func() {
			opts := []string{"slot_save_path:/srv/slots"}
			for _, id := range []string{"", ".", "..", "../a", "a/b", "prompt.bin", "0123456789ABCDEF0123456789abcdef", "0123456789abcdef", "a\x00b"} {
				Expect(call(h, opts, schema.SlotCacheRequest{ID: id})).To(Equal(http.StatusBadRequest), id)
			}
		})
	})

	Describe("save", func() {
		It("rejects a client-chosen ID", func() {
			Expect(call(handlers["save"], []string{"slot_save_path:/srv/slots"}, schema.SlotCacheRequest{ID: goodID})).To(Equal(http.StatusBadRequest))
		})
	})
})

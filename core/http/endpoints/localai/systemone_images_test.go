package localai

import (
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"strings"
)

var _ = Describe("SystemOne image admission", func() {
	It("rejects top-level and structured-state images on the NER path", func() {
		for _, body := range []string{
			`{"state":"x","images":["data:image/png;base64,AA=="],"questions":{"q":{"type":"noul","instructions":"x"}}}`,
			`{"state":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"questions":{"q":{"type":"noul","instructions":"x"}}}`,
		} {
			var req schema.SystemOneRequest
			Expect(json.Unmarshal([]byte(body), &req)).To(Succeed())
			_, err := parseSystemOneRequest(&req)
			Expect(err).To(HaveOccurred())
			Expect(systemOneInputStatus(err)).To(Equal(http.StatusNotImplemented))
		}
	})
	It("bounds image count and aggregate encoded image bytes without changing the router cap", func() {
		req := schema.SystemOneRequest{State: json.RawMessage(`"x"`)}
		req.Images = json.RawMessage(`[` + strings.TrimSuffix(strings.Repeat(`"data:image/png;base64,AA==",`, 9), ",") + `]`)
		Expect(validateSystemOneImages(&req)).NotTo(Succeed())
		req.Images = json.RawMessage(` ["data:image/png;base64,` + strings.Repeat("A", systemOneMaxImageBytes) + `"]`)
		Expect(systemOneInputStatus(validateSystemOneImages(&req))).To(Equal(http.StatusRequestEntityTooLarge))
	})
	It("retains the raw wire cap and rejects overflow before binding", func() {
		e := echo.New()
		r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"state":"`+strings.Repeat("x", systemOneMaxBody)+`"}`))
		r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		c := e.NewContext(r, httptest.NewRecorder())
		var req schema.SystemOneRequest
		Expect(systemOneBindStatus(systemOneBind(c, &req))).To(Equal(http.StatusRequestEntityTooLarge))
	})
})

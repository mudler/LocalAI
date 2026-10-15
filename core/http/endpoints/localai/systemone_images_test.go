package localai

import (
	"context"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
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

var _ = Describe("SystemOne bounded wire and image parsing", func() {
	It("rejects trailing JSON and counts trailing whitespace toward the wire cap", func() {
		for _, item := range []struct {
			body string
			code int
		}{{`{} {}`, 400}, {`{"state":` + strings.Repeat(" ", systemOneMaxBody), 413}, {`{"questions":5,"state":"` + strings.Repeat("x", systemOneMaxBody) + `"}`, 413}, {`{}` + strings.Repeat(" ", systemOneMaxBody), 413}} {
			e := echo.New()
			r := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(item.body))
			r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			var req schema.SystemOneRequest
			Expect(systemOneBindStatus(systemOneBind(e.NewContext(r, httptest.NewRecorder()), &req))).To(Equal(item.code))
		}
	})
	It("rejects empty MIME subtype or image data", func() {
		for _, image := range []string{"data:image/;base64,", "data:image/png;base64,", "data:image/;base64,AA=="} {
			data, err := json.Marshal([]string{image})
			Expect(err).NotTo(HaveOccurred())
			Expect(validateSystemOneImages(&schema.SystemOneRequest{State: json.RawMessage(`"x"`), Images: data})).NotTo(Succeed())
		}
	})
})

type unreadDecisionBody struct{}

func (unreadDecisionBody) Read([]byte) (int, error) {
	Fail("saturated admission read request body")
	return 0, nil
}

var _ = Describe("HTTP decision admission", func() {
	It("rejects saturation before buffering on all decision handlers", func() {
		var releases []func()
		defer func() {
			for _, r := range releases {
				r()
			}
		}()
		for i := 0; i < systemone.MaxAdmissions; i++ {
			r, err := systemone.AcquireAdmission(context.Background())
			Expect(err).NotTo(HaveOccurred())
			releases = append(releases, r)
		}
		for _, handler := range []echo.HandlerFunc{SystemOneEndpoint(nil), SystemOnePermuteEndpoint(nil), SystemOneSeparateEndpoint(nil)} {
			e := echo.New()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/systemone", unreadDecisionBody{})
			Expect(handler(e.NewContext(req, w))).To(Succeed())
			Expect(w.Code).To(Equal(503))
		}
	})
})

var _ = Describe("Exact public decision wire budgets", func() {
	It("accepts exact text/image wire limits but rejects one more byte", func() {
		for _, item := range []struct {
			body  string
			limit int
		}{{`{"state":"x"}`, systemone.MaxBodyBytes}, {`{"state":{},"images":["data:image/png;base64,AA=="]}`, systemone.MaxImageBodyBytes}} {
			for _, extra := range []int{0, 1} {
				body := item.body + strings.Repeat(" ", item.limit-len(item.body)+extra)
				req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				var value schema.SystemOneRequest
				err := systemOneBind(echo.New().NewContext(req, httptest.NewRecorder()), &value)
				if extra == 0 {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(systemOneBindStatus(err)).To(Equal(413))
				}
			}
		}
	})
})

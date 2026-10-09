// SPDX-License-Identifier: MIT
package localai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Decisions conversion", func() {
	It("preserves question order, unnamed questions and typed choices", func() {
		var req schema.DecisionsRequest
		Expect(json.Unmarshal([]byte(`{"model":"m","input":"evidence","questions":[{"type":"predicate","instructions":"yes?"},{"type":"choice","name":"q000000","instructions":"pick","choices":[{"value":true},{"value":"true"}]},{"type":"score","instructions":"rate","levels":[{"label":"low"},{"label":"high"}]}]}`), &req)).To(Succeed())
		translated, err := convertDecisionsRequest(&req)
		Expect(err).NotTo(HaveOccurred())
		Expect(translated.Questions).To(HaveLen(3))
		Expect(translated.Questions[decisionQuestionID(0)].Type).To(Equal("noul"))
		response, err := convertDecisionsResponse(&req, []byte(`{"model":"m","answers":{"q000000":{"type":"noul","noul":0.7},"q000001":{"type":"choice","choice":"c000000","confidence":0.8,"probabilities":{"c000000":0.9,"c000001":0.1}},"q000002":{"type":"score","score":0.4,"confidence":0.6,"probabilities":{"0":0.6,"1":0.4}}},"usage":{"input_tokens":7,"output_tokens":2}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Answers[0].Probability).To(HaveValue(Equal(0.7)))
		Expect(response.Answers[1].Choice).To(Equal(json.RawMessage(`true`)))
		Expect(response.Answers[1].Probabilities[1].Value).To(Equal(json.RawMessage(`"true"`)))
		Expect(response.Answers[2].Probabilities[1].Label).To(Equal("high"))
		Expect(response.Usage.TotalTokens).To(Equal(9))
	})
	It("rejects duplicate names and typed values", func() {
		for _, questions := range []string{`[{"type":"predicate","name":"a","instructions":"x"},{"type":"predicate","name":"a","instructions":"x"}]`, `[{"type":"choice","instructions":"x","choices":[{"value":true},{"value":true}]}]`} {
			var req schema.DecisionsRequest
			Expect(json.Unmarshal([]byte(`{"model":"m","input":"x","questions":`+questions+`}`), &req)).To(Succeed())
			_, err := convertDecisionsRequest(&req)
			Expect(err).To(HaveOccurred())
		}
	})
})

var _ = Describe("Decisions runtime contract", func() {
	const request = `{"model":"m","input":"evidence","questions":[{"type":"predicate","instructions":"yes?"},{"type":"choice","instructions":"pick","choices":[{"value":false},{"value":"false"}]},{"type":"score","instructions":"rate","levels":[{"label":"low"},{"label":"middle"},{"label":"high"}]}]}`
	const result = `{"model":"resolved","answers":{"q000002":{"type":"score","score":0.99,"confidence":0,"probabilities":{"0":0.33,"1":0.33,"2":0.34}},"q000001":{"type":"choice","choice":"c000000","confidence":0,"probabilities":{"c000000":1,"c000001":0}},"q000000":{"type":"noul","noul":0}},"usage":{"input_tokens":7,"output_tokens":0,"input_tokens_details":{"cached_tokens":2}}}`
	parse := func() *schema.DecisionsRequest {
		var req schema.DecisionsRequest
		Expect(json.Unmarshal([]byte(request), &req)).To(Succeed())
		return &req
	}
	It("derives score from the emitted ordered distribution and retains false and zero", func() {
		out, err := convertDecisionsResponse(parse(), []byte(result))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Answers[2].Score).To(HaveValue(BeNumerically("~", 1.01, 1e-12)))
		wire, err := json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(wire)).To(ContainSubstring(`"choice":false`))
		Expect(string(wire)).To(ContainSubstring(`"probability":0`))
		Expect(string(wire)).To(ContainSubstring(`"confidence":0`))
		for _, a := range out.Answers {
			Expect(a.Name).To(BeNil())
		}
		out, err = convertDecisionsResponse(parse(), []byte(strings.ReplaceAll(strings.ReplaceAll(result, `"score":0.99`, `"score":0`), `"0":0.33,"1":0.33,"2":0.34`, `"0":1,"1":0,"2":0`)))
		Expect(err).NotTo(HaveOccurred())
		wire, err = json.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(wire)).To(ContainSubstring(`"score":0`))
	})
	It("rejects missing answers, malformed distributions and invalid accounting", func() {
		for _, mutation := range [][2]string{
			{`"q000000"`, `"unexpected"`},
			{`"noul":0`, `"noul":null`},
			{`"noul":0`, `"noul":1.1`},
			{`"c000000":1`, `"c000000":0.2`},
			{`"c000001":0`, `"c000001":-0.1`},
			{`"c000001":0`, `"c000001":null`},
			{`"0":0.33`, `"0":null`},
			{`"c000001":0`, `"wrong":0`},
			{`"choice":"c000000"`, `"choice":"missing"`},
			{`"confidence":0`, `"confidence":null`},
			{`"score":0.99`, `"score":null`},
			{`"input_tokens":7`, `"input_tokens":-1`},
			{`"output_tokens":0`, `"output_tokens":null`},
			{`"cached_tokens":2`, `"cached_tokens":-2`},
		} {
			_, err := convertDecisionsResponse(parse(), []byte(strings.ReplaceAll(result, mutation[0], mutation[1])))
			Expect(err).To(HaveOccurred(), mutation[0])
		}
	})
	It("passes refusal through without inventing values", func() {
		out, err := convertDecisionsResponse(parse(), []byte(strings.Replace(result, `{"type":"noul","noul":0}`, `{"type":"refusal"}`, 1)))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Answers[0].Type).To(Equal("refusal"))
		Expect(out.Answers[0].Probability).To(BeNil())
	})
	It("preserves user text and inline images and rejects unsupported input", func() {
		req := parse()
		var pixel bytes.Buffer
		Expect(png.Encode(&pixel, image.NewRGBA(image.Rect(0, 0, 1, 1)))).To(Succeed())
		image := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixel.Bytes())
		req.Input = json.RawMessage(`[{"role":"user","content":"first"},{"type":"message","role":"user","content":[{"type":"input_text","text":"second"},{"type":"input_image","image_url":"` + image + `","detail":"auto"}]}]`)
		internal, err := convertDecisionsRequest(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(internal.State)).To(Equal(`"first\nsecond"`))
		Expect(string(internal.Images)).To(Equal(`["` + image + `"]`))
		for _, input := range []string{
			`[{"role":"system","content":"x"}]`,
			`[{"role":"user","content":[{"type":"input_audio"}]}]`,
			`[{"role":"user","content":[{"type":"input_text"}]}]`,
			`[{"role":"user","content":[{"type":"input_image","image_url":"` + image + `","detail":"high"}]}]`,
			`[{"role":"user","content":[{"type":"input_image","image_url":"https://example.org/a.png"}]}]`,
		} {
			req.Input = json.RawMessage(input)
			_, err := convertDecisionsRequest(req)
			Expect(err).To(HaveOccurred(), input)
		}
	})
	It("executes in process and stamps actual usage", func() {
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(request)), rec)
		calls := 0
		err := decisionsEndpoint(func(got echo.Context, req *schema.SystemOneRequest, respond func(string) error) error {
			calls++
			Expect(got).To(Equal(c))
			Expect(req.Questions).To(HaveLen(3))
			return respond(result)
		})(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(Equal(1))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(c.Get(middleware.ContextKeyResponseModel)).To(Equal("resolved"))
		Expect(c.Get(middleware.ContextKeyTotalTokens)).To(Equal(int64(7)))
	})
	It("does not execute invalid requests or publish invalid backend answers", func() {
		for _, body := range []string{`{`, `{"model":"m","input":"x","questions":[]}`, request} {
			e := echo.New()
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body)), rec)
			calls := 0
			Expect(decisionsEndpoint(func(_ echo.Context, _ *schema.SystemOneRequest, respond func(string) error) error {
				calls++
				return respond(`{}`)
			})(c)).To(Succeed())
			if body == request {
				Expect(calls).To(Equal(1))
				Expect(rec.Code).To(Equal(500))
			} else {
				Expect(calls).To(BeZero())
				Expect(rec.Code).To(Equal(400))
			}
			Expect(c.Get(middleware.ContextKeyTotalTokens)).To(BeNil())
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"refusal"`))
		}
	})
	It("preserves execution errors and request cancellation", func() {
		for _, failure := range []error{errors.New("backend unavailable"), context.Canceled} {
			ctx, cancel := context.WithCancel(context.Background())
			e := echo.New()
			c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(request)).WithContext(ctx), httptest.NewRecorder())
			err := decisionsEndpoint(func(got echo.Context, _ *schema.SystemOneRequest, _ func(string) error) error {
				cancel()
				Expect(got.Request().Context().Err()).To(MatchError(context.Canceled))
				return failure
			})(c)
			cancel()
			Expect(err).To(MatchError(failure))
			Expect(c.Response().Committed).To(BeFalse())
			Expect(c.Get(middleware.ContextKeyTotalTokens)).To(BeNil())
		}
	})
})

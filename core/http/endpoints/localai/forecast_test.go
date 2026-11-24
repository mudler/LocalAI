package localai

import (
	"encoding/json"
	"math"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func parseForecastRequest(body string) *schema.ForecastRequest {
	var req schema.ForecastRequest
	ExpectWithOffset(1, json.Unmarshal([]byte(body), &req)).To(Succeed())
	return &req
}

func expectBadRequest(err error, fragment string) {
	ExpectWithOffset(1, err).To(HaveOccurred())
	var httpErr *echo.HTTPError
	ExpectWithOffset(1, err).To(BeAssignableToTypeOf(httpErr))
	httpErr = err.(*echo.HTTPError)
	ExpectWithOffset(1, httpErr.Code).To(Equal(http.StatusBadRequest))
	ExpectWithOffset(1, httpErr.Message).To(ContainSubstring(fragment))
}

var _ = Describe("forecast request parsing", func() {
	It("reads null as a missing value and a flat target as one variate", func() {
		req := parseForecastRequest(`{"model":"m","prediction_length":2,"series":[{"target":[1,null,3]}]}`)
		Expect(req.Series[0].Target).To(HaveLen(1))
		Expect(req.Series[0].Target[0]).To(HaveLen(3))
		Expect(req.Series[0].Target[0][1]).To(BeNil())

		out, err := buildForecastRequest(req)
		Expect(err).ToNot(HaveOccurred())
		s := out.Series[0]
		Expect(s.NumVariates).To(Equal(int32(1)))
		Expect(s.Length).To(Equal(int32(3)))
		Expect(s.Target[0]).To(Equal(float32(1)))
		Expect(math.IsNaN(float64(s.Target[1]))).To(BeTrue())
		Expect(s.Target[2]).To(Equal(float32(3)))
	})

	It("rejects a target that is neither numbers nor arrays of numbers", func() {
		var req schema.ForecastRequest
		Expect(json.Unmarshal([]byte(`{"series":[{"target":"abc"}]}`), &req)).ToNot(Succeed())
		Expect(json.Unmarshal([]byte(`{"series":[{"target":[["a"]]}]}`), &req)).ToNot(Succeed())
	})

	It("rejects the literal NaN, which is not JSON", func() {
		var req schema.ForecastRequest
		Expect(json.Unmarshal([]byte(`{"series":[{"target":[1,NaN]}]}`), &req)).ToNot(Succeed())
	})
})

var _ = Describe("forecast request packing", func() {
	const full = `{
		"model":"m","prediction_length":2,"quantiles":[0.1,0.9],"max_context":64,
		"symmetric_averaging":true,
		"series":[{
			"id":"a",
			"target":[[1,2,3],[4,5,6]],
			"past_covariates":[[0,1,0]],
			"known_covariates":[[0,1,0,1,1]],
			"past_categorical":[[1,null,2]],
			"known_categorical":[[1,1,2,2,3]]
		}]}`

	It("packs every field into the columnar message", func() {
		out, err := buildForecastRequest(parseForecastRequest(full))
		Expect(err).ToNot(HaveOccurred())
		Expect(out.PredictionLength).To(Equal(int32(2)))
		Expect(out.Quantiles).To(Equal([]float64{0.1, 0.9}))
		Expect(out.MaxContext).To(Equal(int32(64)))
		Expect(out.SymmetricAveraging).ToNot(BeNil())
		Expect(*out.SymmetricAveraging).To(BeTrue())
		Expect(out.ClipNonnegative).To(BeNil(), "an absent flag stays unset so the model default applies")

		s := out.Series[0]
		Expect(s.Id).To(Equal("a"))
		Expect(s.NumVariates).To(Equal(int32(2)))
		Expect(s.Target).To(Equal([]float32{1, 2, 3, 4, 5, 6}))
		Expect(s.NumPastCovariates).To(Equal(int32(1)))
		Expect(s.PastCovariates).To(Equal([]float32{0, 1, 0}))
		Expect(s.NumKnownCovariates).To(Equal(int32(1)))
		Expect(s.KnownCovariates).To(HaveLen(5))
		Expect(s.NumPastCategorical).To(Equal(int32(1)))
		Expect(math.IsNaN(s.PastCategorical[1])).To(BeTrue())
		Expect(s.KnownCategorical).To(Equal([]float64{1, 1, 2, 2, 3}))
	})

	DescribeTable("returns 400 for",
		func(body, fragment string) {
			_, err := buildForecastRequest(parseForecastRequest(body))
			expectBadRequest(err, fragment)
		},
		Entry("a zero horizon", `{"prediction_length":0,"series":[{"target":[1]}]}`, "prediction_length"),
		Entry("no series", `{"prediction_length":1,"series":[]}`, "series"),
		Entry("an empty target", `{"prediction_length":1,"series":[{"target":[]}]}`, "target"),
		Entry("a ragged target", `{"prediction_length":1,"series":[{"target":[[1,2],[3]]}]}`, "series[0].target[1]"),
		Entry("a negative max_context", `{"prediction_length":1,"max_context":-1,"series":[{"target":[1]}]}`, "max_context"),
		Entry("a quantile outside (0,1)", `{"prediction_length":1,"quantiles":[1],"series":[{"target":[1]}]}`, "quantiles"),
		Entry("past covariates of the wrong length", `{"prediction_length":1,"series":[{"target":[1,2],"past_covariates":[[1]]}]}`, "past_covariates"),
		Entry("known covariates one step short", `{"prediction_length":2,"series":[{"target":[1,2],"known_covariates":[[1,2,3]]}]}`, "expected 4"),
		Entry("a fractional categorical code", `{"prediction_length":1,"series":[{"target":[1],"past_categorical":[[1.5]]}]}`, "integer code"),
		Entry("a value beyond float32", `{"prediction_length":1,"series":[{"target":[1e300]}]}`, "float32"),
		Entry("the second series being bad", `{"prediction_length":1,"series":[{"target":[1]},{"target":[]}]}`, "series[1]"),
	)

	It("accepts series of different lengths in one request", func() {
		out, err := buildForecastRequest(parseForecastRequest(
			`{"prediction_length":1,"series":[{"target":[1,2,3]},{"target":[1,2]}]}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Series[0].Length).To(Equal(int32(3)))
		Expect(out.Series[1].Length).To(Equal(int32(2)))
	})
})

var _ = Describe("forecast response", func() {
	It("slices the flat V*Q*H values into one [Q][H] block per variate", func() {
		req := &proto.ForecastRequest{Series: []*proto.ForecastSeries{{Id: "a"}, {Id: "b"}}}
		// Each value encodes series, variate, quantile and step as sVQH.
		code := func(s, v, q, h int) float32 { return float32(s*1000 + v*100 + q*10 + h) }
		res := &proto.ForecastResponse{PredictionLength: 3, QuantileLevels: []float64{0.1, 0.9}}
		for s, nv := range []int{2, 1} {
			sr := &proto.ForecastSeriesResult{NumVariates: int32(nv), Id: []string{"a", "b"}[s]}
			for v := 0; v < nv; v++ {
				for q := 0; q < 2; q++ {
					for h := 0; h < 3; h++ {
						sr.Values = append(sr.Values, code(s, v, q, h))
					}
				}
			}
			res.Series = append(res.Series, sr)
		}

		out, err := buildForecastResponse("m", req, res)
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Object).To(Equal("forecast"))
		Expect(out.Model).To(Equal("m"))
		Expect(out.PredictionLength).To(Equal(3))
		Expect(out.QuantileLevels).To(Equal([]float64{0.1, 0.9}))
		Expect(out.Data).To(HaveLen(2))
		Expect(out.Data[0].ID).To(Equal("a"))
		Expect(out.Data[1].Index).To(Equal(1))
		Expect(out.Data[0].Variates).To(HaveLen(2))
		Expect(out.Data[0].Variates[1].Values[1]).To(Equal([]float32{code(0, 1, 1, 0), code(0, 1, 1, 1), code(0, 1, 1, 2)}))
		Expect(out.Data[1].Variates[0].Values[0]).To(Equal([]float32{code(1, 0, 0, 0), code(1, 0, 0, 1), code(1, 0, 0, 2)}))
	})

	It("serialises with the documented JSON field names", func() {
		req := &proto.ForecastRequest{Series: []*proto.ForecastSeries{{}}}
		res := &proto.ForecastResponse{
			PredictionLength: 2, QuantileLevels: []float64{0.5},
			Series: []*proto.ForecastSeriesResult{{Id: "x", NumVariates: 1, Values: []float32{1.5, 2.5}}},
		}
		out, err := buildForecastResponse("m", req, res)
		Expect(err).ToNot(HaveOccurred())
		raw, err := json.Marshal(out)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(MatchJSON(`{
			"object":"forecast","model":"m","prediction_length":2,"quantile_levels":[0.5],
			"data":[{"index":0,"id":"x","variates":[{"index":0,"values":[[1.5,2.5]]}]}]}`))
	})

	It("fails loudly when the backend answers with the wrong shape", func() {
		req := &proto.ForecastRequest{Series: []*proto.ForecastSeries{{}}}
		_, err := buildForecastResponse("m", req, &proto.ForecastResponse{})
		Expect(err).To(HaveOccurred())
		_, err = buildForecastResponse("m", req, &proto.ForecastResponse{
			PredictionLength: 2, QuantileLevels: []float64{0.5},
			Series: []*proto.ForecastSeriesResult{{NumVariates: 1, Values: []float32{1}}},
		})
		Expect(err).To(HaveOccurred())
	})
})

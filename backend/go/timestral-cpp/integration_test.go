package main

// Integration specs against the real libtimestral.so and the 5M F32 model.
// They need TIMESTRAL_BACKEND_TEST_MODEL (path of the GGUF) and a loadable
// library (TIMESTRAL_LIBRARY, or libtimestral.so on LD_LIBRARY_PATH). When
// either is missing the specs skip; they never pass silently. test.sh fetches
// the model and checks its SHA-256.
//
// The weights are real, so the specs check behaviour and invariants (shape,
// finite values, ordered quantiles, repeatability, request order), not the
// forecast values.

import (
	"math"
	"os"
	"sync"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	realLibOnce sync.Once
	realLib     uintptr
	realLibErr  error
)

func openRealLib() (uintptr, error) {
	realLibOnce.Do(func() {
		name := os.Getenv("TIMESTRAL_LIBRARY")
		if name == "" {
			name = "libtimestral.so"
		}
		realLib, realLibErr = purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	})
	return realLib, realLibErr
}

// wave is a smooth, strictly positive series with a given period.
func wave(n int, period float64, offset float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(10 + offset + 3*math.Sin(2*math.Pi*float64(i)/period))
	}
	return out
}

func waveSeries(id string, n int, period, offset float64) *pb.ForecastSeries {
	return &pb.ForecastSeries{Id: id, NumVariates: 1, Length: int32(n), Target: wave(n, period, offset)}
}

var _ = Describe("timestral.cpp integration", Ordered, func() {
	var tc *TimestralCpp

	BeforeAll(func() {
		modelPath := os.Getenv("TIMESTRAL_BACKEND_TEST_MODEL")
		if modelPath == "" {
			Skip("TIMESTRAL_BACKEND_TEST_MODEL is not set; run test.sh to fetch the 5M F32 model")
		}
		if _, err := os.Stat(modelPath); err != nil {
			Skip("model file not found: " + modelPath)
		}
		lib, err := openRealLib()
		if err != nil {
			Skip("libtimestral.so is not loadable: " + err.Error())
		}
		registerLibFuncs(lib)

		tc = &TimestralCpp{}
		Expect(tc.Load(&pb.ModelOptions{ModelFile: modelPath, Threads: 2})).To(Succeed())
	})

	AfterAll(func() {
		if tc != nil {
			Expect(tc.Free()).To(Succeed())
		}
	})

	forecast := func(req *pb.ForecastRequest) *pb.ForecastResponse {
		res, err := tc.Forecast(req)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return &res
	}

	expectSane := func(res *pb.ForecastResponse, h int) {
		q := len(res.QuantileLevels)
		for _, sr := range res.Series {
			ExpectWithOffset(1, sr.Values).To(HaveLen(int(sr.NumVariates) * q * h))
			for v := 0; v < int(sr.NumVariates); v++ {
				for step := 0; step < h; step++ {
					prev := math.Inf(-1)
					for qi := 0; qi < q; qi++ {
						x := float64(sr.Values[(v*q+qi)*h+step])
						ExpectWithOffset(1, math.IsNaN(x) || math.IsInf(x, 0)).To(BeFalse(), "finite")
						ExpectWithOffset(1, x).To(BeNumerically(">=", prev-1e-4), "quantiles do not cross at step %d", step)
						prev = x
					}
				}
			}
		}
	}

	It("exposes ascending quantile levels that include 0.1, 0.5 and 0.9", func() {
		Expect(len(tc.levels)).To(BeNumerically(">", 3))
		for i := 1; i < len(tc.levels); i++ {
			Expect(tc.levels[i]).To(BeNumerically(">", tc.levels[i-1]))
		}
		_, levels, err := selectQuantiles(nil, defaultQuantiles, tc.levels)
		Expect(err).ToNot(HaveOccurred())
		Expect(levels).To(Equal([]float64{0.1, 0.5, 0.9}))
	})

	It("forecasts one univariate series with the default quantiles", func() {
		res := forecast(&pb.ForecastRequest{PredictionLength: 12, Series: []*pb.ForecastSeries{waveSeries("a", 96, 12, 0)}})
		Expect(res.PredictionLength).To(Equal(int32(12)))
		Expect(res.QuantileLevels).To(Equal([]float64{0.1, 0.5, 0.9}))
		Expect(res.Series).To(HaveLen(1))
		expectSane(res, 12)
	})

	It("treats NaN in the target as missing and still returns finite values", func() {
		s := waveSeries("gap", 96, 12, 0)
		s.Target[40], s.Target[41] = float32(math.NaN()), float32(math.NaN())
		res := forecast(&pb.ForecastRequest{PredictionLength: 6, Series: []*pb.ForecastSeries{s}})
		expectSane(res, 6)
	})

	It("forecasts a two-variate series", func() {
		s := &pb.ForecastSeries{Id: "mv", NumVariates: 2, Length: 64}
		s.Target = append(wave(64, 8, 0), wave(64, 16, 5)...)
		res := forecast(&pb.ForecastRequest{PredictionLength: 5, Series: []*pb.ForecastSeries{s}})
		Expect(res.Series[0].NumVariates).To(Equal(int32(2)))
		expectSane(res, 5)
	})

	It("is repeatable: the same request gives byte-identical output", func() {
		req := func() *pb.ForecastRequest {
			return &pb.ForecastRequest{PredictionLength: 8, Series: []*pb.ForecastSeries{waveSeries("a", 80, 10, 0)}}
		}
		Expect(forecast(req()).Series[0].Values).To(Equal(forecast(req()).Series[0].Values))
	})

	It("keeps the request order when series differ in length", func() {
		long := waveSeries("long", 96, 12, 0)
		short := waveSeries("short", 40, 8, 3)
		longAgain := waveSeries("long-2", 96, 12, 4)

		alone := func(s *pb.ForecastSeries) []float32 {
			return forecast(&pb.ForecastRequest{PredictionLength: 6, Series: []*pb.ForecastSeries{s}}).Series[0].Values
		}
		wantLong, wantShort, wantLong2 := alone(long), alone(short), alone(longAgain)

		res := forecast(&pb.ForecastRequest{PredictionLength: 6, Series: []*pb.ForecastSeries{long, short, longAgain}})
		Expect(res.Series).To(HaveLen(3))
		Expect([]string{res.Series[0].Id, res.Series[1].Id, res.Series[2].Id}).To(Equal([]string{"long", "short", "long-2"}))
		Expect(res.Series[1].Values).To(Equal(wantShort), "a series in a mixed request gives what it gives alone")
		Expect(res.Series[0].Values).To(HaveLen(len(wantLong)))
		Expect(res.Series[2].Values).To(HaveLen(len(wantLong2)))
		expectSane(res, 6)
	})

	It("forecasts with past and known covariates and categorical codes", func() {
		const n, h = 64, 6
		s := waveSeries("cov", n, 8, 0)
		s.NumPastCovariates = 1
		s.PastCovariates = wave(n, 8, 0)
		s.NumKnownCovariates = 1
		s.KnownCovariates = wave(n+h, 8, 0)
		s.NumKnownCategorical = 1
		for i := 0; i < n+h; i++ {
			s.KnownCategorical = append(s.KnownCategorical, float64(i%3))
		}
		s.NumPastCategorical = 1
		for i := 0; i < n; i++ {
			s.PastCategorical = append(s.PastCategorical, float64(i%3))
		}
		res := forecast(&pb.ForecastRequest{PredictionLength: h, Series: []*pb.ForecastSeries{s}})
		expectSane(res, h)
	})

	It("returns only the requested quantiles, ascending", func() {
		res := forecast(&pb.ForecastRequest{PredictionLength: 4, Quantiles: []float64{0.9, 0.5}, Series: []*pb.ForecastSeries{waveSeries("a", 64, 8, 0)}})
		Expect(res.QuantileLevels).To(Equal([]float64{0.5, 0.9}))
		expectSane(res, 4)
	})

	It("accepts a max_context shorter than the history", func() {
		res := forecast(&pb.ForecastRequest{PredictionLength: 4, MaxContext: 32, Series: []*pb.ForecastSeries{waveSeries("a", 128, 8, 0)}})
		expectSane(res, 4)
	})

	It("survives parallel callers", func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				res, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 4, Series: []*pb.ForecastSeries{waveSeries("p", 48+i, 8, float64(i))}})
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Series).To(HaveLen(1))
			}(i)
		}
		wg.Wait()
	})

	It("refuses a file that is not a timestral model and keeps the error text", func() {
		bad := GinkgoT().TempDir() + "/not-a-model.gguf"
		Expect(os.WriteFile(bad, []byte("this is not a gguf file"), 0o600)).To(Succeed())
		other := &TimestralCpp{}
		err := other.Load(&pb.ModelOptions{ModelFile: bad})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("load"))
		Expect(err.Error()).ToNot(HaveSuffix(": "), "the library's own message is included")
	})
})

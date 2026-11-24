package main

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var modelLevels = []float64{0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99}

// series builds a series of v variates and t steps. Variate j starts at
// first+j, so a result can be traced back to its input.
func series(id string, first float32, v, t int) *pb.ForecastSeries {
	s := &pb.ForecastSeries{Id: id, NumVariates: int32(v), Length: int32(t)}
	for j := 0; j < v; j++ {
		for i := 0; i < t; i++ {
			s.Target = append(s.Target, first+float32(j))
		}
	}
	return s
}

func boolPtr(b bool) *bool { return &b }

func errCode(err error) codes.Code {
	st, ok := status.FromError(err)
	ExpectWithOffset(1, ok).To(BeTrue(), "not a gRPC status error: %v", err)
	return st.Code()
}

var _ = Describe("TimestralCpp", func() {
	var (
		fake  *fakeLib
		model string
		tc    *TimestralCpp
	)

	loadOpts := func(options ...string) *pb.ModelOptions {
		return &pb.ModelOptions{ModelFile: model, Threads: 3, Options: options}
	}

	BeforeEach(func() {
		fake = &fakeLib{levels: modelLevels, abiVersion: 1}
		fake.install()
		model = filepath.Join(GinkgoT().TempDir(), "timestral-test.gguf")
		Expect(os.WriteFile(model, []byte("gguf"), 0o600)).To(Succeed())
		tc = &TimestralCpp{}
	})

	Describe("Load", func() {
		It("loads the model, reads the quantile levels and passes the thread budget", func() {
			Expect(tc.Load(loadOpts())).To(Succeed())
			Expect(tc.levels).To(Equal(modelLevels))
			Expect(fake.calls[0].threads).To(Equal(int32(3)))
		})

		It("uses 4 threads when LocalAI gives no budget", func() {
			Expect(tc.Load(&pb.ModelOptions{ModelFile: model})).To(Succeed())
			Expect(fake.calls[0].threads).To(Equal(int32(4)))
		})

		It("refuses an empty path and a missing file", func() {
			Expect(tc.Load(&pb.ModelOptions{})).To(MatchError(ContainSubstring("ModelFile is required")))
			Expect(tc.Load(&pb.ModelOptions{ModelFile: model + ".missing"})).To(MatchError(ContainSubstring("not readable")))
			Expect(fake.loads).To(BeZero())
		})

		It("refuses a library with another ABI version", func() {
			fake.abiVersion = 2
			Expect(tc.Load(loadOpts())).To(MatchError(ContainSubstring("ABI version 2")))
			Expect(fake.loads).To(BeZero())
		})

		It("returns the library's own error when the load fails", func() {
			fake.loadFails = true
			Expect(tc.Load(loadOpts())).To(MatchError(ContainSubstring("cannot read model")))
		})

		It("frees the previous handle on a second load", func() {
			Expect(tc.Load(loadOpts())).To(Succeed())
			Expect(tc.Load(loadOpts())).To(Succeed())
			Expect(fake.frees).To(Equal(1))
		})
	})

	Describe("Free", func() {
		It("frees the handle once and makes Forecast fail cleanly", func() {
			Expect(tc.Load(loadOpts())).To(Succeed())
			Expect(tc.Free()).To(Succeed())
			Expect(tc.Free()).To(Succeed())
			Expect(fake.frees).To(Equal(1))
			_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(errCode(err)).To(Equal(codes.FailedPrecondition))
		})
	})

	Describe("Forecast", func() {
		BeforeEach(func() {
			Expect(tc.Load(loadOpts())).To(Succeed())
		})

		It("returns the default quantiles 0.1, 0.5 and 0.9 when none are requested", func() {
			res, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 2, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.QuantileLevels).To(Equal([]float64{0.1, 0.5, 0.9}))
			Expect(res.PredictionLength).To(Equal(int32(2)))
		})

		It("honours the default_quantiles option", func() {
			Expect(tc.Load(loadOpts("default_quantiles:0.25,0.75"))).To(Succeed())
			res, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.QuantileLevels).To(Equal([]float64{0.25, 0.75}))
		})

		It("reshapes the ABI [B,V,H,Q] output to [V][Q][H] for the chosen quantiles", func() {
			req := &pb.ForecastRequest{
				PredictionLength: 3,
				Quantiles:        []float64{0.9, 0.1, 0.9}, // unsorted and repeated on purpose
				Series:           []*pb.ForecastSeries{series("a", 2, 2, 5)},
			}
			res, err := tc.Forecast(req)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.QuantileLevels).To(Equal([]float64{0.1, 0.9}), "ascending, no duplicates")
			sr := res.Series[0]
			Expect(sr.NumVariates).To(Equal(int32(2)))
			var want []float32
			for v := 0; v < 2; v++ {
				for _, q := range []int{1, 5} { // indexes of 0.1 and 0.9 in the model levels
					for h := 0; h < 3; h++ {
						want = append(want, value(float32(2+v), h, q))
					}
				}
			}
			Expect(sr.Values).To(Equal(want))
		})

		It("accepts a requested level within 1e-6 and rejects one further away", func() {
			_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Quantiles: []float64{0.5 + 5e-7}, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(err).ToNot(HaveOccurred())

			_, err = tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Quantiles: []float64{0.4}, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(errCode(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("valid levels: 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99"))
		})

		It("groups series by shape, runs one call per group and keeps the request order", func() {
			req := &pb.ForecastRequest{
				PredictionLength: 2,
				Quantiles:        []float64{0.5},
				Series: []*pb.ForecastSeries{
					series("long-1", 1, 1, 6),
					series("short", 2, 1, 4),
					series("long-2", 3, 1, 6),
					series("wide", 4, 2, 6),
				},
			}
			res, err := tc.Forecast(req)
			Expect(err).ToNot(HaveOccurred())

			calls := fake.forecastCalls()
			Expect(calls).To(HaveLen(3), "shapes: 1x6, 1x4, 2x6")
			Expect(calls[0].B).To(Equal(int32(2)))
			Expect(calls[0].context).To(Equal([]float32{1, 1, 1, 1, 1, 1, 3, 3, 3, 3, 3, 3}), "long-1 then long-2")
			Expect(calls[1].B).To(Equal(int32(1)))
			Expect(calls[1].T).To(Equal(int32(4)))
			Expect(calls[2].V).To(Equal(int32(2)))

			Expect(res.Series).To(HaveLen(4))
			for i, want := range []struct {
				id    string
				first float32
				v     int32
			}{{"long-1", 1, 1}, {"short", 2, 1}, {"long-2", 3, 1}, {"wide", 4, 2}} {
				sr := res.Series[i]
				Expect(sr.Id).To(Equal(want.id))
				Expect(sr.NumVariates).To(Equal(want.v))
				Expect(sr.Values[0]).To(Equal(value(want.first, 0, 3)), "series %s carries its own input", want.id)
				Expect(sr.Values).To(HaveLen(int(want.v) * 2))
			}
		})

		It("passes covariates through to the library per series", func() {
			s1 := series("a", 1, 1, 3)
			s1.NumKnownCovariates = 1
			s1.KnownCovariates = []float32{1, 2, 3, 4, 5} // T+H = 3+2
			s1.NumPastCategorical = 1
			s1.PastCategorical = []float64{7, math.NaN(), 9}
			s2 := series("b", 2, 1, 3)
			s2.NumKnownCovariates = 1
			s2.KnownCovariates = []float32{6, 7, 8, 9, 10}
			s2.NumPastCategorical = 1
			s2.PastCategorical = []float64{1, 2, 3}
			_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 2, Series: []*pb.ForecastSeries{s1, s2}})
			Expect(err).ToNot(HaveOccurred())
			calls := fake.forecastCalls()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].cKnown).To(Equal(int32(1)))
			Expect(calls[0].knownCov).To(Equal([]float32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}))
			Expect(calls[0].ccPast).To(Equal(int32(1)))
			Expect(calls[0].catPast[0]).To(Equal(7.0))
			Expect(math.IsNaN(calls[0].catPast[1])).To(BeTrue())
		})

		It("sends -1 for absent averaging and clipping flags and the value when set", func() {
			base := func() *pb.ForecastRequest {
				return &pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}}
			}
			_, err := tc.Forecast(base())
			Expect(err).ToNot(HaveOccurred())
			r := base()
			r.SymmetricAveraging, r.ClipNonnegative = boolPtr(true), boolPtr(false)
			r.MaxContext = 128
			_, err = tc.Forecast(r)
			Expect(err).ToNot(HaveOccurred())

			calls := fake.forecastCalls()
			Expect(calls[0].sym).To(Equal(int32(-1)))
			Expect(calls[0].clip).To(Equal(int32(-1)))
			Expect(calls[0].maxContext).To(BeZero())
			Expect(calls[1].sym).To(Equal(int32(1)))
			Expect(calls[1].clip).To(Equal(int32(0)))
			Expect(calls[1].maxContext).To(Equal(int32(128)))
		})

		It("falls back to the max_context option", func() {
			Expect(tc.Load(loadOpts("max_context:64"))).To(Succeed())
			_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(err).ToNot(HaveOccurred())
			Expect(fake.forecastCalls()[0].maxContext).To(Equal(int32(64)))
		})

		It("frees the library buffer exactly once per call", func() {
			req := &pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4), series("b", 1, 1, 5)}}
			_, err := tc.Forecast(req)
			Expect(err).ToNot(HaveOccurred())
			Expect(fake.freeBuffers).To(Equal(2), "one buffer per shape group")
		})

		It("reports the library error as InvalidArgument and frees nothing", func() {
			fake.forecastFail = true
			_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
			Expect(errCode(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("invalid request"))
			Expect(fake.freeBuffers).To(BeZero())
		})

		DescribeTable("rejects a malformed request before calling the library",
			func(mutate func(*pb.ForecastRequest), fragment string) {
				req := &pb.ForecastRequest{PredictionLength: 2, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}}
				mutate(req)
				_, err := tc.Forecast(req)
				Expect(errCode(err)).To(Equal(codes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(fragment))
				Expect(fake.forecastCalls()).To(BeEmpty())
			},
			Entry("no series", func(r *pb.ForecastRequest) { r.Series = nil }, "no series"),
			Entry("a zero horizon", func(r *pb.ForecastRequest) { r.PredictionLength = 0 }, "prediction_length"),
			Entry("a negative max_context", func(r *pb.ForecastRequest) { r.MaxContext = -1 }, "max_context"),
			Entry("a target of the wrong length", func(r *pb.ForecastRequest) { r.Series[0].Target = r.Series[0].Target[:3] }, "target holds 3 values, expected 4"),
			Entry("known covariates that stop at the context end", func(r *pb.ForecastRequest) {
				r.Series[0].NumKnownCovariates = 1
				r.Series[0].KnownCovariates = []float32{1, 2, 3, 4}
			}, "known_covariates holds 4 values, expected 6"),
			Entry("zero variates", func(r *pb.ForecastRequest) { r.Series[0].NumVariates = 0 }, "at least one variate"),
			Entry("a negative covariate count", func(r *pb.ForecastRequest) { r.Series[0].NumPastCovariates = -1 }, "negative"),
		)

		It("enforces the per-request series and point limits", func() {
			Expect(tc.Load(loadOpts("max_series:2", "max_total_points:10"))).To(Succeed())
			three := &pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 2), series("b", 1, 1, 2), series("c", 1, 1, 2)}}
			_, err := tc.Forecast(three)
			Expect(err).To(MatchError(ContainSubstring("exceed the limit of 2")))
			big := &pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 6), series("b", 1, 1, 6)}}
			_, err = tc.Forecast(big)
			Expect(err).To(MatchError(ContainSubstring("max_total_points")))
			Expect(fake.forecastCalls()).To(BeEmpty())
		})

		It("never runs two library calls at once", func() {
			fake.delay = 20 * time.Millisecond
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, err := tc.Forecast(&pb.ForecastRequest{PredictionLength: 1, Series: []*pb.ForecastSeries{series("a", 1, 1, 4)}})
					Expect(err).ToNot(HaveOccurred())
				}()
			}
			wg.Wait()
			Expect(fake.maxInFlight.Load()).To(Equal(int32(1)))
			Expect(fake.forecastCalls()).To(HaveLen(6))
		})
	})
})

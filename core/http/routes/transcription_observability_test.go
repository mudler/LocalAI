// SPDX-License-Identifier: MIT
package routes_test

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	corehttp "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type observedTranscriptionBackend struct {
	grpcpkg.Backend
	err   error
	audio []byte
}

type failedTranscriptionWriter struct {
	*httptest.ResponseRecorder
}

func (*failedTranscriptionWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func (*observedTranscriptionBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*observedTranscriptionBackend) IsBusy() bool                              { return false }
func (*observedTranscriptionBackend) Free(context.Context) error                { return nil }
func (b *observedTranscriptionBackend) AudioTranscription(_ context.Context, r *pb.TranscriptRequest, _ ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	var err error
	b.audio, err = os.ReadFile(r.Dst)
	if err != nil {
		return nil, err
	}
	if b.err != nil {
		return nil, b.err
	}
	return &pb.TranscriptResult{Text: "hello world", Segments: []*pb.TranscriptSegment{{Text: "hello world"}}}, nil
}
func (b *observedTranscriptionBackend) AudioTranscriptionStream(ctx context.Context, r *pb.TranscriptRequest, cb func(*pb.TranscriptStreamResponse), _ ...ggrpc.CallOption) error {
	result, err := b.AudioTranscription(ctx, r)
	cb(&pb.TranscriptStreamResponse{Delta: "hello "})
	if err != nil {
		return err
	}
	cb(&pb.TranscriptStreamResponse{FinalResult: result})
	return nil
}

var _ = Describe("transcription observability", func() {
	var app *application.Application
	var handler http.Handler
	var fixture *observedTranscriptionBackend
	var requestedModel string
	BeforeEach(func() {
		requestedModel = "speech-test"
		root := GinkgoT().TempDir()
		var err error
		app, err = application.New(config.EnableTracing, config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithDisableCSRF(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		cfg := config.ModelConfig{Name: "speech-test", Backend: "whisper"}
		cfg.SetDefaults()
		cfg.Model = "speech.bin"
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg, {Name: "speech-alias", Alias: "speech-test"}})
		fixture = &observedTranscriptionBackend{}
		app.ModelLoader().SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://speech", fixture), nil
		})
		handler, err = corehttp.API(app)
		Expect(err).NotTo(HaveOccurred())
		middleware.ClearTraces()
	})
	request := func(route, format string, stream bool) *http.Request {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		Expect(form.WriteField("model", requestedModel)).To(Succeed())
		Expect(form.WriteField("response_format", format)).To(Succeed())
		if stream {
			Expect(form.WriteField("stream", "true")).To(Succeed())
		}
		f, err := form.CreateFormFile("file", "sample.wav")
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write([]byte("private audio bytes"))
		Expect(err).NotTo(HaveOccurred())
		Expect(form.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, route, &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		return req
	}
	DescribeTable("records successful requests with zero tokens and traces multipart uploads", func(route, format string, stream, fail bool) {
		if fail {
			fixture.err = errors.New("transcription failed")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request(route, format, stream))
		expectedStatus := http.StatusOK
		if fail && !stream {
			expectedStatus = http.StatusInternalServerError
		}
		Expect(rec.Code).To(Equal(expectedStatus), rec.Body.String())
		Expect(fixture.audio).To(Equal([]byte("private audio bytes")))
		if stream {
			Expect(rec.Body.String()).To(ContainSubstring("data: [DONE]"))
			if fail {
				Expect(rec.Body.String()).To(ContainSubstring(`"type":"error"`))
			} else {
				Expect(rec.Body.String()).To(ContainSubstring(`"type":"transcript.text.done"`))
			}
		} else if !fail {
			Expect(rec.Body.String()).To(ContainSubstring("hello world"))
		}
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{UserID: app.FallbackUser().ID, Period: "day"})
		Expect(err).NotTo(HaveOccurred())
		if fail {
			Expect(buckets).To(BeEmpty())
		} else {
			Expect(buckets).To(HaveLen(1))
			Expect(buckets[0].Model).To(Equal("speech-test"))
			Expect(buckets[0].RequestCount).To(Equal(int64(1)))
			Expect(buckets[0].TotalTokens).To(BeZero())
			Expect(buckets[0].PromptTokens).To(BeZero())
			Expect(buckets[0].CompletionTokens).To(BeZero())
		}
		Eventually(middleware.GetTraces).Should(ConsistOf(HaveField("Response.Status", expectedStatus)))
		exchange := middleware.GetTraces()[0]
		Expect(exchange.Request.Path).To(Equal(route))
		Expect(exchange.Duration).To(BeNumerically(">", 0))
		Expect(string(*exchange.Request.Body)).NotTo(ContainSubstring("private audio bytes"))
		Expect(string(*exchange.Request.Body)).To(ContainSubstring(`"model":"speech-test"`))
	},
		Entry("JSON", "/v1/audio/transcriptions", "json", false, false),
		Entry("legacy text", "/audio/transcriptions", "txt", false, false),
		Entry("verbose JSON", "/v1/audio/transcriptions", "verbose_json", false, false),
		Entry("SRT", "/v1/audio/transcriptions", "srt", false, false),
		Entry("VTT", "/v1/audio/transcriptions", "vtt", false, false),
		Entry("LRC", "/v1/audio/transcriptions", "lrc", false, false),
		Entry("SSE", "/v1/audio/transcriptions", "", true, false),
		Entry("backend error", "/v1/audio/transcriptions", "json", false, true),
		Entry("SSE error", "/audio/transcriptions", "", true, true),
	)
	DescribeTable("attributes alias usage to the requested model", func(stream bool) {
		requestedModel = "speech-alias"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request("/v1/audio/transcriptions", "json", stream))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("hello world"))
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{UserID: app.FallbackUser().ID, Period: "day"})
		Expect(err).NotTo(HaveOccurred())
		Expect(buckets).To(HaveLen(1))
		Expect(buckets[0].Model).To(Equal("speech-alias"))
		Expect(buckets[0].RequestCount).To(Equal(int64(1)))
		Expect(buckets[0].TotalTokens).To(BeZero())
	}, Entry("JSON", false), Entry("SSE", true))
	DescribeTable("does not count responses the client cannot receive", func(stream bool) {
		rec := &failedTranscriptionWriter{httptest.NewRecorder()}
		handler.ServeHTTP(rec, request("/v1/audio/transcriptions", "json", stream))
		Expect(fixture.audio).To(Equal([]byte("private audio bytes")))
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{UserID: app.FallbackUser().ID, Period: "day"})
		Expect(err).NotTo(HaveOccurred())
		Expect(buckets).To(BeEmpty())
		Eventually(middleware.GetTraces).Should(ConsistOf(HaveField("Error", ContainSubstring("client disconnected"))))
	}, Entry("JSON", false), Entry("SSE", true))
	It("traces invalid formats without recording successful usage", func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request("/audio/transcriptions", "invalid", false))
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(fixture.audio).To(BeNil())
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{UserID: app.FallbackUser().ID, Period: "day"})
		Expect(err).NotTo(HaveOccurred())
		Expect(buckets).To(BeEmpty())
		Eventually(middleware.GetTraces).Should(ConsistOf(HaveField("Response.Status", http.StatusBadRequest)))
	})
})

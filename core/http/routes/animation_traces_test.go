// SPDX-License-Identifier: MIT
package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	corehttp "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/trace"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

const animationTraceMetadata = `{"usage":{"input_units":6,"output_units":60,"accounting_rule":"frame_steps_v1","details":{"output_frames":60,"sampling_steps":1}}}`

type tracedAnimationBackend struct {
	grpcpkg.Backend
	err     error
	started chan struct{}
	release chan struct{}
}

func (*tracedAnimationBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*tracedAnimationBackend) IsBusy() bool                              { return false }
func (*tracedAnimationBackend) Free(context.Context) error                { return nil }
func (b *tracedAnimationBackend) Animate3D(_ context.Context, r *pb.Animate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	if b.started != nil {
		close(b.started)
		<-b.release
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := os.WriteFile(r.Dst, []byte("glTF fixture"), 0600); err != nil {
		return nil, err
	}
	return &pb.Result{Success: true, Metadata: []byte(animationTraceMetadata)}, nil
}

var _ = Describe("animation request traces", func() {
	var app *application.Application
	var handler http.Handler
	var fixture *tracedAnimationBackend
	var loadErr error
	before := func() {
		root := GinkgoT().TempDir()
		var err error
		app, err = application.New(config.EnableTracing, config.WithDataPath(root), config.WithGeneratedContentDir(filepath.Join(root, "generated")), config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithDisableCSRF(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		cfg := config.ModelConfig{Name: "motion", Backend: "kimodocpp"}
		cfg.SetDefaults()
		cfg.Model = "motion.gguf"
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg})
		fixture = &tracedAnimationBackend{}
		loadErr = nil
		app.ModelLoader().SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			if loadErr != nil {
				return nil, loadErr
			}
			return model.NewModelWithClient(id, "test://animation", fixture), nil
		})
		e, err := corehttp.API(app)
		Expect(err).NotTo(HaveOccurred())
		handler = e
		middleware.ClearTraces()
		trace.InitBackendTracingIfEnabled(app.ApplicationConfig().TracingMaxItems, app.ApplicationConfig().TracingMaxBodyBytes)
		trace.ClearBackendTraces()
	}
	BeforeEach(before)
	request := func(frames string) *http.Request {
		body := `{"model":"motion","inputs":{"prompt":{"type":"text","data":"Walk forward"}},"params":{"frames":"` + frames + `","steps":"1","seed":"42"}}`
		req := httptest.NewRequest(http.MethodPost, "/3d/animate", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-trace-secret")
		return req
	}
	animations := func() []trace.BackendTrace {
		var found []trace.BackendTrace
		for _, t := range trace.GetBackendTraces() {
			if t.Type == trace.BackendTrace3DAnimation {
				found = append(found, t)
			}
		}
		return found
	}
	DescribeTable("captures the request and its outcome", func(mode string, code int) {
		frames := "60"
		switch mode {
		case "invalid":
			frames = "1"
		case "inference failure":
			fixture.err = errors.New("animation inference failed")
		case "load failure":
			loadErr = errors.New("animation load failed")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request(frames))
		Expect(w.Code).To(Equal(code))
		Eventually(middleware.GetTraces).Should(ConsistOf(HaveField("Response.Status", code)))
		api := middleware.GetTraces()[0]
		Expect(api.Request.Path).To(Equal("/3d/animate"))
		Expect(api.Response.Status).To(Equal(code))
		Expect(string(*api.Request.Body)).To(ContainSubstring("Walk forward"))
		Expect(api.Request.Headers.Get("Authorization")).NotTo(ContainSubstring("test-trace-secret"))
		if mode == "invalid" {
			Expect(animations()).To(BeEmpty())
			Expect(api.Error).NotTo(BeEmpty())
			return
		}
		Eventually(animations).Should(HaveLen(1))
		bt := animations()[0]
		Expect(bt.ModelName).To(Equal("motion"))
		Expect(bt.Backend).To(Equal("kimodocpp"))
		Expect(bt.Summary).To(ContainSubstring("Walk forward"))
		Expect(bt.Duration).To(BeNumerically(">", 0))
		Expect(bt.Data["inputs"]).To(Equal(map[string]any{"prompt": map[string]any{"type": "text", "text": "Walk forward"}}))
		Expect(bt.Data["params"]).To(Equal(map[string]any{"frames": "60", "steps": "1", "seed": "42"}))
		Expect(bt.Data).To(HaveKey("load_ms"))
		if mode != "success" {
			Expect(bt.Status).To(Equal(trace.BackendTraceFailed))
			Expect(bt.Error).To(ContainSubstring("failed"))
			if mode == "load failure" {
				Expect(bt.Data["stage"]).To(Equal("loading_model"))
			} else {
				Expect(bt.Data["stage"]).To(Equal("inference"))
			}
			return
		}
		Expect(bt.Status).To(Equal(trace.BackendTraceCompleted))
		Expect(bt.Data["stage"]).To(Equal("completed"))
		Expect(bt.Data).To(HaveKey("queue_ms"))
		Expect(bt.Data).To(HaveKey("inference_ms"))
		Expect(bt.Data["output_bytes"]).To(Equal(int64(len("glTF fixture"))))
		encoded, err := json.Marshal(bt.Data["metadata"])
		Expect(err).NotTo(HaveOccurred())
		Expect(encoded).To(MatchJSON(animationTraceMetadata))
		Expect(string(*api.Response.Body)).To(ContainSubstring(`"output_units":60`))
	}, Entry("success", "success", http.StatusOK), Entry("validation failure", "invalid", http.StatusBadRequest), Entry("inference failure", "inference failure", http.StatusInternalServerError), Entry("load failure", "load failure", http.StatusInternalServerError))
	It("shows running requests before inference finishes", func() {
		fixture.started = make(chan struct{})
		fixture.release = make(chan struct{})
		DeferCleanup(func() {
			select {
			case <-fixture.release:
			default:
				close(fixture.release)
			}
		})
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			handler.ServeHTTP(httptest.NewRecorder(), request("60"))
		}()
		Eventually(fixture.started).Should(BeClosed())
		Eventually(animations).Should(HaveLen(1))
		running := animations()[0]
		Expect(running.Status).To(Equal(trace.BackendTraceRunning))
		Expect(running.Summary).To(ContainSubstring("Walk forward"))
		Expect(middleware.GetTraces()).To(HaveLen(1))
		Expect(middleware.GetTraces()[0].Response.Status).To(BeZero())
		close(fixture.release)
		Eventually(done).Should(BeClosed())
		Eventually(func() trace.BackendTraceStatus {
			ts := animations()
			if len(ts) != 1 {
				return ""
			}
			return ts[0].Status
		}).Should(Equal(trace.BackendTraceCompleted))
		Expect(animations()[0].ID).To(Equal(running.ID))
	})
	It("records nothing when tracing is disabled", func() {
		app.ApplicationConfig().EnableTracing = false
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request("60"))
		Expect(w.Code).To(Equal(http.StatusOK))
		Consistently(middleware.GetTraces, 100*time.Millisecond, 10*time.Millisecond).Should(BeEmpty())
		Expect(animations()).To(BeEmpty())
	})
})

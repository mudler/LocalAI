// SPDX-License-Identifier: MIT
package backend_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type nativeDecisionBackend struct {
	grpcPkg.Backend
	request  *pb.ScoreRequest
	fail     bool
	failWith error
}

func (b *nativeDecisionBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *nativeDecisionBackend) IsBusy() bool                              { return false }

func (b *nativeDecisionBackend) Score(_ context.Context, r *pb.ScoreRequest, _ ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	b.request = r
	if b.failWith != nil {
		return nil, b.failWith
	}
	if b.fail {
		return nil, fmt.Errorf("backend echoed secret-text")
	}
	return &pb.ScoreResponse{ResponseJson: `{"answers":{"q":{"type":"noul","noul":0.8}}}`}, nil
}

var _ = Describe("native decision transport", func() {
	It("uses ModelSystemOne Score directly and never records prompt or echoed error", func() {
		rec := &nativeDecisionBackend{}
		state := &system.SystemState{}
		loader := model.NewModelLoader(state)
		loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://native", rec), nil
		})
		cfg := identityModelCfg()
		cfg.Backend = "vllm-cpp"
		cfg.Model = "native-test.gguf"
		flags := config.FLAG_DECISIONS
		cfg.KnownUsecases = &flags
		app := config.NewApplicationConfig(config.WithSystemState(state))
		app.EnableTracing = true
		trace.ClearBackendTraces()
		defer trace.ClearBackendTraces()
		runner := backend.NewDecisionRunner(cfg.Name, func(string) *config.ModelConfig { return &cfg }, loader, app)
		req := &schema.SystemOneRequest{State: json.RawMessage(`"secret-text"`), Questions: map[string]schema.SystemOneQuestion{"q": {Type: "noul"}}}
		response, err := runner.Decide(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(*response.Answers["q"].Noul).To(Equal(.8))
		Expect(rec.request.QuestionType).To(Equal("systemone"))
		Expect(rec.request.ModelIdentity).To(Equal(cfg.Model))
		Expect(rec.request.Prompt).To(ContainSubstring("secret-text"))
		rec.fail = true
		_, err = runner.Decide(context.Background(), req)
		Expect(err).To(HaveOccurred())
		Eventually(func() int { return len(trace.GetBackendTraces()) }).Should(Equal(2))
		traces := trace.GetBackendTraces()
		Expect(traces).NotTo(BeEmpty())
		for _, tr := range traces {
			Expect(tr.Summary).NotTo(ContainSubstring("secret-text"))
			Expect(tr.Error).NotTo(ContainSubstring("secret-text"))
		}
	})

	It("replaces the old-backend scoring error with a plain upgrade message", func() {
		for _, msg := range []string{
			"Score was not enabled when the model was loaded; add score to known_usecases",
			"candidates must be non-empty",
		} {
			rec := &nativeDecisionBackend{failWith: status.Error(codes.FailedPrecondition, msg)}
			state := &system.SystemState{}
			loader := model.NewModelLoader(state)
			loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
				return model.NewModelWithClient(id, "test://stale", rec), nil
			})
			cfg := identityModelCfg()
			cfg.Backend = "llama-cpp"
			cfg.Model = "native-test.gguf"
			flags := config.FLAG_DECISIONS
			cfg.KnownUsecases = &flags
			app := config.NewApplicationConfig(config.WithSystemState(state))
			marked := make(chan struct{}, 1)
			gallery.SetStaleBackendNotifier(func() { marked <- struct{}{} })
			DeferCleanup(func() { gallery.SetStaleBackendNotifier(nil) })
			runner := backend.NewDecisionRunner(cfg.Name, func(string) *config.ModelConfig { return &cfg }, loader, app)
			req := &schema.SystemOneRequest{State: json.RawMessage(`"x"`), Questions: map[string]schema.SystemOneQuestion{"q": {Type: "noul"}}}
			_, err := runner.Decide(context.Background(), req)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(err.Error()).To(ContainSubstring("older than this server"))
			Expect(err.Error()).To(ContainSubstring("llama-cpp"))
			Expect(err.Error()).NotTo(ContainSubstring("known_usecases"))
			Eventually(marked).Should(Receive())
		}
	})

	It("leaves other backend errors unchanged and does not mark the backend", func() {
		rec := &nativeDecisionBackend{failWith: status.Error(codes.InvalidArgument, "bad question")}
		state := &system.SystemState{}
		loader := model.NewModelLoader(state)
		loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://other", rec), nil
		})
		cfg := identityModelCfg()
		cfg.Backend = "vllm-cpp"
		cfg.Model = "native-test.gguf"
		flags := config.FLAG_DECISIONS
		cfg.KnownUsecases = &flags
		app := config.NewApplicationConfig(config.WithSystemState(state))
		notified := false
		gallery.SetStaleBackendNotifier(func() { notified = true })
		DeferCleanup(func() { gallery.SetStaleBackendNotifier(nil) })
		runner := backend.NewDecisionRunner(cfg.Name, func(string) *config.ModelConfig { return &cfg }, loader, app)
		req := &schema.SystemOneRequest{State: json.RawMessage(`"x"`), Questions: map[string]schema.SystemOneQuestion{"q": {Type: "noul"}}}
		_, err := runner.Decide(context.Background(), req)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(notified).To(BeFalse())
	})
})

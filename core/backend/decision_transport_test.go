// SPDX-License-Identifier: MIT
package backend_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type nativeDecisionBackend struct {
	grpcPkg.Backend
	request *pb.ScoreRequest
	fail    bool
}

func (b *nativeDecisionBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *nativeDecisionBackend) IsBusy() bool                              { return false }

func (b *nativeDecisionBackend) Score(_ context.Context, r *pb.ScoreRequest, _ ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	b.request = r
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
})

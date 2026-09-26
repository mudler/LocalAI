package failover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type fakeUpstream struct {
	mu     sync.Mutex
	srv    *httptest.Server
	models []string
	status int
	paths  []string
	auth   string
}

func newFakeUpstream() *fakeUpstream {
	u := &fakeUpstream{status: http.StatusOK}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.paths = append(u.paths, r.Method+" "+r.URL.Path)
		u.auth = r.Header.Get("Authorization")
		status, models := u.status, u.models
		u.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if r.URL.Path == "/v1/models" {
			var data []map[string]string
			for _, m := range models {
				data = append(data, map[string]string{"id": m})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	return u
}

type fakeBackend struct {
	grpc.Backend
	healthy    bool
	predictErr error
	predicted  bool
}

func (b *fakeBackend) HealthCheck(context.Context) (bool, error) { return b.healthy, nil }
func (b *fakeBackend) Predict(context.Context, *pb.PredictOptions, ...ggrpc.CallOption) (*pb.Reply, error) {
	b.predicted = true
	return &pb.Reply{}, b.predictErr
}

var _ = Describe("DefaultProber", func() {
	var (
		up  *fakeUpstream
		p   *DefaultProber
		ctx = context.Background()
	)

	BeforeEach(func() {
		up = newFakeUpstream()
		DeferCleanup(up.srv.Close)
		p = NewProber(nil)
	})

	proxied := func(name, upstreamModel string, usecases ...string) config.ModelConfig {
		c := config.ModelConfig{Name: name, Backend: "cloud-proxy", KnownUsecaseStrings: usecases}
		c.KnownUsecases = config.GetUsecasesFromYAML(usecases)
		c.Proxy.UpstreamURL = up.srv.URL + "/v1/chat/completions"
		c.Proxy.UpstreamModel = upstreamModel
		return c
	}

	DescribeTable("UpstreamBase",
		func(in, want string) {
			got, err := UpstreamBase(in)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("full endpoint", "https://h:8080/v1/chat/completions", "https://h:8080"),
		Entry("path prefix", "https://h/api/v1/chat/completions", "https://h/api"),
		Entry("bare host", "https://h", "https://h"),
		Entry("bare host slash", "https://h/", "https://h"),
	)

	It("passes liveness when the upstream lists the model", func() {
		up.models = []string{"big-llm"}
		Expect(p.Liveness(ctx, proxied("argus-llm", "big-llm"), KindRemote, false)).To(Succeed())
		Expect(up.paths).To(ContainElement("GET /v1/models"))
	})

	It("uses the target name when upstream_model is empty", func() {
		up.models = []string{"argus-llm"}
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(Succeed())
	})

	It("fails liveness when the model is not listed or the upstream errors", func() {
		up.models = []string{"other"}
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(MatchError(ContainSubstring("does not list")))
		up.status = http.StatusServiceUnavailable
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(MatchError(ContainSubstring("503")))
	})

	It("sends the API key as a bearer token", func() {
		GinkgoT().Setenv("FAILOVER_PROBE_KEY", "sekret")
		up.models = []string{"argus-llm"}
		c := proxied("argus-llm", "")
		c.Proxy.APIKeyEnv = "FAILOVER_PROBE_KEY"
		Expect(p.Liveness(ctx, c, KindRemote, false)).To(Succeed())
		Expect(up.auth).To(Equal("Bearer sekret"))
	})

	DescribeTable("remote inference hits the usecase endpoint",
		func(usecase, path string) {
			Expect(p.Inference(ctx, proxied("m", "", usecase), KindRemote, false)).To(Succeed())
			Expect(up.paths).To(ContainElement("POST " + path))
		},
		Entry("chat", "chat", "/v1/chat/completions"),
		Entry("embeddings", "embeddings", "/v1/embeddings"),
		Entry("transcription", "transcript", "/v1/audio/transcriptions"),
		Entry("tts", "tts", "/v1/audio/speech"),
	)

	It("uses HealthCheck for warm local liveness and Predict for local chat inference", func() {
		b := &fakeBackend{healthy: true}
		p = NewProber(func(context.Context, config.ModelConfig) (grpc.Backend, error) { return b, nil })
		c := config.ModelConfig{Name: "gemma", Backend: "llama-cpp", KnownUsecaseStrings: []string{"chat"}}
		c.KnownUsecases = config.GetUsecasesFromYAML(c.KnownUsecaseStrings)
		Expect(p.Liveness(ctx, c, KindLocal, true)).To(Succeed())
		b.healthy = false
		Expect(p.Liveness(ctx, c, KindLocal, true)).To(HaveOccurred())
		Expect(p.Inference(ctx, c, KindLocal, true)).To(Succeed())
		Expect(b.predicted).To(BeTrue())
		b.predictErr = errors.New("boom")
		Expect(p.Inference(ctx, c, KindLocal, true)).To(HaveOccurred())
	})

	It("passes cold local liveness without a model file and without loading", func() {
		p = NewProber(func(context.Context, config.ModelConfig) (grpc.Backend, error) {
			Fail("cold liveness must not load the model")
			return nil, nil
		})
		// None of these files exist: a missing file says nothing about whether
		// the target can serve (download on first use, dotted names, backends
		// that need no file). Only a real request may trip a cold target.
		for _, model := range []string{"weights.gguf", "Phi-3.5-mini", "org/some-hf-repo", ""} {
			c := config.ModelConfig{Name: "cold", Backend: "llama-cpp"}
			c.Model = model
			Expect(p.Liveness(ctx, c, KindLocal, false)).To(Succeed(), model)
		}
	})
})

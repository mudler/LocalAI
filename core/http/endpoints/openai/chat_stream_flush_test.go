package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// flushCounter counts the flushes of a response.
type flushCounter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

var _ = Describe("chat stream flushes", func() {
	var origInference modelInferenceFunc
	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	const tokens = 200

	// stream runs a streaming chat against a backend that sends tokens words,
	// waiting gap between them, and returns the body and the flush count.
	stream := func(gap time.Duration) (string, int) {
		backend.ModelInferenceFunc = func(
			ctx context.Context, s string, messages schema.Messages,
			images, videos, audios []string,
			loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
			o *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool,
			tools, toolChoice string,
			logprobs, topLogprobs *int,
			logitBias map[string]float64,
			metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			return func() (backend.LLMResponse, error) {
				var out strings.Builder
				for i := range tokens {
					if gap > 0 {
						time.Sleep(gap)
					}
					tokenCallback(" word", backend.TokenUsage{Completion: i + 1})
					out.WriteString(" word")
				}
				return backend.LLMResponse{Response: out.String(), Usage: backend.TokenUsage{Completion: tokens}}, nil
			}, nil
		}

		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "m.yaml"), []byte("name: m\nbackend: llama-cpp\nparameters:\n  model: m.gguf\n"), 0644)).To(Succeed())
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		cfg, ok := cl.GetModelConfig("m")
		Expect(ok).To(BeTrue())

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}},
			Messages:          userMessages("hello"),
			Stream:            true,
			Context:           ctx,
			Cancel:            cancel,
		}
		e := echo.New()
		rec := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
		Expect(ChatEndpoint(nil, nil, templates.NewEvaluator(dir), config.NewApplicationConfig(), nil, nil, nil, nil)(c)).To(Succeed())
		return rec.Body.String(), rec.flushes
	}

	normalize := func(body string) string {
		var out []string
		for _, line := range strings.Split(body, "\n") {
			if i := strings.Index(line, `"id":"`); i >= 0 {
				j := strings.Index(line[i+6:], `"`)
				line = line[:i+6] + line[i+6+j:]
			}
			if i := strings.Index(line, `"created":`); i >= 0 {
				j := strings.IndexByte(line[i:], ',')
				line = line[:i] + line[i+j:]
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}

	It("flushes every chunk when the backend is slower than the client", func() {
		body, flushes := stream(time.Millisecond)
		Expect(strings.Count(body, `"content":" word"`)).To(Equal(tokens))
		Expect(flushes).To(BeNumerically(">=", tokens))
	})

	It("writes the chunks that are ready with one flush, and the same bytes", func() {
		paced, _ := stream(time.Millisecond)
		burst, flushes := stream(0)
		Expect(normalize(burst)).To(Equal(normalize(paced)))
		Expect(flushes).To(BeNumerically("<", tokens))
		GinkgoWriter.Printf("burst of %d tokens: %d flushes\n", tokens, flushes)
	})
})

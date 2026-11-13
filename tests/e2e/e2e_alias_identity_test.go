package e2e_test

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A target and its aliases share one backend process, and that process rejects
// a request whose model identity differs from the one it loaded with
// (pkg/grpc/server.go checkModelIdentity). For a target without
// parameters.model the identity used to be the requested name, so the second
// name to reach the process failed with "model identity mismatch". The cloud-proxy
// backend is a real gRPC backend process, which is why these specs use it.
var _ = Describe("Aliases of a model without parameters.model", func() {
	BeforeEach(func() {
		if cloudProxyPath == "" {
			Skip("cloud-proxy backend binary not built (make build-cloud-proxy-backend)")
		}
		cpOpenAIUpstream.SetScript(func([]byte) (int, string, string) {
			return 200, `{"id":"resp-alias","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, "application/json"
		})
	})
	AfterEach(func() {
		cpOpenAIUpstream.SetScript(defaultOpenAIScript)
	})

	chat := func(model string) {
		cp := openai.NewClient(option.WithBaseURL(apiURL), option.WithMaxRetries(0))
		resp, err := cp.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
			Model:    model,
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hello")},
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), model)
		ExpectWithOffset(1, resp.Choices).NotTo(BeEmpty(), model)
	}

	It("serves an alias after its target was loaded under the target's name", func() {
		const target = "cp-noparam-target-first"
		chat(target)
		chat(target + "-a")
		chat(target + "-b")
		// Without upstream_model the cloud-proxy sends the name it was
		// loaded with, so the upstream model no longer depends on which
		// name happened to load the process first.
		Expect(cpOpenAIUpstream.DecodedBody()["model"]).To(Equal(target))
	})

	It("serves the target and a second alias after an alias loaded the process", func() {
		const target = "cp-noparam-alias-first"
		chat(target + "-a")
		Expect(cpOpenAIUpstream.DecodedBody()["model"]).To(Equal(target))
		chat(target)
		chat(target + "-b")
		Expect(cpOpenAIUpstream.DecodedBody()["model"]).To(Equal(target))
	})
})

package e2e_test

import (
	"context"
	"encoding/json"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
)

// Regression test for https://github.com/mudler/LocalAI/issues/9675.
// Relative draft_model paths used to be sent verbatim to the backend, which
// then opened them from its CWD and failed with "No such file or directory".
// The fix in core/backend/options.go resolves draft_model against the
// configured models directory, mirroring the existing handling for the main
// model file and mmproj.
//
// The mock backend stashes the LoadModel ModelOptions and echoes them back
// in response to the ECHO_LOAD_PARAMS prompt, letting the test inspect the
// exact paths that crossed the gRPC boundary.
var _ = Describe("Backend Path Resolution", Label("MockBackend", "PathResolution"), func() {
	It("resolves relative draft_model, mmproj, and main model paths against the models dir", func() {
		resp, err := client.Chat.Completions.New(
			context.TODO(),
			openai.ChatCompletionNewParams{
				Model: "mock-model-path-resolution",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("ECHO_LOAD_PARAMS"),
				},
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Choices).To(HaveLen(1))

		var snapshot map[string]string
		Expect(json.Unmarshal([]byte(resp.Choices[0].Message.Content), &snapshot)).To(Succeed(),
			"expected ECHO_LOAD_PARAMS reply to be JSON, got: %q", resp.Choices[0].Message.Content)

		// The main model file is resolved by pkg/model/loader.go and has
		// always worked; assert it as a baseline so the test fails loudly
		// if that ever regresses too.
		Expect(snapshot["model_file"]).To(Equal(filepath.Join(modelsPath, "subdir", "mock-main.bin")),
			"main model file should be resolved against the models directory")

		// mmproj has had explicit join logic for a while — guard it so the
		// next refactor does not silently drop it.
		Expect(snapshot["mmproj"]).To(Equal(filepath.Join(modelsPath, "subdir", "mock-mmproj.bin")),
			"mmproj should be resolved against the models directory")

		// The actual fix — without it, draft_model would be sent verbatim
		// ("subdir/mock-draft.bin") and llama.cpp would fail to open it.
		Expect(snapshot["draft_model"]).To(Equal(filepath.Join(modelsPath, "subdir", "mock-draft.bin")),
			"draft_model should be resolved against the models directory (regression guard for #9675)")
	})

	// A user-supplied YAML must not be able to escape the models directory
	// by setting draft_model to an absolute path like "/etc/passwd".
	// filepath.Join strips the leading slash, so the resulting path stays
	// rooted at modelsPath even for adversarial input.
	It("clamps absolute draft_model paths to the models directory", func() {
		resp, err := client.Chat.Completions.New(
			context.TODO(),
			openai.ChatCompletionNewParams{
				Model: "mock-model-path-escape",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("ECHO_LOAD_PARAMS"),
				},
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Choices).To(HaveLen(1))

		var snapshot map[string]string
		Expect(json.Unmarshal([]byte(resp.Choices[0].Message.Content), &snapshot)).To(Succeed())

		Expect(snapshot["draft_model"]).To(Equal(filepath.Join(modelsPath, "etc", "passwd")),
			"absolute draft_model paths must be clamped under the models directory")
		Expect(snapshot["draft_model"]).ToNot(Equal("/etc/passwd"),
			"a YAML config must not be able to point the backend at /etc/passwd")
	})
})

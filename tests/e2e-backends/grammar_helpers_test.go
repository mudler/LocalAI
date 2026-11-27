package e2ebackends_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// llguidanceGrammarKey is the PredictOptions metadata key of the llguidance
// form of a grammar (backend/cpp/llama-cpp/grammar_engine.h).
const llguidanceGrammarKey = "llguidance_grammar"

// grammarRun is one constrained generation.
type grammarRun struct {
	Output     string
	Tokens     int32
	Wall       time.Duration
	MsPerToken float64 // from the backend's own generation timing
}

// toolCallGrammars returns the GBNF grammar of the tool calls of funcs and its
// llguidance form, built as LocalAI builds them for a chat request.
func toolCallGrammars(funcs functions.Functions) (gbnf, llg string) {
	GinkgoHelper()
	js := funcs.ToJSONStructure("", "")
	gbnf, err := js.Grammar()
	Expect(err).NotTo(HaveOccurred())
	llg, err = js.LLGuidanceGrammar()
	Expect(err).NotTo(HaveOccurred())
	return gbnf, llg
}

// predictWithGrammar sends a chat request whose output is constrained by gbnf
// and, when llg is set, its llguidance form. Temperature 0 keeps the output of
// the two engines comparable.
func predictWithGrammar(client pb.BackendClient, user, gbnf, llg string, tokens int32) grammarRun {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := &pb.PredictOptions{
		Messages: []*pb.Message{
			{Role: "system", Content: "You are a helpful assistant. Answer with one function call in JSON."},
			{Role: "user", Content: user},
		},
		UseTokenizerTemplate: true,
		Grammar:              gbnf,
		Tokens:               tokens,
		Temperature:          0,
		TopK:                 1,
		Seed:                 1,
	}
	if llg != "" {
		opts.Metadata = map[string]string{llguidanceGrammarKey: llg}
	}
	start := time.Now()
	res, err := client.Predict(ctx, opts)
	Expect(err).NotTo(HaveOccurred())
	run := grammarRun{Output: string(res.GetMessage()), Tokens: res.GetTokens(), Wall: time.Since(start)}
	if run.Tokens > 0 {
		run.MsPerToken = res.GetTimingTokenGeneration() / float64(run.Tokens)
	}
	return run
}

// parseToolCall parses a tool call made under a tool-call grammar.
func parseToolCall(out string) (name string, args map[string]any) {
	GinkgoHelper()
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	Expect(json.Unmarshal([]byte(strings.TrimSpace(out)), &call)).To(Succeed(), "not a JSON tool call: %q", out)
	return call.Name, call.Arguments
}

func weatherFunctions() functions.Functions {
	return functions.Functions{{
		Name:        "get_weather",
		Description: "Get the current weather for a city",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "The city name"},
				"unit": map[string]any{"type": "string", "enum": []any{"celsius", "fahrenheit"}},
			},
		},
	}}
}

// stressFunctions returns n tools with a dozen arguments each: nested
// objects, arrays, enums and numbers, the shape of a large agent tool set.
// The GBNF grammar of this set has thousands of rules.
func stressFunctions(n int) functions.Functions {
	var fs functions.Functions
	for i := range n {
		fs = append(fs, functions.Function{
			Name:        fmt.Sprintf("tool_%02d", i),
			Description: fmt.Sprintf("Operation number %d of the service", i),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string"},
					"title":    map[string]any{"type": "string"},
					"count":    map[string]any{"type": "integer"},
					"ratio":    map[string]any{"type": "number"},
					"enabled":  map[string]any{"type": "boolean"},
					"priority": map[string]any{"type": "string", "enum": []any{"low", "normal", "high", "urgent"}},
					"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"owner": map[string]any{"type": "object", "properties": map[string]any{
						"name":  map[string]any{"type": "string"},
						"email": map[string]any{"type": "string"},
						"team":  map[string]any{"type": "string", "enum": []any{"core", "infra", "ui", "docs"}},
					}},
					"window": map[string]any{"type": "object", "properties": map[string]any{
						"start": map[string]any{"type": "string"},
						"end":   map[string]any{"type": "string"},
					}},
					"limits": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
						"key":   map[string]any{"type": "string"},
						"value": map[string]any{"type": "integer"},
					}}},
					"note": map[string]any{"type": []any{"string", "null"}},
					"mode": map[string]any{"type": "string", "enum": []any{"dry_run", "apply"}},
				},
			},
		})
	}
	return fs
}

package main

// E2E over a real model + the built libvllm. Gated on VLLM_CPP_MODEL (a .gguf
// file or a safetensors model dir): without it the suite skips, so CI runs
// only the unit specs. test.sh auto-downloads a small GGUF when the gate is
// unset and the download is allowed.

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vllm-cpp e2e", Label("e2e"), Ordered, func() {
	var backend *VllmCpp

	BeforeAll(func() {
		modelPath := os.Getenv("VLLM_CPP_MODEL")
		if modelPath == "" {
			Skip("VLLM_CPP_MODEL not set; skipping e2e")
		}
		lib := os.Getenv("VLLM_CPP_LIBRARY")
		if lib == "" {
			if runtime.GOOS == "darwin" {
				lib = "./libvllm.dylib"
			} else {
				lib = "./libvllm.so"
			}
		}
		Expect(registerLib(lib)).To(Succeed())

		backend = &VllmCpp{}
		Expect(backend.Load(&pb.ModelOptions{
			ModelFile:   modelPath,
			ContextSize: 2048,
		})).To(Succeed())
	})

	AfterAll(func() {
		if backend != nil {
			Expect(backend.Free()).To(Succeed())
		}
	})

	It("refuses a foreign model artefact", func() {
		other := &VllmCpp{}
		Expect(other.Load(&pb.ModelOptions{ModelFile: "/nonexistent/foreign.bin"})).NotTo(Succeed())
	})

	It("completes a prompt (greedy)", func() {
		text, err := backend.Predict(&pb.PredictOptions{
			Prompt:      "The capital of France is",
			Tokens:      16,
			Temperature: 0,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(text).NotTo(BeEmpty())
	})

	It("is deterministic under greedy decoding", func() {
		opts := &pb.PredictOptions{Prompt: "1 2 3 4", Tokens: 8, Temperature: 0}
		a, err := backend.Predict(opts)
		Expect(err).NotTo(HaveOccurred())
		b, err := backend.Predict(opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(b))
	})

	It("streams deltas that concatenate to the blocking result", func() {
		opts := &pb.PredictOptions{Prompt: "Count: one two", Tokens: 12, Temperature: 0}
		blocking, err := backend.Predict(opts)
		Expect(err).NotTo(HaveOccurred())

		results := make(chan string)
		Expect(backend.PredictStream(opts, results)).To(Succeed())
		var sb strings.Builder
		for delta := range results {
			sb.WriteString(delta)
		}
		Expect(sb.String()).To(Equal(blocking))
	})

	It("honors stop words", func() {
		text, err := backend.Predict(&pb.PredictOptions{
			Prompt:      "a b c d e f",
			Tokens:      64,
			Temperature: 0,
			StopPrompts: []string{"g"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(text).NotTo(ContainSubstring("g h"))
	})

	It("constrains generation with a GBNF grammar (tool-call path)", func() {
		text, err := backend.Predict(&pb.PredictOptions{
			Prompt:      "Answer strictly yes or no: is water wet?",
			Tokens:      4,
			Temperature: 0,
			Grammar:     "root ::= \"yes\" | \"no\"",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Or(HavePrefix("yes"), HavePrefix("no")))
	})

	It("serves concurrent streams", func() {
		const n = 4
		type result struct {
			text string
			err  error
		}
		done := make(chan result, n)
		for i := 0; i < n; i++ {
			go func() {
				results := make(chan string)
				err := backend.PredictStream(&pb.PredictOptions{
					Prompt: "Hello", Tokens: 8, Temperature: 0,
				}, results)
				var sb strings.Builder
				for delta := range results {
					sb.WriteString(delta)
				}
				done <- result{sb.String(), err}
			}()
		}
		for i := 0; i < n; i++ {
			r := <-done
			Expect(r.err).NotTo(HaveOccurred())
			Expect(r.text).NotTo(BeEmpty())
		}
	})
})

var _ = Describe("vllm-cpp chat e2e", Label("e2e"), Ordered, func() {
	var backend *VllmCpp

	weatherTools := `[{"type":"function","function":{"name":"get_weather",` +
		`"description":"Get the current weather for a city.",` +
		`"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`

	BeforeAll(func() {
		modelPath := os.Getenv("VLLM_CPP_MODEL")
		if modelPath == "" {
			Skip("VLLM_CPP_MODEL not set; skipping chat e2e")
		}
		lib := os.Getenv("VLLM_CPP_LIBRARY")
		if lib == "" {
			if runtime.GOOS == "darwin" {
				lib = "./libvllm.dylib"
			} else {
				lib = "./libvllm.so"
			}
		}
		Expect(registerLib(lib)).To(Succeed())

		backend = &VllmCpp{}
		Expect(backend.Load(&pb.ModelOptions{
			ModelFile:   modelPath,
			ContextSize: 2048,
		})).To(Succeed())
	})

	AfterAll(func() {
		if backend != nil {
			Expect(backend.Free()).To(Succeed())
		}
	})

	chatOpts := func() *pb.PredictOptions {
		return &pb.PredictOptions{
			UseTokenizerTemplate: true,
			Messages: []*pb.Message{
				{Role: "user", Content: "Reply with one short sentence: what is the capital of France?"},
			},
			Tokens:      512,
			Temperature: 0,
		}
	}

	It("answers a plain chat turn through the engine-side template", func() {
		reply, err := backend.PredictRich(chatOpts())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(reply.Message)).NotTo(BeEmpty())
		Expect(string(reply.Message)).To(ContainSubstring("Paris"))
	})

	It("streams chat deltas that concatenate to the blocking answer", func() {
		blocking, err := backend.PredictRich(chatOpts())
		Expect(err).NotTo(HaveOccurred())

		results := make(chan *pb.Reply, 64)
		done := make(chan error, 1)
		go func() {
			done <- backend.PredictStreamRich(chatOpts(), results)
			close(results)
		}()
		var sb strings.Builder
		for r := range results {
			sb.WriteString(string(r.Message))
		}
		Expect(<-done).To(Succeed())
		Expect(sb.String()).To(Equal(string(blocking.Message)))
	})

	It("emits a parsed tool call when tool_choice requires it", func() {
		opts := chatOpts()
		opts.Messages = []*pb.Message{
			{Role: "user", Content: "What is the weather in Rome right now?"},
		}
		opts.Tools = weatherTools
		opts.ToolChoice = "required"
		opts.Tokens = 256

		reply, err := backend.PredictRich(opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.ChatDeltas).NotTo(BeEmpty())
		var name, args string
		for _, d := range reply.ChatDeltas {
			for _, tc := range d.ToolCalls {
				if tc.Name != "" {
					name = tc.Name
				}
				args += tc.Arguments
			}
		}
		Expect(name).To(Equal("get_weather"))
		var parsed map[string]any
		Expect(json.Unmarshal([]byte(args), &parsed)).To(Succeed(),
			"tool arguments must be valid JSON: %q", args)
		Expect(parsed).To(HaveKey("city"))
	})

	It("lets the engine decide on auto tool choice and streams tool deltas", func() {
		opts := chatOpts()
		opts.Messages = []*pb.Message{
			{Role: "user", Content: "Use the get_weather tool to check the weather in Rome."},
		}
		opts.Tools = weatherTools
		opts.Tokens = 512

		results := make(chan *pb.Reply, 128)
		done := make(chan error, 1)
		go func() {
			done <- backend.PredictStreamRich(opts, results)
			close(results)
		}()
		sawToolDelta := false
		for r := range results {
			for _, d := range r.ChatDeltas {
				if len(d.ToolCalls) > 0 {
					sawToolDelta = true
				}
			}
		}
		Expect(<-done).To(Succeed())
		// tool_choice auto is a LAZY constraint: the model may or may not call.
		// With an explicit instruction the gate model reliably does; treat a
		// no-call run as a soft signal rather than a hard failure only if the
		// engine produced SOME output.
		Expect(sawToolDelta).To(BeTrue(), "expected the engine to engage the tool")
	})
})

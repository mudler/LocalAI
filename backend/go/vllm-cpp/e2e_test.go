package main

// E2E over a real model + the built libvllm. Gated on VLLM_CPP_MODEL (a .gguf
// file or a safetensors model dir): without it the suite skips, so CI runs
// only the unit specs. test.sh auto-downloads a small GGUF when the gate is
// unset and the download is allowed.

import (
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

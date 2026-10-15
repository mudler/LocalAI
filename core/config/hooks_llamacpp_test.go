// SPDX-License-Identifier: MIT
package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AliasGuessing", func() {
	BeforeEach(func() { GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false") })

	DescribeTable("skips the parser for aliases while preserving context defaults",
		func(context *int, expected int) {
			cfg := &ModelConfig{Name: "production", Alias: "target", LLMConfig: LLMConfig{ContextSize: context}}
			calls := 0
			llamaCppDefaultsWithParser(cfg, GinkgoT().TempDir(), func(path string, opts ...gguf.GGUFReadOption) (*gguf.GGUFFile, error) {
				calls++
				return gguf.ParseGGUFFile(path, opts...)
			})
			Expect(calls).To(Equal(0), "an alias must not parse the models directory")
			Expect(cfg.ContextSize).To(HaveValue(Equal(expected)))
		},
		Entry("unset context", nil, DefaultContextSize),
		Entry("auto-max context", aliasGuessingInt(-1), DefaultContextSize),
		Entry("custom context", aliasGuessingInt(8192), 8192),
		Entry("zero context", aliasGuessingInt(0), 0),
	)

	DescribeTable("keeps file-backed guessing and parser options",
		func(backend string) {
			dir := GinkgoT().TempDir()
			cfg := &ModelConfig{Backend: backend, PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "weights.gguf"}}}
			calls := 0
			var gotPath string
			var gotOptions []gguf.GGUFReadOption
			llamaCppDefaultsWithParser(cfg, dir, func(path string, opts ...gguf.GGUFReadOption) (*gguf.GGUFFile, error) {
				calls++
				gotPath, gotOptions = path, opts
				return nil, errors.New("unreadable fixture")
			})
			Expect(calls).To(Equal(1))
			Expect(gotPath).To(Equal(filepath.Join(dir, "weights.gguf")))
			Expect(gotOptions).To(HaveLen(2))
			// The parser's option struct is private; inspect the applied public options.
			options := reflect.New(reflect.TypeOf(gotOptions[0]).In(0).Elem())
			for _, opt := range gotOptions {
				reflect.ValueOf(opt).Call([]reflect.Value{options})
			}
			Expect(options.Elem().FieldByName("MMap").Bool()).To(BeTrue())
			Expect(options.Elem().FieldByName("SkipLargeMetadata").Bool()).To(BeTrue())
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultContextSize)))
		},
		Entry("explicit llama-cpp", "llama-cpp"),
		Entry("auto-detected backend", ""),
	)

	It("keeps the global disable and empty model path behavior", func() {
		for _, disabled := range []string{"false", "true"} {
			GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", disabled)
			path := ""
			if disabled == "true" {
				path = GinkgoT().TempDir()
			}
			cfg := &ModelConfig{Backend: "llama-cpp"}
			calls := 0
			llamaCppDefaultsWithParser(cfg, path, func(string, ...gguf.GGUFReadOption) (*gguf.GGUFFile, error) {
				calls++
				return nil, errors.New("unexpected parse")
			})
			Expect(calls).To(BeZero())
			Expect(cfg.ContextSize).To(BeNil())
		}
	})

	It("retains generic and loader defaults through SetDefaults", func() {
		cfg := &ModelConfig{Name: "production", Alias: "target"}
		cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionThreads(7), LoadOptionContextSize(8192))
		Expect(cfg.ContextSize).To(HaveValue(Equal(8192)))
		Expect(cfg.Threads).To(HaveValue(Equal(7)))
		Expect(cfg.PromptCacheAll).To(HaveValue(BeTrue()))
		valid, err := cfg.Validate()
		Expect(err).NotTo(HaveOccurred())
		Expect(valid).To(BeTrue())
	})

	DescribeTable("does not accept malformed aliases after SetDefaults",
		func(cfg ModelConfig, message string) {
			cfg.SetDefaults(ModelPath(GinkgoT().TempDir()))
			valid, err := cfg.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("missing name", ModelConfig{Alias: "target"}, "requires a name"),
		Entry("self reference", ModelConfig{Name: "loop", Alias: "loop"}, "itself"),
		Entry("backend", ModelConfig{Name: "production", Alias: "target", Backend: "llama-cpp"}, "pure redirect"),
		Entry("model file", ModelConfig{Name: "production", Alias: "target", PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "weights.gguf"}}}, "pure redirect"),
	)

	It("resolves to the same effective target config without leaking alias defaults", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "target.yaml"), []byte("name: target\nbackend: llama-cpp\ncontext_size: 16384\nthreads: 3\nparameters:\n  model: weights.gguf\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "alias.yaml"), []byte("name: production\nalias: target\ncontext_size: 8192\nthreads: 9\n"), 0600)).To(Succeed())
		loader := NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		resolved, err := loader.LoadResolvedModelConfig("production", dir)
		Expect(err).NotTo(HaveOccurred())
		direct, err := loader.LoadResolvedModelConfig("target", dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).To(Equal(direct))
		Expect(resolved.IsAlias()).To(BeFalse())
		Expect(resolved.ContextSize).To(HaveValue(Equal(16384)))
		Expect(resolved.Threads).To(HaveValue(Equal(3)))
	})
})

func aliasGuessingInt(value int) *int { return &value }

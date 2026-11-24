// SPDX-License-Identifier: MIT
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// presetContext is set on fixture-backed configs. EstimateLLaMACppRun needs
// tensors, which a fabricated file lacks, so the trained-context estimate
// panics on it; a preset context keeps guessGGUFFromFile on the non-estimate
// path while the cache is exercised.
var presetContext = 8192

// headerCacheFixture fabricates a minimal in-memory GGUF. It carries the keys
// guessGGUFFromFile reads (architecture, name, chat template, MTP head) so a
// cached result can be compared field by field with a fresh parse.
func headerCacheFixture() *gguf.GGUFFile {
	return &gguf.GGUFFile{
		Header: gguf.GGUFHeader{
			MetadataKV: gguf.GGUFMetadataKVs{
				{Key: "general.architecture", ValueType: gguf.GGUFMetadataValueTypeString, Value: "llama"},
				{Key: "general.name", ValueType: gguf.GGUFMetadataValueTypeString, Value: "header-cache-fixture"},
				{Key: "tokenizer.chat_template", ValueType: gguf.GGUFMetadataValueTypeString, Value: "{{ messages }}"},
				{Key: "llama.nextn_predict_layers", ValueType: gguf.GGUFMetadataValueTypeUint32, Value: uint32(1)},
			},
		},
	}
}

// countingParser stands in for gguf.ParseGGUFFile. It counts calls and either
// returns the fixture or the configured error.
type countingParser struct {
	calls atomic.Int32
	fail  error
}

func (p *countingParser) parse(string, ...gguf.GGUFReadOption) (*gguf.GGUFFile, error) {
	p.calls.Add(1)
	if p.fail != nil {
		return nil, p.fail
	}
	return headerCacheFixture(), nil
}

func writeHeaderCacheFile(path string, size int) {
	ExpectWithOffset(1, os.WriteFile(path, make([]byte, size), 0o600)).To(Succeed())
}

var _ = Describe("GGUF header cache in front of model defaults", func() {
	BeforeEach(func() { GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false") })

	It("parses an unchanged file once and serves later calls from the cache", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		p := &countingParser{}

		first := presetConfig("weights.gguf")
		llamaCppDefaultsWithParser(first, dir, p.parse)
		second := presetConfig("weights.gguf")
		llamaCppDefaultsWithParser(second, dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(1)), "an unchanged file must not be parsed again")
	})

	It("re-parses when the file size changes", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "weights.gguf")
		writeHeaderCacheFile(path, 128)
		p := &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)

		writeHeaderCacheFile(path, 256)
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(2)))
	})

	It("re-parses when only the modification time changes", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "weights.gguf")
		writeHeaderCacheFile(path, 128)
		p := &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)

		later := time.Now().Add(time.Hour)
		ExpectWithOffset(1, os.Chtimes(path, later, later)).To(Succeed())
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(2)))
	})

	It("produces the same ModelConfig from a cache hit as from a fresh parse", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		p := &countingParser{}

		fresh := presetConfig("weights.gguf")
		llamaCppDefaultsWithParser(fresh, dir, p.parse)
		hit := presetConfig("weights.gguf")
		llamaCppDefaultsWithParser(hit, dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(1)))
		Expect(hit.Name).To(Equal("header-cache-fixture"))
		Expect(hit.Name).To(Equal(fresh.Name))
		Expect(hit.Options).To(Equal(fresh.Options))
		Expect(hit.ContextSize).To(Equal(fresh.ContextSize))
		Expect(hit.NGPULayers).To(Equal(fresh.NGPULayers))
		Expect(hit.KnownUsecaseStrings).To(Equal(fresh.KnownUsecaseStrings))
		Expect(hit.TemplateConfig.UseTokenizerTemplate).To(Equal(fresh.TemplateConfig.UseTokenizerTemplate))
		Expect(hit).To(Equal(fresh))
	})

	It("does not cache a missing file and keeps the existing fallback", func() {
		dir := GinkgoT().TempDir()
		p := &countingParser{fail: errors.New("no such file")}

		for i := 0; i < 2; i++ {
			cfg := &ModelConfig{Backend: "llama-cpp", PredictionOptions: predictionModel("missing.gguf")}
			Expect(func() { llamaCppDefaultsWithParser(cfg, dir, p.parse) }).NotTo(Panic())
			Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultContextSize)))
			Expect(cfg.NGPULayers).To(BeNil())
		}
		Expect(p.calls.Load()).To(Equal(int32(2)), "a missing file must reach the parser on every call")

		// Once the file exists it must be parsed, not served from a stale error.
		writeHeaderCacheFile(filepath.Join(dir, "missing.gguf"), 64)
		p.fail = nil
		cfg := presetConfig("missing.gguf")
		llamaCppDefaultsWithParser(cfg, dir, p.parse)
		Expect(cfg.Name).To(Equal("header-cache-fixture"))
	})

	It("does not cache a parse error", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		p := &countingParser{fail: errors.New("truncated header")}

		for i := 0; i < 2; i++ {
			cfg := &ModelConfig{Backend: "llama-cpp", PredictionOptions: predictionModel("weights.gguf")}
			Expect(func() { llamaCppDefaultsWithParser(cfg, dir, p.parse) }).NotTo(Panic())
			Expect(cfg.Name).To(BeEmpty())
		}
		Expect(p.calls.Load()).To(Equal(int32(2)))
	})

	It("still short-circuits on LOCALAI_DISABLE_GUESSING before consulting the cache", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "weights.gguf"), 128)
		p := &countingParser{}
		llamaCppDefaultsWithParser(presetConfig("weights.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(int32(1)))

		GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "true")
		cfg := presetConfig("weights.gguf")
		cfg.ContextSize = nil
		llamaCppDefaultsWithParser(cfg, dir, p.parse)

		Expect(p.calls.Load()).To(Equal(int32(1)))
		Expect(cfg.ContextSize).To(BeNil())
		Expect(cfg.Name).To(BeEmpty())
	})

	It("evicts entries beyond its bound so a long-unused file is parsed again", func() {
		dir := GinkgoT().TempDir()
		p := &countingParser{}
		// More distinct files than the cache holds: the first one must be evicted.
		for i := 0; i < 300; i++ {
			name := fmt.Sprintf("model-%03d.gguf", i)
			writeHeaderCacheFile(filepath.Join(dir, name), 32)
			llamaCppDefaultsWithParser(presetConfig(name), dir, p.parse)
		}
		before := p.calls.Load()
		llamaCppDefaultsWithParser(presetConfig("model-000.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(before + 1))

		Expect(ggufHeaders.len()).To(BeNumerically("<=", ggufHeaderCacheSize))

		// The most recent file is still cached.
		before = p.calls.Load()
		llamaCppDefaultsWithParser(presetConfig("model-299.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(before))
	})

	It("is safe for concurrent callers on the same and different files", func() {
		dir := GinkgoT().TempDir()
		writeHeaderCacheFile(filepath.Join(dir, "a.gguf"), 128)
		writeHeaderCacheFile(filepath.Join(dir, "b.gguf"), 128)
		p := &countingParser{}

		var wg sync.WaitGroup
		names := make([]string, 32)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				name := "a.gguf"
				if i%2 == 1 {
					name = "b.gguf"
				}
				cfg := presetConfig(name)
				llamaCppDefaultsWithParser(cfg, dir, p.parse)
				names[i] = cfg.Name
			}(i)
		}
		wg.Wait()
		for _, n := range names {
			Expect(n).To(Equal("header-cache-fixture"))
		}

		// Two files, so at most one parse per file in the steady state; concurrent
		// first callers may each parse before the entry lands.
		Expect(p.calls.Load()).To(BeNumerically("<=", 32))
		before := p.calls.Load()
		llamaCppDefaultsWithParser(presetConfig("a.gguf"), dir, p.parse)
		llamaCppDefaultsWithParser(presetConfig("b.gguf"), dir, p.parse)
		Expect(p.calls.Load()).To(Equal(before))
	})
})

func presetConfig(name string) *ModelConfig {
	ctx := presetContext
	return &ModelConfig{Backend: "llama-cpp", PredictionOptions: predictionModel(name), LLMConfig: LLMConfig{ContextSize: &ctx}}
}

func predictionModel(name string) schema.PredictionOptions {
	return schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: name}}
}

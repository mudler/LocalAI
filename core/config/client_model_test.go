package config

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClientModelProblem", func() {
	load := func(files map[string]string) *ModelConfigLoader {
		dir := GinkgoT().TempDir()
		for name, body := range files {
			Expect(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)).To(Succeed())
		}
		l := NewModelConfigLoader(dir)
		Expect(l.LoadModelConfigsFromPath(dir)).To(Succeed())
		return l
	}

	It("accepts a plain model and refuses a name that has no configuration", func() {
		l := load(map[string]string{"plain.yaml": "name: plain\nbackend: llama-cpp\nparameters:\n  model: plain.gguf\n"})
		Expect(l.ClientModelProblem("plain")).To(Succeed())
		Expect(l.ClientModelProblem("nope")).To(MatchError(ErrClientModelUnknown))
		var none *ModelConfigLoader
		Expect(none.ClientModelProblem("plain")).To(MatchError(ErrClientModelUnknown))
	})

	It("refuses a model with MCP servers, automatic MCP or an agent loop", func() {
		l := load(map[string]string{
			"mcp.yaml":   "name: with-mcp\nbackend: llama-cpp\nparameters:\n  model: a.gguf\nmcp:\n  remote: |\n    {\"mcpServers\":{\"s\":{\"url\":\"https://example.com/mcp\"}}}\n",
			"auto.yaml":  "name: with-auto\nbackend: llama-cpp\nparameters:\n  model: a.gguf\nmcp:\n  auto: true\n",
			"agent.yaml": "name: with-agent\nbackend: llama-cpp\nparameters:\n  model: a.gguf\nagent:\n  max_iterations: 5\n",
		})
		for _, n := range []string{"with-mcp", "with-auto", "with-agent"} {
			Expect(l.ClientModelProblem(n)).To(MatchError(ErrClientModelTools), n)
		}
	})

	It("looks through aliases, failover targets and pipeline stages", func() {
		l := load(map[string]string{
			"tool.yaml":     "name: tool\nbackend: llama-cpp\nparameters:\n  model: a.gguf\nmcp:\n  auto: true\n",
			"plain.yaml":    "name: plain\nbackend: llama-cpp\nparameters:\n  model: b.gguf\n",
			"alias.yaml":    "name: via-alias\nalias: tool\n",
			"chain.yaml":    "name: chain\nfailover:\n  targets:\n    - model: plain\n    - model: tool\n",
			"pipeline.yaml": "name: voice\npipeline:\n  llm: tool\n  tts: plain\n  transcription: plain\n",
			"okpipe.yaml":   "name: voice-ok\npipeline:\n  llm: plain\n  tts: plain\n  transcription: plain\n",
		})
		for _, n := range []string{"via-alias", "chain", "voice"} {
			err := l.ClientModelProblem(n)
			Expect(err).To(MatchError(ErrClientModelTools), n)
			Expect(err.Error()).To(ContainSubstring(`"tool"`), n)
		}
		Expect(l.ClientModelProblem("voice-ok")).To(Succeed())
	})

	It("looks through the candidates and the fallback of a router", func() {
		l := load(map[string]string{
			"tool.yaml":  "name: tool\nbackend: llama-cpp\nparameters:\n  model: a.gguf\nmcp:\n  auto: true\n",
			"plain.yaml": "name: plain\nbackend: llama-cpp\nparameters:\n  model: b.gguf\n",
			"agent.yaml": "name: agent\nbackend: llama-cpp\nparameters:\n  model: c.gguf\nagent:\n  max_iterations: 3\n",
			"r-cand.yaml": "name: r-cand\nbackend: llama-cpp\nparameters:\n  model: r.gguf\nrouter:\n  classifier_model: plain\n" +
				"  policies:\n    - label: code\n      description: code\n  candidates:\n    - model: plain\n      labels: [code]\n    - model: tool\n      labels: [code]\n",
			"r-fall.yaml": "name: r-fall\nbackend: llama-cpp\nparameters:\n  model: r.gguf\nrouter:\n  classifier_model: plain\n" +
				"  policies:\n    - label: code\n      description: code\n  candidates:\n    - model: plain\n      labels: [code]\n  fallback: agent\n",
			"r-ok.yaml": "name: r-ok\nbackend: llama-cpp\nparameters:\n  model: r.gguf\nrouter:\n  classifier_model: plain\n" +
				"  policies:\n    - label: code\n      description: code\n  candidates:\n    - model: plain\n      labels: [code]\n  fallback: plain\n",
		})
		err := l.ClientModelProblem("r-cand")
		Expect(err).To(MatchError(ErrClientModelTools))
		Expect(err.Error()).To(ContainSubstring(`"tool"`))
		err = l.ClientModelProblem("r-fall")
		Expect(err).To(MatchError(ErrClientModelTools))
		Expect(err.Error()).To(ContainSubstring(`"agent"`))
		Expect(l.ClientModelProblem("r-ok")).To(Succeed())
	})
})

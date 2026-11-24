package config_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("agent options of a model file", func() {
	It("keeps the options the chats loop honours", func() {
		var c config.ModelConfig
		Expect(yaml.Unmarshal([]byte("name: m\nagent:\n  max_attempts: 3\n  max_iterations: 7\n  force_reasoning_tool: true\n"), &c)).To(Succeed())
		Expect(c.Agent).To(Equal(config.AgentConfig{MaxAttempts: 3, MaxIterations: 7, ForceReasoningTool: true}))
		Expect(c.Agent.RemovedOptions()).To(BeEmpty())
	})

	It("loads a file with the removed options and names them", func() {
		dir := GinkgoT().TempDir()
		file := filepath.Join(dir, "m.yaml")
		Expect(os.WriteFile(file, []byte("name: m\nagent:\n  enable_planning: true\n  loop_detection: 5\n  enable_reasoning: true\n  max_iterations: 4\n"), 0o600)).To(Succeed())
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		c, ok := loader.GetModelConfig("m")
		Expect(ok).To(BeTrue())
		Expect(c.Agent.MaxIterations).To(Equal(4))
		Expect(c.Agent.RemovedOptions()).To(ConsistOf("enable_planning", "loop_detection", "enable_reasoning"))
	})
})

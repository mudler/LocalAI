package config_test

import (
	"os"

	. "github.com/go-skynet/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test cases for config related functions", func() {

	var (
		configFile string
	)

	Context("Test Read configuration functions", func() {
		configFile = os.Getenv("CONFIG_FILE")
		It("Test ReadConfigFile", func() {
			config, err := ReadBackendConfigFile(configFile)
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config[0].Name).To(Equal("list1"))
			Expect(config[1].Name).To(Equal("list2"))
		})

		It("Test LoadConfigs", func() {
			cm := NewBackendConfigLoader()
			err := cm.LoadBackendConfigsFromPath(os.Getenv("MODELS_PATH"))
			Expect(err).To(BeNil())
			Expect(cm.ListBackendConfigs()).ToNot(BeNil())

			Expect(cm.ListBackendConfigs()).To(ContainElements("code-search-ada-code-001"))

			// config should includes text-embedding-ada-002 models's api.config
			Expect(cm.ListBackendConfigs()).To(ContainElements("text-embedding-ada-002"))

			// config should includes rwkv_test models's api.config
			Expect(cm.ListBackendConfigs()).To(ContainElements("rwkv_test"))

			// config should includes whisper-1 models's api.config
			Expect(cm.ListBackendConfigs()).To(ContainElements("whisper-1"))
		})
	})
})

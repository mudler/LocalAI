package config

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test cases for config related functions", func() {

	var (
		configFile string
	)

	Context("Test Read configuration functions", func() {
		configFile = os.Getenv("CONFIG_FILE")
		It("Test readConfigFile", func() {
			config, err := readMultipleBackendConfigsFromFile(configFile)
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config[0].Name).To(Equal("list1"))
			Expect(config[1].Name).To(Equal("list2"))
		})

		It("Test LoadConfigs", func() {
			bcl := NewBackendConfigLoader()
			opts := NewApplicationConfig()
			err := bcl.LoadBackendConfigsFromPath(opts.ModelPath)
			Expect(err).To(BeNil())
			configs := bcl.GetAllBackendConfigs()
			loadedModelNames := []string{}
			for _, v := range configs {
				loadedModelNames = append(loadedModelNames, v.Name)
			}
			Expect(configs).ToNot(BeNil())

			// config should includes gpt4all models's api.config
			Expect(loadedModelNames).To(ContainElements("gpt4all"))

			// config should includes gpt2 models's api.config
			Expect(loadedModelNames).To(ContainElements("gpt4all-2"))

			// config should includes text-embedding-ada-002 models's api.config
			Expect(loadedModelNames).To(ContainElements("text-embedding-ada-002"))

			// config should includes rwkv_test models's api.config
			Expect(loadedModelNames).To(ContainElements("rwkv_test"))

			// config should includes whisper-1 models's api.config
			Expect(loadedModelNames).To(ContainElements("whisper-1"))
		})
	})
})

package api_config_test

import (
	"os"

	. "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/model"
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
			config, err := ReadConfigFile(configFile)
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config[0].Name).To(Equal("list1"))
			Expect(config[1].Name).To(Equal("list2"))
		})

		It("Test LoadConfigs", func() {
			cm := NewConfigLoader()
			opts := options.NewOptions()
			modelLoader := model.NewModelLoader(os.Getenv("MODELS_PATH"))
			options.WithModelLoader(modelLoader)(opts)

			err := cm.LoadConfigs(opts.Loader.ModelPath)
			Expect(err).To(BeNil())
			Expect(cm.ListConfigs()).ToNot(BeNil())

			// config should includes gpt4all models's api.config
			Expect(cm.ListConfigs()).To(ContainElements("gpt4all"))

			// config should includes gpt2 models's api.config
			Expect(cm.ListConfigs()).To(ContainElements("gpt4all-2"))

			// config should includes text-embedding-ada-002 models's api.config
			Expect(cm.ListConfigs()).To(ContainElements("text-embedding-ada-002"))

			// config should includes rwkv_test models's api.config
			Expect(cm.ListConfigs()).To(ContainElements("rwkv_test"))

			// config should includes whisper-1 models's api.config
			Expect(cm.ListConfigs()).To(ContainElements("whisper-1"))
		})
	})
})

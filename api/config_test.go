package api

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
		It("Test ReadConfigFile", func() {
			config, err := ReadConfigFile(configFile)
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(len(config)).To(Equal(2))
		})

	})
})

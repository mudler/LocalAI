package config

import (
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test cases for config related functions", func() {
	Context("Test Read configuration functions", func() {
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`backend: "foo-bar"
parameters:
  model: "foo-bar"`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			Expect(config.Validate()).To(BeFalse())
		})
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`name: bar-baz
backend: "foo-bar"
parameters:
  model: "foo-bar"`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("bar-baz"))
			Expect(config.Validate()).To(BeTrue())

			// download https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml
			httpClient := http.Client{}
			resp, err := httpClient.Get("https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml")
			Expect(err).To(BeNil())
			defer resp.Body.Close()
			tmp, err = os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = io.Copy(tmp, resp.Body)
			Expect(err).To(BeNil())
			config, err = readBackendConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("hermes-2-pro-mistral"))
			Expect(config.Validate()).To(BeTrue())
		})
	})
})

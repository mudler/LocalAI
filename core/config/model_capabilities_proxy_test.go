package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// LocalAI cannot probe what a cloud model behind the proxy accepts, so a proxy
// model sees images only when its config declares it.
var _ = Describe("a cloud-proxy model and images", func() {
	load := func(src string) *ModelConfig {
		var c ModelConfig
		Expect(yaml.Unmarshal([]byte(src), &c)).To(Succeed())
		return &c
	}
	const head = `
name: cloud
backend: cloud-proxy
proxy:
  upstream_url: https://upstream.example/v1/chat/completions
`

	It("cannot see images when the config declares only chat", func() {
		c := load(head + "known_usecases: [chat]\n")
		Expect(c.VisionSupported()).To(BeFalse())
		Expect(c.Capabilities()).ToNot(ContainElement(UsecaseVision))
		Expect(c.InputModalities()).To(Equal([]string{ModalityText}))
	})

	It("sees images when the config declares vision", func() {
		c := load(head + "known_usecases: [chat, vision]\n")
		Expect(c.VisionSupported()).To(BeTrue())
		Expect(c.Capabilities()).To(ContainElements(UsecaseChat, UsecaseVision))
		Expect(c.InputModalities()).To(Equal([]string{ModalityText, ModalityImage}))
	})

	It("keeps the declaration when the config is written back", func() {
		c := load(head + "known_usecases: [chat, vision]\n")
		out, err := yaml.Marshal(c)
		Expect(err).ToNot(HaveOccurred())
		Expect(load(string(out)).VisionSupported()).To(BeTrue(), "written back:\n%s", out)
	})
})

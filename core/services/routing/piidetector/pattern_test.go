package piidetector_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piidetector"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPiidetector(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "piidetector suite")
}

func patternModel() config.ModelConfig {
	c := config.ModelConfig{Name: "secret-filter", Backend: "pattern"}
	c.PIIDetection.Builtins = []string{"anthropic_api_key"}
	c.PIIDetection.Patterns = []config.PIIPattern{{Name: "INTERNAL_TOKEN", Match: `tok-[A-Za-z0-9]{8,}`}}
	return c
}

var _ = Describe("pattern detector", func() {
	It("matches built-in and custom secrets as whole-span deterministic hits", func() {
		det, err := piidetector.NewPattern(patternModel(), &config.ApplicationConfig{})
		Expect(err).NotTo(HaveOccurred())

		ents, err := det.Detect(context.Background(), "use sk-ant-api03-AAAABBBBCCCCDDDDEEEE and tok-ABCD1234 ok")
		Expect(err).NotTo(HaveOccurred())

		byGroup := map[string]pii.NEREntity{}
		for _, e := range ents {
			byGroup[e.Group] = e
			Expect(e.Score).To(BeEquivalentTo(float32(1.0)), "pattern matches are deterministic")
		}
		Expect(byGroup).To(HaveKey("ANTHROPIC_KEY"))
		Expect(byGroup["INTERNAL_TOKEN"].Text).To(Equal("tok-ABCD1234"))
	})

	It("still detects (and exercises the trace path) with tracing enabled", func() {
		det, err := piidetector.NewPattern(patternModel(), &config.ApplicationConfig{
			EnableTracing: true, TracingMaxItems: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		ents, err := det.Detect(context.Background(), "sk-ant-api03-AAAABBBBCCCCDDDDEEEE")
		Expect(err).NotTo(HaveOccurred())
		Expect(ents).To(HaveLen(1))
		Expect(ents[0].Group).To(Equal("ANTHROPIC_KEY"))
	})

	It("fails to build on an invalid (unanchored) custom pattern", func() {
		c := config.ModelConfig{Name: "bad", Backend: "pattern"}
		c.PIIDetection.Patterns = []config.PIIPattern{{Name: "X", Match: `.*`}}
		_, err := piidetector.NewPattern(c, &config.ApplicationConfig{})
		Expect(err).To(HaveOccurred())
	})
})

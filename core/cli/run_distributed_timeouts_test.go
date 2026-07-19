package cli

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseDistributedDuration", func() {
	It("parses a valid operator-supplied duration", func() {
		d, err := parseDistributedDuration("LOCALAI_NATS_MODEL_LOAD_TIMEOUT", "45m")
		Expect(err).ToNot(HaveOccurred())
		Expect(d).To(Equal(45 * time.Minute))
	})

	It("names the env var and the bad value so a typo is actionable", func() {
		_, err := parseDistributedDuration("LOCALAI_NATS_MODEL_LOAD_TIMEOUT", "45mins")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_NATS_MODEL_LOAD_TIMEOUT"))
		Expect(err.Error()).To(ContainSubstring("45mins"))
	})
})

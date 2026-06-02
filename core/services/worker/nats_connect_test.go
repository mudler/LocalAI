package worker

import (
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("connectNATS", func() {
	It("requires JWT when requireAuth is set and no credentials are provided", func() {
		_, err := connectNATS("nats://127.0.0.1:4222", "", "", "", "", true, messaging.TLSFiles{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("NATS JWT+seed required"))
	})
})
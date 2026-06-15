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

	// A JWT supplied without its paired seed (or vice-versa) is an operator
	// misconfiguration. Today connectNATS silently drops the unpaired credential
	// and connects anonymously, so the operator believes the link is
	// authenticated when it is not. It should refuse instead.
	It("rejects a JWT supplied without a seed instead of connecting anonymously", func() {
		client, err := connectNATS("nats://127.0.0.1:4222", "jwt-without-seed", "", "", "", false, messaging.TLSFiles{})
		if client != nil {
			client.Close()
		}
		Expect(err).To(HaveOccurred(),
			"connectNATS should reject an unpaired JWT rather than silently connecting anonymously")
	})
})

package messaging_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TLSFiles", func() {
	It("requires cert and key together", func() {
		Expect((messaging.TLSFiles{Cert: "/tmp/c.pem"}).Validate()).To(HaveOccurred())
		Expect((messaging.TLSFiles{Key: "/tmp/k.pem"}).Validate()).To(HaveOccurred())
	})

	It("validates files exist", func() {
		dir := GinkgoT().TempDir()
		ca := filepath.Join(dir, "ca.pem")
		Expect(os.WriteFile(ca, []byte("x"), 0600)).To(Succeed())
		Expect((messaging.TLSFiles{CA: ca}).Validate()).To(Succeed())
	})
})
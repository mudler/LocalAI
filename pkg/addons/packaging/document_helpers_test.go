// SPDX-License-Identifier: MIT
package packaging_test

import (
	"os"
 "crypto/sha256"
 "fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sandbox document helpers", func() {
	It("installs unchanged PDF helpers and their license with a build-time fixture check", func() {
		guest := "../../../addons/sandbox/images/guest/"
        fingerprints := map[string]string{
            "content_qa.py": "06c41695cfb72a3b30d3f1a4ad2c9b1a5fd3b62ed82be090b48fdd27bd031de6",
            "verify_pdf.py": "6e8844de335bb10492865f6d7f078d81637d47da32b0d86dce5fe2948650b9f9",
            "notice.txt": "a178aa53599c2a10f619781d4799f517ef7b6567a749866bbd9db556e02b0625",
        }
        // Task 3 compared these bytes with the original Feather core copies.
        // Keep trusted fingerprints, not an executable duplicate in core.
        for name, fingerprint := range fingerprints {
            data, err := os.ReadFile(guest + "document-skills/pdf/" + name)
            Expect(err).NotTo(HaveOccurred())
            Expect(fmt.Sprintf("%x", sha256.Sum256(data))).To(Equal(fingerprint), name)
        }
        notice, err := os.ReadFile(guest + "document-skills/pdf/notice.txt")
        Expect(err).NotTo(HaveOccurred())
        for _, text := range []string{"MIT License", "Copyright (c) 2026 Paper Instruments, Inc.", "Permission is hereby granted", "THE SOFTWARE IS PROVIDED"} {
            Expect(string(notice)).To(ContainSubstring(text))
        }
		dockerfile, err := os.ReadFile(guest + "Dockerfile")
		Expect(err).NotTo(HaveOccurred())
		text := string(dockerfile)
		Expect(text).To(ContainSubstring("COPY addons/sandbox/images/guest/document-skills/ /usr/local/lib/localai/document-skills/"))
		Expect(text).To(ContainSubstring("COPY addons/sandbox/images/guest/check-document-skills.py /usr/local/lib/localai/document-skills/check-document-skills.py"))
		Expect(text).To(ContainSubstring("RUN python3 /usr/local/lib/localai/document-skills/check-document-skills.py"))
		Expect(strings.Index(text, "RUN python3 /usr/local/lib/localai/document-skills/check-document-skills.py")).To(BeNumerically(">", strings.Index(text, "python3 /tmp/guest-python/check-python.py")))
		_, err = os.Stat(guest + "check-document-skills.py")
		Expect(err).NotTo(HaveOccurred())
	})
})

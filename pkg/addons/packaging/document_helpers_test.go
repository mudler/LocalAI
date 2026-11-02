// SPDX-License-Identifier: MIT
package packaging_test

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sandbox document helpers", func() {
	It("installs unchanged PDF helpers and their license with a build-time fixture check", func() {
		guest := "../../../addons/sandbox/images/guest/"
		for _, name := range []string{"content_qa.py", "verify_pdf.py"} {
			original, err := os.ReadFile("../../../core/services/studio/builtinskills/tree/pdf/scripts/" + name)
			Expect(err).NotTo(HaveOccurred())
			copied, err := os.ReadFile(guest + "document-skills/pdf/" + name)
			Expect(err).NotTo(HaveOccurred())
			Expect(copied).To(Equal(original))
		}
		notice, err := os.ReadFile(guest + "document-skills/pdf/notice.txt")
		Expect(err).NotTo(HaveOccurred())
		originalNotice, err := os.ReadFile("../../../core/services/studio/builtinskills/tree/NOTICE")
		Expect(err).NotTo(HaveOccurred())
		license := strings.Split(strings.Split(string(originalNotice), "The MIT licence of the Feather project\n--------------------------------------\n\n")[1], "\nThe generated API pages")[0]
		Expect(string(notice)).To(ContainSubstring(strings.TrimSpace(license)))
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

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package safefile

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Race-safe exact removal", func() {
	DescribeTable("keeps replacement targets untouched after an opened parent is exchanged",
		func(targetInsideRoot bool) {
			root := GinkgoT().TempDir()
			originalRequest := filepath.Join(root, "ephemeral", "request-id")
			originalCategory := filepath.Join(originalRequest, "audio")
			Expect(os.MkdirAll(originalCategory, 0750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(originalCategory, "input.wav"), []byte("original"), 0640)).To(Succeed())

			replacement := GinkgoT().TempDir()
			if targetInsideRoot {
				replacement = filepath.Join(root, "replacement")
				Expect(os.MkdirAll(replacement, 0750)).To(Succeed())
			}
			Expect(os.MkdirAll(filepath.Join(replacement, "audio"), 0750)).To(Succeed())
			replacementFile := filepath.Join(replacement, "audio", "input.wav")
			Expect(os.WriteFile(replacementFile, []byte("replacement"), 0640)).To(Succeed())

			renamedRequest := filepath.Join(root, "ephemeral", "opened-request")
			opened := make(chan struct{})
			swapped := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- removeExact(root, filepath.Join("ephemeral", "request-id", "audio", "input.wav"), []string{".sha256", ".sha256.target"}, 2, func() {
					close(opened)
					<-swapped
				})
			}()

			<-opened
			Expect(os.Rename(originalRequest, renamedRequest)).To(Succeed())
			Expect(os.Symlink(replacement, originalRequest)).To(Succeed())
			close(swapped)
			Expect(<-done).To(Succeed())

			data, err := os.ReadFile(replacementFile)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal([]byte("replacement")))
			Expect(originalRequest).To(BeAnExistingFile())
			Expect(filepath.Join(renamedRequest, "audio", "input.wav")).NotTo(BeAnExistingFile())
		},
		Entry("for an internal replacement", true),
		Entry("for an external replacement", false),
	)
})

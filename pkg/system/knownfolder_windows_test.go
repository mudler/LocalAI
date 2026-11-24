//go:build windows

package system

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Windows default folders", func() {
	It("finds the local application data and ProgramData folders", func() {
		for _, f := range []knownFolder{folderLocalAppData, folderProgramData} {
			p, err := knownFolderPath(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(filepath.IsAbs(p)).To(BeTrue(), p)
			Expect(p).To(BeADirectory())
		}
	})

	It("uses the local application data folder by default", func() {
		if os.Getenv(BasePathEnv) != "" {
			Skip("LOCALAI_BASE_PATH is set")
		}
		local, err := knownFolderPath(folderLocalAppData)
		Expect(err).NotTo(HaveOccurred())
		d := ResolveDefaultDirs()
		if d.Origin == OriginLegacy {
			Skip("the current folder holds LocalAI files")
		}
		Expect(d.Base).To(Equal(filepath.Join(local, "LocalAI")))
	})

	It("creates, writes and reads below a folder with spaces, non-ASCII letters and a long path", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "Local AI [test] äöü")
		for len(dir) < 300 {
			dir = filepath.Join(dir, strings.Repeat("d", 40))
		}
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		f := filepath.Join(dir, "model.yaml")
		Expect(os.WriteFile(f, []byte("name: x\n"), 0o600)).To(Succeed())
		Expect(os.ReadFile(f)).To(Equal([]byte("name: x\n")))
		Expect(LongPath("windows", f)).To(BeTrue())
	})
})

// SPDX-License-Identifier: MIT
package packages

import (
	"io/fs"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type modeInfo fs.FileMode

func (m modeInfo) Name() string       { return "x" }
func (m modeInfo) Size() int64        { return 1 }
func (m modeInfo) Mode() fs.FileMode  { return fs.FileMode(m) }
func (m modeInfo) ModTime() time.Time { return time.Time{} }
func (m modeInfo) IsDir() bool        { return false }
func (m modeInfo) Sys() any           { return nil }

var _ os.FileInfo = modeInfo(0)

var _ = Describe("The executable of a package", func() {
	It("needs the owner execute bit on Linux and macOS", func() {
		for _, goos := range []string{"linux", "darwin"} {
			Expect(runnable(goos, modeInfo(0o700), "tables", false)).To(BeTrue())
			Expect(runnable(goos, modeInfo(0o600), "tables", false)).To(BeFalse())
			Expect(runnable(goos, modeInfo(0o600), "tables.exe", false)).To(BeFalse())
		}
	})
	It("takes the Linux program of a package that runs in WSL 2 on Windows, without .exe", func() {
		Expect(runnable("windows", modeInfo(0o666), "sandbox", true)).To(BeTrue())
		Expect(runnable("windows", modeInfo(0o666), "sandbox", false)).To(BeFalse())
	})
	It("needs the .exe extension on Windows, which has no execute bit", func() {
		Expect(runnable("windows", modeInfo(0o666), "tables.exe", false)).To(BeTrue())
		Expect(runnable("windows", modeInfo(0o666), "bin/Tables.EXE", false)).To(BeTrue())
		Expect(runnable("windows", modeInfo(0o777), "tables", false)).To(BeFalse())
		Expect(runnable("windows", modeInfo(0o777), "tables.sh", false)).To(BeFalse())
	})
})

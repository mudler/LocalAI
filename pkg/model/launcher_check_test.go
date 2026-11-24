package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("checkWindowsDirectLaunch", func() {
	It("accepts Windows executables", func() {
		for _, name := range []string{"backend.exe", "BACKEND.EXE", "run.cmd", "run.bat", "tool.com", "grpc-server"} {
			Expect(checkWindowsDirectLaunch("dir/"+name)).To(Succeed(), name)
		}
	})

	It("explains that a run.sh backend has no Windows build", func() {
		err := checkWindowsDirectLaunch("backends/whisper/run.sh")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("has no Windows build"))
		Expect(err.Error()).To(ContainSubstring("run.sh"))
		Expect(checkWindowsDirectLaunch("backends/x/backend.py")).To(HaveOccurred())
	})
})

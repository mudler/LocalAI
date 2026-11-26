package model

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HideBackendOutput", func() {
	It("hides the lines of one model's backend while a request holds it, and a moment after", func() {
		ml := &ModelLoader{}
		Expect(ml.outputLine("m", "prompt: secret")).To(Equal("prompt: secret"))
		release := ml.HideBackendOutput("m")
		Expect(ml.outputLine("m", "prompt: secret")).To(Equal(HiddenOutputLine))
		Expect(ml.outputLine("other", "prompt: other")).To(Equal("prompt: other"), "another backend is not hidden")
		second := ml.HideBackendOutput("m")
		release()
		release() // a second call changes nothing
		second()
		Expect(ml.outputLine("m", "late line")).To(Equal(HiddenOutputLine), "the grace covers the lines that arrive late")
		Eventually(func() string { return ml.outputLine("m", "after") }, 2*hiddenOutputGrace, 100*time.Millisecond).Should(Equal("after"))
	})
})

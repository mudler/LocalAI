package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("worker paths from another OS", func() {
	It("detects the path style", func() {
		Expect(isWindowsStylePath(`C:\w\models`)).To(BeTrue())
		Expect(isWindowsStylePath(`c:/w/models`)).To(BeTrue())
		Expect(isWindowsStylePath(`\\server\share\m`)).To(BeTrue())
		Expect(isWindowsStylePath(`models\x`)).To(BeTrue())
		Expect(isWindowsStylePath(`/w/models`)).To(BeFalse())
		Expect(isWindowsStylePath(`/w/odd\name`)).To(BeFalse())
	})

	It("takes the base name with either separator", func() {
		Expect(remoteBase(`C:\w\staging\localai-output-1.tmp`)).To(Equal("localai-output-1.tmp"))
		Expect(remoteBase(`/w/staging/localai-output-1.tmp`)).To(Equal("localai-output-1.tmp"))
		Expect(remoteBase(`name.tmp`)).To(Equal("name.tmp"))
		Expect(remoteBase(`/w/dir/`)).To(Equal("dir"))
	})

	It("takes the directory with either separator", func() {
		Expect(remoteDirOf(`C:\w\staging\a.png`)).To(Equal(`C:\w\staging`))
		Expect(remoteDirOf(`C:\a.png`)).To(Equal(`C:\`))
		Expect(remoteDirOf(`/w/staging/a.png`)).To(Equal(`/w/staging`))
		Expect(remoteDirOf(`/a.png`)).To(Equal(`/`))
		Expect(remoteDirOf(`a.png`)).To(Equal(`.`))
	})

	It("joins with the separator of the worker", func() {
		Expect(remoteJoin(`C:\w\models\m`, "voice.onnx")).To(Equal(`C:\w\models\m\voice.onnx`))
		Expect(remoteJoin(`C:\w\models\m\`, "sub/voice.onnx")).To(Equal(`C:\w\models\m\sub\voice.onnx`))
		Expect(remoteJoin(`/w/models/m`, `sub\voice.onnx`)).To(Equal(`/w/models/m/sub/voice.onnx`))
	})

	Describe("DeriveRemoteModelPath", func() {
		It("handles a Windows worker behind a Linux frontend", func() {
			Expect(DeriveRemoteModelPath(`C:\w\models\my-model\sd-cpp\models\flux.gguf`, "sd-cpp/models/flux.gguf")).
				To(Equal(`C:\w\models\my-model`))
		})

		It("handles a Linux worker behind a Windows frontend", func() {
			Expect(DeriveRemoteModelPath(`/w/models/my-model/sd-cpp/models/flux.gguf`, `sd-cpp\models\flux.gguf`)).
				To(Equal(`/w/models/my-model`))
		})

		It("keeps a Windows drive root", func() {
			Expect(DeriveRemoteModelPath(`C:\flux.gguf`, "flux.gguf")).To(Equal(`C:\`))
		})

		It("does not cut a partial name", func() {
			Expect(DeriveRemoteModelPath(`/w/models/myflux.gguf`, "flux.gguf")).To(Equal(`/w/models/myflux.gguf`))
		})
	})
})

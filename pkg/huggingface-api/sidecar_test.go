package hfapi_test

import (
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sidecar detection", func() {
	DescribeTable("SidecarOf",
		func(path, kind string) {
			sc, ok := hfapi.SidecarOf(path)
			Expect(ok).To(Equal(kind != ""))
			Expect(sc.Kind).To(Equal(kind))
		},
		Entry("root dspark", "dspark-Inkling-2-Mini-Q8_0.gguf", "dspark"),
		Entry("dspark in a folder", "dspark/dspark-Inkling-2-Mini-BF16.gguf", "dspark"),
		Entry("dflash", "a/dflash-x.gguf", "dflash"),
		Entry("eagle3", "eagle3-x.gguf", "eagle3"),
		Entry("mtp", "mtp-x.gguf", "mtp"),
		Entry("plain model", "Model-Q4_K_M.gguf", ""),
		Entry("keyword only in the directory", "dspark-models/Model-Q4_K_M.gguf", ""),
		Entry("capitalised keyword is not a sidecar, as in llama.cpp", "Qwen-MTP-Q4_K_M.gguf", ""),
	)

	It("recognises imatrix files by name", func() {
		Expect(hfapi.IsImatrix("sub/imatrix_unsloth.gguf")).To(BeTrue())
		Expect(hfapi.IsImatrix("model.gguf")).To(BeFalse())
	})
})

package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApexEntries(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "apexentries")
}

var _ = Describe("ParseRepoFiles", func() {
	It("returns gguf siblings with their lfs sha256", func() {
		body := []byte(`{"siblings":[
			{"rfilename":"Model-APEX-I-Quality.gguf","size":10,"lfs":{"sha256":"aa","size":10}},
			{"rfilename":"README.md"},
			{"rfilename":"mmproj.gguf","size":5,"lfs":{"sha256":"bb","size":5}}
		]}`)

		files, err := ParseRepoFiles(body)

		Expect(err).ToNot(HaveOccurred())
		Expect(files).To(HaveLen(2))
		Expect(files[0].Name).To(Equal("Model-APEX-I-Quality.gguf"))
		Expect(files[0].SHA256).To(Equal("aa"))
		Expect(files[1].Name).To(Equal("mmproj.gguf"))
	})

	It("reports a gguf that carries no lfs sha256", func() {
		body := []byte(`{"siblings":[{"rfilename":"mmproj.gguf","size":5}]}`)

		_, err := ParseRepoFiles(body)

		Expect(err).To(MatchError(ErrNoSHA256))
	})
})

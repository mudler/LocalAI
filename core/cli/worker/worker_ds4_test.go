package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ds4 worker CLI", func() {
	It("uses the ds4 backend gallery name and worker binary name", func() {
		Expect(ds4GalleryName).To(Equal("ds4"))
		Expect(ds4WorkerBinaryName).To(Equal("ds4-worker"))
	})

	It("assembles direct exec args as [binary, extra-split...]", func() {
		args := ds4WorkerArgs("/b/ds4-worker", "--role worker --model m.gguf --layers 20:output --coordinator 10.0.0.1 1234")
		Expect(args).To(Equal([]string{
			"/b/ds4-worker",
			"--role", "worker",
			"--model", "m.gguf",
			"--layers", "20:output",
			"--coordinator", "10.0.0.1", "1234",
		}))
	})

	It("drops empty extra args to a bare binary invocation", func() {
		Expect(ds4WorkerArgs("/b/ds4-worker", "")).To(Equal([]string{"/b/ds4-worker"}))
	})
})

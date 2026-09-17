// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("real-model generation", Label("real-models"), func() {
	It("loads the text bundle and generates two clips with one native session", func() {
		library := os.Getenv("KIMODO_TEST_LIBRARY")
		if library == "" {
			Skip("set KIMODO_TEST_LIBRARY, KIMODO_TEST_MOTION and KIMODO_TEST_TEXT for real-model smoke testing")
		}
		Expect(loadNativeLibrary(library)).To(Succeed())
		backend := &Kimodo{}
		DeferCleanup(backend.Free)
		device := os.Getenv("KIMODO_TEST_DEVICE")
		if device == "" {
			device = "cpu"
		}
		Expect(backend.Load(&pb.ModelOptions{ModelFile: os.Getenv("KIMODO_TEST_MOTION"), Threads: 8,
			Options: []string{"text_bundle:" + os.Getenv("KIMODO_TEST_TEXT"), "device:" + device}})).To(Succeed())
		for index := range 2 {
			path := filepath.Join(GinkgoT().TempDir(), "animation.glb")
			By("generating a clip with the existing session")
			Expect(backend.Animate3D(&pb.Animate3DRequest{Dst: path,
				Inputs: map[string]*pb.AnimationInput{"prompt": {Type: "text", Data: "A person walks forward."}},
				Params: map[string]string{"frames": "60", "steps": "1", "seed": "42"},
			})).To(Succeed(), "clip %d", index)
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 1000))
			Expect(string(data[:4])).To(Equal("glTF"))
		}
	})
})

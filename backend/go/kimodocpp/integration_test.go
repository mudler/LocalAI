// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/grpc/metadata"
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
		options := []string{"text_bundle:" + os.Getenv("KIMODO_TEST_TEXT"), "device:" + device}
		if chunk := os.Getenv("KIMODO_TEST_TEXT_LAYER_CHUNK"); chunk != "" {
			options = append(options, "text_layer_chunk:"+chunk)
		}
		Expect(backend.Load(&pb.ModelOptions{ModelFile: os.Getenv("KIMODO_TEST_MOTION"), Threads: 8,
			Options: options})).To(Succeed())
		for index := range 2 {
			path := filepath.Join(GinkgoT().TempDir(), "animation.glb")
			By("generating a clip with the existing session")
			data, err := backend.Animate3DWithMetadata(&pb.Animate3DRequest{Dst: path,
				Inputs: map[string]*pb.AnimationInput{"prompt": {Type: "text", Data: "A person walks forward."}},
				Params: map[string]string{"frames": "60", "steps": "1", "seed": "42"},
			})
			Expect(err).NotTo(HaveOccurred(), "clip %d", index)
			usage, err := metadata.ParseUsage(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(usage).NotTo(BeNil())
			Expect(usage.InputUnits).To(BeNumerically(">", 1))
			Expect(usage.OutputUnits).To(Equal(60))
			Expect(usage.Details).To(MatchJSON(`{"output_frames":60,"sampling_steps":1}`))
			Expect(usage.AccountingRule).To(Equal("frame_steps_v1"))
			data, err = os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 1000))
			Expect(string(data[:4])).To(Equal("glTF"))
		}
	})
})

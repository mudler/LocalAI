// SPDX-License-Identifier: MIT
package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKimodo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kimodo backend")
}

var _ = Describe("generation parameters", func() {
	It("uses the reference sampling settings and the C ABI layout", func() {
		options, err := parseGeneration(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(options.Size).To(Equal(uint32(32)))
		Expect(unsafe.Offsetof(options.Seed)).To(Equal(uintptr(8)))
		Expect(options.Frames).To(Equal(uint32(150)))
		Expect(options.Steps).To(Equal(uint32(100)))
		Expect(options.TextGuidance).To(Equal(float32(2)))
	})
	It("preserves explicit zero guidance and seed", func() {
		options, err := parseGeneration(map[string]string{"seed": "0", "text_guidance": "0"})
		Expect(err).NotTo(HaveOccurred())
		Expect(options.Seed).To(BeZero())
		Expect(options.TextGuidance).To(BeZero())
	})
	DescribeTable("rejects invalid parameters before inference", func(name, value string) {
		_, err := parseGeneration(map[string]string{name: value})
		Expect(err).To(HaveOccurred())
	},
		Entry("short clip", "frames", "59"), Entry("long clip", "frames", "151"),
		Entry("negative seed", "seed", "-1"), Entry("overflow seed", "seed", "18446744073709551616"),
		Entry("fractional steps", "steps", "2.5"), Entry("zero steps", "steps", "0"),
		Entry("NaN guidance", "text_guidance", "NaN"), Entry("infinite guidance", "text_guidance", "+Inf"),
		Entry("unsupported texture", "texture_steps", "12"),
	)
})

var _ = Describe("text encoder loading", func() {
	var chunk int32
	var textPath string
	BeforeEach(func() {
		configure, load, free := nativeConfigure, nativeLoad, nativeFree
		DeferCleanup(func() { nativeConfigure, nativeLoad, nativeFree = configure, load, free })
		chunk, textPath = 0, ""
		nativeConfigure = func(_ string, threads, layers int32) int32 {
			Expect(threads).To(Equal(int32(8)))
			chunk = layers
			return 0
		}
		nativeLoad = func(_ string, text, _ string, _ uintptr, _ *byte, _ int32) uintptr {
			textPath = text
			return 1
		}
		nativeFree = func(uintptr) {}
	})
	DescribeTable("loads monolithic encoders or legacy bundles with all layers resident by default", func(text string) {
		backend := &Kimodo{}
		DeferCleanup(backend.Free)
		Expect(backend.Load(&pb.ModelOptions{ModelFile: "motion.gguf", ModelPath: "/models", Threads: 8,
			Options: []string{"text_bundle:" + text}})).To(Succeed())
		Expect(chunk).To(Equal(int32(32)))
		Expect(textPath).To(Equal(filepath.Join("/models", text)))
	}, Entry("Q8 monolith", "kimodo/text/Llama-3-Kimodo-Q8_0.gguf"), Entry("low-bit monolith", "kimodo/text/Llama-3-Kimodo-Q4_K_M.gguf"), Entry("legacy directory", "kimodo/text"))
	DescribeTable("honors the layer residency option", func(value string, expected int32) {
		backend := &Kimodo{}
		DeferCleanup(backend.Free)
		Expect(backend.Load(&pb.ModelOptions{ModelFile: "motion.gguf", Threads: 8,
			Options: []string{"text_bundle:/custom/encoder.gguf", "text_layer_chunk:" + value}})).To(Succeed())
		Expect(chunk).To(Equal(expected))
		Expect(textPath).To(Equal("/custom/encoder.gguf"))
	}, Entry("single layer", "1", int32(1)), Entry("streaming", "8", int32(8)), Entry("full residency", "32", int32(32)))
	DescribeTable("rejects invalid layer counts before native loading", func(value string) {
		backend := &Kimodo{}
		Expect(backend.Load(&pb.ModelOptions{ModelFile: "motion.gguf", Threads: 8,
			Options: []string{"text_bundle:encoder.gguf", "text_layer_chunk:" + value}})).To(MatchError("text_layer_chunk must be in 1..32"))
		Expect(chunk).To(BeZero())
		Expect(textPath).To(BeEmpty())
	}, Entry("zero", "0"), Entry("negative", "-1"), Entry("too many", "33"), Entry("fractional", "8.5"), Entry("empty", ""))
})

var _ = Describe("skeleton GLB export", func() {
	DescribeTable("writes animation channels for every joint", func(count int) {
		joints := make([]animationJoint, count)
		for index := range joints {
			joints[index] = animationJoint{Name: "joint", Parent: index - 1}
		}
		rotations := make([]float32, 2*count*4)
		for i := 3; i < len(rotations); i += 4 {
			rotations[i] = 1
		}
		path := filepath.Join(GinkgoT().TempDir(), "animation.glb")
		Expect(writeAnimationGLB(path, []float32{0, 0, 0, 1, 2, 3}, rotations, joints)).To(Succeed())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data[:4])).To(Equal("glTF"))
		Expect(binary.LittleEndian.Uint32(data[4:])).To(Equal(uint32(2)))
		Expect(binary.LittleEndian.Uint32(data[8:])).To(Equal(uint32(len(data))))
		jsonLength := int(binary.LittleEndian.Uint32(data[12:]))
		var document struct {
			Nodes      []json.RawMessage   `json:"nodes"`
			Meshes     []json.RawMessage   `json:"meshes"`
			Accessors  []animationAccessor `json:"accessors"`
			Animations []struct {
				Channels []json.RawMessage `json:"channels"`
			} `json:"animations"`
		}
		Expect(json.Unmarshal(data[20:20+jsonLength], &document)).To(Succeed())
		Expect(document.Nodes).To(HaveLen(count))
		Expect(document.Meshes).To(BeEmpty())
		Expect(document.Animations).To(HaveLen(1))
		Expect(document.Animations[0].Channels).To(HaveLen(count + 1))
		Expect(document.Accessors[0].Min).To(Equal([]float32{0}))
		Expect(document.Accessors[0].Max).To(Equal([]float32{1.0 / 30}))
		binaryStart := 28 + jsonLength
		Expect(math.Float32frombits(binary.LittleEndian.Uint32(data[binaryStart+4:]))).To(Equal(float32(1.0 / 30)))
	}, Entry("SMPL-X", 22), Entry("SOMA", 30), Entry("G1", 34))
	It("rejects malformed or non-finite native buffers", func() {
		joint := []animationJoint{{Name: "root", Parent: -1}}
		Expect(writeAnimationGLB("", []float32{0}, nil, joint)).NotTo(Succeed())
		Expect(writeAnimationGLB("", []float32{0, 0, 0}, []float32{0, 0, float32(math.NaN()), 1}, joint)).NotTo(Succeed())
	})
})

var _ = Describe("native resource lifetime", func() {
	It("releases the native motion when exporting fails", func() {
		oldGenerate, oldFree := nativeGenerate, nativeMotionFree
		oldFrames, oldJoints := nativeFrames, nativeJoints
		DeferCleanup(func() {
			nativeGenerate, nativeMotionFree = oldGenerate, oldFree
			nativeFrames, nativeJoints = oldFrames, oldJoints
		})
		nativeGenerate = func(uintptr, string, *generationOptions, *byte, int32) uintptr { return 2 }
		freed := false
		nativeMotionFree = func(handle uintptr) { Expect(handle).To(Equal(uintptr(2))); freed = true }
		nativeFrames = func(uintptr) int32 { return 150 }
		nativeJoints = func(uintptr) int32 { return 999 }
		backend := &Kimodo{model: 1}
		err := backend.Animate3D(&pb.Animate3DRequest{Dst: "unused.glb", Inputs: map[string]*pb.AnimationInput{"prompt": {Type: "text", Data: "walking"}}})
		Expect(err).To(MatchError(ContainSubstring("dimensions")))
		Expect(freed).To(BeTrue())
	})
})

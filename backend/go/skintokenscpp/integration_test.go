// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SkinTokens native ABI", func() {
	It("loads the pinned bridge and refuses a non-model directory", func() {
		library := os.Getenv("SKINTOKENS_LIBRARY")
		if library == "" {
			Skip("set SKINTOKENS_LIBRARY to test the built native library")
		}
		Expect(loadNativeLibrary(library)).To(Succeed())
		s := &SkinTokens{}
		Expect(s.Load(&pb.ModelOptions{ModelFile: filepath.Join(GinkgoT().TempDir(), "missing"), Options: []string{"device:cpu"}})).NotTo(Succeed())
		Expect(s.model).To(BeZero())
		Expect(s.Free()).To(Succeed())
	})
	It("generates learned rig and skin weights while preserving source materials", func() {
		library, bundle, mesh := os.Getenv("SKINTOKENS_LIBRARY"), os.Getenv("SKINTOKENS_TEST_BUNDLE"), os.Getenv("SKINTOKENS_TEST_MESH")
		if library == "" || bundle == "" || mesh == "" {
			Skip("set SKINTOKENS_LIBRARY, SKINTOKENS_TEST_BUNDLE and SKINTOKENS_TEST_MESH for real-model acceptance")
		}
		Expect(loadNativeLibrary(library)).To(Succeed())
		s := &SkinTokens{}
		device := os.Getenv("SKINTOKENS_TEST_DEVICE")
		if device == "" {
			device = "cpu"
		}
		Expect(s.Load(&pb.ModelOptions{ModelFile: bundle, Threads: 4, Options: []string{"device:" + device}})).To(Succeed())
		DeferCleanup(s.Free)
		source, err := readGLB(mesh)
		Expect(err).NotTo(HaveOccurred())
		dir := GinkgoT().TempDir()
		rig := filepath.Join(dir, "rig.glb")
		check := func(output string, animated bool) {
			result, err := readGLB(output)
			Expect(err).NotTo(HaveOccurred())
			skins, err := glbField[[]glbObject](result.root, "skins")
			Expect(err).NotTo(HaveOccurred())
			Expect(skins).NotTo(BeEmpty())
			primitive, _, err := singlePrimitive(result)
			Expect(err).NotTo(HaveOccurred())
			attrs, err := glbField[map[string]int](primitive, "attributes")
			Expect(err).NotTo(HaveOccurred())
			Expect(attrs).To(HaveKey("JOINTS_0"))
			Expect(attrs).To(HaveKey("WEIGHTS_0"))
			for _, key := range []string{"materials", "textures", "samplers"} {
				if len(source.root[key]) > 0 {
					Expect(result.root[key]).To(MatchJSON(string(source.root[key])))
				}
			}
			if animated {
				animations, err := glbField[[]glbObject](result.root, "animations")
				Expect(err).NotTo(HaveOccurred())
				Expect(animations).NotTo(BeEmpty())
			}
		}
		Expect(s.Animate3D(&pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{"mesh": {Type: "mesh", Data: mesh}}, Dst: rig})).To(Succeed())
		check(rig, false)
		skinned := filepath.Join(dir, "skinned.glb")
		Expect(s.Animate3D(&pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{
			"mesh": {Type: "mesh", Data: mesh}, "skeleton": {Type: "mesh", Data: rig},
		}, Dst: skinned})).To(Succeed())
		check(skinned, false)
		if motion := os.Getenv("SKINTOKENS_TEST_MOTION"); motion != "" {
			animated := filepath.Join(dir, "animated.glb")
			Expect(s.Animate3D(&pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{
				"mesh": {Type: "mesh", Data: rig}, "motion": {Type: "motion", Data: motion},
			}, Dst: animated})).To(Succeed())
			check(animated, true)
		}
	})
})

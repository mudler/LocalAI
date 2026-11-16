// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSkinTokens(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "SkinTokens backend") }

var _ = Describe("SkinTokens requests", func() {
	var request *pb.Animate3DRequest
	BeforeEach(func() {
		request = &pb.Animate3DRequest{Inputs: map[string]*pb.AnimationInput{"mesh": {Type: "mesh", Data: "mesh.glb"}}, Dst: "out.glb"}
	})
	It("distinguishes rigging, supplied-skeleton skinning and motion retargeting", func() {
		op, mesh, secondary, err := validateRequest(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(op).To(Equal("rig"))
		Expect(mesh).To(Equal("mesh.glb"))
		Expect(secondary).To(BeEmpty())
		request.Inputs["skeleton"] = &pb.AnimationInput{Type: "motion", Data: "motion.glb"}
		op, _, secondary, err = validateRequest(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(op).To(Equal("skin"))
		Expect(secondary).To(Equal("motion.glb"))
		delete(request.Inputs, "skeleton")
		request.Inputs["motion"] = &pb.AnimationInput{Type: "motion", Data: "motion.glb"}
		op, _, _, err = validateRequest(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(op).To(Equal("retarget"))
	})
	It("rejects unknown parameters, unsafe strings and malformed input combinations", func() {
		for _, params := range []map[string]string{{"seed": "-1"}, {"max_tokens": "7"}, {"temperature": "NaN"}, {"top_p": "1.1"}, {"fit": "global"}, {"unknown": "x"}} {
			request.Params = params
			_, _, _, err := validateRequest(request)
			Expect(err).To(HaveOccurred())
		}
		request.Params = nil
		request.Inputs["mesh"].Data = "mesh\x00.glb"
		_, _, _, err := validateRequest(request)
		Expect(err).To(HaveOccurred())
		_, _, _, err = validateRequest(nil)
		Expect(err).To(HaveOccurred())
		request.Inputs["mesh"].Data = "mesh.glb"
		request.Inputs["prompt"] = &pb.AnimationInput{Type: "text", Data: "walk"}
		_, _, _, err = validateRequest(request)
		Expect(err).To(HaveOccurred())
	})
	It("does not permit lowering retarget confidence through unknown parameters", func() {
		request.Inputs["motion"] = &pb.AnimationInput{Type: "motion", Data: "motion.glb"}
		request.Params = map[string]string{"minimum_confidence": "0"}
		_, _, _, err := validateRequest(request)
		Expect(err).To(HaveOccurred())
	})
	It("refuses empty model probes and invalid devices before native loading", func() {
		s := &SkinTokens{}
		Expect(s.Load(&pb.ModelOptions{})).NotTo(Succeed())
		Expect(s.Load(&pb.ModelOptions{ModelFile: "bundle", Options: []string{"device:invalid"}})).NotTo(Succeed())
	})
	It("does not publish a partial output or silently accept geometric weights", func() {
		oldRun := nativeRun
		DeferCleanup(func() { nativeRun = oldRun })
		request.Dst = filepath.Join(GinkgoT().TempDir(), "result.glb")
		nativeRun = func(_ uintptr, _, _, _, output, _ string, _ *int32, _ *byte, _ uintptr) int32 {
			Expect(os.WriteFile(output, []byte("partial"), 0600)).To(Succeed())
			return 0
		}
		s := &SkinTokens{model: 42}
		Expect(s.Animate3D(request)).To(MatchError(ContainSubstring("rather than learned")))
		_, err := os.Stat(request.Dst)
		Expect(os.IsNotExist(err)).To(BeTrue())
		files, err := os.ReadDir(filepath.Dir(request.Dst))
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
	It("retains a loaded model when replacement fails and frees it exactly once", func() {
		oldLoad, oldFree := nativeLoad, nativeFree
		DeferCleanup(func() { nativeLoad, nativeFree = oldLoad, oldFree })
		var freed []uintptr
		nativeFree = func(handle uintptr) { freed = append(freed, handle) }
		nativeLoad = func(_, _ string, _ uint32, _ string, _ *uintptr, _ *byte, _ uintptr) int32 { return 5 }
		s := &SkinTokens{model: 42}
		Expect(s.Load(&pb.ModelOptions{ModelFile: "not-a-bundle"})).NotTo(Succeed())
		Expect(s.model).To(Equal(uintptr(42)))
		Expect(freed).To(BeEmpty())
		Expect(s.Free()).To(Succeed())
		Expect(s.Free()).To(Succeed())
		Expect(freed).To(Equal([]uintptr{42}))
	})
})

// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("native request controls", Label("native-controls"), Serial, func() {
	var backend *OmnivoiceCpp
	var req *pb.TTSRequest
	var reset func()
	var calls, frees, defaults, frames, postproc, otherDefaults func() int
	BeforeEach(func() {
		path := os.Getenv("OMNIVOICE_TEST_LIBRARY")
		Expect(path).NotTo(BeEmpty(), "run verify-controls.sh to compile the actual wrapper fixture")
		lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
		Expect(err).NotTo(HaveOccurred())
		oldLoad, oldTTS, oldStream, oldFree, oldUnload := CppLoad, CppTTS, CppTTSStream, CppPCMFree, CppUnload
		DeferCleanup(func() {
			CppUnload()
			reset()
			CppLoad, CppTTS, CppTTSStream, CppPCMFree, CppUnload = oldLoad, oldTTS, oldStream, oldFree, oldUnload
			Expect(purego.Dlclose(lib)).To(Succeed())
		})
		purego.RegisterLibFunc(&CppLoad, lib, "omni_load")
		purego.RegisterLibFunc(&CppTTS, lib, "omni_tts")
		purego.RegisterLibFunc(&CppTTSStream, lib, "omni_tts_stream")
		purego.RegisterLibFunc(&CppPCMFree, lib, "omni_pcm_free")
		purego.RegisterLibFunc(&CppUnload, lib, "omni_unload")
		purego.RegisterLibFunc(&reset, lib, "fixture_reset")
		purego.RegisterLibFunc(&calls, lib, "fixture_calls")
		purego.RegisterLibFunc(&frees, lib, "fixture_frees")
		purego.RegisterLibFunc(&defaults, lib, "fixture_defaults")
		purego.RegisterLibFunc(&frames, lib, "fixture_frames")
		purego.RegisterLibFunc(&postproc, lib, "fixture_postproc")
		purego.RegisterLibFunc(&otherDefaults, lib, "fixture_other_defaults")
		reset()
		Expect(CppLoad("fixture-model", "fixture-codec", 1, 0)).To(BeZero())
		backend = &OmnivoiceCpp{opts: parseOptions(nil)}
		dir, err := os.MkdirTemp("", "omnivoice-controls-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
		req = &pb.TTSRequest{Text: "Fixture speech", Dst: filepath.Join(dir, "out.wav")}
	})
	It("preserves native defaults and frees engine and wrapper PCM", func() {
		nativeFree := CppPCMFree
		wrapperFrees := 0
		CppPCMFree = func(p uintptr) { wrapperFrees++; nativeFree(p) }
		for _, params := range []map[string]string{nil, {}, {"unrelated": "ignored"}} {
			req.Params = params
			Expect(backend.TTS(req)).To(Succeed())
			Expect(frames()).To(BeZero())
			Expect(postproc()).To(Equal(1))
			Expect(otherDefaults()).To(Equal(1))
			audio, err := readWAVAsFloat(req.Dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(audio).To(HaveLen(2))
			Expect(audio[0]).To(BeNumerically("~", .25, .001))
		}
		Expect(calls()).To(Equal(3))
		Expect(defaults()).To(Equal(3))
		Expect(frees()).To(Equal(3))
		Expect(wrapperFrees).To(Equal(3))
	})
	It("passes duration to the actual engine struct", func() {
		req.Params = map[string]string{"duration": "2.5"}
		Expect(backend.TTS(req)).To(Succeed())
		Expect(frames()).To(Equal(62))
	})
	It("passes buffered false without duration", func() {
		req.Params = map[string]string{"postprocess": "false"}
		Expect(backend.TTS(req)).To(Succeed())
		Expect(postproc()).To(BeZero())
	})
	It("rejects invalid duration before synthesis or output", func() {
		req.Params = map[string]string{"duration": "NaN"}
		Expect(backend.TTS(req)).NotTo(Succeed())
		Expect(calls()).To(BeZero())
		_, err := os.Stat(req.Dst)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
	It("rejects streaming false without header or native calls", func() {
		req.Params = map[string]string{"postprocess": "false"}
		results := make(chan []byte, 4)
		Expect(backend.TTSStream(req, results)).NotTo(Succeed())
		Expect(calls()).To(BeZero())
		_, open := <-results
		Expect(open).To(BeFalse())
	})
})

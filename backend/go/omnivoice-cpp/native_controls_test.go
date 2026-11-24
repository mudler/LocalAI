//go:build native_controls

// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

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
	var seed func() uint64
	var denoise, refN func() int
	var refFirst func() float32
	var text, lang, instruct, refText func() string
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
		purego.RegisterLibFunc(&seed, lib, "fixture_seed")
		purego.RegisterLibFunc(&denoise, lib, "fixture_denoise")
		purego.RegisterLibFunc(&refN, lib, "fixture_ref_n")
		purego.RegisterLibFunc(&refFirst, lib, "fixture_ref_first")
		purego.RegisterLibFunc(&text, lib, "fixture_text")
		purego.RegisterLibFunc(&lang, lib, "fixture_lang")
		purego.RegisterLibFunc(&instruct, lib, "fixture_instruct")
		purego.RegisterLibFunc(&refText, lib, "fixture_ref_text")
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
	It("preserves argument positions and resets controls between calls", func() {
		language, instruction := "English", "soft"
		req.Language, req.Instructions = &language, &instruction
		req.Voice = filepath.Join(filepath.Dir(req.Dst), "ref.wav")
		Expect(writeWAV24k(req.Voice, []float32{.5, -.5, .25})).To(Succeed())
		backend.opts.denoise = false
		for _, flag := range []string{"false", "true"} {
			req.Params = map[string]string{"duration": "30", "postprocess": flag, "seed": "12345678901", "ref_text": "Reference transcript"}
			Expect(backend.TTS(req)).To(Succeed())
			Expect(frames()).To(Equal(750))
			Expect(postproc()).To(Equal(boolToInt(flag == "true")))
			Expect(seed()).To(Equal(uint64(12345678901)))
			Expect(denoise()).To(BeZero())
			Expect(refN()).To(Equal(3))
			Expect(refFirst()).To(BeNumerically("~", .5, .001))
			Expect(text()).To(Equal(req.Text))
			Expect(lang()).To(Equal("en"))
			Expect(instruct()).To(Equal(instruction))
			Expect(refText()).To(Equal("Reference transcript"))
			Expect(otherDefaults()).To(Equal(1))
		}
		req.Params = nil
		req.Voice = ""
		Expect(backend.TTS(req)).To(Succeed())
		Expect(frames()).To(BeZero())
		Expect(postproc()).To(Equal(1))
		Expect(seed()).To(Equal(uint64(42)))
		Expect(denoise()).To(Equal(1))
		Expect(refN()).To(BeZero())
		Expect(defaults()).To(Equal(3))
	})
	It("streams duration and true while preserving defaults and PCM callbacks", func() {
		for _, params := range []map[string]string{{"duration": "2.5", "postprocess": "true"}, {"duration": "1"}, nil} {
			req.Params = params
			results := make(chan []byte, 4)
			Expect(backend.TTSStream(req, results)).To(Succeed())
			var chunks [][]byte
			for chunk := range results {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0]).To(Equal(wavHeader24k()))
			Expect(chunks[1]).To(Equal(floatToPCM16LE([]float32{.25, -.25})))
			want := 0
			if params["duration"] == "2.5" {
				want = 62
			} else if params["duration"] == "1" {
				want = 25
			}
			Expect(frames()).To(Equal(want))
			Expect(postproc()).To(Equal(1))
			Expect(otherDefaults()).To(Equal(1))
		}
		Expect(calls()).To(Equal(3))
		Expect(frees()).To(Equal(3))
	})
	It("rejects invalid controls before reference reads, callbacks, or output", func() {
		req.Voice = filepath.Join(filepath.Dir(req.Dst), "invalid.wav")
		Expect(os.WriteFile(req.Voice, []byte("not a WAV"), 0600)).To(Succeed())
		savedPtr := streamCbPtr
		savedChan := streamChan
		// Preserve the once state without copying its mutex.
		initialized := false
		streamCbOnce.Do(func() { initialized = true })
		streamCbOnce = sync.Once{}
		streamCbPtr = 0
		DeferCleanup(func() {
			streamCbPtr, streamChan = savedPtr, savedChan
			streamCbOnce = sync.Once{}
			if !initialized {
				streamCbOnce.Do(func() {})
			}
		})
		cases := []map[string]string{}
		for _, value := range invalidDurations {
			cases = append(cases, map[string]string{"duration": value})
		}
		for _, value := range invalidPostprocess {
			cases = append(cases, map[string]string{"postprocess": value})
		}
		for _, params := range cases {
			req.Params = params
			key := "duration"
			if _, ok := params["postprocess"]; ok {
				key = "postprocess"
			}
			Expect(backend.TTS(req)).To(MatchError(ContainSubstring(key)))
			results := make(chan []byte, 4)
			Expect(backend.TTSStream(req, results)).To(MatchError(ContainSubstring(key)))
			_, open := <-results
			Expect(open).To(BeFalse())
			Expect(streamCbPtr).To(BeZero())
			Expect(streamChan).To(BeNil())
		}
		req.Params = map[string]string{"postprocess": "false"}
		results := make(chan []byte, 4)
		Expect(backend.TTSStream(req, results)).To(MatchError(ContainSubstring("postprocess=false")))
		_, open := <-results
		Expect(open).To(BeFalse())
		Expect(streamCbPtr).To(BeZero())
		Expect(calls()).To(BeZero())
		Expect(defaults()).To(BeZero())
		_, err := os.Stat(req.Dst)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
	It("defensively rejects direct native invalid controls", func() {
		streamCbOnce.Do(func() { streamCbPtr = purego.NewCallback(streamCallback) })
		for _, pair := range [][2]int32{{-1, -1}, {751, -1}, {2147483647, -1}, {0, -2}, {0, 2}} {
			n := int32(99)
			ptr := CppTTS("text", "", "", nil, 0, "", -1, 1, pair[0], pair[1], unsafe.Pointer(&n))
			Expect(ptr).To(BeZero())
			Expect(n).To(BeZero())
			Expect(CppTTSStream("text", "", "", nil, 0, "", -1, 1, pair[0], pair[1], streamCbPtr, 0)).NotTo(BeZero())
		}
		Expect(CppTTSStream("text", "", "", nil, 0, "", -1, 1, 25, 0, streamCbPtr, 0)).NotTo(BeZero())
		Expect(calls()).To(BeZero())
		Expect(defaults()).To(BeZero())
	})
})

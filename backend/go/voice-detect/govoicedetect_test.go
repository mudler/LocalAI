package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVoiceDetect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "voice-detect Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the C-API
// bridge without spinning up the gRPC server. Records the error (the smoke
// specs skip themselves) when libvoicedetect.so is not loadable from cwd
// (LD_LIBRARY_PATH or a symlink in ./).
func ensureLibLoaded() error {
	libLoadOnce.Do(func() {
		libName := os.Getenv("VOICEDETECT_LIBRARY")
		if libName == "" {
			libName = "libvoicedetect.so"
		}
		lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppAbiVersion, lib, "voicedetect_capi_abi_version")
		purego.RegisterLibFunc(&CppLoad, lib, "voicedetect_capi_load")
		purego.RegisterLibFunc(&CppFree, lib, "voicedetect_capi_free")
		purego.RegisterLibFunc(&CppLastError, lib, "voicedetect_capi_last_error")
		purego.RegisterLibFunc(&CppFreeString, lib, "voicedetect_capi_free_string")
		purego.RegisterLibFunc(&CppFreeVec, lib, "voicedetect_capi_free_vec")
		purego.RegisterLibFunc(&CppEmbedPath, lib, "voicedetect_capi_embed_path")
		purego.RegisterLibFunc(&CppEmbedPCM, lib, "voicedetect_capi_embed_pcm")
		purego.RegisterLibFunc(&CppVerifyPaths, lib, "voicedetect_capi_verify_paths")
		purego.RegisterLibFunc(&CppAnalyzeJSON, lib, "voicedetect_capi_analyze_path_json")
	})
	return libLoadErr
}

var _ = Describe("parseOptions", func() {
	It("defaults verify_threshold to 0.25", func() {
		o := parseOptions(nil)
		Expect(o.verifyThreshold).To(Equal(float32(0.25)))
		Expect(o.modelName).To(Equal(""))
	})

	It("parses verify_threshold, threshold alias and model_name", func() {
		o := parseOptions([]string{"verify_threshold:0.4", "model_name:ecapa", "unknown:x"})
		Expect(o.verifyThreshold).To(Equal(float32(0.4)))
		Expect(o.modelName).To(Equal("ecapa"))

		o2 := parseOptions([]string{"threshold:0.3"})
		Expect(o2.verifyThreshold).To(Equal(float32(0.3)))
	})

	It("ignores non-positive thresholds and keeps the default", func() {
		o := parseOptions([]string{"verify_threshold:0", "threshold:-1"})
		Expect(o.verifyThreshold).To(Equal(float32(0.25)))
	})
})

var _ = Describe("parseAnalyzeJSON", func() {
	It("maps age, gender label+scores and emotion label+scores", func() {
		doc := `{"age":42.0,
			"gender":{"label":"female","female":0.88,"male":0.12},
			"emotion":{"label":"neutral","scores":{"neutral":0.7,"happy":0.2,"sad":0.1}}}`
		seg, err := parseAnalyzeJSON(doc)
		Expect(err).ToNot(HaveOccurred())
		Expect(seg.Age).To(BeNumerically("~", 42.0, 1e-4))
		Expect(seg.Start).To(Equal(float32(0)))
		Expect(seg.End).To(Equal(float32(0)))

		Expect(seg.DominantGender).To(Equal("female"))
		Expect(seg.Gender).To(HaveKeyWithValue("female", BeNumerically("~", 0.88, 1e-4)))
		Expect(seg.Gender).To(HaveKeyWithValue("male", BeNumerically("~", 0.12, 1e-4)))
		// The "label" entry is consumed into DominantGender, not the score map.
		Expect(seg.Gender).ToNot(HaveKey("label"))

		Expect(seg.DominantEmotion).To(Equal("neutral"))
		Expect(seg.Emotion).To(HaveKeyWithValue("neutral", BeNumerically("~", 0.7, 1e-4)))
		Expect(seg.Emotion).To(HaveKeyWithValue("happy", BeNumerically("~", 0.2, 1e-4)))
	})

	It("tolerates a missing gender block", func() {
		seg, err := parseAnalyzeJSON(`{"age":30.0,"emotion":{"label":"happy","scores":{"happy":1.0}}}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(seg.DominantGender).To(Equal(""))
		Expect(seg.DominantEmotion).To(Equal("happy"))
	})

	It("returns an error on malformed JSON", func() {
		_, err := parseAnalyzeJSON(`{not-json`)
		Expect(err).To(HaveOccurred())
	})
})

// The specs below exercise the real C-API end to end. They run only when both a
// model GGUF and a test WAV are provided, and skip cleanly otherwise so the
// suite stays green without large assets.
var _ = Describe("VoiceDetect end-to-end", Ordered, func() {
	var (
		v         *VoiceDetect
		modelPath = os.Getenv("VOICEDETECT_BACKEND_TEST_MODEL")
		wavPath   = os.Getenv("VOICEDETECT_BACKEND_TEST_WAV")
	)

	BeforeAll(func() {
		if modelPath == "" || wavPath == "" {
			Skip("set VOICEDETECT_BACKEND_TEST_MODEL and VOICEDETECT_BACKEND_TEST_WAV to run the e2e specs")
		}
		if err := ensureLibLoaded(); err != nil {
			Skip("libvoicedetect.so not loadable: " + err.Error())
		}
		v = &VoiceDetect{}
		Expect(v.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
	})

	It("embeds an audio clip", func() {
		resp, err := v.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: wavPath})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Embedding).ToNot(BeEmpty())
		Expect(resp.Model).ToNot(BeEmpty())
	})

	It("verifies a clip against itself as the same speaker", func() {
		resp, err := v.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: wavPath, Audio2: wavPath})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Verified).To(BeTrue())
		Expect(resp.Distance).To(BeNumerically("<=", resp.Threshold))
	})
})

// cstr returns a NUL-terminated copy of s and its address, the way the library
// hands out a borrowed char*. keep holds the buffer so the GC does not drop it.
func cstr(s string) (ptr uintptr, keep []byte) {
	b := append([]byte(s), 0)
	return uintptr(unsafe.Pointer(&b[0])), b
}

// stubEncoder replaces the encoder accessors with fakes that return the given
// strings (nil pointer when the value is nil) and restores them after the spec.
func stubEncoder(arch, name, family *string) {
	oldA, oldN, oldF := CppEncoderArch, CppEncoderName, CppEncoderFamily
	DeferCleanup(func() { CppEncoderArch, CppEncoderName, CppEncoderFamily = oldA, oldN, oldF })
	mk := func(s *string) func(uintptr) uintptr {
		if s == nil {
			return func(uintptr) uintptr { return 0 }
		}
		ptr, keep := cstr(*s)
		return func(uintptr) uintptr { _ = keep; return ptr }
	}
	CppEncoderArch, CppEncoderName, CppEncoderFamily = mk(arch), mk(name), mk(family)
}

var _ = Describe("encoder family", func() {
	str := func(s string) *string { return &s }

	It("reads the family the library reports", func() {
		stubEncoder(str("ecapa_tdnn"), str("speechbrain/spkrec-ecapa-voxceleb"), str("voicedetect:ecapa_tdnn:speechbrain/spkrec-ecapa-voxceleb:192"))
		Expect(readEncoderFamily(1)).To(Equal("voicedetect:ecapa_tdnn:speechbrain/spkrec-ecapa-voxceleb:192"))
		Expect(readBorrowed(CppEncoderArch, 1)).To(Equal("ecapa_tdnn"))
		Expect(readBorrowed(CppEncoderName, 1)).To(Equal("speechbrain/spkrec-ecapa-voxceleb"))
	})

	It("is empty when the library lacks the symbols", func() {
		oldA, oldN, oldF := CppEncoderArch, CppEncoderName, CppEncoderFamily
		DeferCleanup(func() { CppEncoderArch, CppEncoderName, CppEncoderFamily = oldA, oldN, oldF })
		CppEncoderArch, CppEncoderName, CppEncoderFamily = nil, nil, nil
		Expect(readEncoderFamily(1)).To(BeEmpty())
		Expect(readBorrowed(CppEncoderArch, 1)).To(BeEmpty())
	})

	It("is empty on a NULL pointer", func() {
		stubEncoder(nil, nil, nil)
		Expect(readEncoderFamily(1)).To(BeEmpty())
	})

	It("is empty on an empty string and on a family with every field missing", func() {
		stubEncoder(str(""), str(""), str(""))
		Expect(readEncoderFamily(1)).To(BeEmpty())
		stubEncoder(nil, nil, str(":::"))
		Expect(readEncoderFamily(1)).To(BeEmpty())
	})

	It("keeps a family with a missing field (colons kept)", func() {
		stubEncoder(nil, nil, str("voicedetect:ecapa_tdnn::192"))
		Expect(readEncoderFamily(1)).To(Equal("voicedetect:ecapa_tdnn::192"))
	})

	It("does not read for a NULL context", func() {
		called := false
		CppEncoderFamily = func(uintptr) uintptr { called = true; return 0 }
		DeferCleanup(func() { CppEncoderFamily = nil })
		Expect(readEncoderFamily(0)).To(BeEmpty())
		Expect(called).To(BeFalse())
	})
})

var _ = Describe("fileIdentity", func() {
	It("is the sha256 of the file bytes", func() {
		path := filepath.Join(GinkgoT().TempDir(), "m.gguf")
		Expect(os.WriteFile(path, []byte("weights"), 0o600)).To(Succeed())
		sum := sha256.Sum256([]byte("weights"))
		Expect(fileIdentity(path)).To(Equal("sha256:" + hex.EncodeToString(sum[:])))
	})
	It("is empty for a missing path and for a directory", func() {
		dir := GinkgoT().TempDir()
		Expect(fileIdentity(filepath.Join(dir, "none.gguf"))).To(BeEmpty())
		Expect(fileIdentity(dir)).To(BeEmpty())
	})
})

var _ = Describe("VoiceEmbed fingerprint", func() {
	stubEmbed := func() {
		oldP, oldF := CppEmbedPath, CppFreeVec
		DeferCleanup(func() { CppEmbedPath, CppFreeVec = oldP, oldF })
		vec := []float32{0.6, 0.8}
		CppEmbedPath = func(_ uintptr, _ string, outVec, outDim unsafe.Pointer) int32 {
			*(*uintptr)(outVec) = uintptr(unsafe.Pointer(&vec[0]))
			*(*int32)(outDim) = int32(len(vec))
			return 0
		}
		CppFreeVec = func(uintptr) {}
	}

	It("returns the family and the weights next to the embedding", func() {
		stubEmbed()
		v := &VoiceDetect{ctxPtr: 1, encoderFamily: "voicedetect:a:b:2", encoderWeights: "sha256:ab"}
		v.opts.modelName = "m.gguf"
		resp, err := v.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: "x.wav"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Embedding).To(Equal([]float32{0.6, 0.8}))
		Expect(resp.Model).To(Equal("m.gguf"))
		Expect(resp.EncoderFamily).To(Equal("voicedetect:a:b:2"))
		Expect(resp.EncoderWeights).To(Equal("sha256:ab"))
	})

	It("leaves both empty on a library that cannot report them", func() {
		stubEmbed()
		v := &VoiceDetect{ctxPtr: 1}
		resp, err := v.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: "x.wav"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.EncoderFamily).To(BeEmpty())
		Expect(resp.EncoderWeights).To(BeEmpty())
	})
})

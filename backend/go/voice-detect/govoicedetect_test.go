package main

import (
	"os"
	"sync"
	"testing"

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

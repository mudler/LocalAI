package main

import (
	"os"
	"path/filepath"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ = Describe("ParakeetCpp.VoiceEmbed", func() {
	var (
		restore func()
		pool    *diarizeCstrPool
		keep    [][]float32 // the fake "C" vectors, alive until freed
		freed   []uintptr
		got     struct {
			ctx  uintptr
			n    int32
			rate int32
		}
		vector []float32
		rc     int32
	)
	BeforeEach(func() {
		sEmbed, sFree, sID, sErr := CppSpeakerEmbedPCM, CppFreeFloats, CppSpeakerIdentity, CppLastError
		restore = func() { CppSpeakerEmbedPCM, CppFreeFloats, CppSpeakerIdentity, CppLastError = sEmbed, sFree, sID, sErr }
		pool = &diarizeCstrPool{}
		keep, freed, vector, rc = nil, nil, []float32{0.5, 0.25, 0.125}, 0
		got.ctx, got.n, got.rate = 0, 0, 0
		CppSpeakerEmbedPCM = func(spk uintptr, pcm *float32, n, rate int32, out, dim unsafe.Pointer) int32 {
			got.ctx, got.n, got.rate = spk, n, rate
			if rc != 0 {
				return rc
			}
			v := append([]float32(nil), vector...)
			keep = append(keep, v)
			*(*uintptr)(out) = uintptr(unsafe.Pointer(&v[0]))
			*(*int32)(dim) = int32(len(v))
			return 0
		}
		CppFreeFloats = func(p uintptr) { freed = append(freed, p) }
		CppSpeakerIdentity = func(uintptr) uintptr { return pool.cstr("sha256:abcd") }
		CppLastError = func(uintptr) string { return "stub failure" }
	})
	AfterEach(func() { restore() })

	It("embeds the decoded clip with the speaker context and frees the C vector", func() {
		p := &ParakeetCpp{spkCtx: 9}
		res, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: diarizeWav(1)})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Embedding).To(Equal([]float32{0.5, 0.25, 0.125}))
		Expect(res.Model).To(Equal("sha256:abcd"))
		Expect(got.ctx).To(Equal(uintptr(9)))
		Expect(got.n).To(Equal(int32(16000)))
		Expect(got.rate).To(Equal(int32(16000)))
		Expect(freed).To(HaveLen(1))
		Expect(freed[0]).To(Equal(uintptr(unsafe.Pointer(&keep[0][0]))))
	})

	It("fails with FailedPrecondition when the model has no speaker encoder", func() {
		_, err := (&ParakeetCpp{}).VoiceEmbed(&pb.VoiceEmbedRequest{Audio: diarizeWav(1)})
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("speaker"))
	})

	It("fails with Unimplemented on a library without the embed symbols", func() {
		CppSpeakerEmbedPCM, CppFreeFloats = nil, nil
		_, err := (&ParakeetCpp{spkCtx: 9}).VoiceEmbed(&pb.VoiceEmbedRequest{Audio: diarizeWav(1)})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(err.Error()).To(ContainSubstring("parakeet_capi_speaker_embed_pcm"))
	})

	It("rejects a missing path, an undecodable file and empty audio", func() {
		p := &ParakeetCpp{spkCtx: 9}
		_, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		bad := filepath.Join(GinkgoT().TempDir(), "bad.wav")
		Expect(os.WriteFile(bad, []byte("not audio"), 0o600)).To(Succeed())
		_, err = p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: bad})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		empty := filepath.Join(GinkgoT().TempDir(), "empty.wav")
		writeMono16kWav(empty, 0)
		_, err = p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: empty})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(got.n).To(BeZero(), "the encoder must not run on invalid audio")
	})

	It("returns the library error when the encoder fails", func() {
		rc = 1
		_, err := (&ParakeetCpp{spkCtx: 9}).VoiceEmbed(&pb.VoiceEmbedRequest{Audio: diarizeWav(1)})
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(err.Error()).To(ContainSubstring("stub failure"))
	})

	Describe("VoiceVerify", func() {
		It("accepts the same voice and rejects a different one", func() {
			p := &ParakeetCpp{spkCtx: 9}
			res, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: diarizeWav(1), Audio2: diarizeWav(1)})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Verified).To(BeTrue())
			Expect(res.Distance).To(BeNumerically("~", 0, 1e-6))
			Expect(res.Threshold).To(BeNumerically("~", 0.5, 1e-6))
			Expect(res.Confidence).To(BeNumerically("~", 100, 1e-3))

			vectors := [][]float32{{1, 0, 0}, {0, 1, 0}}
			calls := 0
			CppSpeakerEmbedPCM = func(_ uintptr, _ *float32, _, _ int32, out, dim unsafe.Pointer) int32 {
				v := vectors[calls%2]
				calls++
				keep = append(keep, v)
				*(*uintptr)(out) = uintptr(unsafe.Pointer(&v[0]))
				*(*int32)(dim) = 3
				return 0
			}
			res, err = p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: diarizeWav(1), Audio2: diarizeWav(1), Threshold: 0.3})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Verified).To(BeFalse())
			Expect(res.Distance).To(BeNumerically("~", 1, 1e-6))
			Expect(res.Threshold).To(BeNumerically("~", 0.3, 1e-6))
		})

		It("refuses anti-spoofing and missing clips", func() {
			p := &ParakeetCpp{spkCtx: 9}
			_, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: "a", Audio2: "b", AntiSpoofing: true})
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			_, err = p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: "a"})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})
})

// PARAKEET_BACKEND_TEST_BUNDLE (or ..._SPEAKER_MODEL) points at a GGUF with a speaker
// encoder and PARAKEET_BACKEND_TEST_WAV at any speech WAV; the spec needs a libparakeet.so
// that exports parakeet_capi_speaker_embed_pcm (PARAKEET_LIBRARY).
var _ = Describe("ParakeetCpp.VoiceEmbed (real libparakeet.so)", func() {
	It("embeds a clip, repeats exactly and reports the encoder identity", func() {
		model := os.Getenv("PARAKEET_BACKEND_TEST_SPEAKER_BUNDLE")
		wav := os.Getenv("PARAKEET_BACKEND_TEST_WAV")
		if model == "" || wav == "" {
			Skip("set PARAKEET_BACKEND_TEST_SPEAKER_BUNDLE (a bundle GGUF with a voice component) and PARAKEET_BACKEND_TEST_WAV")
		}
		ensureLibLoaded()
		if CppSpeakerEmbedPCM == nil {
			Skip("libparakeet.so has no parakeet_capi_speaker_embed_pcm")
		}
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: model, Options: []string{"speaker_component:voice"}})).To(Succeed())
		defer func() { _ = p.Free() }()

		first, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: wav})
		Expect(err).ToNot(HaveOccurred())
		Expect(first.Embedding).ToNot(BeEmpty())
		Expect(first.Model).To(HavePrefix("sha256:"))
		again, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: wav})
		Expect(err).ToNot(HaveOccurred())
		Expect(cosineDistance(first.Embedding, again.Embedding)).To(BeNumerically("<", 1e-4))
	})
})

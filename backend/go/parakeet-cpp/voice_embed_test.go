package main

import (
	"os"
	"os/exec"
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

		It("uses voice_verify_threshold when the request has none and rejects a bad value", func() {
			p := &ParakeetCpp{spkCtx: 9, verifyDistance: 0.2}
			res, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: diarizeWav(1), Audio2: diarizeWav(1)})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Threshold).To(BeNumerically("~", 0.2, 1e-6))
			for _, bad := range []string{"x", "0", "2", "-1"} {
				_, err := parseVerifyThreshold(bad)
				Expect(err).To(HaveOccurred(), bad)
			}
			v, err := parseVerifyThreshold("")
			Expect(err).ToNot(HaveOccurred())
			Expect(v).To(BeZero())
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

// The real-library spec needs a libparakeet.so that exports parakeet_capi_speaker_embed_pcm
// (PARAKEET_LIBRARY), a bundle GGUF with a diar and a voice component
// (PARAKEET_BACKEND_TEST_SPEAKER_BUNDLE) and parakeet.cpp's tests/fixtures/two_speakers.wav
// (PARAKEET_BACKEND_TEST_WAV): voice A speaks 0.5-5.5 s and voice B 6.9-13.5 s. ffmpeg cuts the clips.
var _ = Describe("ParakeetCpp.VoiceEmbed (real libparakeet.so)", func() {
	It("embeds deterministically, scores the same voice above another and verifies", func() {
		bundle := os.Getenv("PARAKEET_BACKEND_TEST_SPEAKER_BUNDLE")
		wav := os.Getenv("PARAKEET_BACKEND_TEST_WAV")
		if bundle == "" || wav == "" {
			Skip("set PARAKEET_BACKEND_TEST_SPEAKER_BUNDLE (a bundle GGUF with diar and voice components) and PARAKEET_BACKEND_TEST_WAV (two_speakers.wav)")
		}
		ensureLibLoaded()
		if CppSpeakerEmbedPCM == nil {
			Skip("libparakeet.so has no parakeet_capi_speaker_embed_pcm")
		}
		dir := GinkgoT().TempDir()
		clip := func(name string, start, dur string) string {
			out := filepath.Join(dir, name+".wav")
			cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-ss", start, "-t", dur, "-i", wav, "-ar", "16000", "-ac", "1", out)
			Expect(cmd.Run()).To(Succeed())
			return out
		}
		a1, a2, b1 := clip("a1", "0.5", "4"), clip("a2", "14.8", "3.5"), clip("b1", "7", "4")

		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: bundle, Options: []string{"diar_component:diar", "speaker_component:voice"}})).To(Succeed())
		defer func() { _ = p.Free() }()

		first, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: a1})
		Expect(err).ToNot(HaveOccurred())
		Expect(first.Embedding).To(HaveLen(256))
		Expect(first.Model).To(HavePrefix("sha256:"))
		again, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: a1})
		Expect(err).ToNot(HaveOccurred())
		Expect(cosineDistance(first.Embedding, again.Embedding)).To(BeNumerically("<", 1e-5))

		other, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: a2})
		Expect(err).ToNot(HaveOccurred())
		diff, err := p.VoiceEmbed(&pb.VoiceEmbedRequest{Audio: b1})
		Expect(err).ToNot(HaveOccurred())
		same, different := cosineDistance(first.Embedding, other.Embedding), cosineDistance(first.Embedding, diff.Embedding)
		GinkgoWriter.Printf("model %s dim %d same-voice distance %.4f different-voice distance %.4f\n", first.Model, len(first.Embedding), same, different)
		Expect(same).To(BeNumerically("<", different))

		ok, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: a1, Audio2: a2})
		Expect(err).ToNot(HaveOccurred())
		Expect(ok.Verified).To(BeTrue(), "distance %.4f", ok.Distance)
		no, err := p.VoiceVerify(&pb.VoiceVerifyRequest{Audio1: a1, Audio2: b1})
		Expect(err).ToNot(HaveOccurred())
		Expect(no.Verified).To(BeFalse(), "distance %.4f", no.Distance)
		GinkgoWriter.Printf("verify same %.4f (%v) different %.4f (%v)\n", ok.Distance, ok.Verified, no.Distance, no.Verified)
	})
})

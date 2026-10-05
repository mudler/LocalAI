package main

import (
	"fmt"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fpAdd struct{ key, family, weights string }

const (
	famECAPA  = "voicedetect:ecapa_tdnn:speechbrain/spkrec-ecapa-voxceleb:192"
	famCAMPP  = "voicedetect:campplus:3dspeaker/campplus:192"
	hashLoad  = "sha256:aaaa"
	hashOther = "sha256:bbbb"
)

// The fingerprint specs run against stubbed C entry points, like the registry
// specs in speaker_registry_test.go.
var _ = Describe("speaker registry encoder fingerprint", func() {
	var (
		restore   func()
		pool      *diarizeCstrPool
		plain     []string
		fp        []fpAdd
		strictSet []int32
		freed     []uintptr
		fpFails   bool
	)
	BeforeEach(func() {
		sNew, sFree, sAdd, sAddFP, sDim, sErr := CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerRegistryAddEmbeddingFP, CppSpeakerDim, CppSpeakerRegistryLastError
		sStrict, sFam, sID := CppSpeakerRegistrySetStrict, CppSpeakerEncoderFamily, CppSpeakerIdentity
		restore = func() {
			CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerRegistryAddEmbeddingFP, CppSpeakerDim, CppSpeakerRegistryLastError = sNew, sFree, sAdd, sAddFP, sDim, sErr
			CppSpeakerRegistrySetStrict, CppSpeakerEncoderFamily, CppSpeakerIdentity = sStrict, sFam, sID
		}
		pool = &diarizeCstrPool{}
		plain, fp, strictSet, freed, fpFails = nil, nil, nil, nil, false
		CppSpeakerDim = func(uintptr) int32 { return 2 }
		CppSpeakerRegistryNew = func() uintptr { return 77 }
		CppSpeakerRegistryFree = func(r uintptr) { freed = append(freed, r) }
		CppSpeakerRegistryLastError = func(uintptr) string { return "stub error" }
		CppSpeakerRegistryAddEmbedding = func(_ uintptr, name string, _ *float32, _ int32) int32 {
			plain = append(plain, name)
			return 0
		}
		CppSpeakerRegistryAddEmbeddingFP = func(_ uintptr, name string, _ *float32, _ int32, family, weights string) int32 {
			if fpFails {
				return 1
			}
			fp = append(fp, fpAdd{name, family, weights})
			return 0
		}
		CppSpeakerRegistrySetStrict = func(_ uintptr, v int32) { strictSet = append(strictSet, v) }
		CppSpeakerEncoderFamily = func(uintptr) uintptr { return pool.cstr(famECAPA) }
		CppSpeakerIdentity = func(uintptr) uintptr { return pool.cstr(hashLoad) }
	})
	AfterEach(func() { restore() })

	v := func(id, family, weights string) *pb.KnownVoice {
		return &pb.KnownVoice{Id: id, Name: "n" + id, Embedding: []float32{1, 0}, EncoderFamily: family, EncoderWeights: weights}
	}

	It("stores the family and weights per voice and passes them to the registry", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad), v("b", famECAPA, hashOther)})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(77)))
		Expect(fp).To(Equal([]fpAdd{{"a", famECAPA, hashLoad}, {"b", famECAPA, hashOther}}))
		Expect(plain).To(BeEmpty())
	})

	It("takes the family of the loaded encoder for a voice with the same weights and no family", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", "", hashLoad)})
		Expect(err).ToNot(HaveOccurred())
		Expect(fp).To(Equal([]fpAdd{{"a", famECAPA, hashLoad}}))
	})

	It("does not guess the family from another weights hash", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", "", hashOther)})
		Expect(err).ToNot(HaveOccurred())
		Expect(fp).To(BeEmpty())
		Expect(plain).To(Equal([]string{"a"}))
	})

	It("keeps the old path for voices without a fingerprint", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", "", "")})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(77)))
		Expect(plain).To(Equal([]string{"a"}))
		Expect(fp).To(BeEmpty())
		Expect(strictSet).To(BeEmpty())
	})

	It("puts fingerprinted and unfingerprinted voices into one registry without a fingerprint", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad), v("b", "", ""), v("c", famCAMPP, hashOther)})
		Expect(err).ToNot(HaveOccurred())
		// The library refuses to mix them in one registry; the voice of another
		// family can never match and is left out.
		Expect(plain).To(ConsistOf("b", "a"))
		Expect(fp).To(BeEmpty())
	})

	It("leaves out voices of another family when a matching one exists", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad), v("c", famCAMPP, hashOther)})
		Expect(err).ToNot(HaveOccurred())
		Expect(fp).To(Equal([]fpAdd{{"a", famECAPA, hashLoad}}))
	})

	It("builds the registry from voices of another family when nothing else is usable, so the library refuses by name", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("c", famCAMPP, hashOther)})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(77)))
		Expect(fp).To(Equal([]fpAdd{{"c", famCAMPP, hashOther}}))
	})

	It("frees the registry when the library refuses every voice", func() {
		fpFails = true
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad)})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(BeZero())
		Expect(freed).To(Equal([]uintptr{77}))
	})

	Describe("speaker_strict", func() {
		It("drops unfingerprinted voices when a matching one exists", func() {
			p := &ParakeetCpp{spkCtx: 5, speakerStrict: true}
			_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad), v("b", "", "")})
			Expect(err).ToNot(HaveOccurred())
			Expect(fp).To(Equal([]fpAdd{{"a", famECAPA, hashLoad}}))
			Expect(plain).To(BeEmpty())
		})

		It("builds a strict registry from unfingerprinted voices alone, so the library rejects it", func() {
			p := &ParakeetCpp{spkCtx: 5, speakerStrict: true}
			reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("b", "", "")})
			Expect(err).ToNot(HaveOccurred())
			Expect(reg).To(Equal(uintptr(77)))
			Expect(plain).To(Equal([]string{"b"}))
			Expect(strictSet).To(Equal([]int32{1}))
		})

		It("is not set on a registry that has a fingerprint", func() {
			p := &ParakeetCpp{spkCtx: 5, speakerStrict: true}
			_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad)})
			Expect(err).ToNot(HaveOccurred())
			Expect(strictSet).To(BeEmpty())
		})
	})

	It("treats every voice as unfingerprinted on a library without the fingerprint", func() {
		CppSpeakerRegistryAddEmbeddingFP, CppSpeakerEncoderFamily = nil, nil
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{v("a", famECAPA, hashLoad)})
		Expect(err).ToNot(HaveOccurred())
		Expect(plain).To(Equal([]string{"a"}))
	})

	It("reports the family of the encoder in Status next to its identity", func() {
		sDim, sID := CppSpeakerDim, CppSpeakerIdentity
		defer func() { CppSpeakerDim, CppSpeakerIdentity = sDim, sID }()
		p := &ParakeetCpp{spkCtx: 5}
		st, err := p.Status()
		Expect(err).ToNot(HaveOccurred())
		Expect(st.GetSpeakerEncoder().GetIdentity()).To(Equal(hashLoad))
		Expect(st.GetSpeakerEncoder().GetFamily()).To(Equal(famECAPA))
		Expect(st.GetSpeakerEncoder().GetDimension()).To(Equal(int32(2)))
	})

	It("surfaces the library's family mismatch message from Diarize", func() {
		rs := diarizeStubs()
		defer rs()
		sNamed, sPCM := CppDiarizeNamedPCMJSON, CppDiarizePCM
		defer func() { CppDiarizeNamedPCMJSON, CppDiarizePCM = sNamed, sPCM }()
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr { return 0 }
		msg := fmt.Sprintf("registry encoder family %q differs from the speaker model's %q", famCAMPP, famECAPA)
		CppDiarizeNamedPCMJSON = func(_, _, _ uintptr, _ *float32, _, _ int32, _, _ float32) uintptr { return 0 }
		CppLastError = func(ctx uintptr) string {
			if ctx == 2 {
				return msg
			}
			return ""
		}
		CppSpeakerDim = func(uintptr) int32 { return 2 }
		CppSpeakerRegistryNew = func() uintptr { return 77 }
		CppSpeakerRegistryFree = func(uintptr) {}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: []*pb.KnownVoice{v("c", famCAMPP, hashOther)}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(famCAMPP))
		Expect(err.Error()).To(ContainSubstring(famECAPA))
		Expect(fp).To(Equal([]fpAdd{{"c", famCAMPP, hashOther}}))
	})
})

var _ = Describe("speaker_strict option", func() {
	It("fails Load on a library without the strict switch", func() {
		saved := CppSpeakerRegistrySetStrict
		CppSpeakerRegistrySetStrict = nil
		defer func() { CppSpeakerRegistrySetStrict = saved }()
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization).withModel("spk.gguf", modelKindSpeaker)
		restore := f.install()
		defer restore()
		err := (&ParakeetCpp{}).Load(&pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{"speaker_model:spk.gguf", "speaker_strict:true"}})
		Expect(err).To(HaveOccurred())
	})

	It("rejects a value that is not a boolean", func() {
		f := newFakeLib().withModel("diar.gguf", modelKindDiarization)
		restore := f.install()
		defer restore()
		err := (&ParakeetCpp{}).Load(&pb.ModelOptions{ModelFile: "diar.gguf", Options: []string{"speaker_strict:maybe"}})
		Expect(err).To(MatchError(ContainSubstring("speaker_strict")))
	})
})

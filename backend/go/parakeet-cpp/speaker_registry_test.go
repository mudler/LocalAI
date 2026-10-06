package main

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("speaker options", func() {
	It("turns a distance threshold into the cosine the C side takes", func() {
		a, err := parseSpeakerThreshold("")
		Expect(err).ToNot(HaveOccurred())
		Expect(a).To(BeNumerically("~", 0.5, 1e-6)) // default distance 0.5
		a, err = parseSpeakerThreshold("0.3")
		Expect(err).ToNot(HaveOccurred())
		Expect(a).To(BeNumerically("~", 0.7, 1e-6))
	})
	It("rejects a threshold that is not a distance in (0, 2)", func() {
		for _, bad := range []string{"abc", "0", "-0.1", "2", "2.5", "NaN"} {
			_, err := parseSpeakerThreshold(bad)
			Expect(err).To(HaveOccurred(), bad)
		}
	})
	It("never hands the C side an exact zero, which it reads as use the default", func() {
		a, err := parseSpeakerThreshold("1") // distance 1 is cosine 0
		Expect(err).ToNot(HaveOccurred())
		Expect(a).To(Equal(float32(1e-6)))
		m, err := parseSpeakerMargin("0")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal(float32(1e-6)))
		a, _ = parseSpeakerThreshold("")
		Expect(a).To(BeNumerically("~", 0.5, 1e-6))
		m, _ = parseSpeakerMargin("")
		Expect(m).To(BeNumerically("~", 0.05, 1e-6))
	})
	It("parses the margin, default 0.05, within [0, 1)", func() {
		m, err := parseSpeakerMargin("")
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(BeNumerically("~", 0.05, 1e-6))
		_, err = parseSpeakerMargin("-1")
		Expect(err).To(HaveOccurred())
		_, err = parseSpeakerMargin("1")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("buildSpeakerRegistry", func() {
	var restore func()
	var added []string
	var freed []uintptr
	BeforeEach(func() {
		sNew, sFree, sAdd, sDim, sErr := CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSpeakerRegistryLastError
		restore = func() {
			CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSpeakerRegistryLastError = sNew, sFree, sAdd, sDim, sErr
		}
		added, freed = nil, nil
		CppSpeakerDim = func(uintptr) int32 { return 3 }
		CppSpeakerRegistryNew = func() uintptr { return 77 }
		CppSpeakerRegistryFree = func(r uintptr) { freed = append(freed, r) }
		CppSpeakerRegistryAddEmbedding = func(r uintptr, name string, emb *float32, dim int32) int32 {
			added = append(added, name)
			return 0
		}
		CppSpeakerRegistryLastError = func(uintptr) string { return "stub error" }
	})
	AfterEach(func() { restore() })

	voice := func(name string, n int) *pb.KnownVoice {
		return &pb.KnownVoice{Name: name, Embedding: make([]float32, n)}
	}

	It("keeps duplicate display names under distinct registration IDs", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{
			{Id: "id-a", Name: "Ada", Embedding: []float32{1, 0, 0}},
			{Id: "id-b", Name: "Ada", Embedding: []float32{0, 1, 0}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(Equal([]string{"id-a", "id-b"}))
	})

	It("adds every known voice, in order", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{
			{Name: "ada", Embedding: []float32{1, 0, 0}}, {Name: "ben", Embedding: []float32{0, 1, 0}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(77)))
		Expect(added).To(Equal([]string{"ada", "ben"}))
		Expect(freed).To(BeEmpty())
	})
	It("skips a voice of the wrong size without failing, and frees the registry when none is left", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{voice("cy", 5)})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(0)))
		Expect(added).To(BeEmpty())
		Expect(freed).To(Equal([]uintptr{77}))
	})
	It("keeps the usable voices when another one has the wrong size", func() {
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{voice("cy", 5), {Name: "ada", Embedding: []float32{1, 0, 0}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(77)))
		Expect(added).To(Equal([]string{"ada"}))
		Expect(freed).To(BeEmpty())
	})
	It("skips a voice the C side refuses, and frees the registry when none is left", func() {
		CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 1 }
		p := &ParakeetCpp{spkCtx: 5}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{{Name: "ada", Embedding: []float32{0, 0, 0}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(0)))
		Expect(freed).To(Equal([]uintptr{77}))
	})
	It("returns no registry when no speaker model is loaded", func() {
		p := &ParakeetCpp{}
		reg, err := p.buildSpeakerRegistry([]*pb.KnownVoice{voice("ada", 3)})
		Expect(err).ToNot(HaveOccurred())
		Expect(reg).To(Equal(uintptr(0)))
	})
	It("skips a voice with no name or embedding instead of failing the request", func() {
		p := &ParakeetCpp{spkCtx: 5}
		_, err := p.buildSpeakerRegistry([]*pb.KnownVoice{{Name: "", Embedding: []float32{1, 0, 0}}, {Name: "x"}, {Name: "ada", Embedding: []float32{1, 0, 0}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(added).To(Equal([]string{"ada"}))
	})
	It("frees a registry and ignores a zero handle", func() {
		p := &ParakeetCpp{}
		p.freeSpeakerRegistry(0)
		p.freeSpeakerRegistry(9)
		Expect(freed).To(Equal([]uintptr{9}))
	})
})

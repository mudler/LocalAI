package main

import (
	"context"
	"encoding/json"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// bundleLib extends fakeLib with bundle files. Paths registered with withBundle
// answer the component list and open components by name; the plain
// CppLoad on a bundle behaves like the library (the only ASR component, else
// the only component).
type bundleLib struct {
	*fakeLib
	bundles    map[string][]bundleComponent
	pool       liveCstrPool
	loadedComp []string
	loadErr    string
}

func newBundleLib() *bundleLib {
	return &bundleLib{fakeLib: newFakeLib(), bundles: map[string][]bundleComponent{}}
}

func (b *bundleLib) withBundle(path string, comps ...bundleComponent) *bundleLib {
	b.bundles[path] = comps
	return b
}

func (b *bundleLib) withPlain(path string, kind int32) *bundleLib {
	b.fakeLib.withModel(path, kind)
	return b
}

func (b *bundleLib) open(kind string) uintptr {
	mk, _ := componentKindModel(kind)
	ctx := b.next
	b.next++
	b.ctxKind[ctx] = mk
	return ctx
}

func (b *bundleLib) install() (restore func()) {
	restoreBase := b.fakeLib.install()
	savedJSON, savedComp, savedErr, savedFreeStr, savedLoad := CppBundleComponentsJSON, CppLoadComponent, CppLoadError, CppFreeString, CppLoad
	baseLoad := CppLoad
	CppFreeString = func(uintptr) {}
	CppBundleComponentsJSON = func(path string) uintptr {
		comps, ok := b.bundles[path]
		if !ok {
			return 0
		}
		raw, _ := json.Marshal(comps)
		return b.pool.cstr(string(raw))
	}
	CppLoadError = func() string { return b.loadErr }
	CppLoadComponent = func(path, name string) uintptr {
		for _, c := range b.bundles[path] {
			if c.Name == name {
				b.loadedComp = append(b.loadedComp, path+"#"+name)
				return b.open(c.Kind)
			}
		}
		b.loadErr = "no such component " + name
		return 0
	}
	CppLoad = func(path string) uintptr {
		comps, ok := b.bundles[path]
		if !ok {
			return baseLoad(path)
		}
		asr := componentsOfKind(comps, componentASR)
		switch {
		case len(asr) == 1:
			return CppLoadComponent(path, asr[0])
		case len(comps) == 1:
			return CppLoadComponent(path, comps[0].Name)
		}
		b.loadErr = "bundle has several candidates"
		return 0
	}
	return func() {
		restoreBase()
		CppBundleComponentsJSON, CppLoadComponent, CppLoadError, CppFreeString, CppLoad = savedJSON, savedComp, savedErr, savedFreeStr, savedLoad
	}
}

var smallBundle = []bundleComponent{
	{Name: "asr", Kind: "asr", License: "CC-BY-4.0"},
	{Name: "diar", Kind: "diar", License: "OpenMDW-1.1"},
	{Name: "ced", Kind: "ced", License: "Apache-2.0"},
	{Name: "voice", Kind: "voice", License: "CC-BY-4.0"},
	{Name: "vad", Kind: "vad", License: "MIT"},
}

var _ = Describe("bundle GGUF files (stubbed C API)", func() {
	var restore func()
	var savedAdd func(uintptr, string, *float32, int32) int32
	var savedDim func(uintptr) int32
	var savedBegin func(asr, diar, tagger, speaker, reg uintptr, o *cSceneOpts) uintptr

	BeforeEach(func() {
		savedAdd, savedDim, savedBegin = CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSceneStreamBeginSpeaker
		CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 0 }
		CppSpeakerDim = func(uintptr) int32 { return 256 }
		CppSceneStreamBeginSpeaker = func(_, _, _, _, _ uintptr, _ *cSceneOpts) uintptr { return 0 }
	})
	AfterEach(func() {
		if restore != nil {
			restore()
			restore = nil
		}
		CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppSceneStreamBeginSpeaker = savedAdd, savedDim, savedBegin
	})

	load := func(b *bundleLib, opts ...string) (*ParakeetCpp, error) {
		restore = b.install()
		p := &ParakeetCpp{}
		return p, p.Load(&pb.ModelOptions{ModelFile: "small.gguf", Options: opts})
	}

	It("opens the ASR component and the Silero component of a bundle with no options", func() {
		b := newBundleLib().withBundle("small.gguf", smallBundle...)
		p, err := load(b)
		Expect(err).ToNot(HaveOccurred())
		Expect(b.loadedComp).To(Equal([]string{"small.gguf#asr", "small.gguf#vad"}))
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(p.diarCtx).To(BeZero(), "diarization is opt-in")
		Expect(p.tagCtx).To(BeZero())
		Expect(p.bundle).To(HaveLen(5))
		Expect(p.vad).To(BeFalse(), "vad:true stays opt-in")
	})

	It("loads every role of a bundle from the component options and frees each context once", func() {
		CppTranscribePathJSONVad = func(uintptr, string, int32) uintptr { return 0 }
		DeferCleanup(func() { CppTranscribePathJSONVad = nil })
		b := newBundleLib().withBundle("small.gguf", smallBundle...)
		p, err := load(b, "diar_component:diar", "sound_component:ced", "speaker_component:voice", "vad:true")
		Expect(err).ToNot(HaveOccurred())
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.tagCtx).ToNot(BeZero())
		Expect(p.spkCtx).ToNot(BeZero())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(p.vad).To(BeTrue())
		Expect(p.Free()).To(Succeed())
		Expect(b.freed).To(HaveLen(5))
	})

	It("takes the same file for a role through the companion option", func() {
		b := newBundleLib().withBundle("small.gguf", smallBundle...)
		p, err := load(b, "diarization_model:small.gguf", "sound_model:small.gguf")
		Expect(err).ToNot(HaveOccurred())
		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.tagCtx).ToNot(BeZero())
		Expect(b.loadedComp).To(ContainElements("small.gguf#diar", "small.gguf#ced"))
	})

	It("uses a bundle as the companion of a plain primary", func() {
		b := newBundleLib().
			withBundle("small.gguf", smallBundle...).
			withPlain("asr.gguf", modelKindASR)
		restore = b.install()
		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{ModelFile: "asr.gguf", Options: []string{"diarization_model:small.gguf"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(p.diarCtx).ToNot(BeZero())
		Expect(p.bundle).To(BeNil(), "the primary is a plain file")
		Expect(p.vadCtx).To(BeZero(), "the bundle VAD is only picked up from the primary file")
	})

	It("chooses an ASR component by bundle_asr", func() {
		two := append([]bundleComponent{{Name: "fast", Kind: "asr"}, {Name: "big", Kind: "asr"}}, smallBundle[4])
		b := newBundleLib().withBundle("small.gguf", two...)
		_, err := load(b)
		Expect(err).To(MatchError(And(ContainSubstring("several"), ContainSubstring("fast, big"), ContainSubstring("bundle_asr"))))
		Expect(b.loadedComp).To(BeEmpty())

		b2 := newBundleLib().withBundle("small.gguf", two...)
		p, err := load(b2, "bundle_asr:big")
		Expect(err).ToNot(HaveOccurred())
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(b2.loadedComp).To(ContainElement("small.gguf#big"))
	})

	It("rejects an unknown or wrong-kind component name and frees what it opened", func() {
		b := newBundleLib().withBundle("small.gguf", smallBundle...)
		_, err := load(b, "bundle_asr:nope")
		Expect(err).To(MatchError(And(ContainSubstring(`no component "nope"`), ContainSubstring("vad (vad)"))))

		b2 := newBundleLib().withBundle("small.gguf", smallBundle...)
		_, err = load(b2, "diar_component:vad")
		Expect(err).To(MatchError(ContainSubstring(`kind "vad"`)))
		Expect(b2.freed).To(HaveLen(len(b2.loadedComp)), "every opened context is freed")
	})

	It("gives a clear error for a role the bundle has no component for", func() {
		noDiar := []bundleComponent{smallBundle[0], smallBundle[4]}
		b := newBundleLib().withBundle("small.gguf", noDiar...)
		_, err := load(b, "diar_component:diar")
		Expect(err).To(MatchError(ContainSubstring(`no component "diar"`)))

		b2 := newBundleLib().withBundle("small.gguf", noDiar...)
		_, err = load(b2, "diarization_model:small.gguf")
		Expect(err).To(MatchError(And(ContainSubstring(`needs a "diar" component`), ContainSubstring("asr (asr), vad (vad)"))))
	})

	It("names the role mismatch in the RPC errors of a bundle", func() {
		noDiar := []bundleComponent{smallBundle[0], smallBundle[4]}
		b := newBundleLib().withBundle("small.gguf", noDiar...)
		p, err := load(b)
		Expect(err).ToNot(HaveOccurred())

		_, err = p.Diarize(&pb.DiarizeRequest{Dst: "x.wav"})
		Expect(err).To(MatchError(And(ContainSubstring("not a diarization model"), ContainSubstring(`without a "diar" component`))))

		_, err = p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: "x.wav"})
		Expect(err).To(MatchError(ContainSubstring(`without a "ced" component`)))

		full := newBundleLib().withBundle("small.gguf", smallBundle...)
		restore()
		p, err = load(full)
		Expect(err).ToNot(HaveOccurred())
		_, err = p.Diarize(&pb.DiarizeRequest{Dst: "x.wav"})
		Expect(err).To(MatchError(ContainSubstring("diar_component:diar")))
	})

	It("does not load the VAD component when vad_model or vad_component is given", func() {
		b := newBundleLib().
			withBundle("small.gguf", smallBundle...).
			withPlain("silero.gguf", modelKindVAD)
		restore = b.install()
		p := &ParakeetCpp{}
		CppTranscribePathJSONVadWith = func(ctx, vadCtx uintptr, wav string, dec int32, o string) uintptr { return 0 }
		DeferCleanup(func() { CppTranscribePathJSONVadWith = nil })
		Expect(p.Load(&pb.ModelOptions{ModelFile: "small.gguf", Options: []string{"vad_model:silero.gguf"}})).To(Succeed())
		Expect(b.loadedComp).To(Equal([]string{"small.gguf#asr"}))
		Expect(p.vad).To(BeTrue())
	})

	It("skips an ambiguous bundle VAD silently, and refuses it when vad_component is set without a name match", func() {
		two := append(append([]bundleComponent{}, smallBundle...), bundleComponent{Name: "vad2", Kind: "vad"})
		b := newBundleLib().withBundle("small.gguf", two...)
		p, err := load(b)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.vadCtx).To(BeZero())

		CppTranscribePathJSONVadWith = func(ctx, vadCtx uintptr, wav string, dec int32, o string) uintptr { return 0 }
		DeferCleanup(func() { CppTranscribePathJSONVadWith = nil })
		restore()
		b2 := newBundleLib().withBundle("small.gguf", two...)
		p, err = load(b2, "vad_component:vad2")
		Expect(err).ToNot(HaveOccurred())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(b2.loadedComp).To(ContainElement("small.gguf#vad2"))
	})

	It("rejects a component option on a file that is not a bundle", func() {
		b := newBundleLib().withPlain("small.gguf", modelKindASR)
		_, err := load(b, "diar_component:diar")
		Expect(err).To(MatchError(ContainSubstring("not one")))
		Expect(b.freed).To(HaveLen(1), "the primary is freed")

		b2 := newBundleLib().withPlain("small.gguf", modelKindASR)
		_, err = load(b2, "bundle_asr:asr")
		Expect(err).To(MatchError(ContainSubstring("bundle_asr needs a bundle")))
	})

	It("loads a plain file exactly as before when the library has the bundle symbols", func() {
		b := newBundleLib().withPlain("small.gguf", modelKindASR)
		p, err := load(b)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.ctxPtr).ToNot(BeZero())
		Expect(p.bundle).To(BeNil())
		Expect(b.loadedComp).To(BeEmpty())
	})

	It("loads a bundle without an ASR component through the library default", func() {
		b := newBundleLib().withBundle("small.gguf", bundleComponent{Name: "vad", Kind: "vad"})
		p, err := load(b)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.vadCtx).ToNot(BeZero())
		Expect(p.ctxPtr).To(BeZero())
	})

	It("reports the library reason when a bundle component does not open", func() {
		b := newBundleLib().withBundle("small.gguf", smallBundle...)
		restore = b.install()
		saved := CppLoadComponent
		CppLoadComponent = func(string, string) uintptr { b.loadErr = "component asr is damaged"; return 0 }
		DeferCleanup(func() { CppLoadComponent = saved })
		p := &ParakeetCpp{}
		err := p.Load(&pb.ModelOptions{ModelFile: "small.gguf"})
		Expect(err).To(MatchError(ContainSubstring("component asr is damaged")))
	})

	Describe("a libparakeet.so without the bundle symbols", func() {
		It("loads a plain file as before, treating the file as ASR", func() {
			f := newFakeLib().withModel("a.gguf", modelKindASR)
			restore = f.install()
			savedJSON, savedComp := CppBundleComponentsJSON, CppLoadComponent
			CppBundleComponentsJSON, CppLoadComponent = nil, nil
			DeferCleanup(func() { CppBundleComponentsJSON, CppLoadComponent = savedJSON, savedComp })
			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: "a.gguf"})).To(Succeed())
			Expect(p.bundle).To(BeNil())
		})

		It("refuses the component options", func() {
			f := newFakeLib().withModel("a.gguf", modelKindASR)
			restore = f.install()
			savedJSON, savedComp := CppBundleComponentsJSON, CppLoadComponent
			CppBundleComponentsJSON, CppLoadComponent = nil, nil
			DeferCleanup(func() { CppBundleComponentsJSON, CppLoadComponent = savedJSON, savedComp })
			for _, opt := range []string{"bundle_asr:asr", "diar_component:diar", "sound_component:ced", "vad_component:vad"} {
				p := &ParakeetCpp{}
				err := p.Load(&pb.ModelOptions{ModelFile: "a.gguf", Options: []string{opt}})
				Expect(err).To(HaveOccurred(), opt)
				Expect(err.Error()).To(Or(ContainSubstring("not one"), ContainSubstring("needs a bundle"), ContainSubstring("need")), opt)
			}
		})
	})
})

var _ = Describe("bundle helpers", func() {
	comps := []bundleComponent{{Name: "a", Kind: "asr"}, {Name: "v", Kind: "vad"}}

	It("maps kinds both ways", func() {
		for _, k := range []string{"asr", "vad", "diar", "ced", "voice"} {
			m, ok := componentKindModel(k)
			Expect(ok).To(BeTrue(), k)
			Expect(kindForModel(m)).To(Equal(k))
		}
		_, ok := componentKindModel("future")
		Expect(ok).To(BeFalse())
	})

	It("picks the only component of a kind and names the alternatives otherwise", func() {
		name, err := pickComponent("f", comps, "", "vad", "vad_model", "vad_component")
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(Equal("v"))
		_, err = pickComponent("f", comps, "", "ced", "sound_model", "sound_component")
		Expect(err).To(MatchError(ContainSubstring("a (asr), v (vad)")))
	})
})

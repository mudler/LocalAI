package openai

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("voiceGate.Resolve + authorize", func() {
	mkGate := func(allow []string) *voiceGate {
		return &voiceGate{
			cfg: config.PipelineVoiceRecognition{
				Mode:      config.VoiceGateModeIdentify,
				Threshold: 0.25,
				Allow:     config.VoiceRecognitionAllow{Names: allow},
			},
			registry: &fakeRegistry{matches: []voicerecognition.Match{
				{Distance: 0.1, Metadata: voicerecognition.Metadata{ID: "spk_1", Name: "alice", Labels: map[string]string{"family": "yes"}}},
			}},
			embedFn: func(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil },
		}
	}

	It("resolves a confident identity with name, id and a 0..100 confidence", func() {
		r, err := mkGate(nil).Resolve(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(r.found).To(BeTrue())
		Expect(r.speaker.Name).To(Equal("alice"))
		Expect(r.speaker.ID).To(Equal("spk_1"))
		Expect(r.speaker.Matched).To(BeTrue())
		Expect(r.speaker.Confidence).To(BeNumerically(">", 0))
		Expect(r.speaker.Confidence).To(BeNumerically("<=", 100))
	})

	It("marks a candidate above the threshold as not matched", func() {
		g := mkGate(nil)
		g.registry = &fakeRegistry{matches: []voicerecognition.Match{
			{Distance: 0.9, Metadata: voicerecognition.Metadata{Name: "alice"}},
		}}
		r, err := g.Resolve(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(r.found).To(BeTrue())
		Expect(r.speaker.Matched).To(BeFalse())
		Expect(r.speaker.Name).To(Equal("alice")) // name still surfaced
	})

	It("authorize allows a confident match in the allow list", func() {
		g := mkGate([]string{"alice"})
		r, _ := g.Resolve(context.Background(), "x.wav")
		allowed, reason := g.authorize(r)
		Expect(allowed).To(BeTrue())
		Expect(reason).To(BeEmpty())
	})

	It("authorize denies a confident match outside the allow list", func() {
		g := mkGate([]string{"bob"})
		r, _ := g.Resolve(context.Background(), "x.wav")
		allowed, reason := g.authorize(r)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(Equal("speaker not in allow list"))
	})

	It("authorize allows by label when names do not match", func() {
		g := mkGate(nil)
		g.cfg.Allow = config.VoiceRecognitionAllow{Labels: []string{"family"}}
		r, _ := g.Resolve(context.Background(), "x.wav")
		allowed, _ := g.authorize(r)
		Expect(allowed).To(BeTrue())
	})
})

var _ = Describe("confidence", func() {
	It("is 100 at zero distance", func() {
		Expect(confidence(0, 0.25)).To(BeNumerically("~", 100, 1e-4))
	})
	It("clamps to 0 above the threshold", func() {
		Expect(confidence(0.5, 0.25)).To(BeNumerically("~", 0, 1e-4))
	})
	It("is 0 for a non-positive threshold", func() {
		Expect(confidence(0.1, 0)).To(BeNumerically("~", 0, 1e-4))
	})
})

var _ = Describe("cosineDistance", func() {
	It("is 0 for identical vectors", func() {
		Expect(cosineDistance([]float32{1, 0, 0}, []float32{1, 0, 0})).To(BeNumerically("~", 0, 1e-6))
	})
	It("is ~1 for orthogonal vectors", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{0, 1})).To(BeNumerically("~", 1, 1e-6))
	})
	It("is ~2 for opposite vectors", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{-1, 0})).To(BeNumerically("~", 2, 1e-6))
	})
	It("returns 1 for length mismatch", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{1})).To(BeNumerically("~", 1, 1e-6))
	})
	It("returns 1 for a zero vector", func() {
		Expect(cosineDistance([]float32{0, 0}, []float32{1, 0})).To(BeNumerically("~", 1, 1e-6))
	})
})

type fakeRegistry struct {
	matches []voicerecognition.Match
	err     error
}

func (f *fakeRegistry) Register(ctx context.Context, emb []float32, m voicerecognition.Metadata) (voicerecognition.Metadata, error) {
	return m, nil
}
func (f *fakeRegistry) Identify(ctx context.Context, probe []float32, topK int) ([]voicerecognition.Match, error) {
	return f.matches, f.err
}
func (f *fakeRegistry) Forget(ctx context.Context, id string) error { return nil }

var _ = Describe("voiceGate identify mode", func() {
	stubEmbed := func(emb []float32, err error) func(context.Context, string) ([]float32, error) {
		return func(context.Context, string) ([]float32, error) { return emb, err }
	}
	mkGate := func(allow config.VoiceRecognitionAllow, matches []voicerecognition.Match, embErr error) *voiceGate {
		return &voiceGate{
			cfg:      config.PipelineVoiceRecognition{Mode: config.VoiceGateModeIdentify, Threshold: 0.25, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent, Allow: allow},
			registry: &fakeRegistry{matches: matches},
			embedFn:  stubEmbed([]float32{1, 0, 0}, embErr),
		}
	}

	It("allows a registered speaker within threshold and in the allow list", func() {
		g := mkGate(config.VoiceRecognitionAllow{Names: []string{"alice"}},
			[]voicerecognition.Match{{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "alice"}}}, nil)
		allowed, matched, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(allowed).To(BeTrue())
		Expect(matched).To(Equal("alice"))
	})
	It("allows any registered speaker when the allow list is empty", func() {
		g := mkGate(config.VoiceRecognitionAllow{},
			[]voicerecognition.Match{{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "carol"}}}, nil)
		allowed, _, _, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeTrue())
	})
	It("allows by label", func() {
		g := mkGate(config.VoiceRecognitionAllow{Labels: []string{"family"}},
			[]voicerecognition.Match{{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "bob", Labels: map[string]string{"family": "yes"}}}}, nil)
		allowed, _, _, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeTrue())
	})
	It("denies a speaker not in the allow list", func() {
		g := mkGate(config.VoiceRecognitionAllow{Names: []string{"alice"}},
			[]voicerecognition.Match{{Distance: 0.1, Metadata: voicerecognition.Metadata{Name: "mallory"}}}, nil)
		allowed, matched, reason, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
		Expect(matched).To(Equal("mallory"))
		Expect(reason).To(ContainSubstring("allow"))
	})
	It("denies a match above the threshold", func() {
		g := mkGate(config.VoiceRecognitionAllow{},
			[]voicerecognition.Match{{Distance: 0.9, Metadata: voicerecognition.Metadata{Name: "alice"}}}, nil)
		allowed, matched, _, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
		Expect(matched).To(Equal("alice"))
	})
	It("denies when no registry match", func() {
		g := mkGate(config.VoiceRecognitionAllow{}, nil, nil)
		allowed, _, reason, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("unknown"))
	})
	It("denies (no error) when no speech is detected", func() {
		g := mkGate(config.VoiceRecognitionAllow{}, nil, nil)
		g.embedFn = stubEmbed(nil, nil)
		allowed, _, reason, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("no speech"))
	})
	It("denies and surfaces the error when embedding fails", func() {
		g := mkGate(config.VoiceRecognitionAllow{}, nil, errors.New("boom"))
		allowed, _, reason, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).To(HaveOccurred())
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("embed"))
	})
	It("denies and surfaces the error when identify fails", func() {
		g := mkGate(config.VoiceRecognitionAllow{}, nil, nil)
		g.registry = &fakeRegistry{err: errors.New("boom")}
		allowed, _, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).To(HaveOccurred())
		Expect(allowed).To(BeFalse())
	})
})

var _ = Describe("voiceGate verify mode", func() {
	It("allows when the utterance matches a reference embedding", func() {
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refEmbeds: []namedEmbedding{{name: "alice", emb: []float32{1, 0, 0}}},
			embedFn:   func(context.Context, string) ([]float32, error) { return []float32{1, 0, 0}, nil },
		}
		allowed, matched, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(allowed).To(BeTrue())
		Expect(matched).To(Equal("alice"))
	})
	It("denies when no reference is within threshold", func() {
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refEmbeds: []namedEmbedding{{name: "alice", emb: []float32{1, 0, 0}}},
			embedFn:   func(context.Context, string) ([]float32, error) { return []float32{0, 1, 0}, nil },
		}
		allowed, _, reason, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("reference"))
	})
	It("denies (no error) when no speech is detected", func() {
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refEmbeds: []namedEmbedding{{name: "alice", emb: []float32{1, 0, 0}}},
			embedFn:   func(context.Context, string) ([]float32, error) { return nil, nil },
		}
		allowed, _, reason, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("no speech"))
	})
	It("denies and surfaces the error when embedding fails", func() {
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refEmbeds: []namedEmbedding{{name: "alice", emb: []float32{1, 0, 0}}},
			embedFn:   func(context.Context, string) ([]float32, error) { return nil, errors.New("boom") },
		}
		allowed, _, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).To(HaveOccurred())
		Expect(allowed).To(BeFalse())
	})
	It("uses verifyFn when anti-spoofing is enabled", func() {
		called := false
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, AntiSpoofing: true, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refAudios: []config.VoiceReference{{Name: "alice", Audio: "/alice.wav"}},
			verifyFn:  func(context.Context, string, string) (bool, error) { called = true; return true, nil },
		}
		allowed, matched, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).ToNot(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(allowed).To(BeTrue())
		Expect(matched).To(Equal("alice"))
	})
	It("denies and surfaces the error when verifyFn fails (anti-spoofing)", func() {
		g := &voiceGate{
			cfg:       config.PipelineVoiceRecognition{Mode: config.VoiceGateModeVerify, Threshold: 0.25, AntiSpoofing: true, When: config.VoiceGateWhenEvery, OnReject: config.VoiceGateRejectEvent},
			refAudios: []config.VoiceReference{{Name: "alice", Audio: "/alice.wav"}},
			verifyFn:  func(context.Context, string, string) (bool, error) { return false, errors.New("boom") },
		}
		allowed, _, _, err := g.Authorize(context.Background(), "x.wav")
		Expect(err).To(HaveOccurred())
		Expect(allowed).To(BeFalse())
	})
})

var _ = Describe("newVoiceGate", func() {
	It("fails fast when identify mode has no registry (before touching the loader)", func() {
		cfg := config.PipelineVoiceRecognition{Model: "spk", Mode: config.VoiceGateModeIdentify}
		g, err := newVoiceGate(cfg, nil, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(g).To(BeNil())
	})
	It("fails fast when verify mode has no references", func() {
		cfg := config.PipelineVoiceRecognition{Model: "spk", Mode: config.VoiceGateModeVerify}
		g, err := newVoiceGate(cfg, nil, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(g).To(BeNil())
	})
})

var _ = Describe("voiceGate decide", func() {
	gate := func(when string) *voiceGate {
		return &voiceGate{cfg: config.PipelineVoiceRecognition{When: when}}
	}
	It("every: proceeds iff allowed, never marks verified", func() {
		proceed, mark := gate(config.VoiceGateWhenEvery).decide(false, true)
		Expect(proceed).To(BeTrue())
		Expect(mark).To(BeFalse())
		proceed, mark = gate(config.VoiceGateWhenEvery).decide(false, false)
		Expect(proceed).To(BeFalse())
		Expect(mark).To(BeFalse())
	})
	It("first: marks verified on first allow", func() {
		proceed, mark := gate(config.VoiceGateWhenFirst).decide(false, true)
		Expect(proceed).To(BeTrue())
		Expect(mark).To(BeTrue())
	})
	It("first: denies on first reject without marking", func() {
		proceed, mark := gate(config.VoiceGateWhenFirst).decide(false, false)
		Expect(proceed).To(BeFalse())
		Expect(mark).To(BeFalse())
	})
	It("first: proceeds without re-check once already verified", func() {
		proceed, mark := gate(config.VoiceGateWhenFirst).decide(true, false)
		Expect(proceed).To(BeTrue())
		Expect(mark).To(BeFalse())
	})
})

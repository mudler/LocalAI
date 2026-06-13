package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
		allowed, _, reason, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("allow"))
	})
	It("denies a match above the threshold", func() {
		g := mkGate(config.VoiceRecognitionAllow{},
			[]voicerecognition.Match{{Distance: 0.9, Metadata: voicerecognition.Metadata{Name: "alice"}}}, nil)
		allowed, _, _, _ := g.Authorize(context.Background(), "x.wav")
		Expect(allowed).To(BeFalse())
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
})

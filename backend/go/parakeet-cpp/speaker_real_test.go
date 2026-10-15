package main

import (
	"encoding/json"
	"os"
	"sort"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixtureVoices reads testdata/two_speakers_embeddings.json: WeSpeaker ResNet34
// embeddings of voice A (two_speakers.wav 0.6-4.6 s) and voice B (6.9-10.9 s).
func fixtureVoices() (a, b *pb.KnownVoice) {
	raw, err := os.ReadFile("testdata/two_speakers_embeddings.json")
	Expect(err).ToNot(HaveOccurred())
	var doc struct {
		Dim    int `json:"dim"`
		Voices []struct {
			Name      string    `json:"name"`
			Embedding []float32 `json:"embedding"`
		} `json:"voices"`
	}
	Expect(json.Unmarshal(raw, &doc)).To(Succeed())
	Expect(doc.Voices).To(HaveLen(2))
	Expect(doc.Voices[0].Embedding).To(HaveLen(doc.Dim))
	mk := func(i int) *pb.KnownVoice {
		return &pb.KnownVoice{Name: doc.Voices[i].Name, Embedding: doc.Voices[i].Embedding}
	}
	return mk(0), mk(1)
}

func speakerFixturesOrSkip() (diarModel, speakerModel, wav string) {
	diarModel = os.Getenv("PARAKEET_BACKEND_TEST_DIAR_MODEL")
	speakerModel = os.Getenv("PARAKEET_BACKEND_TEST_SPEAKER_MODEL")
	wav = os.Getenv("PARAKEET_BACKEND_TEST_WAV")
	if diarModel == "" || speakerModel == "" || wav == "" {
		Skip("set PARAKEET_BACKEND_TEST_DIAR_MODEL, PARAKEET_BACKEND_TEST_SPEAKER_MODEL and " +
			"PARAKEET_BACKEND_TEST_WAV (parakeet.cpp tests/fixtures/two_speakers.wav)")
	}
	ensureLibLoaded()
	if CppDiarizeNamedPCMJSON == nil {
		Skip("libparakeet.so has no ABI 10 speaker naming (parakeet_capi_diarize_named_pcm_json)")
	}
	return
}

// namesBySlot maps a speaker label to the set of names its segments carry.
func namesBySlot(segs []*pb.DiarizeSegment) map[string][]string {
	out := map[string][]string{}
	for _, s := range segs {
		out[s.Speaker] = append(out[s.Speaker], s.Name)
	}
	return out
}

var _ = Describe("ParakeetCpp speaker names (real libparakeet.so, ABI 10)", func() {
	load := func(diarModel, speakerModel string, extra ...string) *ParakeetCpp {
		p := &ParakeetCpp{}
		opts := append([]string{"speaker_model:" + speakerModel}, extra...)
		Expect(p.Load(&pb.ModelOptions{ModelFile: diarModel, Options: opts})).To(Succeed())
		return p
	}

	// two_speakers.wav: voice A speaks 0.5-5.5 s and 14.8-18.7 s, voice B 6.9-13.5 s and
	// 20.1-23.6 s; diarization slot 0 is voice A and slot 1 is voice B.
	It("names diarized segments from registered voices, whatever order they arrive in", func() {
		diarModel, speakerModel, wav := speakerFixturesOrSkip()
		a, b := fixtureVoices()
		p := load(diarModel, speakerModel)
		defer func() { _ = p.Free() }()

		// Reversed on purpose: naming by arrival order would swap the speakers.
		res, err := p.Diarize(&pb.DiarizeRequest{Dst: wav, KnownVoices: []*pb.KnownVoice{b, a}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).To(HaveLen(5))
		for _, s := range res.Segments {
			GinkgoWriter.Printf("[%5.1f-%5.1f] slot %s name %q score %.3f\n", s.Start, s.End, s.Speaker, s.Name, s.NameScore)
			want := map[string]string{"0": "voice_a", "1": "voice_b"}[s.Speaker]
			Expect(want).ToNot(BeEmpty(), "unexpected slot %q", s.Speaker)
			Expect(s.Name).To(Equal(want))
			Expect(s.NameScore).To(BeNumerically(">", 0.5))
		}
	})

	It("leaves a slot unnamed when its voice is not registered", func() {
		diarModel, speakerModel, wav := speakerFixturesOrSkip()
		_, b := fixtureVoices()
		// Distance 0.3 means cosine 0.7; a different speaker scores near 0 with WeSpeaker.
		p := load(diarModel, speakerModel, "speaker_threshold:0.3")
		defer func() { _ = p.Free() }()

		res, err := p.Diarize(&pb.DiarizeRequest{Dst: wav, KnownVoices: []*pb.KnownVoice{b}})
		Expect(err).ToNot(HaveOccurred())
		names := namesBySlot(res.Segments)
		Expect(names).To(HaveKey("0"))
		Expect(names).To(HaveKey("1"))
		for _, n := range names["0"] {
			Expect(n).To(BeEmpty())
		}
		for _, n := range names["1"] {
			Expect(n).To(Equal("voice_b"))
		}
	})

	// A distance of 0.01 asks for cosine 0.99, above any genuine score. If purego did not
	// hand the float32 to C, the C side would read 0 and fall back to its 0.5 default,
	// which names both slots, so an unnamed result proves the argument arrives.
	It("passes the float32 accept threshold through purego (a 0.99 cosine names nobody)", func() {
		diarModel, speakerModel, wav := speakerFixturesOrSkip()
		a, b := fixtureVoices()
		p := load(diarModel, speakerModel, "speaker_threshold:0.01")
		defer func() { _ = p.Free() }()

		res, err := p.Diarize(&pb.DiarizeRequest{Dst: wav, KnownVoices: []*pb.KnownVoice{a, b}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).ToNot(BeEmpty())
		for _, s := range res.Segments {
			Expect(s.Name).To(BeEmpty(), "slot %s was named at cosine 0.99", s.Speaker)
		}
	})

	It("names live speaker segments from the registered voices", func() {
		diarModel, speakerModel, wav := speakerFixturesOrSkip()
		streamModel := os.Getenv("PARAKEET_BACKEND_TEST_STREAM_MODEL")
		if streamModel == "" {
			Skip("set PARAKEET_BACKEND_TEST_STREAM_MODEL (cache-aware streaming model) for the live path")
		}
		if CppSceneStreamBeginSpeaker == nil {
			Skip("libparakeet.so has no scene_stream_begin_speaker")
		}
		a, b := fixtureVoices()
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{
			ModelFile: streamModel,
			Options:   []string{"diarization_model:" + diarModel, "speaker_model:" + speakerModel},
		})).To(Succeed())
		defer func() { _ = p.Free() }()

		pcm, _, err := decodeWavMono16k(wav)
		Expect(err).ToNot(HaveOccurred())

		in := make(chan *pb.TranscriptLiveRequest, 8)
		out := make(chan *pb.TranscriptLiveResponse, 256)
		errCh := make(chan error, 1)
		go func() { errCh <- p.AudioTranscriptionLive(in, out) }()
		in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Config{
			Config: &pb.TranscriptLiveConfig{KnownVoices: []*pb.KnownVoice{b, a}},
		}}
		go func() {
			const chunk = 8000 // 0.5 s
			for i := 0; i < len(pcm); i += chunk {
				end := min(i+chunk, len(pcm))
				in <- liveAudio(pcm[i:end])
			}
			close(in)
		}()

		var segs []*pb.LiveSpeakerSegment
		for r := range out {
			segs = append(segs, r.GetSpeakers()...)
		}
		Expect(<-errCh).ToNot(HaveOccurred())

		named := 0
		seen := map[string]bool{}
		for _, s := range segs {
			GinkgoWriter.Printf("live slot %s [%d-%d] name %q\n", s.Speaker, s.Start, s.End, s.Name)
			seen[s.Speaker] = true
			if s.Name == "" {
				continue
			}
			named++
			Expect(s.Name).To(Equal(map[string]string{"0": "voice_a", "1": "voice_b"}[s.Speaker]))
		}
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		Expect(named).To(BeNumerically(">", 0), "no live speaker segment was named; slots seen: %v", keys)
	})
})

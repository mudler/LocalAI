package main

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ds(spk int, start, end float64) diarSegmentDoc {
	return diarSegmentDoc{Speaker: spk, Start: start, End: end}
}

// writeGGUFWithArch writes a GGUF v3 file with no tensors and a single string
// KV, parakeet.arch=arch: enough for parakeetArch, which reads metadata only.
func writeGGUFWithArch(path, arch string) {
	GinkgoHelper()
	var b []byte
	b = append(b, "GGUF"...)
	b = binary.LittleEndian.AppendUint32(b, 3) // version
	b = binary.LittleEndian.AppendUint64(b, 0) // tensor count
	b = binary.LittleEndian.AppendUint64(b, 1) // kv count
	str := func(s string) {
		b = binary.LittleEndian.AppendUint64(b, uint64(len(s)))
		b = append(b, s...)
	}
	str("parakeet.arch")
	b = binary.LittleEndian.AppendUint32(b, 8) // GGUF string type
	str(arch)
	Expect(os.WriteFile(path, b, 0o644)).To(Succeed())
}

var _ = Describe("speaker diarization helpers", func() {
	Context("assignSpeakers", func() {
		It("picks the speaker with the largest overlap", func() {
			words := []transcriptWord{tw("a", 0.5, 0.8)}
			segs := []diarSegmentDoc{ds(0, 0.0, 0.6), ds(1, 0.55, 2.0)}
			Expect(assignSpeakers(words, segs)).To(Equal([]int{1}))
		})

		It("snaps a word just outside a segment to it, but not a far one", func() {
			words := []transcriptWord{tw("well", 19.92, 20.00), tw("far", 25.0, 25.2)}
			segs := []diarSegmentDoc{ds(0, 14.78, 18.75), ds(1, 20.10, 23.60)}
			Expect(assignSpeakers(words, segs)).To(Equal([]int{1, -1}))
		})

		It("leaves every word unassigned without segments", func() {
			Expect(assignSpeakers([]transcriptWord{tw("a", 0, 1)}, nil)).To(Equal([]int{-1}))
		})
	})

	Context("splitAtSpeakerChanges", func() {
		It("splits groups where the speaker changes and keeps the rest intact", func() {
			groups := [][]transcriptWord{
				{tw("a", 0, 1), tw("b", 1, 2), tw("c.", 2, 3)},
				{tw("d", 4, 5)},
			}
			out, spk := splitAtSpeakerChanges(groups, []int{0, 0, 1, 1})
			Expect(out).To(HaveLen(3))
			Expect(out[0]).To(HaveLen(2))
			Expect(out[1][0].W).To(Equal("c."))
			Expect(out[2][0].W).To(Equal("d"))
			Expect(spk).To(Equal([]int{0, 1, 1}))
		})
	})

	Context("transcriptResultWithSpeakers", func() {
		It("labels segments with their speaker and splits at speaker turns", func() {
			doc := transcriptJSON{
				Text: "hi there. hello",
				Words: []transcriptWord{
					tw("hi", 0.0, 0.3), tw("there.", 0.3, 0.6), tw("hello", 1.0, 1.4),
				},
			}
			// Punctuation gives [hi there.] [hello]; the turn after "hi" splits
			// the first group.
			res := transcriptResultWithSpeakers(doc, &pb.TranscriptRequest{}, 0, []int{0, 1, 1})
			Expect(res.Segments).To(HaveLen(3))
			Expect(res.Segments[0].Text).To(Equal("hi"))
			Expect(res.Segments[0].Speaker).To(Equal("0"))
			Expect(res.Segments[1].Text).To(Equal("there."))
			Expect(res.Segments[1].Speaker).To(Equal("1"))
			Expect(res.Segments[2].Text).To(Equal("hello"))
			Expect(res.Segments[2].Speaker).To(Equal("1"))

			// With word timestamps requested, words carry their speaker too.
			res = transcriptResultWithSpeakers(doc, &pb.TranscriptRequest{TimestampGranularities: []string{"word"}}, 0, []int{0, 1, 1})
			Expect(res.Segments[0].Words[0].Speaker).To(Equal("0"))
			Expect(res.Segments[2].Words[0].Speaker).To(Equal("1"))
			for i, seg := range res.Segments {
				Expect(seg.Id).To(Equal(int32(i)))
			}
		})

		It("leaves speakers empty without diarization", func() {
			doc := transcriptJSON{Text: "hi.", Words: []transcriptWord{tw("hi.", 0, 0.3)}}
			res := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
			Expect(res.Segments).To(HaveLen(1))
			Expect(res.Segments[0].Speaker).To(BeEmpty())
		})
	})

	Context("diarizeSegments", func() {
		It("fills each segment's text with its speaker's words", func() {
			doc := diarJSON{Speakers: 8, Segments: []diarSegmentDoc{ds(0, 0, 2), ds(1, 2, 4)}}
			words := []transcriptWord{tw("hello", 0.2, 0.6), tw("world", 0.7, 1.1), tw("hi", 2.5, 2.8)}
			segs, n := diarizeSegments(doc, words)
			Expect(n).To(Equal(int32(2)))
			Expect(segs).To(HaveLen(2))
			Expect(segs[0].Speaker).To(Equal("0"))
			Expect(segs[0].Text).To(Equal("hello world"))
			Expect(segs[1].Text).To(Equal("hi"))
		})

		It("leaves text empty without words", func() {
			segs, _ := diarizeSegments(diarJSON{Segments: []diarSegmentDoc{ds(0, 0, 1)}}, nil)
			Expect(segs[0].Text).To(BeEmpty())
		})
	})

	Context("live speaker labels", func() {
		It("assumes an open segment continues through words diarization has not reached", func() {
			d := &liveDiarizer{
				closed: []diarSegmentDoc{ds(0, 0.0, 2.0)},
				active: []diarSegmentDoc{ds(1, 2.5, 3.0)}, // diarized up to 3.0 s
			}
			words := []transcriptWord{tw("a", 0.5, 0.9), tw("b", 2.6, 2.9), tw("c", 3.4, 3.9)}
			Expect(d.speakersFor(words)).To(Equal([]int{0, 1, 1}))
		})

		It("groups labelled words into speaker turns", func() {
			words := []transcriptWord{tw("hi", 0, 0.3), tw("there.", 0.3, 0.6), tw("hello", 1.0, 1.4)}
			segs := speakerTurns(words, []int{0, 0, 1})
			Expect(segs).To(HaveLen(2))
			Expect(segs[0].Text).To(Equal("hi there."))
			Expect(segs[0].Speaker).To(Equal("0"))
			Expect(segs[1].Text).To(Equal("hello"))
			Expect(segs[1].Speaker).To(Equal("1"))
			Expect(segs[1].Id).To(Equal(int32(1)))
		})

		It("picks the speaker covering most of an utterance", func() {
			words := []transcriptWord{tw("a", 0, 0.2), tw("b", 0.2, 1.5), tw("c", 1.5, 1.6)}
			Expect(majoritySpeaker(words, []int{0, 1, 0})).To(Equal(1))
			Expect(majoritySpeaker(words, []int{-1, -1, -1})).To(Equal(-1))
		})

		It("maps diar_latency option values to the C-API modes", func() {
			Expect(diarLatencyModes).To(HaveKeyWithValue("model", int32(0)))
			Expect(diarLatencyModes).To(HaveKeyWithValue(defaultLiveDiarLatency, int32(1)))
			Expect(diarLatencyModes).To(HaveKeyWithValue("ultra_low", int32(3)))
		})
	})

	Context("parakeetArch", func() {
		It("reads parakeet.arch from the GGUF metadata", func() {
			path := filepath.Join(GinkgoT().TempDir(), "m.gguf")
			writeGGUFWithArch(path, "diarization")
			arch, err := parakeetArch(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(arch).To(Equal("diarization"))
		})

		It("fails on a file that is not a GGUF", func() {
			path := filepath.Join(GinkgoT().TempDir(), "x.gguf")
			Expect(os.WriteFile(path, []byte("nope"), 0o644)).To(Succeed())
			_, err := parakeetArch(path)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("options", func() {
		It("resolves diar_model against the models directory", func() {
			opts := &pb.ModelOptions{Options: []string{"batch_max_size:4", "diar_model: diar/model.gguf"}}
			Expect(optString(opts, "diar_model")).To(Equal("diar/model.gguf"))
			Expect(resolveModelPath("/models", "diar/model.gguf")).To(Equal("/models/diar/model.gguf"))
			Expect(resolveModelPath("/models", "/abs/m.gguf")).To(Equal("/abs/m.gguf"))
		})

		It("names the request fields Sortformer ignores", func() {
			Expect(unsupportedDiarizeFields(&pb.DiarizeRequest{NumSpeakers: 2, MinDurationOn: 0.3})).
				To(ConsistOf("num_speakers", "min_duration_on"))
			Expect(unsupportedDiarizeFields(&pb.DiarizeRequest{IncludeText: true})).To(BeEmpty())
		})
	})
})

// diarFixturesOrSkip returns the diarization model, the ASR model and a
// multi-speaker 16 kHz WAV (parakeet.cpp's tests/fixtures/two_speakers.wav:
// two LibriSpeech speakers alternating A-B-A-B), or skips.
func diarFixturesOrSkip() (diarModel, asrModel, wavPath string) {
	diarModel = os.Getenv("PARAKEET_BACKEND_TEST_DIAR_MODEL")
	asrModel = os.Getenv("PARAKEET_BACKEND_TEST_MODEL")
	wavPath = os.Getenv("PARAKEET_BACKEND_TEST_DIAR_WAV")
	if diarModel == "" || asrModel == "" || wavPath == "" {
		Skip("set PARAKEET_BACKEND_TEST_DIAR_MODEL, PARAKEET_BACKEND_TEST_MODEL and " +
			"PARAKEET_BACKEND_TEST_DIAR_WAV to run this spec")
	}
	return diarModel, asrModel, wavPath
}

// turns collapses consecutive repeats: [0 0 1 0] -> [0 1 0].
func turns(labels []string) []string {
	var out []string
	for _, l := range labels {
		if len(out) == 0 || out[len(out)-1] != l {
			out = append(out, l)
		}
	}
	return out
}

var _ = Describe("ParakeetCpp speaker diarization", func() {
	It("diarizes with a diarization model and refuses to transcribe", func() {
		diarModel, _, wavPath := diarFixturesOrSkip()
		ensureLibLoaded()
		if CppDiarizePcm == nil {
			Skip("libparakeet.so has no diarization C-API")
		}
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: diarModel})).To(Succeed())
		defer func() { _ = p.Free() }()

		res, err := p.Diarize(&pb.DiarizeRequest{Dst: wavPath})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.NumSpeakers).To(Equal(int32(2)))
		labels := make([]string, len(res.Segments))
		for i, s := range res.Segments {
			labels[i] = s.Speaker
			Expect(s.End).To(BeNumerically(">", s.Start))
		}
		Expect(turns(labels)).To(Equal([]string{"0", "1", "0", "1"}))

		_, err = p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wavPath})
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
	})

	It("tags transcript segments with speakers when diar_model is attached", func() {
		diarModel, asrModel, wavPath := diarFixturesOrSkip()
		ensureLibLoaded()
		if CppDiarizePcm == nil {
			Skip("libparakeet.so has no diarization C-API")
		}
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{
			ModelFile: asrModel,
			Options:   []string{"diar_model:" + diarModel},
		})).To(Succeed())
		defer func() { _ = p.Free() }()

		res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wavPath, Diarize: true})
		Expect(err).ToNot(HaveOccurred())
		labels := make([]string, len(res.Segments))
		for i, s := range res.Segments {
			labels[i] = s.Speaker
			Expect(s.Speaker).ToNot(BeEmpty(), "segment %d %q has no speaker", i, s.Text)
		}
		Expect(turns(labels)).To(Equal([]string{"0", "1", "0", "1"}))

		// diarize=false keeps the plain transcript.
		plain, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wavPath})
		Expect(err).ToNot(HaveOccurred())
		for _, s := range plain.Segments {
			Expect(s.Speaker).To(BeEmpty())
		}
		Expect(plain.Text).To(Equal(res.Text))

		// Diarize with include_text attributes the words to the segments.
		d, err := p.Diarize(&pb.DiarizeRequest{Dst: wavPath, IncludeText: true})
		Expect(err).ToNot(HaveOccurred())
		var all []string
		for _, s := range d.Segments {
			if s.Text != "" {
				all = append(all, s.Text)
			}
		}
		Expect(strings.Join(all, " ")).To(ContainSubstring("Quilter"))
	})

	It("reports diarization as unimplemented on a plain ASR model", func() {
		_, asrModel, wavPath := diarFixturesOrSkip()
		ensureLibLoaded()
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: asrModel})).To(Succeed())
		defer func() { _ = p.Free() }()
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: wavPath})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
})

var _ = Describe("ParakeetCpp live speaker labels", func() {
	It("labels live words and returns speaker turns in the final result", func() {
		diarModel := os.Getenv("PARAKEET_BACKEND_TEST_DIAR_MODEL")
		streamModel := os.Getenv("PARAKEET_BACKEND_TEST_STREAM_MODEL")
		wavPath := os.Getenv("PARAKEET_BACKEND_TEST_DIAR_WAV")
		if diarModel == "" || streamModel == "" || wavPath == "" {
			Skip("set PARAKEET_BACKEND_TEST_DIAR_MODEL, PARAKEET_BACKEND_TEST_STREAM_MODEL " +
				"(a cache-aware streaming ASR model) and PARAKEET_BACKEND_TEST_DIAR_WAV")
		}
		ensureLibLoaded()
		if !liveDiarizationAvailable() {
			Skip("libparakeet.so has no streaming diarization C-API")
		}
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{
			ModelFile: streamModel,
			Options:   []string{"diar_model:" + diarModel},
		})).To(Succeed())
		defer func() { _ = p.Free() }()

		pcm, _, err := decodeWavMono16k(wavPath)
		Expect(err).ToNot(HaveOccurred())
		in := make(chan *pb.TranscriptLiveRequest)
		out := make(chan *pb.TranscriptLiveResponse, 1024)
		errCh := make(chan error, 1)
		go func() { errCh <- p.AudioTranscriptionLive(in, out) }()
		in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{}}}
		for lo := 0; lo < len(pcm); lo += 1600 {
			hi := min(lo+1600, len(pcm))
			in <- &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Audio{
				Audio: &pb.TranscriptLiveAudio{Pcm: pcm[lo:hi]}}}
		}
		close(in)
		Expect(<-errCh).To(Succeed())

		var live []string
		var final *pb.TranscriptResult
		for r := range out {
			for _, w := range r.GetWords() {
				live = append(live, w.GetSpeaker())
			}
			if r.GetFinalResult() != nil {
				final = r.GetFinalResult()
			}
		}
		Expect(live).ToNot(BeEmpty())
		labelled := 0
		for _, l := range live {
			if l != "" {
				labelled++
			}
		}
		// Words finalized before diarization's first chunk (1.04 s) have no
		// speaker yet; nearly all others do.
		Expect(labelled).To(BeNumerically(">=", len(live)*9/10), "live labels: %v", live)
		Expect(turns(live)).To(ContainElements("0", "1"))

		Expect(final).ToNot(BeNil())
		labels := make([]string, len(final.Segments))
		for i, s := range final.Segments {
			labels[i] = s.Speaker
		}
		Expect(turns(labels)).To(Equal([]string{"0", "1", "0", "1"}), "final turns: %v", labels)
	})
})

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// writeMono16kWAV writes `frames` samples of 16 kHz mono 16-bit silence.
// That is already AudioToWav's target format, so the decode path copies the
// file through instead of shelling out to ffmpeg, which the test host may not
// have.
func writeMono16kWAV(path string, frames int) {
	GinkgoHelper()
	f, err := os.Create(path)
	Expect(err).ToNot(HaveOccurred())
	enc := wav.NewEncoder(f, 16000, 16, 1, 1)
	Expect(enc.Write(&audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: 16000},
		SourceBitDepth: 16,
		Data:           make([]int, frames),
	})).To(Succeed())
	Expect(enc.Close()).To(Succeed())
	Expect(f.Close()).To(Succeed())
}

var _ = Describe("wordsToSegments", func() {
	It("groups words into one segment per speaker run", func() {
		words := []asrWord{
			{Text: "hello", Start: 0, End: 400, Speaker: 1},
			{Text: "there", Start: 400, End: 800, Speaker: 1},
			{Text: "hi", Start: 900, End: 1200, Speaker: 2},
		}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Text).To(Equal("hello there"))
		Expect(segs[1].Text).To(Equal("hi"))
	})

	// A run is bounded by a CHANGE of speaker, not by the speaker id being new.
	// Grouping that keyed on the id itself (a map, or a comparison against the
	// first word) would merge the two A turns into one segment spanning B, and
	// the three-word spec above cannot see that because it never returns to an
	// earlier speaker.
	It("starts a new segment when an earlier speaker takes another turn", func() {
		words := []asrWord{
			{Text: "one", Start: 0, End: 100, Speaker: 1},
			{Text: "two", Start: 100, End: 200, Speaker: 2},
			{Text: "three", Start: 200, End: 300, Speaker: 1},
		}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(3))
		Expect(segs[0].Text).To(Equal("one"))
		Expect(segs[1].Text).To(Equal("two"))
		Expect(segs[2].Text).To(Equal("three"))
	})

	// TranscriptSegment.start/end are int64 nanoseconds, not seconds:
	// core/backend/transcript.go reads them straight into a time.Duration. The
	// runtime reports word offsets in milliseconds (src/asr/types.h:46).
	It("converts millisecond word times to nanoseconds", func() {
		words := []asrWord{{Text: "a", Start: 1500, End: 2250, Speaker: 0}}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(1))
		Expect(time.Duration(segs[0].Start)).To(Equal(1500 * time.Millisecond))
		Expect(time.Duration(segs[0].End)).To(Equal(2250 * time.Millisecond))
	})

	It("spans a segment from its first word's start to its last word's end", func() {
		words := []asrWord{
			{Text: "a", Start: 100, End: 200, Speaker: 0},
			{Text: "b", Start: 500, End: 900, Speaker: 0},
		}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(1))
		Expect(time.Duration(segs[0].Start)).To(Equal(100 * time.Millisecond))
		Expect(time.Duration(segs[0].End)).To(Equal(900 * time.Millisecond))
	})

	It("produces a single segment when no speaker tags are present", func() {
		words := []asrWord{
			{Text: "a", Start: 0, End: 100, Speaker: 0},
			{Text: "b", Start: 100, End: 200, Speaker: 0},
		}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Text).To(Equal("a b"))
	})

	It("returns no segments for no words", func() {
		Expect(wordsToSegments(nil, false)).To(BeEmpty())
		Expect(wordsToSegments([]asrWord{}, false)).To(BeEmpty())
	})

	It("numbers the segments from zero in order", func() {
		words := []asrWord{
			{Text: "a", Speaker: 1},
			{Text: "b", Speaker: 2},
			{Text: "c", Speaker: 3},
		}
		segs := wordsToSegments(words, false)
		Expect(segs).To(HaveLen(3))
		for i, s := range segs {
			Expect(s.Id).To(Equal(int32(i)))
		}
	})

	// TranscriptSegment.Words is what core/backend/transcript.go turns into the
	// response's word list, so an unset one makes timestamp_granularities:
	// ["word"] come back empty however good the timings were.
	It("attaches the per-word timings only when they were asked for", func() {
		words := []asrWord{
			{Text: "a", Start: 0, End: 100},
			{Text: "b", Start: 100, End: 250},
		}
		with := wordsToSegments(words, true)
		Expect(with[0].Words).To(HaveLen(2))
		Expect(with[0].Words[1].Text).To(Equal("b"))
		Expect(time.Duration(with[0].Words[1].Start)).To(Equal(100 * time.Millisecond))
		Expect(time.Duration(with[0].Words[1].End)).To(Equal(250 * time.Millisecond))

		Expect(wordsToSegments(words, false)[0].Words).To(BeEmpty())
	})

	// A speaker change splits the run, and each segment must carry only its own
	// words rather than the whole utterance's.
	It("gives each speaker run only its own words", func() {
		segs := wordsToSegments([]asrWord{
			{Text: "a", Speaker: 1},
			{Text: "b", Speaker: 2},
		}, true)
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Words).To(HaveLen(1))
		Expect(segs[0].Words[0].Text).To(Equal("a"))
		Expect(segs[1].Words[0].Text).To(Equal("b"))
	})

	// The C ABI documents the speaker tag as 1-based with 0 meaning "untagged",
	// so a run of untagged words must not come back attributed to a speaker
	// literally named "0".
	It("labels a diarized run and leaves an untagged one unlabelled", func() {
		Expect(wordsToSegments([]asrWord{{Text: "a", Speaker: 2}}, false)[0].Speaker).To(Equal("2"))
		Expect(wordsToSegments([]asrWord{{Text: "a", Speaker: 0}}, false)[0].Speaker).To(BeEmpty())
	})
})

var _ = Describe("wordsRequested", func() {
	It("recognises the OpenAI word granularity in any casing or padding", func() {
		Expect(wordsRequested([]string{"word"})).To(BeTrue())
		Expect(wordsRequested([]string{"segment", " Word "})).To(BeTrue())
	})

	It("defaults to segment level", func() {
		Expect(wordsRequested(nil)).To(BeFalse())
		Expect(wordsRequested([]string{"segment"})).To(BeFalse())
	})
})

var _ = Describe("recognizeF32", func() {
	// &pcm[0] panics on a zero-length slice, and a silent or empty upload is
	// ordinary input rather than an exotic one. The C side rejects empty audio
	// too, but Go never gets that far.
	It("refuses empty audio instead of indexing an empty slice", func() {
		// A zero options struct is enough: the guard has to fire before the
		// options are ever handed across the ABI, and building real ones would
		// need the library bound, which this spec deliberately does not.
		opts := cASRRecognitionOptions{}
		for _, pcm := range [][]float32{nil, {}} {
			var (
				handle uintptr
				err    error
			)
			Expect(func() { handle, err = recognizeF32(0, &opts, pcm, 16000) }).ToNot(Panic())
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("empty audio"))
			Expect(handle).To(BeZero())
		}
	})
})

var _ = Describe("AudioTranscription", func() {
	// The gate has to fire before anything expensive: a model loaded as TTS
	// cannot transcribe whatever the request says, and reading the audio first
	// would report a file problem for a configuration one.
	It("refuses a model loaded as another family, before it reads the audio", func() {
		n := &NemoSpeech{fam: familyTTS}
		_, err := n.AudioTranscription(context.Background(), &pb.TranscriptRequest{
			Dst: filepath.Join(GinkgoT().TempDir(), "does-not-exist.wav"),
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(err.Error()).To(ContainSubstring("tts"))
	})

	It("refuses an unloaded model", func() {
		n := &NemoSpeech{}
		_, err := n.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: "ignored.wav"})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	// A lock leaked on a rejection path deadlocks the next request rather than
	// failing it, which is far harder to diagnose than the failure itself.
	It("releases the engine lock on every rejection path", func() {
		n := &NemoSpeech{fam: familyTTS}
		_, err := n.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: "x.wav"})
		Expect(err).To(HaveOccurred())
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("reports an audio file it cannot read", func() {
		n := &NemoSpeech{fam: familyASR}
		_, err := n.AudioTranscription(context.Background(), &pb.TranscriptRequest{
			Dst: filepath.Join(GinkgoT().TempDir(), "absent.wav"),
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("requires a destination path", func() {
		n := &NemoSpeech{fam: familyASR}
		_, err := n.AudioTranscription(context.Background(), &pb.TranscriptRequest{})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	// The whole rejection path end to end, on the input that actually reaches
	// it: a silent or truncated upload decodes to zero samples, and the guard
	// has to fire between the decode and the ABI. Nothing is loaded here (no
	// recognizer, and the specs that bind the library may not have run), so
	// this also pins the ORDER: a guard placed after the options are built
	// calls a nil-bound entry point and panics rather than failing.
	It("refuses a decodable clip that carries no samples", func() {
		path := filepath.Join(GinkgoT().TempDir(), "silence.wav")
		writeMono16kWAV(path, 0)

		n := &NemoSpeech{fam: familyASR, recognizer: 0}
		var err error
		Expect(func() {
			_, err = n.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: path})
		}).ToNot(Panic())
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("empty audio"))
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})
})

var _ = Describe("sampleRateOf", func() {
	// 0 is not "unknown" to this runtime: nemo_speech_asr_recognize_f32 and
	// nemo_speech_asr_stream_push_f32 both read a 0 rate as "these samples are
	// already at the model rate" and skip resampling. Falling back to it for an
	// undecodable header would silently pitch-shift the audio instead of
	// failing, so an unknown rate has to be an error.
	It("rejects a buffer whose format the decoder did not fill in", func() {
		_, err := sampleRateOf(&audio.IntBuffer{})
		Expect(err).To(HaveOccurred())
	})

	It("rejects a non-positive sample rate", func() {
		_, err := sampleRateOf(&audio.IntBuffer{Format: &audio.Format{SampleRate: 0, NumChannels: 1}})
		Expect(err).To(HaveOccurred())
	})

	// The WAV header carries the sample rate as an unsigned 32-bit field, which
	// go-audio widens to int. Anything above the int32 range therefore passes a
	// "> 0" test and then narrows to a NEGATIVE rate, which the runtime would take
	// as a resampling ratio rather than reject. The failure is silent, so the
	// bound is asserted rather than left to the caller.
	//
	// Written as a conversion plus one rather than as the constant MaxInt32+1:
	// the untyped form does not fit an int on a 32-bit build and would not
	// compile there, while this wraps to a negative rate the same guard rejects.
	It("rejects a rate that would not survive the narrowing to int32", func() {
		_, err := sampleRateOf(&audio.IntBuffer{
			Format: &audio.Format{SampleRate: int(math.MaxInt32) + 1, NumChannels: 1},
		})
		Expect(err).To(HaveOccurred())
	})

	It("returns the decoded rate", func() {
		rate, err := sampleRateOf(&audio.IntBuffer{Format: &audio.Format{SampleRate: 22050, NumChannels: 1}})
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int32(22050)))
	})
})

var _ = Describe("decodeAudioMono16k", func() {
	It("decodes a 16 kHz mono WAV to float32 samples at its own rate", func() {
		path := filepath.Join(GinkgoT().TempDir(), "silence.wav")
		writeMono16kWAV(path, 800)

		pcm, rate, err := decodeAudioMono16k(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(rate).To(Equal(int32(16000)))
		Expect(pcm).To(HaveLen(800))
	})

	// A zero-frame WAV is what a truncated upload decodes to, and it is the
	// input recognizeF32's guard exists for.
	It("decodes a WAV with no frames to an empty slice", func() {
		path := filepath.Join(GinkgoT().TempDir(), "empty.wav")
		writeMono16kWAV(path, 0)

		pcm, _, err := decodeAudioMono16k(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(pcm).To(BeEmpty())
	})

	It("reports a file that does not exist", func() {
		_, _, err := decodeAudioMono16k(filepath.Join(GinkgoT().TempDir(), "nope.wav"))
		Expect(err).To(HaveOccurred())
	})
})

// The six frame counts on the recognizer-attached diarizer are
// sentinel-sensitive and invisible to every other check in the tree.
// src/asr/c_api.cpp:151-165 applies five of them when they are > 0 but applies
// left_context_frames when it is >= 0, so a dropped -1 does not fall back to
// the model's own streaming geometry, it pins the left context to zero. The
// struct is the right shape either way, so abi_test.go's layout assertions
// cannot see it.
var _ = Describe("asrDiarConfig", func() {
	It("keeps the model path it was given", func() {
		Expect(asrDiarConfig(42).ModelPath).To(Equal(uintptr(42)))
	})

	// A config sent with the wrong size has every field past it ignored by
	// HAS_FIELD, and the diarizer attaches with defaults instead of failing.
	It("declares the size the runtime validates against", func() {
		Expect(asrDiarConfig(42).Size).To(Equal(unsafe.Sizeof(cASRDiarConfig{})))
	})

	It("leaves every frame count at the sentinel that means default", func() {
		cfg := asrDiarConfig(42)
		Expect(cfg.ChunkFrames).To(Equal(diarGeometryDefault))
		Expect(cfg.RightContextFrames).To(Equal(diarGeometryDefault))
		Expect(cfg.LeftContextFrames).To(Equal(diarGeometryDefault))
		Expect(cfg.FIFOFrames).To(Equal(diarGeometryDefault))
		Expect(cfg.SpkcacheFrames).To(Equal(diarGeometryDefault))
		Expect(cfg.UpdatePeriodFrames).To(Equal(diarGeometryDefault))
	})

	// Stated separately from the field-by-field assertions above: the whole
	// group is only "unset" to the runtime while the sentinel stays negative,
	// and zero is a value it would apply to the left context.
	It("uses a negative sentinel, not zero", func() {
		Expect(diarGeometryDefault).To(BeNumerically("<", 0))
	})
})

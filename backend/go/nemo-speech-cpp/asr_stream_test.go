package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeSession is a scripted asrSession. It stands in for the streaming C API,
// not for a model: no NeMo GGUF is small enough to keep in the tree, and the
// need-more-audio drain is the easiest thing in this file to get subtly wrong
// (a mishandled NULL either spins forever or drops every result).
//
// script is one batch of results per drain. next() hands back the current
// batch one result at a time and then reports "need more audio" exactly once,
// which advances to the next batch. That is precisely the C contract:
// nemo_speech_asr_stream_next returns OK with a NULL handle when the decoder
// has consumed the buffered audio, and the loop must resume after the next
// push rather than treat it as the end of the stream.
type fakeSession struct {
	script [][]streamResult
	batch  int
	pos    int

	pushed   [][]float32
	rates    []int32
	finished int
	closed   int

	pushErr   error
	finishErr error
	nextErr   error
}

func (f *fakeSession) push(pcm []float32, sampleRate int32) error {
	if f.pushErr != nil {
		return f.pushErr
	}
	f.pushed = append(f.pushed, pcm)
	f.rates = append(f.rates, sampleRate)
	return nil
}

func (f *fakeSession) finish() error {
	if f.finishErr != nil {
		return f.finishErr
	}
	f.finished++
	return nil
}

func (f *fakeSession) next() (streamResult, bool, error) {
	if f.nextErr != nil {
		return streamResult{}, false, f.nextErr
	}
	if f.batch >= len(f.script) {
		return streamResult{}, false, nil
	}
	if f.pos >= len(f.script[f.batch]) {
		f.batch++
		f.pos = 0
		return streamResult{}, false, nil
	}
	r := f.script[f.batch][f.pos]
	f.pos++
	return r, true, nil
}

func (f *fakeSession) close() { f.closed++ }

// samples returns the flat concatenation of everything pushed, so a spec can
// assert the whole clip reached the engine without caring how it was sliced.
func (f *fakeSession) samples() []float32 {
	var out []float32
	for _, c := range f.pushed {
		out = append(out, c...)
	}
	return out
}

// collect drains a response channel into a slice. The channels are unbuffered
// in the specs on purpose: a producer that stops honouring cancellation would
// otherwise fill a buffer and look healthy.
func collect[T any](ch chan T) chan []T {
	done := make(chan []T, 1)
	go func() {
		var got []T
		for v := range ch {
			got = append(got, v)
		}
		done <- got
	}()
	return done
}

var _ = Describe("chunkPCM", func() {
	It("splits into equal chunks when evenly divisible", func() {
		chunks := chunkPCM(make([]float32, 400), 100)
		Expect(chunks).To(HaveLen(4))
		for _, c := range chunks {
			Expect(c).To(HaveLen(100))
		}
	})

	// Padding the tail with silence would push phantom audio through the
	// encoder and shift the tail word timings, so the final chunk stays short.
	It("makes the final chunk short rather than padding it", func() {
		chunks := chunkPCM(make([]float32, 250), 100)
		Expect(chunks).To(HaveLen(3))
		Expect(chunks[2]).To(HaveLen(50))
	})

	It("returns one chunk when the input is shorter than the chunk size", func() {
		chunks := chunkPCM(make([]float32, 10), 100)
		Expect(chunks).To(HaveLen(1))
		Expect(chunks[0]).To(HaveLen(10))
	})

	It("returns nothing for empty input", func() {
		Expect(chunkPCM(nil, 100)).To(BeEmpty())
		Expect(chunkPCM([]float32{}, 100)).To(BeEmpty())
	})

	// Every spec above works on all-zero audio, so none of them can tell a
	// correct slicing from one that reorders or repeats windows. Audio fed out
	// of order still decodes, it just decodes to nonsense.
	It("preserves sample order across the chunk boundaries", func() {
		pcm := []float32{1, 2, 3, 4, 5}
		chunks := chunkPCM(pcm, 2)
		Expect(chunks).To(HaveLen(3))
		Expect(chunks[0]).To(Equal([]float32{1, 2}))
		Expect(chunks[1]).To(Equal([]float32{3, 4}))
		Expect(chunks[2]).To(Equal([]float32{5}))
	})
})

var _ = Describe("drain", func() {
	It("emits every result in a batch and stops on need-more-audio", func() {
		sess := &fakeSession{script: [][]streamResult{
			{{Text: "a"}, {Text: "b", Final: true}},
			{{Text: "c"}},
		}}
		var got []string
		Expect(drain(sess, func(r streamResult) error {
			got = append(got, r.Text)
			return nil
		})).To(Succeed())
		Expect(got).To(Equal([]string{"a", "b"}))
	})

	// The NULL handle is a pause, not an end: the next drain, after more audio
	// has been pushed, must pick the stream back up.
	It("resumes on the next drain after a need-more-audio pause", func() {
		sess := &fakeSession{script: [][]streamResult{{{Text: "a"}}, {{Text: "b"}}}}
		var got []string
		emit := func(r streamResult) error { got = append(got, r.Text); return nil }
		Expect(drain(sess, emit)).To(Succeed())
		Expect(drain(sess, emit)).To(Succeed())
		Expect(got).To(Equal([]string{"a", "b"}))
	})

	It("returns nothing and no error for a stream with no results ready", func() {
		var got []string
		Expect(drain(&fakeSession{}, func(r streamResult) error {
			got = append(got, r.Text)
			return nil
		})).To(Succeed())
		Expect(got).To(BeEmpty())
	})

	It("propagates a failure from the runtime", func() {
		sess := &fakeSession{nextErr: errors.New("boom")}
		Expect(drain(sess, func(streamResult) error { return nil })).To(MatchError(ContainSubstring("boom")))
	})

	It("stops pulling once emit fails", func() {
		sess := &fakeSession{script: [][]streamResult{{{Text: "a"}, {Text: "b"}}}}
		Expect(drain(sess, func(streamResult) error {
			return errors.New("send failed")
		})).To(MatchError(ContainSubstring("send failed")))
		Expect(sess.pos).To(Equal(1))
	})
})

var _ = Describe("streamPCM", func() {
	streamWords := func(ctx context.Context, sess asrSession, pcm []float32, rate int32, wantWords bool) ([]*pb.TranscriptStreamResponse, error) {
		GinkgoHelper()
		results := make(chan *pb.TranscriptStreamResponse)
		done := collect(results)
		err := streamPCM(ctx, sess, pcm, rate, wantWords, results)
		close(results)
		return <-done, err
	}
	stream := func(ctx context.Context, sess asrSession, pcm []float32, rate int32) ([]*pb.TranscriptStreamResponse, error) {
		GinkgoHelper()
		return streamWords(ctx, sess, pcm, rate, false)
	}

	It("pushes the whole clip in chunks at the clip's own sample rate", func() {
		sess := &fakeSession{}
		pcm := make([]float32, streamChunkSamples*2+7)
		_, err := stream(context.Background(), sess, pcm, 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(sess.pushed).To(HaveLen(3))
		Expect(sess.samples()).To(HaveLen(len(pcm)))
		for _, r := range sess.rates {
			Expect(r).To(Equal(int32(16000)))
		}
	})

	It("finishes the stream once, after the last chunk", func() {
		sess := &fakeSession{}
		_, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(sess.finished).To(Equal(1))
	})

	// Interims are the decoder's running hypothesis for the utterance in
	// flight. The wire contract is that delta is newly FINALIZED text and that
	// concatenating the deltas reproduces the transcript, so forwarding an
	// interim would duplicate every word it later re-sends inside the final.
	It("emits a delta per final and nothing for interims", func() {
		sess := &fakeSession{script: [][]streamResult{{
			{Text: "hel"},
			{Text: "hello"},
			{Text: "Hello.", Final: true},
		}}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())

		var deltas []string
		for _, r := range got {
			if r.GetDelta() != "" {
				deltas = append(deltas, r.GetDelta())
			}
		}
		Expect(deltas).To(Equal([]string{"Hello."}))
	})

	It("reproduces the final transcript by concatenating the deltas", func() {
		sess := &fakeSession{script: [][]streamResult{{
			{Text: "One.", Final: true},
			{Text: "Two.", Final: true},
		}}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())

		var joined string
		var final *pb.TranscriptResult
		for _, r := range got {
			joined += r.GetDelta()
			if r.GetFinalResult() != nil {
				final = r.GetFinalResult()
			}
		}
		Expect(final).ToNot(BeNil())
		Expect(final.GetText()).To(Equal("One. Two."))
		Expect(joined).To(Equal(final.GetText()))
	})

	It("sends the terminal final result last and only once", func() {
		sess := &fakeSession{script: [][]streamResult{{{Text: "hi", Final: true}}}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).ToNot(BeEmpty())

		var finals int
		for _, r := range got {
			if r.GetFinalResult() != nil {
				finals++
			}
		}
		Expect(finals).To(Equal(1))
		Expect(got[len(got)-1].GetFinalResult()).ToNot(BeNil())
	})

	It("reports the clip duration in seconds", func() {
		sess := &fakeSession{}
		got, err := stream(context.Background(), sess, make([]float32, 8000), 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(got[len(got)-1].GetFinalResult().GetDuration()).To(BeNumerically("~", 0.5, 1e-6))
	})

	It("builds per-utterance segments with nanosecond timestamps", func() {
		sess := &fakeSession{script: [][]streamResult{{
			{Text: "one", Final: true, Words: []asrWord{{Text: "one", Start: 0, End: 500}}},
			{Text: "two", Final: true, Words: []asrWord{{Text: "two", Start: 900, End: 1400}}},
		}}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())

		segs := got[len(got)-1].GetFinalResult().GetSegments()
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].GetId()).To(Equal(int32(0)))
		Expect(segs[1].GetId()).To(Equal(int32(1)))
		Expect(time.Duration(segs[1].GetStart())).To(Equal(900 * time.Millisecond))
		Expect(time.Duration(segs[1].GetEnd())).To(Equal(1400 * time.Millisecond))
	})

	// core/backend/transcript.go builds the response's word list out of
	// TranscriptSegment.Words, so leaving it unset makes
	// timestamp_granularities: ["word"] come back empty.
	It("attaches the word timings only when they were asked for", func() {
		script := func() [][]streamResult {
			return [][]streamResult{{{Text: "one", Final: true,
				Words: []asrWord{{Text: "one", Start: 100, End: 500}}}}}
		}

		got, err := streamWords(context.Background(), &fakeSession{script: script()}, make([]float32, 10), 16000, true)
		Expect(err).ToNot(HaveOccurred())
		segs := got[len(got)-1].GetFinalResult().GetSegments()
		Expect(segs[0].GetWords()).To(HaveLen(1))
		Expect(segs[0].GetWords()[0].GetText()).To(Equal("one"))
		Expect(time.Duration(segs[0].GetWords()[0].GetStart())).To(Equal(100 * time.Millisecond))

		got, err = streamWords(context.Background(), &fakeSession{script: script()}, make([]float32, 10), 16000, false)
		Expect(err).ToNot(HaveOccurred())
		segs = got[len(got)-1].GetFinalResult().GetSegments()
		Expect(segs[0].GetText()).To(Equal("one"))
		Expect(segs[0].GetWords()).To(BeEmpty())
	})

	// The flush that nemo_speech_asr_stream_finish triggers returns whatever the
	// decoder was still holding. Nothing held back means the last endpoint
	// consumed the audio, which is exactly "the clip ended on an utterance
	// boundary"; text coming back means it ended mid-utterance.
	It("marks eou when the tail flush had nothing left to emit", func() {
		sess := &fakeSession{script: [][]streamResult{
			{{Text: "done.", Final: true}},
			{{Text: "", Final: true}},
		}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(got[len(got)-1].GetFinalResult().GetEou()).To(BeTrue())
	})

	It("does not mark eou when the tail flush produced text", func() {
		sess := &fakeSession{script: [][]streamResult{
			{{Text: "done.", Final: true}},
			{{Text: "and more", Final: true}},
		}}
		got, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).ToNot(HaveOccurred())
		Expect(got[len(got)-1].GetFinalResult().GetEou()).To(BeFalse())
	})

	// The RPC body runs inside withEngine, so it holds the engine mutex for the
	// whole stream and Free waits on it. A loop that ignored cancellation would
	// pin the model against unload for as long as a disconnected client's audio
	// takes to push.
	It("stops promptly when the request context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sess := &fakeSession{}
		_, err := stream(ctx, sess, make([]float32, streamChunkSamples*4), 16000)
		Expect(status.Code(err)).To(Equal(codes.Canceled))
		Expect(sess.pushed).To(BeEmpty())
	})

	It("reports a push failure", func() {
		sess := &fakeSession{pushErr: errors.New("push blew up")}
		_, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).To(MatchError(ContainSubstring("push blew up")))
	})

	It("reports a finish failure", func() {
		sess := &fakeSession{finishErr: errors.New("finish blew up")}
		_, err := stream(context.Background(), sess, make([]float32, 10), 16000)
		Expect(err).To(MatchError(ContainSubstring("finish blew up")))
	})
})

var _ = Describe("runLive", func() {
	// live drives runLive against a fake opener and returns everything the RPC
	// wrote plus the sessions it opened.
	live := func(reqs []*pb.TranscriptLiveRequest, script ...[][]streamResult) ([]*pb.TranscriptLiveResponse, []*fakeSession, error) {
		GinkgoHelper()
		var opened []*fakeSession
		open := func(language string) (asrSession, error) {
			s := &fakeSession{}
			if len(opened) < len(script) {
				s.script = script[len(opened)]
			}
			opened = append(opened, s)
			return s, nil
		}

		in := make(chan *pb.TranscriptLiveRequest)
		out := make(chan *pb.TranscriptLiveResponse)
		done := collect(out)
		go func() {
			defer close(in)
			for _, r := range reqs {
				in <- r
			}
		}()
		err := runLive(open, in, out)
		close(out)
		return <-done, opened, err
	}

	cfg := func(rate int32) *pb.TranscriptLiveRequest {
		return &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Config{
			Config: &pb.TranscriptLiveConfig{SampleRate: rate},
		}}
	}
	audio := func(pcm ...float32) *pb.TranscriptLiveRequest {
		return &pb.TranscriptLiveRequest{Payload: &pb.TranscriptLiveRequest_Audio{
			Audio: &pb.TranscriptLiveAudio{Pcm: pcm},
		}}
	}

	It("requires the first message to carry a config", func() {
		_, opened, err := live([]*pb.TranscriptLiveRequest{audio(1, 2, 3)})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(opened).To(BeEmpty())
	})

	It("returns without error when the caller closes without sending anything", func() {
		got, opened, err := live(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
		Expect(opened).To(BeEmpty())
	})

	// Callers block on the first Recv waiting for this ack, and degrade to
	// non-live transcription when it does not arrive.
	It("acknowledges a successful open before any transcript", func() {
		got, _, err := live([]*pb.TranscriptLiveRequest{cfg(0)})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).ToNot(BeEmpty())
		Expect(got[0].GetReady()).To(BeTrue())
	})

	// The proto documents 0 as "16 kHz". The C API reads 0 as "these samples
	// are already at the model rate" and skips resampling, so forwarding the
	// zero through would silently mean something else.
	It("resolves the default sample rate to 16 kHz before pushing", func() {
		_, opened, err := live([]*pb.TranscriptLiveRequest{cfg(0), audio(1, 2, 3)})
		Expect(err).ToNot(HaveOccurred())
		Expect(opened).To(HaveLen(1))
		Expect(opened[0].rates).To(Equal([]int32{16000}))
	})

	It("pushes at the configured sample rate", func() {
		_, opened, err := live([]*pb.TranscriptLiveRequest{cfg(8000), audio(1, 2, 3)})
		Expect(err).ToNot(HaveOccurred())
		Expect(opened[0].rates).To(Equal([]int32{8000}))
		Expect(opened[0].samples()).To(Equal([]float32{1, 2, 3}))
	})

	It("rejects a sample rate the runtime cannot resample", func() {
		_, opened, err := live([]*pb.TranscriptLiveRequest{cfg(4000)})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(opened).To(BeEmpty())
	})

	It("ignores an empty audio frame instead of pushing it", func() {
		_, opened, err := live([]*pb.TranscriptLiveRequest{cfg(0), audio()})
		Expect(err).ToNot(HaveOccurred())
		Expect(opened[0].pushed).To(BeEmpty())
	})

	It("streams a delta with its words and marks the utterance boundary", func() {
		got, _, err := live(
			[]*pb.TranscriptLiveRequest{cfg(0), audio(1)},
			[][]streamResult{{
				{Text: "partial"},
				{Text: "Hello there.", Final: true, Words: []asrWord{
					{Text: "Hello", Start: 100, End: 400},
					{Text: "there", Start: 400, End: 900},
				}},
			}},
		)
		Expect(err).ToNot(HaveOccurred())

		var deltas []*pb.TranscriptLiveResponse
		for _, r := range got {
			if r.GetDelta() != "" {
				deltas = append(deltas, r)
			}
		}
		Expect(deltas).To(HaveLen(1))
		Expect(deltas[0].GetDelta()).To(Equal("Hello there."))
		Expect(deltas[0].GetEou()).To(BeTrue())
		Expect(deltas[0].GetWords()).To(HaveLen(2))
		Expect(time.Duration(deltas[0].GetWords()[1].GetStart())).To(Equal(400 * time.Millisecond))
		Expect(time.Duration(deltas[0].GetWords()[1].GetEnd())).To(Equal(900 * time.Millisecond))
	})

	It("finishes and closes the session when the caller closes the send side", func() {
		got, opened, err := live(
			[]*pb.TranscriptLiveRequest{cfg(0), audio(1)},
			[][]streamResult{{{Text: "one.", Final: true}}, {{Text: "two.", Final: true}}},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(opened[0].finished).To(Equal(1))
		Expect(opened[0].closed).To(Equal(1))
		Expect(got[len(got)-1].GetFinalResult()).ToNot(BeNil())
		Expect(got[len(got)-1].GetFinalResult().GetText()).To(Equal("one. two."))
	})

	// The live path is the one with a consumer that really concatenates: the
	// realtime semantic-VAD path joins the accumulated deltas with the empty
	// string and clears them only at a turn reset, never at an utterance
	// boundary. A separator added when assembling the terminal text instead of
	// inside the delta makes the running caption read "one.two." while the
	// committed transcript reads "one. two.".
	It("reproduces the final transcript by concatenating the deltas", func() {
		got, _, err := live(
			[]*pb.TranscriptLiveRequest{cfg(0), audio(1)},
			[][]streamResult{{{Text: "one.", Final: true}}, {{Text: "two.", Final: true}}},
		)
		Expect(err).ToNot(HaveOccurred())

		var joined string
		var final *pb.TranscriptResult
		for _, r := range got {
			joined += r.GetDelta()
			if r.GetFinalResult() != nil {
				final = r.GetFinalResult()
			}
		}
		Expect(final).ToNot(BeNil())
		Expect(final.GetText()).To(Equal("one. two."))
		Expect(joined).To(Equal(final.GetText()))
	})

	// Eou is the model's endpoint, which is a user yielding the turn. The final
	// that comes back from the tail flush is the end of the stream: the send
	// side has already closed, so reporting a turn boundary there tells the
	// turn detector something that did not happen.
	It("marks the endpoint finals but not the tail flush", func() {
		got, _, err := live(
			[]*pb.TranscriptLiveRequest{cfg(0), audio(1)},
			[][]streamResult{{{Text: "one.", Final: true}}, {{Text: "two.", Final: true}}},
		)
		Expect(err).ToNot(HaveOccurred())

		var eous []bool
		for _, r := range got {
			if r.GetDelta() != "" {
				eous = append(eous, r.GetEou())
			}
		}
		Expect(eous).To(Equal([]bool{true, false}))
	})

	// A rate cannot change inside a stream and the decoder keeps no state
	// across a reset, so a second config has to be a fresh session, not a
	// reconfigured one.
	It("opens a fresh session on a mid-stream config and drops the old transcript", func() {
		got, opened, err := live(
			[]*pb.TranscriptLiveRequest{cfg(0), audio(1), cfg(0), audio(2)},
			[][]streamResult{{{Text: "dropped.", Final: true}}},
			[][]streamResult{{{Text: "kept.", Final: true}}},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(opened).To(HaveLen(2))
		Expect(opened[0].closed).To(Equal(1))
		Expect(got[len(got)-1].GetFinalResult().GetText()).To(Equal("kept."))
	})

	It("reports a push failure and still closes the session", func() {
		var opened []*fakeSession
		open := func(string) (asrSession, error) {
			s := &fakeSession{pushErr: errors.New("push blew up")}
			opened = append(opened, s)
			return s, nil
		}
		in := make(chan *pb.TranscriptLiveRequest, 2)
		in <- cfg(0)
		in <- audio(1, 2)
		close(in)
		out := make(chan *pb.TranscriptLiveResponse, 8)

		err := runLive(open, in, out)
		Expect(err).To(MatchError(ContainSubstring("push blew up")))
		Expect(opened[0].closed).To(Equal(1))
	})

	It("propagates a failure to open the session", func() {
		open := func(string) (asrSession, error) { return nil, errors.New("no streaming here") }
		in := make(chan *pb.TranscriptLiveRequest, 1)
		in <- cfg(0)
		close(in)
		out := make(chan *pb.TranscriptLiveResponse, 8)
		Expect(runLive(open, in, out)).To(MatchError(ContainSubstring("no streaming here")))
	})
})

var _ = Describe("AudioTranscriptionStream", func() {
	run := func(ctx context.Context, n *NemoSpeech, req *pb.TranscriptRequest) ([]*pb.TranscriptStreamResponse, error) {
		GinkgoHelper()
		results := make(chan *pb.TranscriptStreamResponse)
		done := collect(results)
		err := n.AudioTranscriptionStream(ctx, req, results)
		return <-done, err
	}

	// The RPC owns the channel: the gRPC host ranges over it and only returns
	// once it closes, so a rejection path that forgets to close hangs the call
	// instead of failing it.
	It("closes the results channel on every rejection path", func() {
		for _, n := range []*NemoSpeech{{fam: familyTTS}, {fam: familyASR}, {}} {
			_, err := run(context.Background(), n, &pb.TranscriptRequest{})
			Expect(err).To(HaveOccurred())
			Expect(n.engineMu.TryLock()).To(BeTrue())
			n.engineMu.Unlock()
		}
	})

	It("refuses a model loaded as another family", func() {
		n := &NemoSpeech{fam: familyTTS}
		_, err := run(context.Background(), n, &pb.TranscriptRequest{Dst: "x.wav"})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(err.Error()).To(ContainSubstring("tts"))
	})

	It("requires a destination path", func() {
		n := &NemoSpeech{fam: familyASR}
		_, err := run(context.Background(), n, &pb.TranscriptRequest{})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	// Cancellation is checked before the decode so a client that has already
	// gone away does not pay for an ffmpeg run, and so the check cannot be
	// mistaken for the decode failing.
	It("returns cancelled without touching the audio", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		n := &NemoSpeech{fam: familyASR}
		_, err := run(ctx, n, &pb.TranscriptRequest{
			Dst: filepath.Join(GinkgoT().TempDir(), "absent.wav"),
		})
		Expect(status.Code(err)).To(Equal(codes.Canceled))
	})

	It("reports an audio file it cannot read", func() {
		n := &NemoSpeech{fam: familyASR}
		_, err := run(context.Background(), n, &pb.TranscriptRequest{
			Dst: filepath.Join(GinkgoT().TempDir(), "absent.wav"),
		})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	// Same ordering constraint as the offline path: a clip that decodes to no
	// samples has to be refused before a session is opened, which is also
	// before any bound entry point is called. Nothing is loaded here, so a
	// guard placed after the open would panic instead of failing.
	It("refuses a decodable clip that carries no samples, before opening a session", func() {
		path := filepath.Join(GinkgoT().TempDir(), "silence.wav")
		writeMono16kWAV(path, 0)

		n := &NemoSpeech{fam: familyASR}
		var err error
		Expect(func() {
			_, err = run(context.Background(), n, &pb.TranscriptRequest{Dst: path})
		}).ToNot(Panic())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("empty audio"))
	})
})

var _ = Describe("AudioTranscriptionLive", func() {
	It("refuses a model loaded as another family and closes the output", func() {
		n := &NemoSpeech{fam: familyNMT}
		in := make(chan *pb.TranscriptLiveRequest)
		close(in)
		out := make(chan *pb.TranscriptLiveResponse)
		done := collect(out)

		err := n.AudioTranscriptionLive(in, out)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(<-done).To(BeEmpty())
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})

	It("refuses an unloaded model", func() {
		n := &NemoSpeech{}
		in := make(chan *pb.TranscriptLiveRequest)
		close(in)
		out := make(chan *pb.TranscriptLiveResponse)
		done := collect(out)

		err := n.AudioTranscriptionLive(in, out)
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(<-done).To(BeEmpty())
	})
})

package main

import (
	"sync"
	"time"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The live-RPC specs drive AudioTranscriptionLive entirely against stubbed
// Cpp* package vars (the same seam batcher_test.go uses), so they run
// without libparakeet.so.

// liveCstrPool hands out NUL-terminated C-style strings backed by Go memory
// and keeps them alive for the duration of a spec (goStringFromCPtr reads
// through the raw pointer; Go's GC must not collect the backing array while
// a stub's return value is in flight).
type liveCstrPool struct {
	mu   sync.Mutex
	bufs [][]byte
}

func (p *liveCstrPool) cstr(s string) uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := append([]byte(s), 0)
	p.bufs = append(p.bufs, b)
	return uintptr(unsafe.Pointer(&b[0]))
}

// liveStubs swaps every C entry point the live path touches and returns a
// restore func for AfterEach.
func liveStubs() (restore func()) {
	savedBegin, savedBeginLang := CppStreamBegin, CppStreamBeginLang
	savedFeed, savedFeedJSON := CppStreamFeed, CppStreamFeedJSON
	savedFinalize, savedFinalizeJSON := CppStreamFinalize, CppStreamFinalizeJSON
	savedFree, savedLastError := CppStreamFree, CppLastError
	savedFreeString := CppFreeString
	return func() {
		CppStreamBegin, CppStreamBeginLang = savedBegin, savedBeginLang
		CppStreamFeed, CppStreamFeedJSON = savedFeed, savedFeedJSON
		CppStreamFinalize, CppStreamFinalizeJSON = savedFinalize, savedFinalizeJSON
		CppStreamFree, CppLastError = savedFree, savedLastError
		CppFreeString = savedFreeString
	}
}

// runLive starts the RPC on its own goroutine and returns the request
// channel plus a collector for everything the backend emitted.
func runLive(p *ParakeetCpp) (chan *pb.TranscriptLiveRequest, chan *pb.TranscriptLiveResponse, chan error) {
	in := make(chan *pb.TranscriptLiveRequest)
	out := make(chan *pb.TranscriptLiveResponse, 32)
	errCh := make(chan error, 1)
	go func() { errCh <- p.AudioTranscriptionLive(in, out) }()
	return in, out, errCh
}

func liveConfig(lang string) *pb.TranscriptLiveRequest {
	return &pb.TranscriptLiveRequest{
		Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{Language: lang}},
	}
}

func liveAudio(pcm []float32) *pb.TranscriptLiveRequest {
	return &pb.TranscriptLiveRequest{
		Payload: &pb.TranscriptLiveRequest_Audio{Audio: &pb.TranscriptLiveAudio{Pcm: pcm}},
	}
}

func collectLive(out chan *pb.TranscriptLiveResponse) []*pb.TranscriptLiveResponse {
	var got []*pb.TranscriptLiveResponse
	for r := range out {
		got = append(got, r)
	}
	return got
}

var _ = Describe("AudioTranscriptionLive (stubbed C API)", func() {
	var (
		pool    *liveCstrPool
		restore func()
		p       *ParakeetCpp
	)

	BeforeEach(func() {
		pool = &liveCstrPool{}
		restore = liveStubs()
		p = &ParakeetCpp{ctxPtr: 1}

		CppStreamBeginLang = nil
		CppStreamBegin = func(ctx uintptr) uintptr { return 7 }
		CppStreamFree = func(s uintptr) {}
		CppFreeString = func(s uintptr) {}
		CppLastError = func(ctx uintptr) string { return "stub error" }
		CppStreamFeed = nil
		CppStreamFeedJSON = nil
		CppStreamFinalize = nil
		CppStreamFinalizeJSON = nil
	})

	AfterEach(func() { restore() })

	It("rejects a stream whose first message is not a config", func() {
		in, out, errCh := runLive(p)
		in <- liveAudio([]float32{0.1})
		close(in)

		err := <-errCh
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(collectLive(out)).To(BeEmpty())
	})

	It("rejects a non-16k sample rate", func() {
		in, _, errCh := runLive(p)
		in <- &pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{SampleRate: 8000}},
		}
		close(in)
		Expect(status.Code(<-errCh)).To(Equal(codes.InvalidArgument))
	})

	It("returns the typed Unimplemented signal for non-streaming models, before any ack", func() {
		CppStreamBegin = func(ctx uintptr) uintptr { return 0 }

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		close(in)

		err := <-errCh
		Expect(grpcerrors.IsLiveTranscriptionUnsupported(err)).To(BeTrue())
		Expect(collectLive(out)).To(BeEmpty())
	})

	It("streams deltas, eou flags and words on the JSON path and finalizes on close", func() {
		var freed []uintptr
		CppStreamFree = func(s uintptr) { freed = append(freed, s) }
		feeds := 0
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr {
			feeds++
			switch feeds {
			case 1:
				return pool.cstr(`{"text":"hello ","eou":0,"frame_sec":0.08,` +
					`"words":[{"w":"hello","start":0.1,"end":0.4,"conf":0.9}]}`)
			default:
				return pool.cstr(`{"text":"world","eou":1,"frame_sec":0.08,` +
					`"words":[{"w":"world","start":0.5,"end":0.8,"conf":0.9}]}`)
			}
		}
		CppStreamFinalizeJSON = func(s uintptr) uintptr {
			return pool.cstr(`{"text":"","eou":0,"frame_sec":0.08,"words":[]}`)
		}

		in, out, errCh := runLive(p)
		in <- liveConfig("en")
		in <- liveAudio(make([]float32, 100))
		in <- liveAudio(make([]float32, 200))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		Expect(got).To(HaveLen(4)) // ready, two deltas, final

		Expect(got[0].Ready).To(BeTrue())

		Expect(got[1].Delta).To(Equal("hello "))
		Expect(got[1].Eou).To(BeFalse())
		Expect(got[1].Words).To(HaveLen(1))
		Expect(got[1].Words[0].Text).To(Equal("hello"))

		Expect(got[2].Delta).To(Equal("world"))
		Expect(got[2].Eou).To(BeTrue())

		final := got[3].FinalResult
		Expect(final).NotTo(BeNil())
		Expect(final.Text).To(Equal("hello world"))
		// The live FinalResult carries only Text. Per-utterance segments,
		// duration and the terminal eou flag are an offline-path concern (see
		// boundary.go / AudioTranscriptionStream); the realtime core reads the
		// streamed per-feed tokens above plus this Text.
		Expect(final.Eou).To(BeFalse())
		Expect(final.Segments).To(BeEmpty())
		Expect(final.Duration).To(BeZero())

		Expect(freed).To(Equal([]uintptr{7}))
	})

	It("falls back to the text feed (eou out-param) when the JSON entry points are absent", func() {
		feeds := 0
		CppStreamFeed = func(s uintptr, pcm []float32, n int32, eouOut unsafe.Pointer) uintptr {
			feeds++
			if feeds == 2 {
				*(*int32)(eouOut) = 1
				return pool.cstr("done")
			}
			return pool.cstr("first ")
		}
		CppStreamFinalize = func(s uintptr) uintptr { return pool.cstr("") }

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))
		in <- liveAudio(make([]float32, 10))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		Expect(got).To(HaveLen(4))
		Expect(got[1].Delta).To(Equal("first "))
		Expect(got[1].Eou).To(BeFalse())
		Expect(got[2].Delta).To(Equal("done"))
		Expect(got[2].Eou).To(BeTrue())
		Expect(got[3].FinalResult.Text).To(Equal("first done"))
	})

	It("forwards <EOB> as eob — a backchannel, never an eou (ABI v5 JSON)", func() {
		feeds := 0
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr {
			feeds++
			if feeds == 1 {
				return pool.cstr(`{"text":"uh-huh","eou":0,"eob":1,"frame_sec":0.08,` +
					`"words":[{"w":"uh-huh","start":0.1,"end":0.3,"conf":0.9}]}`)
			}
			return pool.cstr(`{"text":"the turn","eou":1,"eob":0,"frame_sec":0.08,` +
				`"words":[{"w":"the","start":0.5,"end":0.6,"conf":0.9},{"w":"turn","start":0.6,"end":0.8,"conf":0.9}]}`)
		}
		CppStreamFinalizeJSON = func(s uintptr) uintptr {
			return pool.cstr(`{"text":"","eou":0,"eob":0,"frame_sec":0.08,"words":[]}`)
		}

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))
		in <- liveAudio(make([]float32, 10))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		Expect(got).To(HaveLen(4))
		Expect(got[1].Eob).To(BeTrue())
		Expect(got[1].Eou).To(BeFalse(), "a backchannel must not masquerade as a turn boundary")
		Expect(got[2].Eou).To(BeTrue())
	})

	It("maps the v5 eou_out bitmask on the text path (bit0 <EOU>, bit1 <EOB>)", func() {
		feeds := 0
		CppStreamFeed = func(s uintptr, pcm []float32, n int32, eouOut unsafe.Pointer) uintptr {
			feeds++
			if feeds == 1 {
				*(*int32)(eouOut) = 2 // <EOB> only
				return pool.cstr("uh-huh")
			}
			*(*int32)(eouOut) = 1 // <EOU>
			return pool.cstr(" done")
		}
		CppStreamFinalize = func(s uintptr) uintptr { return pool.cstr("") }

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))
		in <- liveAudio(make([]float32, 10))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		Expect(got).To(HaveLen(4))
		Expect(got[1].Eob).To(BeTrue())
		Expect(got[1].Eou).To(BeFalse())
		Expect(got[2].Eou).To(BeTrue())
		Expect(got[2].Eob).To(BeFalse())
	})

	It("accumulates trailing text after an EOU into the final transcript", func() {
		feeds := 0
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr {
			feeds++
			if feeds == 1 {
				return pool.cstr(`{"text":"turn one","eou":1,"frame_sec":0.08,"words":[]}`)
			}
			return pool.cstr(`{"text":" and more","eou":0,"frame_sec":0.08,"words":[]}`)
		}
		CppStreamFinalizeJSON = func(s uintptr) uintptr {
			return pool.cstr(`{"text":"","eou":0,"frame_sec":0.08,"words":[]}`)
		}

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))
		in <- liveAudio(make([]float32, 10))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		final := got[len(got)-1].FinalResult
		Expect(final.Text).To(Equal("turn one and more"))
	})

	It("resets the decode session on a mid-stream config", func() {
		var begun, freed int
		CppStreamBegin = func(ctx uintptr) uintptr { begun++; return uintptr(10 + begun) }
		CppStreamFree = func(s uintptr) { freed++ }
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr {
			return pool.cstr(`{"text":"x","eou":0,"frame_sec":0.08,"words":[]}`)
		}
		CppStreamFinalizeJSON = func(s uintptr) uintptr {
			return pool.cstr(`{"text":"","eou":0,"frame_sec":0.08,"words":[]}`)
		}

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))
		in <- liveConfig("") // reset
		in <- liveAudio(make([]float32, 10))
		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())

		got := collectLive(out)
		final := got[len(got)-1].FinalResult
		Expect(final.Text).To(Equal("x"), "pre-reset transcript dropped")
		Expect(begun).To(Equal(2))
		Expect(freed).To(Equal(2), "old session freed on reset, new one on unwind")
	})

	It("does not hold engineMu between feeds (unary work interleaves with a live session)", func() {
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr {
			return pool.cstr(`{"text":"","eou":0,"frame_sec":0.08,"words":[]}`)
		}
		CppStreamFinalizeJSON = func(s uintptr) uintptr {
			return pool.cstr(`{"text":"","eou":0,"frame_sec":0.08,"words":[]}`)
		}

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))

		// The session is open and idle between feeds: the engine lock must be
		// acquirable, which is what lets batched unary transcription proceed
		// mid-session. Under stream-lifetime locking this probe would block
		// until the stream ended and the Eventually would time out.
		locked := make(chan struct{})
		go func() {
			p.engineMu.Lock()
			p.engineMu.Unlock() //nolint:staticcheck // probe: acquire-release proves availability
			close(locked)
		}()
		Eventually(locked, time.Second).Should(BeClosed())

		close(in)
		Expect(<-errCh).NotTo(HaveOccurred())
		collectLive(out)
	})

	It("errors out and reads last_error under the lock when a feed fails", func() {
		CppStreamFeedJSON = func(s uintptr, pcm []float32, n int32) uintptr { return 0 }

		in, out, errCh := runLive(p)
		in <- liveConfig("")
		in <- liveAudio(make([]float32, 10))

		err := <-errCh
		Expect(err).To(MatchError(ContainSubstring("stub error")))
		got := collectLive(out)
		Expect(got).To(HaveLen(1)) // just the ready ack
		close(in)
	})
})

var _ = Describe("stripEouMarker", func() {
	It("strips a trailing <EOU> and reports it", func() {
		text, eou := stripEouMarker("it is certainly very like the old portrait<EOU>")
		Expect(text).To(Equal("it is certainly very like the old portrait"))
		Expect(eou).To(BeTrue())
	})

	It("strips a trailing <EOB> WITHOUT reporting an utterance end", func() {
		// A decode ending on a backchannel must not confirm the
		// retranscribe gate — the user was acknowledging, not yielding.
		text, eou := stripEouMarker("uh-huh<EOB>")
		Expect(text).To(Equal("uh-huh"))
		Expect(eou).To(BeFalse())
	})

	It("leaves marker-free text alone", func() {
		text, eou := stripEouMarker("plain transcript")
		Expect(text).To(Equal("plain transcript"))
		Expect(eou).To(BeFalse())
	})

	It("does not strip a marker in the middle of the text", func() {
		text, eou := stripEouMarker("a<EOU>b")
		Expect(text).To(Equal("a<EOU>b"))
		Expect(eou).To(BeFalse())
	})
})

var _ = Describe("transcriptResultFromDoc EOU handling", func() {
	It("strips the offline marker from text and sets the result flag", func() {
		doc := transcriptJSON{Text: "the old portrait<EOU>"}
		res := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
		Expect(res.Text).To(Equal("the old portrait"))
		Expect(res.Eou).To(BeTrue())
		Expect(res.Segments).To(HaveLen(1))
		Expect(res.Segments[0].Text).To(Equal("the old portrait"))
	})

	It("reports eou=false for marker-free decodes", func() {
		doc := transcriptJSON{Text: "no marker here"}
		res := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
		Expect(res.Text).To(Equal("no marker here"))
		Expect(res.Eou).To(BeFalse())
	})
})

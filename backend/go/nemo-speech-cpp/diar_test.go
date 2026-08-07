package main

import (
	"errors"
	"path/filepath"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fakeDiarStream scripts what the C API returns for one diarization job.
//
// There is no Sortformer GGUF in the tree, so this is the only way the loop on
// top of the ABI (the empty guard, chunking, the count-then-fill protocol, the
// buffer growth retry) gets tested at all. It fakes the C contract, not the
// model: segs is whatever nemo_speech_diar_segments would have produced.
type fakeDiarStream struct {
	segs []cDiarSegment

	// countErr and fillErrs script failures. fillErrs is consumed one entry per
	// fillSegments call so a growth retry can be scripted.
	countErr error
	fillErrs []error
	// queryCount, when non-zero, is what the size query reports instead of
	// len(segs), so a runtime that under-reported can be scripted.
	queryCount uint64
	// growTo, when non-zero, is the count reported by the FIRST fillSegments
	// call, standing in for a runtime whose segment list outgrew the size query.
	growTo uint64

	pushed   [][]float32
	rates    []int32
	finished int
	closed   int
	counts   int
	fills    int
	// opened records the segmentation config the opener was handed.
	cfg *cDiarSegmentationConfig
}

func (f *fakeDiarStream) push(pcm []float32, rate int32) error {
	f.pushed = append(f.pushed, pcm)
	f.rates = append(f.rates, rate)
	return nil
}

func (f *fakeDiarStream) finish() error {
	f.finished++
	return nil
}

func (f *fakeDiarStream) close() { f.closed++ }

func (f *fakeDiarStream) countSegments() (uint64, error) {
	f.counts++
	if f.countErr != nil {
		return 0, f.countErr
	}
	if f.queryCount > 0 {
		return f.queryCount, nil
	}
	return uint64(len(f.segs)), nil
}

func (f *fakeDiarStream) fillSegments(buf []cDiarSegment) (uint64, error) {
	f.fills++
	var err error
	if len(f.fillErrs) > 0 {
		err, f.fillErrs = f.fillErrs[0], f.fillErrs[1:]
	}
	if f.fills == 1 && f.growTo > 0 {
		// The runtime writes *count before it rejects a short buffer, so a
		// growth failure still reports the count the caller needs.
		return f.growTo, err
	}
	n := copy(buf, f.segs)
	return uint64(n), err
}

// allPushed flattens what the fake received, so a spec can assert the audio
// arrived intact regardless of how it was chunked.
func (f *fakeDiarStream) allPushed() []float32 {
	var out []float32
	for _, c := range f.pushed {
		out = append(out, c...)
	}
	return out
}

func (f *fakeDiarStream) opener() diarStreamOpener {
	return func(cfg *cDiarSegmentationConfig) (diarStream, error) {
		f.cfg = cfg
		return f, nil
	}
}

var _ = Describe("Diarize", func() {
	It("refuses when the loaded model is not a diarization model", func() {
		n := &NemoSpeech{fam: familyASR}
		_, err := n.Diarize(&pb.DiarizeRequest{Dst: "/tmp/whatever.wav"})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("refuses on a model that was never loaded", func() {
		n := &NemoSpeech{}
		_, err := n.Diarize(&pb.DiarizeRequest{Dst: "/tmp/whatever.wav"})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	// The family gate has to run before anything reads the request, or a
	// misrouted request would be reported as a bad path rather than as a model
	// that cannot diarize.
	It("reports a missing audio path on a diarization model", func() {
		n := &NemoSpeech{fam: familyDiarization}
		_, err := n.Diarize(&pb.DiarizeRequest{})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("dst"))
	})

	It("reports audio it cannot read", func() {
		n := &NemoSpeech{fam: familyDiarization}
		missing := filepath.Join(GinkgoT().TempDir(), "absent.wav")
		_, err := n.Diarize(&pb.DiarizeRequest{Dst: missing})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("read audio"))
	})

	// A rejection must leave the mutex free, or the next request deadlocks
	// rather than fails.
	It("releases the engine lock on every rejection path", func() {
		n := &NemoSpeech{fam: familyDiarization}
		_, err := n.Diarize(&pb.DiarizeRequest{})
		Expect(err).To(HaveOccurred())
		Expect(n.engineMu.TryLock()).To(BeTrue())
		n.engineMu.Unlock()
	})
})

var _ = Describe("diarizePCM", func() {
	// Task 7 found that a purego-bound entry point reached with zero samples
	// panics on &pcm[0], so a silent clip must be rejected before the stream is
	// ever opened, not inside the push.
	It("rejects empty audio without opening a stream", func() {
		opened := false
		open := func(*cDiarSegmentationConfig) (diarStream, error) {
			opened = true
			return &fakeDiarStream{}, nil
		}
		_, err := diarizePCM(open, nil, 16000, nil)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("empty audio"))
		Expect(opened).To(BeFalse())
	})

	It("pushes the whole clip, finishes, and closes the stream", func() {
		pcm := make([]float32, streamChunkSamples*2+7)
		for i := range pcm {
			pcm[i] = float32(i)
		}
		f := &fakeDiarStream{}
		_, err := diarizePCM(f.opener(), pcm, 16000, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(f.allPushed()).To(Equal(pcm))
		Expect(f.pushed).To(HaveLen(3), "the clip must be chunked, not pushed whole")
		Expect(f.rates).To(HaveEach(int32(16000)))
		Expect(f.finished).To(Equal(1))
		Expect(f.closed).To(Equal(1))
	})

	// Segments must come from a finished stream: the tail of the audio is only
	// labelled by finish, so asking first silently drops the last turn.
	It("finishes the stream before it asks for segments", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{{StartTime: 0, EndTime: 1, Speaker: 1}}}
		f.fillErrs = nil
		var finishedAtCount int
		wrapped := func(cfg *cDiarSegmentationConfig) (diarStream, error) {
			f.cfg = cfg
			return &countObserver{fakeDiarStream: f, seen: &finishedAtCount}, nil
		}
		_, err := diarizePCM(wrapped, []float32{1, 2, 3}, 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(finishedAtCount).To(Equal(1), "the size query ran before finish")
	})

	It("converts the runtime's seconds straight through and numbers the segments", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{
			{StartTime: 0, EndTime: 0.8, Speaker: 1},
			{StartTime: 0.8, EndTime: 2.0, Speaker: 2},
		}}
		res, err := diarizePCM(f.opener(), []float32{1, 2, 3}, 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).To(HaveLen(2))

		Expect(res.Segments[0].GetId()).To(Equal(int32(0)))
		Expect(res.Segments[0].GetStart()).To(BeNumerically("~", 0.0, 1e-6))
		Expect(res.Segments[0].GetEnd()).To(BeNumerically("~", 0.8, 1e-6))
		Expect(res.Segments[0].GetSpeaker()).To(Equal("1"))

		Expect(res.Segments[1].GetId()).To(Equal(int32(1)))
		Expect(res.Segments[1].GetStart()).To(BeNumerically("~", 0.8, 1e-6))
		Expect(res.Segments[1].GetEnd()).To(BeNumerically("~", 2.0, 1e-6))
		Expect(res.Segments[1].GetSpeaker()).To(Equal("2"))
	})

	It("reports the clip duration in seconds", func() {
		f := &fakeDiarStream{}
		res, err := diarizePCM(f.opener(), make([]float32, 32000), 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.GetDuration()).To(BeNumerically("~", 2.0, 1e-6))
	})

	// 0 is the proto's documented "unknown", and it is also the C API's "these
	// samples are already at the model rate", so a rate that cannot be trusted
	// must not be turned into a duration.
	It("reports no duration when the sample rate is unknown", func() {
		f := &fakeDiarStream{}
		res, err := diarizePCM(f.opener(), make([]float32, 32000), 0, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.GetDuration()).To(BeZero())
	})

	It("hands the segmentation config to the opener", func() {
		f := &fakeDiarStream{}
		cfg := &cDiarSegmentationConfig{MinDurationSec: 0.5}
		_, err := diarizePCM(f.opener(), []float32{1}, 16000, cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.cfg).To(BeIdenticalTo(cfg))
	})

	It("closes the stream when the segment query fails", func() {
		f := &fakeDiarStream{countErr: errors.New("boom")}
		_, err := diarizePCM(f.opener(), []float32{1}, 16000, nil)
		Expect(err).To(MatchError(ContainSubstring("boom")))
		Expect(f.closed).To(Equal(1))
	})

	// The pipeline carries no ASR, so text and language stay empty whatever the
	// caller asked for.
	It("leaves the transcript fields empty", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{{StartTime: 0, EndTime: 1, Speaker: 1}}}
		res, err := diarizePCM(f.opener(), []float32{1}, 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.GetLanguage()).To(BeEmpty())
		Expect(res.Segments[0].GetText()).To(BeEmpty())
	})
})

// countObserver records how many size queries had run by the time finish was
// called, so the ordering can be asserted without reaching into diarizePCM.
type countObserver struct {
	*fakeDiarStream
	seen *int
}

func (c *countObserver) finish() error {
	*c.seen = c.counts + 1 // finish must run before the first query
	return c.fakeDiarStream.finish()
}

var _ = Describe("distinctSpeakers", func() {
	It("counts labels, not segments", func() {
		segs := []*pb.DiarizeSegment{
			{Speaker: "1"}, {Speaker: "2"}, {Speaker: "1"}, {Speaker: "2"}, {Speaker: "1"},
		}
		Expect(distinctSpeakers(segs)).To(Equal(int32(2)))
	})

	It("counts a single-speaker recording as one", func() {
		segs := []*pb.DiarizeSegment{{Speaker: "1"}, {Speaker: "1"}, {Speaker: "1"}}
		Expect(distinctSpeakers(segs)).To(Equal(int32(1)))
	})

	// Four segments over three labels, not three over three: with the segment
	// count and the label count equal, a `return len(segs)` would satisfy this
	// spec and it would assert nothing.
	It("counts every distinct label once", func() {
		segs := []*pb.DiarizeSegment{{Speaker: "1"}, {Speaker: "2"}, {Speaker: "3"}, {Speaker: "2"}}
		Expect(distinctSpeakers(segs)).To(Equal(int32(3)))
	})

	It("is zero with no segments", func() {
		Expect(distinctSpeakers(nil)).To(BeZero())
	})

	// The response field is documented as the count of distinct labels in
	// `segments`, which is not the model's capacity: Sortformer v2 can label
	// four speakers whatever the clip actually contains.
	It("reports what the segments contain, not the model capacity", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{
			{StartTime: 0, EndTime: 1, Speaker: 1},
			{StartTime: 1, EndTime: 2, Speaker: 2},
			{StartTime: 2, EndTime: 3, Speaker: 1},
		}}
		res, err := diarizePCM(f.opener(), []float32{1}, 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.GetNumSpeakers()).To(Equal(int32(2)))
	})

	It("reports no speakers when the runtime found no segments", func() {
		f := &fakeDiarStream{}
		res, err := diarizePCM(f.opener(), []float32{1}, 16000, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Segments).To(BeEmpty())
		Expect(res.GetNumSpeakers()).To(BeZero())
	})
})

var _ = Describe("collectSegments", func() {
	It("skips the fill entirely when there is nothing to collect", func() {
		f := &fakeDiarStream{}
		segs, err := collectSegments(f)
		Expect(err).ToNot(HaveOccurred())
		Expect(segs).To(BeEmpty())
		Expect(f.counts).To(Equal(1))
		Expect(f.fills).To(BeZero(), "a zero count must not be followed by a fill")
	})

	It("sizes the buffer from the query and fills it", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{
			{StartTime: 0, EndTime: 1, Speaker: 1},
			{StartTime: 1, EndTime: 2, Speaker: 2},
		}}
		segs, err := collectSegments(f)
		Expect(err).ToNot(HaveOccurred())
		Expect(segs).To(HaveLen(2))
		Expect(segs[1].Speaker).To(Equal(int32(2)))
		Expect(f.counts).To(Equal(1))
		Expect(f.fills).To(Equal(1))
	})

	// nemo_speech_diar_segments writes *count and only then rejects a buffer
	// that is too small, so the rejected call still reports the size to retry
	// with. Truncating instead would silently drop turns.
	It("grows the buffer and retries when the count outran the query", func() {
		f := &fakeDiarStream{
			segs: []cDiarSegment{
				{StartTime: 0, EndTime: 1, Speaker: 1},
				{StartTime: 1, EndTime: 2, Speaker: 2},
				{StartTime: 2, EndTime: 3, Speaker: 1},
			},
			// The query saw two, the fill found three and rejected the buffer.
			queryCount: 2,
			growTo:     3,
			fillErrs:   []error{errors.New("capacity too small (need 3)")},
		}
		segs, err := collectSegments(f)
		Expect(err).ToNot(HaveOccurred())
		Expect(segs).To(HaveLen(3))
		Expect(f.fills).To(Equal(2))
	})

	It("propagates a failure that is not about capacity", func() {
		f := &fakeDiarStream{
			segs:     []cDiarSegment{{StartTime: 0, EndTime: 1, Speaker: 1}},
			fillErrs: []error{errors.New("boom")},
		}
		_, err := collectSegments(f)
		Expect(err).To(MatchError(ContainSubstring("boom")))
		Expect(f.fills).To(Equal(1), "a non-capacity failure must not be retried")
	})

	// make() panics on a length it cannot satisfy, and a panic in an RPC
	// handler kills the backend process. A count that could only come from an
	// uninitialised or corrupted size_t must fail the request instead.
	It("refuses an implausible count rather than trying to allocate it", func() {
		f := &fakeDiarStream{queryCount: maxDiarSegments + 1}
		_, err := collectSegments(f)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(err.Error()).To(ContainSubstring("ceiling"))
		Expect(f.fills).To(BeZero(), "nothing must be allocated or filled for a bad count")
	})

	It("still accepts a count right at the ceiling", func() {
		// Only the guard is under test, so the fill is scripted to report zero
		// rather than actually materialising a hundred megabytes of segments.
		f := &fakeDiarStream{queryCount: maxDiarSegments}
		segs, err := collectSegments(f)
		Expect(err).ToNot(HaveOccurred())
		Expect(segs).To(BeEmpty())
		Expect(f.fills).To(Equal(1))
	})

	It("propagates a failed size query", func() {
		f := &fakeDiarStream{countErr: errors.New("no stream")}
		_, err := collectSegments(f)
		Expect(err).To(MatchError(ContainSubstring("no stream")))
		Expect(f.fills).To(BeZero())
	})

	// A runtime whose count grew on every attempt would otherwise loop forever
	// holding engineMu, which blocks the unload too.
	It("gives up rather than retrying forever", func() {
		f := &fakeDiarStream{segs: []cDiarSegment{{StartTime: 0, EndTime: 1, Speaker: 1}}}
		g := &alwaysGrowing{fakeDiarStream: f}
		_, err := collectSegments(g)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(f.fills).To(Equal(diarSegmentsMaxAttempts))
	})
})

// alwaysGrowing reports a bigger count on every fill, which is the pathological
// case the attempt bound exists for.
type alwaysGrowing struct {
	*fakeDiarStream
	n uint64
}

func (a *alwaysGrowing) fillSegments([]cDiarSegment) (uint64, error) {
	a.n += 10
	a.fills++
	return a.n, errors.New("capacity too small")
}

// The six frame counts are the one part of the create config that no other
// check in the tree can see. The layout assertions pin the struct's shape, and
// a wrong VALUE keeps that shape exactly, so without these specs deleting a
// sentinel is invisible: c_api.cpp applies left_context_frames at >= 0, so a
// dropped -1 there silently pins the model's left context to zero.
var _ = Describe("diarModelConfig", func() {
	It("declares its own size so the runtime accepts the fields", func() {
		Expect(diarModelConfig(0, -1).Size).To(Equal(unsafe.Sizeof(cDiarModelConfig{})))
	})

	It("carries the model path and the configured device", func() {
		cfg := diarModelConfig(0xDEADBEEF, 2)
		Expect(cfg.ModelPath).To(Equal(uintptr(0xDEADBEEF)))
		Expect(cfg.GPU).To(Equal(int32(2)))
	})

	It("passes the CPU sentinel through untouched", func() {
		Expect(diarModelConfig(0, -1).GPU).To(Equal(int32(-1)))
	})

	// Asserted field by field rather than as a whole struct so a failure names
	// the sentinel that went missing.
	It("leaves every frame-geometry override at the negative sentinel", func() {
		cfg := diarModelConfig(0, -1)
		Expect(cfg.ChunkFrames).To(Equal(int32(-1)), "chunk_frames")
		Expect(cfg.RightContextFrames).To(Equal(int32(-1)), "right_context_frames")
		Expect(cfg.FIFOFrames).To(Equal(int32(-1)), "fifo_frames")
		Expect(cfg.SpkcacheFrames).To(Equal(int32(-1)), "spkcache_frames")
		Expect(cfg.UpdatePeriodFrames).To(Equal(int32(-1)), "update_period_frames")

		// Called out on its own because it is the only one of the six the
		// runtime applies at >= 0: zero here is a valid explicit left context,
		// not "unset", so this is the field a dropped sentinel actually breaks.
		Expect(cfg.LeftContextFrames).To(Equal(int32(-1)), "left_context_frames")
		Expect(cfg.LeftContextFrames).To(BeNumerically("<", 0),
			"left_context_frames is applied at >= 0, so a non-negative value pins the geometry")
	})

	// The preset selects the streaming geometry wholesale, so it has to stay
	// NULL until there is a model to verify a different one against.
	It("leaves the preset unset", func() {
		Expect(diarModelConfig(0, -1).Preset).To(BeZero())
	})
})

var _ = Describe("segmentationConfig", func() {
	// A request that set nothing must stay NULL on the C side: diar.h documents
	// NULL as "library defaults", and those defaults are NeMo's callhome-tuned
	// values for this checkpoint rather than zeros.
	It("is absent when the request asked for no postprocessing", func() {
		Expect(segmentationConfig(&pb.DiarizeRequest{})).To(BeNil())
	})

	It("maps min_duration_on onto the minimum segment duration", func() {
		cfg := segmentationConfig(&pb.DiarizeRequest{MinDurationOn: 0.4})
		Expect(cfg).ToNot(BeNil())
		Expect(cfg.MinDurationSec).To(BeNumerically("~", 0.4, 1e-6))
		Expect(cfg.MinGapSec).To(BeZero())
	})

	It("maps min_duration_off onto the gap fill", func() {
		cfg := segmentationConfig(&pb.DiarizeRequest{MinDurationOff: 0.25})
		Expect(cfg).ToNot(BeNil())
		Expect(cfg.MinGapSec).To(BeNumerically("~", 0.25, 1e-6))
		Expect(cfg.MinDurationSec).To(BeZero())
	})

	It("declares its own size so the runtime accepts the fields", func() {
		cfg := segmentationConfig(&pb.DiarizeRequest{MinDurationOn: 0.4})
		Expect(cfg.Size).To(Equal(sizeofDiarSegmentationConfig()))
	})

	// The onset/offset hysteresis is not a clustering threshold and Sortformer
	// has no clustering stage at all, so mapping one onto the other would be an
	// invented equivalence. It has to stay unset.
	It("ignores fields this pipeline has no equivalent for", func() {
		Expect(segmentationConfig(&pb.DiarizeRequest{
			NumSpeakers:         2,
			MinSpeakers:         1,
			MaxSpeakers:         4,
			ClusteringThreshold: 0.7,
			IncludeText:         true,
			Threads:             8,
		})).To(BeNil())
	})

	It("ignores non-positive values, which the runtime reads as unset", func() {
		Expect(segmentationConfig(&pb.DiarizeRequest{MinDurationOn: -1, MinDurationOff: 0})).To(BeNil())
	})
})

var _ = Describe("unsupportedRequestFields", func() {
	It("is empty for a request this backend can honour in full", func() {
		Expect(unsupportedRequestFields(&pb.DiarizeRequest{
			Dst:            "/tmp/a.wav",
			MinDurationOn:  0.4,
			MinDurationOff: 0.2,
		})).To(BeEmpty())
	})

	It("names every field it had to drop", func() {
		Expect(unsupportedRequestFields(&pb.DiarizeRequest{
			NumSpeakers:         2,
			MinSpeakers:         1,
			MaxSpeakers:         4,
			ClusteringThreshold: 0.7,
			IncludeText:         true,
			Threads:             8,
		})).To(ConsistOf(
			"num_speakers", "min_speakers", "max_speakers",
			"clustering_threshold", "include_text", "threads",
		))
	})
})

package main

import (
	"strconv"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// diarSegmentsMaxAttempts bounds the count-then-fill retry.
//
// On a finished stream the count is stable and one attempt is always enough.
// The bound exists because the RPC holds engineMu for its whole body, so a
// runtime whose count kept growing would not merely spin, it would block the
// unload behind it.
const diarSegmentsMaxAttempts = 4

// maxDiarSegments caps the buffer collectSegments will allocate from a count
// the C side reported.
//
// make() panics rather than erroring on a length it cannot satisfy, and a
// panic in an RPC handler takes the backend process down, so an uninitialised
// or corrupted size_t coming back across the ABI would kill the model rather
// than fail the request. The ceiling turns that into a diagnosable error.
//
// It is set far above anything real: a segment spans at least one 80 ms frame,
// so 2^22 segments is upwards of 93 hours of audio, and the buffer itself
// would already be 100 MB at 24 bytes each.
const maxDiarSegments = 1 << 22

// diarSegmenter is the result half of the diarization C API: the two-call
// protocol nemo_speech_diar_segments documents.
//
// The two calls are the same C function with a different `out`, but they are
// separate methods here because their contracts differ. countSegments passes
// out=NULL, which the runtime answers by writing *count and returning OK
// without touching a buffer. fillSegments passes a real buffer and gets
// INVALID_ARGUMENT if it is too short, having written *count first, which is
// what makes a growth retry possible at all.
type diarSegmenter interface {
	// countSegments is the size query. It never fails for lack of a buffer.
	countSegments() (uint64, error)
	// fillSegments fills buf and returns the count the runtime reported. That
	// count is meaningful even alongside an error: on a short buffer the
	// runtime writes it before rejecting the call.
	fillSegments(buf []cDiarSegment) (uint64, error)
}

// diarStream is one diarization job over the C API, narrowed to what the RPC
// uses.
//
// It is an interface for the same reason asrSession is: no Sortformer GGUF is
// small enough to keep in the tree, so without a seam at the ABI the loop on
// top of it (the empty guard, chunking, finish-before-query, the growth retry)
// would have no test at all. A fake here scripts what C returns; it does not
// pretend to diarize anything.
type diarStream interface {
	diarSegmenter
	push(pcm []float32, sampleRate int32) error
	finish() error
	close()
}

// diarStreamOpener creates a job. n.openDiarStream is the C-backed one.
//
// The segmentation config is handed over at open time rather than per query
// because it belongs to the whole job: every segments call on one stream must
// use the same postprocessing or the segment ids would not be comparable
// between calls.
type diarStreamOpener func(cfg *cDiarSegmentationConfig) (diarStream, error)

// cDiarStream is the real diarStream, over one nemo_speech_diar_stream.
type cDiarStream struct {
	handle uintptr
	cfg    *cDiarSegmentationConfig
}

// cfgPtr hands the segmentation config to C, or NULL when the request asked
// for no postprocessing. NULL is not the same as a zeroed struct in spirit
// even though src/asr/c_api.cpp treats them alike today: diar.h documents NULL
// as "library defaults", so it is the one form that cannot be invalidated by a
// future field whose sentinel is not zero.
func (s *cDiarStream) cfgPtr() unsafe.Pointer {
	if s.cfg == nil {
		return nil
	}
	// #nosec G103 -- a plain *T to unsafe.Pointer conversion of a non-nil,
	// GC-traced field. cDiarSegmentationConfig is pure scalars (no uintptr
	// members to pin) and the stream owns it for its whole life, so the only
	// requirement is that it outlive the DiarSegments call, which it does.
	return unsafe.Pointer(s.cfg)
}

func (s *cDiarStream) push(pcm []float32, sampleRate int32) error {
	// &pcm[0] panics on an empty slice before the C side ever sees the call.
	if len(pcm) == 0 {
		return nil
	}
	if st := DiarStreamPushF32(s.handle, &pcm[0], uint64(len(pcm)), sampleRate); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: diarization push: %s", ASRLastError())
	}
	return nil
}

func (s *cDiarStream) finish() error {
	if st := DiarStreamFinish(s.handle); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: diarization finish: %s", ASRLastError())
	}
	return nil
}

func (s *cDiarStream) close() { DiarStreamClose(s.handle) }

func (s *cDiarStream) countSegments() (uint64, error) {
	var count uint64
	// out=NULL and capacity=0: the size query. The runtime reads capacity only
	// once it has a buffer to check it against.
	if st := DiarSegments(s.handle, s.cfgPtr(), nil, 0, &count); st != 0 {
		return 0, statusErrorf(st,
			"nemo-speech-cpp: diarization segment count: %s", ASRLastError())
	}
	return count, nil
}

func (s *cDiarStream) fillSegments(buf []cDiarSegment) (uint64, error) {
	if len(buf) == 0 {
		// A NULL out would silently turn this into a second size query, and the
		// caller would read it as "filled nothing" rather than "asked nothing".
		return 0, status.Error(codes.Internal,
			"nemo-speech-cpp: diarization segment fill needs a buffer")
	}
	var count uint64
	// #nosec G103 -- &buf[0] is guarded by the empty check above, and the
	// capacity handed over is exactly len(buf), so the runtime cannot write past
	// the caller's allocation. collectSegments sizes buf under maxDiarSegments
	// and rejects a reported count larger than it rather than slicing to it.
	st := DiarSegments(s.handle, s.cfgPtr(), unsafe.Pointer(&buf[0]), uint64(len(buf)), &count)
	if st != 0 {
		// count is returned alongside the error on purpose: a too-small buffer
		// is rejected only after the runtime has written the size it wanted.
		return count, statusErrorf(st,
			"nemo-speech-cpp: diarization segments: %s", ASRLastError())
	}
	return count, nil
}

// diarGeometryDefault is the sentinel that means "keep the preset's value" for
// every one of nemo_speech_diar_model_config's six frame counts.
//
// It has to be negative, not zero, and that is not a style choice.
// src/asr/c_api.cpp:497-512 applies five of the six overrides when they are
// > 0 but applies left_context_frames when it is >= 0, so a zero-valued config
// reads as "unset" for five fields and as an explicit left context of zero for
// the sixth. That silently changes the model's streaming geometry, and no
// layout assertion can see it because the struct is the right shape either way.
const diarGeometryDefault int32 = -1

// diarModelConfig builds the create-time config for the standalone diarizer.
//
// Extracted from loadDiarizer purely so the sentinels above can be asserted:
// they are invisible to every other check in the tree, including the layout
// assertions, so a spec pinning them is the only thing standing between a
// dropped -1 and a quietly mis-configured model.
//
// modelPath is a C pointer from cstr, not a Go string, and the caller owns its
// release. preset is deliberately left NULL, which diar.h reads as "streaming".
// The "offline" preset is a different accuracy/latency tradeoff for long files
// and is worth exposing, but not on an unverified guess: no Sortformer GGUF
// exists here to measure the difference on.
func diarModelConfig(modelPath uintptr, gpu int32) cDiarModelConfig {
	return cDiarModelConfig{
		Size:               unsafe.Sizeof(cDiarModelConfig{}),
		ModelPath:          modelPath,
		GPU:                gpu,
		ChunkFrames:        diarGeometryDefault,
		RightContextFrames: diarGeometryDefault,
		LeftContextFrames:  diarGeometryDefault,
		FIFOFrames:         diarGeometryDefault,
		SpkcacheFrames:     diarGeometryDefault,
		UpdatePeriodFrames: diarGeometryDefault,
	}
}

// loadDiarizer creates the standalone Sortformer diarizer.
//
// This must not take engineMu: Load is its only caller and already holds it.
func (n *NemoSpeech) loadDiarizer(modelFile string) error {
	pathP, freePath := cstr(modelFile)
	defer freePath()

	cfg := diarModelConfig(pathP, n.opts.gpu)

	xlog.Info("nemo-speech-cpp: creating diarizer", "gpu", n.opts.gpu)

	// #nosec G103 -- cfg is a local POD struct borrowed for this call only. Its
	// only uintptr member is ModelPath, the cstr allocation pinned by the
	// deferred freePath above (Preset is deliberately NULL), and
	// nemo_speech_diar_create deep-copies the path and retains nothing.
	if st := DiarCreate(unsafe.Pointer(&cfg), &n.diarizer); st != 0 {
		return statusErrorf(st, "nemo-speech-cpp: diarizer create: %s", ASRLastError())
	}
	return nil
}

// openDiarStream starts a diarization job on the loaded model.
//
// The caller must hold engineMu.
func (n *NemoSpeech) openDiarStream(cfg *cDiarSegmentationConfig) (diarStream, error) {
	var handle uintptr
	if st := DiarStreamOpen(n.diarizer, &handle); st != 0 {
		return nil, statusErrorf(st,
			"nemo-speech-cpp: diarization stream open: %s", ASRLastError())
	}
	return &cDiarStream{handle: handle, cfg: cfg}, nil
}

// sizeofDiarSegmentationConfig is the size the runtime validates the config
// against. It is a function so the specs can assert the value the config
// actually carries rather than restate the number.
func sizeofDiarSegmentationConfig() uintptr {
	return unsafe.Sizeof(cDiarSegmentationConfig{})
}

// segmentationConfig maps the request's postprocessing knobs onto
// nemo_speech_diar_segmentation_config, or returns nil when none were set.
//
// Only two of DiarizeRequest's tuning fields have a real equivalent here, and
// both are exact rather than approximate: NeMo's ts_vad postprocessing is the
// same algorithm the proto's wording describes.
//
//   - min_duration_on  ("discard segments shorter than this") is min_duration_sec
//     ("drop segments shorter than this"), which c_api.cpp assigns to
//     DiarSegmentationCfg.min_duration_on.
//   - min_duration_off ("merge gaps shorter than this") is min_gap_sec ("fill
//     silence gaps shorter than this"), assigned to min_duration_off.
//
// The names cross over between the proto and the C header, which is exactly the
// kind of transposition a layout assertion cannot see, so each mapping is
// pinned by its own spec.
//
// Nothing is written for a non-positive value: the runtime tests every field
// with > 0 and keeps its default otherwise, so a zero here means "unset" on
// both sides.
func segmentationConfig(req *pb.DiarizeRequest) *cDiarSegmentationConfig {
	cfg := cDiarSegmentationConfig{Size: sizeofDiarSegmentationConfig()}

	var set bool
	if v := req.GetMinDurationOn(); v > 0 {
		cfg.MinDurationSec = float64(v)
		set = true
	}
	if v := req.GetMinDurationOff(); v > 0 {
		cfg.MinGapSec = float64(v)
		set = true
	}
	if !set {
		return nil
	}
	return &cfg
}

// unsupportedRequestFields names the DiarizeRequest fields this backend cannot
// honour, so they are logged rather than silently dropped.
//
// Each is a deliberate omission, not a gap waiting to be filled:
//
//   - num_speakers, min_speakers, max_speakers: Sortformer is end-to-end and
//     its speaker capacity is fixed by the checkpoint (v2: 4).
//     nemo_speech_diar_num_speakers reports that capacity, it does not set it,
//     and there is no config field for a target count.
//   - clustering_threshold: there is no clustering stage. The nearest knob is
//     the onset/offset probability hysteresis, which is a different quantity on
//     a different scale, so mapping one onto the other would invent an
//     equivalence the header does not have.
//   - include_text: this pipeline carries no ASR at all (diar.h: "no ASR
//     involved"). Word-level speaker tags on a transcript are the ASR surface's
//     job, through diar_model plus enable_speaker_diarization.
//   - threads: neither nemo_speech_diar_model_config nor the segmentation
//     config has a thread count.
func unsupportedRequestFields(req *pb.DiarizeRequest) []string {
	var out []string
	if req.GetNumSpeakers() != 0 {
		out = append(out, "num_speakers")
	}
	if req.GetMinSpeakers() != 0 {
		out = append(out, "min_speakers")
	}
	if req.GetMaxSpeakers() != 0 {
		out = append(out, "max_speakers")
	}
	if req.GetClusteringThreshold() != 0 {
		out = append(out, "clustering_threshold")
	}
	if req.GetIncludeText() {
		out = append(out, "include_text")
	}
	if req.GetThreads() != 0 {
		out = append(out, "threads")
	}
	return out
}

// collectSegments runs the count-then-fill protocol and returns the segments.
//
// The growth retry is not defensive padding. nemo_speech_diar_segments writes
// *count and only then rejects a buffer that is too small, so the size a
// rejected call reports is the size to retry with; without the retry a stream
// that gained a segment between the two calls would fail the whole request.
// Truncating to the first count instead would be worse still, dropping turns
// with nothing to show for it.
func collectSegments(s diarSegmenter) ([]cDiarSegment, error) {
	want, err := s.countSegments()
	if err != nil {
		return nil, err
	}

	for range diarSegmentsMaxAttempts {
		if want == 0 {
			// No segments means no fill: the fill call needs a non-empty buffer
			// to be distinguishable from a second size query.
			return nil, nil
		}
		if want > maxDiarSegments {
			return nil, status.Errorf(codes.Internal,
				"nemo-speech-cpp: diarization reported %d segments, above the %d ceiling", want, maxDiarSegments)
		}

		buf := make([]cDiarSegment, want)
		got, fillErr := s.fillSegments(buf)
		if fillErr == nil {
			if got > want {
				// The runtime cannot report this on success (it rejects a short
				// buffer instead), so it means the ABI is not what this code
				// thinks it is. Slicing to it would read past the allocation.
				return nil, status.Errorf(codes.Internal,
					"nemo-speech-cpp: diarization returned %d segments for a %d-segment buffer", got, want)
			}
			return buf[:got], nil
		}
		// A count that did not grow means the call failed for some other
		// reason, and retrying the same size would just fail the same way.
		if got <= want {
			return nil, fillErr
		}
		want = got
	}

	return nil, status.Error(codes.Internal,
		"nemo-speech-cpp: diarization segment count kept growing, giving up")
}

// toDiarizeSegments converts the runtime's segments to the wire form.
//
// No unit conversion happens here, and that is the point: nemo_speech_diar_segment
// carries start_time and end_time in SECONDS already (diar.h), and
// DiarizeSegment.start/end are seconds too. The frame indices the model works
// in never reach this layer, so nemo_speech_diar_seconds_per_frame is not
// involved. The narrowing to float32 is the proto's choice of type; at 80 ms
// resolution it is lossless for any clip short enough to hold in memory.
//
// The speaker label is the runtime's 1-based tag rendered as a decimal string,
// which is what wordsToSegments emits for the ASR path. The same speaker has to
// read the same way whether the caller diarized a file or transcribed it.
func toDiarizeSegments(in []cDiarSegment) []*pb.DiarizeSegment {
	if len(in) == 0 {
		return nil
	}
	out := make([]*pb.DiarizeSegment, 0, len(in))
	for i, s := range in {
		out = append(out, &pb.DiarizeSegment{
			Id:      int32(i),
			Start:   float32(s.StartTime),
			End:     float32(s.EndTime),
			Speaker: strconv.Itoa(int(s.Speaker)),
		})
	}
	return out
}

// distinctSpeakers counts the speaker labels present in the segments.
//
// This is what DiarizeResponse.num_speakers is documented to hold, and it is
// NOT nemo_speech_diar_num_speakers: that reports the checkpoint's capacity
// (four for Sortformer v2), so a two-person interview would come back claiming
// four speakers.
func distinctSpeakers(segs []*pb.DiarizeSegment) int32 {
	seen := make(map[string]struct{}, len(segs))
	for _, s := range segs {
		seen[s.GetSpeaker()] = struct{}{}
	}
	// #nosec G115 -- seen holds at most one entry per segment, and collectSegments
	// refuses any count above maxDiarSegments (2^22), so this is orders of
	// magnitude below the int32 the proto field is.
	return int32(len(seen))
}

// diarizePCM drives one whole clip through a diarization job.
//
// The caller must hold engineMu.
func diarizePCM(open diarStreamOpener, pcm []float32, sampleRate int32, cfg *cDiarSegmentationConfig) (*pb.DiarizeResponse, error) {
	// Before the stream is opened, not inside the push: a silent or truncated
	// upload decodes to zero samples, &pcm[0] panics on that, and there is no
	// diarization to be had from it anyway.
	if len(pcm) == 0 {
		return nil, status.Error(codes.InvalidArgument, "nemo-speech-cpp: empty audio")
	}

	stream, err := open(cfg)
	if err != nil {
		return nil, err
	}
	defer stream.close()

	// Chunked rather than pushed whole so the runtime advances as it goes
	// instead of buffering the entire clip before the first chunk boundary.
	for _, chunk := range chunkPCM(pcm, streamChunkSamples) {
		if err := stream.push(chunk, sampleRate); err != nil {
			return nil, err
		}
	}
	// Before the query, always: finish is what labels the audio tail, so
	// segmenting first drops the last turn of every clip.
	if err := stream.finish(); err != nil {
		return nil, err
	}

	raw, err := collectSegments(stream)
	if err != nil {
		return nil, err
	}

	segs := toDiarizeSegments(raw)
	out := &pb.DiarizeResponse{
		Segments:    segs,
		NumSpeakers: distinctSpeakers(segs),
	}
	// 0 is the proto's "unknown" and the C API's "already at the model rate",
	// so a rate that means the latter must not be divided by.
	if sampleRate > 0 {
		out.Duration = float32(len(pcm)) / float32(sampleRate)
	}
	// Language and the per-segment text stay empty: there is no ASR in this
	// pipeline to fill them, and the proto documents both as optional.
	return out, nil
}

// Diarize labels who spoke when in the audio at req.Dst.
//
// The whole body runs inside withEngine, so the family check and the C calls
// that trust the handle happen under a single acquisition of engineMu. The
// audio decode is in there too, for the reason documented on
// AudioTranscription: the backend already serialises RPCs, so the wider hold
// costs nothing, and the narrower one is the gap Free can land in.
func (n *NemoSpeech) Diarize(req *pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	var out *pb.DiarizeResponse
	if err := n.withEngine(familyDiarization, func() error {
		r, err := n.diarize(req)
		out = r
		return err
	}); err != nil {
		return pb.DiarizeResponse{}, err
	}
	if out == nil {
		return pb.DiarizeResponse{}, status.Error(codes.Internal,
			"nemo-speech-cpp: diarization produced no result")
	}

	// Assembled field by field rather than dereferenced: the RPC returns the
	// message by value and the message embeds a mutex, so copying the struct is
	// a copylocks violation.
	return pb.DiarizeResponse{
		Segments:    out.Segments,
		NumSpeakers: out.NumSpeakers,
		Duration:    out.Duration,
		Language:    out.Language,
	}, nil
}

// diarize is Diarize's body. The caller must hold engineMu.
func (n *NemoSpeech) diarize(req *pb.DiarizeRequest) (*pb.DiarizeResponse, error) {
	if req.GetDst() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"nemo-speech-cpp: DiarizeRequest.dst (audio path) is required")
	}
	// Logged rather than rejected: a client that asks for a speaker count still
	// wants the diarization it can have, and a request that names a field this
	// backend drops should say so somewhere the operator can find it.
	if dropped := unsupportedRequestFields(req); len(dropped) > 0 {
		xlog.Warn("nemo-speech-cpp: ignoring diarization request fields this model has no equivalent for",
			"fields", dropped)
	}

	pcm, sampleRate, err := decodeAudioMono16k(req.GetDst())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "nemo-speech-cpp: read audio: %v", err)
	}

	return diarizePCM(n.openDiarStream, pcm, sampleRate, segmentationConfig(req))
}

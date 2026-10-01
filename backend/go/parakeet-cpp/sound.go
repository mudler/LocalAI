package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// soundFeedChunkSamples is how much 16 kHz mono PCM soundStreamScores hands
// to sound_stream_feed per call (10 s), matching the window/hop it asks for
// below. The clip is fed in these pieces with is_last set on the final one,
// mirroring the streaming ASR path's chunked feed.
const soundFeedChunkSamples = 10 * 16000

// soundTagJSON mirrors one element of a soundWindowJSON's "tags" array.
type soundTagJSON struct {
	Index int     `json:"index"`
	Label string  `json:"label"`
	Score float32 `json:"score"`
}

// soundWindowJSON mirrors one element of the array
// parakeet_capi_sound_stream_drain_scores_json returns:
//
//	[{"start":0.0,"end":10.0,"tags":[{"index":0,"label":"Speech","score":0.93}, ...]}]
type soundWindowJSON struct {
	Start float64        `json:"start"`
	End   float64        `json:"end"`
	Tags  []soundTagJSON `json:"tags"`
}

// classAvg is one class's score averaged across the drained windows, plus
// the label the tagger reported for it.
type classAvg struct {
	Index int
	Label string
	Score float32
}

// SoundDetection runs the loaded CED model (p.tagCtx) over the clip at
// req.Src through a one-shot sound stream (window 10 s, hop 10 s, top_k set
// to the tagger's full class count so every window's drain carries a score
// for every class), averages each class's score across the drained windows,
// sorts descending, applies req.Threshold, then req.TopK (0 = all classes).
func (p *ParakeetCpp) SoundDetection(ctx context.Context, req *pb.SoundDetectionRequest) (*pb.SoundDetectionResponse, error) {
	if p.tagCtx == 0 {
		return nil, status.Error(codes.FailedPrecondition,
			"parakeet-cpp: model is not a sound (CED) model")
	}
	if CppSoundStreamBegin == nil || CppSoundStreamFeed == nil || CppSoundStreamDrainScoresJSON == nil ||
		CppSoundStreamFree == nil || CppSoundOptsDefault == nil || CppNumClasses == nil {
		return nil, status.Error(codes.Unimplemented,
			"parakeet-cpp: loaded libparakeet.so has no sound-event detection support "+
				"(parakeet_capi_sound_stream_* missing)")
	}
	if req.GetSrc() == "" {
		return nil, status.Error(codes.InvalidArgument,
			"parakeet-cpp: SoundDetectionRequest.src (audio path) is required")
	}

	pcm, _, err := decodeWavMono16k(req.GetSrc())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parakeet-cpp: decode audio: %s", err)
	}

	windows, nClasses, err := p.soundStreamScores(ctx, pcm)
	if err != nil {
		return nil, err
	}

	avgs := averageWindowScores(windows, nClasses)
	sortSoundDetectionsDesc(avgs)
	avgs = filterSoundDetections(avgs, req.GetThreshold(), req.GetTopK())

	resp := &pb.SoundDetectionResponse{Detections: make([]*pb.SoundClass, 0, len(avgs))}
	for _, a := range avgs {
		resp.Detections = append(resp.Detections, &pb.SoundClass{
			Label: a.Label,
			Score: a.Score,
			Index: int32(a.Index),
		})
	}
	return resp, nil
}

// soundStreamScores runs pcm through a fresh sound stream and returns the
// drained per-window scores plus the tagger's class count. The C calls run
// under engineMu (see soundStreamDrain); JSON decoding happens after the
// lock is released.
func (p *ParakeetCpp) soundStreamScores(ctx context.Context, pcm []float32) ([]soundWindowJSON, int, error) {
	doc, nClasses, err := p.soundStreamDrain(ctx, pcm)
	if err != nil {
		return nil, nClasses, err
	}

	var windows []soundWindowJSON
	if err := json.Unmarshal([]byte(doc), &windows); err != nil {
		return nil, nClasses, fmt.Errorf("parakeet-cpp: decode sound scores json: %w", err)
	}
	return windows, nClasses, nil
}

// soundStreamDrain runs pcm through a fresh sound stream and returns the
// raw JSON document parakeet_capi_sound_stream_drain_scores_json drained,
// plus the tagger's class count. Every C call (opts default, begin, feed,
// free, drain) runs under engineMu; the stream is freed (deferred right
// after a successful begin) even when a later feed or drain call fails, or
// ctx is cancelled mid-feed. Each feed's returned segments array is freed
// with parakeet_capi_free_sound_segments even though SoundDetection has no
// use for the segments themselves (it only reads the drained window
// scores). ctx.Err() is checked before each feed slice, mirroring
// driver.go's feedSlices, so a long clip can be cancelled mid-feed; the
// caller decodes the returned JSON outside the lock.
func (p *ParakeetCpp) soundStreamDrain(ctx context.Context, pcm []float32) (string, int, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()

	// SoundDetection's own p.tagCtx==0 check runs before this lock is taken;
	// re-check here so a Free() racing in between (which zeroes p.tagCtx
	// under this same engineMu) is caught instead of handed to the C side,
	// mirroring streamFeedDoc's/sceneFeed's re-check.
	if p.tagCtx == 0 {
		return "", 0, grpcerrors.ModelNotLoaded("parakeet-cpp")
	}

	nClasses := int(CppNumClasses(p.tagCtx))

	var opts cSoundOpts
	CppSoundOptsDefault(&opts)
	opts.WindowSec = 10
	opts.HopSec = 10
	opts.TopK = int32(nClasses)

	stream := CppSoundStreamBegin(p.tagCtx, &opts)
	if stream == 0 {
		return "", nClasses, fmt.Errorf("parakeet-cpp: sound_stream_begin failed: %s", soundLastError(p.tagCtx))
	}
	defer CppSoundStreamFree(stream)

	offset := 0
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", nClasses, status.Error(codes.Canceled, "parakeet-cpp: sound detection cancelled")
			}
		}

		end := offset + soundFeedChunkSamples
		isLast := int32(0)
		if end >= len(pcm) {
			end = len(pcm)
			isLast = 1
		}
		var samplePtr *float32
		if end > offset {
			samplePtr = &pcm[offset]
		}

		var segsOut uintptr
		var nOut int32
		rc := CppSoundStreamFeed(stream, samplePtr, int32(end-offset), isLast, &segsOut, &nOut)
		if segsOut != 0 && CppFreeSoundSegments != nil {
			CppFreeSoundSegments(segsOut)
		}
		if rc != 0 {
			return "", nClasses, fmt.Errorf("parakeet-cpp: sound_stream_feed failed: %s", soundLastError(p.tagCtx))
		}

		offset = end
		if isLast == 1 {
			break
		}
	}

	raw := CppSoundStreamDrainScoresJSON(stream)
	if raw == 0 {
		return "", nClasses, fmt.Errorf("parakeet-cpp: sound_stream_drain_scores_json failed: %s", soundLastError(p.tagCtx))
	}
	doc := goStringFromCPtr(raw)
	CppFreeString(raw)
	return doc, nClasses, nil
}

// soundLastError reads ctx's last_error, substituting a fallback message
// when the C side left it empty.
func soundLastError(ctx uintptr) string {
	msg := CppLastError(ctx)
	if msg == "" {
		msg = "unknown error"
	}
	return msg
}

// averageWindowScores averages each class's score across the drained
// per-window scores: CED's own long-clip method, summing a class's score
// over every window and dividing by the window count (a class absent from a
// window's tags counts as 0 in that window). Only classes that appeared in
// at least one window are returned, in no particular order; callers sort and
// filter afterward. A pure function so it is easy to unit test in isolation
// from the C stream.
func averageWindowScores(windows []soundWindowJSON, nClasses int) []classAvg {
	if len(windows) == 0 {
		return nil
	}

	sums := make(map[int]float32)
	labels := make(map[int]string)
	for _, w := range windows {
		for _, t := range w.Tags {
			if t.Index < 0 || (nClasses > 0 && t.Index >= nClasses) {
				continue
			}
			sums[t.Index] += t.Score
			if _, ok := labels[t.Index]; !ok {
				labels[t.Index] = t.Label
			}
		}
	}

	n := float32(len(windows))
	out := make([]classAvg, 0, len(sums))
	for idx, sum := range sums {
		out = append(out, classAvg{Index: idx, Label: labels[idx], Score: sum / n})
	}
	return out
}

// sortSoundDetectionsDesc sorts avgs by score descending, breaking ties by
// class index for a deterministic order (map iteration in
// averageWindowScores is otherwise unordered).
func sortSoundDetectionsDesc(avgs []classAvg) {
	sort.Slice(avgs, func(i, j int) bool {
		if avgs[i].Score != avgs[j].Score {
			return avgs[i].Score > avgs[j].Score
		}
		return avgs[i].Index < avgs[j].Index
	})
}

// filterSoundDetections drops entries scoring below threshold, then keeps
// only the first topK entries (0 = keep all). avgs is assumed already sorted
// descending by score.
func filterSoundDetections(avgs []classAvg, threshold float32, topK int32) []classAvg {
	out := avgs[:0:0]
	for _, a := range avgs {
		if a.Score < threshold {
			continue
		}
		out = append(out, a)
	}
	if topK > 0 && int32(len(out)) > topK {
		out = out[:topK]
	}
	return out
}

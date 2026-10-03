package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// vadSampleRate is the rate of VADRequest.audio: the VAD endpoint takes float32
// PCM at 16 kHz, the rate the other VAD backends (silero-vad, whisper) assume.
const vadSampleRate = 16000

// vadTuning maps a model option to the key of the library's VAD options object.
// All values are seconds except the threshold. An unset option leaves the
// default of the detector in use (the VAD head and Silero differ), so no value
// is repeated here.
var vadTuning = []struct {
	option, key string
	// min and max bound the value; the library rejects the rest, but failing the
	// load names the option instead of failing every request.
	min, max float64
	minOpen  bool // true when min itself is not allowed (the threshold is in (0, 1])
}{
	{"vad_threshold", "threshold", 0, 1, true},
	{"vad_min_pause", "min_pause", 0, math.Inf(1), true},
	{"vad_min_speech", "min_speech", 0, math.Inf(1), false},
	{"vad_speech_pad", "speech_pad", 0, math.Inf(1), false},
	{"vad_max_segment", "max_segment", 0, math.Inf(1), true},
}

// parseVADTuning reads the vad_threshold, vad_min_pause, vad_min_speech,
// vad_speech_pad and vad_max_segment model options and returns them as the JSON
// options object of the C-API, or "" when none is set. A value that does not
// parse or is out of range fails the load.
func parseVADTuning(opts *pb.ModelOptions) (string, error) {
	obj := map[string]float64{}
	for _, t := range vadTuning {
		raw := optString(opts, t.option)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("parakeet-cpp: option %s: %q is not a number", t.option, raw)
		}
		if v < t.min || (t.minOpen && v == t.min) || v > t.max {
			return "", fmt.Errorf("parakeet-cpp: option %s: %v is out of range", t.option, v)
		}
		obj[t.key] = v
	}
	if len(obj) == 0 {
		return "", nil
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// vadDocument is the part of the C-API VAD result the RPC needs.
type vadDocument struct {
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"segments"`
}

// VAD runs the standalone voice activity detection of libparakeet on the
// request audio and returns the speech regions in seconds. It uses the Silero
// context (the model itself or the vad_model: companion) when there is one, else
// the VAD head of the ASR model. An ASR model without a head fails with the
// library's message.
func (p *ParakeetCpp) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	if CppVadPcmJSON == nil {
		return pb.VADResponse{}, errors.New("parakeet-cpp: VAD needs a libparakeet.so with parakeet_capi_vad_pcm_json; rebuild the backend against a newer parakeet.cpp")
	}
	p.engineMu.Lock()
	ctx := p.vadCtx
	if ctx == 0 {
		ctx = p.ctxPtr
	}
	if ctx == 0 {
		p.engineMu.Unlock()
		if err := p.notASRError(); err != nil {
			return pb.VADResponse{}, err
		}
		return pb.VADResponse{}, errors.New("parakeet-cpp: no model loaded for VAD")
	}
	cstr := CppVadPcmJSON(ctx, req.GetAudio(), int32(len(req.GetAudio())), vadSampleRate, p.vadOptions)
	var lastErr string
	if cstr == 0 {
		lastErr = CppLastError(ctx)
	}
	p.engineMu.Unlock()
	if cstr == 0 {
		return pb.VADResponse{}, fmt.Errorf("parakeet-cpp: vad failed: %s", lastErr)
	}
	raw := goStringFromCPtr(cstr)
	CppFreeString(cstr)

	var doc vadDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return pb.VADResponse{}, fmt.Errorf("parakeet-cpp: decode vad json: %w", err)
	}
	segs := make([]*pb.VADSegment, 0, len(doc.Segments))
	for _, s := range doc.Segments {
		segs = append(segs, &pb.VADSegment{Start: float32(s.Start), End: float32(s.End)})
	}
	return pb.VADResponse{Segments: segs}, nil
}

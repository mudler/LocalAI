package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const transcriptionsPath = "/v1/audio/transcriptions"

// ttsRequest is the body of LocalAI's /tts (schema.TTSRequest).
type ttsRequest struct {
	Model        string            `json:"model"`
	Input        string            `json:"input"`
	Voice        string            `json:"voice,omitempty"`
	Language     string            `json:"language,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Params       map[string]string `json:"params,omitempty"`
	Stream       bool              `json:"stream,omitempty"`
}

func (p *LocalAIProxy) ttsRequest(req *pb.TTSRequest, stream bool) ttsRequest {
	return ttsRequest{
		Model:        p.model(""),
		Input:        req.GetText(),
		Voice:        req.GetVoice(),
		Language:     req.GetLanguage(),
		Instructions: req.GetInstructions(),
		Params:       req.GetParams(),
		Stream:       stream,
	}
}

func (p *LocalAIProxy) TTS(req *pb.TTSRequest) error {
	return p.postJSONToFile(context.Background(), "/tts", p.ttsRequest(req, false), req.GetDst())
}

// TTSStream forwards the upstream's chunked audio unchanged. That body is
// already what core expects from a streaming backend: a WAV header followed
// by PCM. out is closed on every path because the gRPC server drains it until
// closed and would otherwise hang.
func (p *LocalAIProxy) TTSStream(req *pb.TTSRequest, out chan []byte) error {
	defer close(out)
	resp, err := p.postStream(context.Background(), "/tts", p.ttsRequest(req, true))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// The reader reuses buf, so each chunk needs its own copy.
			out <- append([]byte(nil), buf[:n]...)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// A cut-off stream is a failed synthesis, not a short one.
			return transportError("/tts", err)
		}
	}
}

// soundGenerationRequest is the body of /v1/sound-generation
// (schema.ElevenLabsSoundGenerationRequest). Pointers keep "unset" distinct
// from zero so the upstream model's defaults apply.
type soundGenerationRequest struct {
	ModelID       string   `json:"model_id"`
	Text          string   `json:"text"`
	Duration      *float32 `json:"duration_seconds,omitempty"`
	Temperature   *float32 `json:"prompt_influence,omitempty"`
	DoSample      *bool    `json:"do_sample,omitempty"`
	Think         *bool    `json:"think,omitempty"`
	Caption       string   `json:"caption,omitempty"`
	Lyrics        string   `json:"lyrics,omitempty"`
	BPM           *int32   `json:"bpm,omitempty"`
	Keyscale      string   `json:"keyscale,omitempty"`
	Language      string   `json:"language,omitempty"`
	Timesignature string   `json:"timesignature,omitempty"`
	Instrumental  *bool    `json:"instrumental,omitempty"`
}

// SoundGeneration refuses audio-conditioned requests: the REST endpoint has no
// field for a source clip, and dropping it would return unconditioned audio as
// if it were the answer.
func (p *LocalAIProxy) SoundGeneration(req *pb.SoundGenerationRequest) error {
	if req.GetSrc() != "" {
		return unimplemented("SoundGeneration with src")
	}
	body := soundGenerationRequest{
		ModelID:       p.model(""),
		Text:          req.GetText(),
		Duration:      req.Duration,
		Temperature:   req.Temperature,
		DoSample:      req.Sample,
		Think:         req.Think,
		Caption:       req.GetCaption(),
		Lyrics:        req.GetLyrics(),
		BPM:           req.Bpm,
		Keyscale:      req.GetKeyscale(),
		Language:      req.GetLanguage(),
		Timesignature: req.GetTimesignature(),
		Instrumental:  req.Instrumental,
	}
	return p.postJSONToFile(context.Background(), "/v1/sound-generation", body, req.GetDst())
}

// transcriptionForm builds the /v1/audio/transcriptions upload. Dst is the
// input audio (core names it that way). Language and translate are only sent
// when set so the upstream model config's defaults still apply; diarize is
// always sent because the upstream treats a missing field as true.
func (p *LocalAIProxy) transcriptionForm(req *pb.TranscriptRequest, stream bool) multipartForm {
	f := url.Values{}
	f.Set("model", p.model(""))
	f.Set("diarize", strconv.FormatBool(req.GetDiarize()))
	// verbose_json keeps segments and words; the default drops nothing today,
	// but naming it keeps the reply shape pinned.
	f.Set("response_format", "verbose_json")
	if v := req.GetLanguage(); v != "" {
		f.Set("language", v)
	}
	if req.GetTranslate() {
		f.Set("translate", "true")
	}
	if v := req.GetPrompt(); v != "" {
		f.Set("prompt", v)
	}
	if v := req.GetTemperature(); v != 0 {
		f.Set("temperature", formatFloat(v))
	}
	for _, g := range req.GetTimestampGranularities() {
		f.Add("timestamp_granularities[]", g)
	}
	if stream {
		f.Set("stream", "true")
	}
	return multipartForm{fields: f, files: []formFile{{field: "file", path: req.GetDst()}}}
}

// transcriptionResult is TranscriptionResultSeconds: the REST API reports
// times in seconds, pb in nanoseconds (core reads them as time.Duration).
type transcriptionResult struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language"`
	Duration float64                `json:"duration"`
	Segments []transcriptionSegment `json:"segments"`
}

type transcriptionSegment struct {
	ID      int32   `json:"id"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Tokens  []int32 `json:"tokens"`
	Speaker string  `json:"speaker"`
	Words   []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"words"`
}

// toProto drops the top-level words: core rebuilds them from segment words.
func (r transcriptionResult) toProto() *pb.TranscriptResult {
	out := &pb.TranscriptResult{Text: r.Text, Language: r.Language, Duration: float32(r.Duration)}
	for _, s := range r.Segments {
		seg := &pb.TranscriptSegment{
			Id: s.ID, Start: nanos(s.Start), End: nanos(s.End), Text: s.Text,
			Tokens: s.Tokens, Speaker: s.Speaker,
		}
		for _, w := range s.Words {
			seg.Words = append(seg.Words, &pb.TranscriptWord{Start: nanos(w.Start), End: nanos(w.End), Text: w.Text})
		}
		out.Segments = append(out.Segments, seg)
	}
	return out
}

func nanos(seconds float64) int64 {
	return int64(seconds * float64(time.Second))
}

func (p *LocalAIProxy) AudioTranscription(ctx context.Context, req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	var resp transcriptionResult
	if err := p.postForm(ctx, transcriptionsPath, p.transcriptionForm(req, false), &resp); err != nil {
		return pb.TranscriptResult{}, err
	}
	return *resp.toProto(), nil
}

// transcriptEvent covers every frame of the upstream transcription SSE stream.
type transcriptEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	transcriptionResult
}

// AudioTranscriptionStream maps upstream SSE frames to stream responses. out
// is closed on every path: the gRPC server drains it until closed.
func (p *LocalAIProxy) AudioTranscriptionStream(ctx context.Context, req *pb.TranscriptRequest, out chan *pb.TranscriptStreamResponse) error {
	defer close(out)
	resp, err := p.postMultipartStream(ctx, transcriptionsPath, p.transcriptionForm(req, true))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	// The done frame carries every segment of the recording.
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev transcriptEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			xlog.Debug("localai-proxy: skip malformed SSE frame", "path", transcriptionsPath, "error", err)
			continue
		}
		switch ev.Type {
		case "transcript.text.delta":
			out <- &pb.TranscriptStreamResponse{Delta: ev.Delta}
		case "transcript.text.done":
			out <- &pb.TranscriptStreamResponse{FinalResult: ev.toProto()}
			return nil
		case "error":
			msg := "unknown error"
			if ev.Error != nil {
				msg = ev.Error.Message
			}
			xlog.Warn("localai-proxy: upstream stream error", "path", transcriptionsPath, "error", msg)
			return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s stream failed: %s", transcriptionsPath, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return transportError(transcriptionsPath, err)
	}
	// The upstream always ends with a done or error frame, so a stream that
	// stops without one was cut off; returning nil would pass the partial
	// deltas off as the whole transcript.
	return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s stream ended before the final transcript", transcriptionsPath)
}

func (p *LocalAIProxy) Diarize(req *pb.DiarizeRequest) (pb.DiarizeResponse, error) {
	f := url.Values{}
	f.Set("model", p.model(""))
	// verbose_json keeps per-segment text; core strips it again for callers
	// that did not ask for it.
	f.Set("response_format", "verbose_json")
	if v := req.GetLanguage(); v != "" {
		f.Set("language", v)
	}
	for name, v := range map[string]int32{
		"num_speakers": req.GetNumSpeakers(), "min_speakers": req.GetMinSpeakers(), "max_speakers": req.GetMaxSpeakers(),
	} {
		if v != 0 {
			f.Set(name, strconv.Itoa(int(v)))
		}
	}
	for name, v := range map[string]float32{
		"clustering_threshold": req.GetClusteringThreshold(),
		"min_duration_on":      req.GetMinDurationOn(),
		"min_duration_off":     req.GetMinDurationOff(),
	} {
		if v != 0 {
			f.Set(name, formatFloat(v))
		}
	}
	if req.GetIncludeText() {
		f.Set("include_text", "true")
	}

	var resp struct {
		Duration    float64 `json:"duration"`
		Language    string  `json:"language"`
		NumSpeakers int32   `json:"num_speakers"`
		Segments    []struct {
			ID      int32   `json:"id"`
			Speaker string  `json:"speaker"`
			Label   string  `json:"label"`
			Start   float32 `json:"start"`
			End     float32 `json:"end"`
			Text    string  `json:"text"`
		} `json:"segments"`
	}
	if err := p.postForm(context.Background(), "/v1/audio/diarization",
		multipartForm{fields: f, files: []formFile{{field: "file", path: req.GetDst()}}}, &resp); err != nil {
		return pb.DiarizeResponse{}, err
	}
	var segments []*pb.DiarizeSegment
	for _, s := range resp.Segments {
		// The upstream renames speakers to SPEAKER_NN and keeps the backend's
		// own id in label. Core renames again, so hand it the raw id: the
		// caller then sees the same speakers and labels a direct call gives.
		speaker := s.Label
		if speaker == "" {
			speaker = s.Speaker
		}
		segments = append(segments, &pb.DiarizeSegment{
			Id: s.ID, Start: s.Start, End: s.End, Speaker: speaker, Text: s.Text,
		})
	}
	return pb.DiarizeResponse{
		Segments: segments, NumSpeakers: resp.NumSpeakers, Duration: float32(resp.Duration), Language: resp.Language,
	}, nil
}

func (p *LocalAIProxy) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	var resp struct {
		Segments []struct {
			Start float32 `json:"start"`
			End   float32 `json:"end"`
		} `json:"segments"`
	}
	body := map[string]any{"model": p.model(""), "audio": req.GetAudio()}
	if err := p.postJSON(context.Background(), "/v1/vad", body, &resp); err != nil {
		return pb.VADResponse{}, err
	}
	var segments []*pb.VADSegment
	for _, s := range resp.Segments {
		segments = append(segments, &pb.VADSegment{Start: s.Start, End: s.End})
	}
	return pb.VADResponse{Segments: segments}, nil
}

func (p *LocalAIProxy) SoundDetection(ctx context.Context, req *pb.SoundDetectionRequest) (*pb.SoundDetectionResponse, error) {
	f := url.Values{}
	f.Set("model", p.model(""))
	if v := req.GetTopK(); v != 0 {
		f.Set("top_k", strconv.Itoa(int(v)))
	}
	if v := req.GetThreshold(); v != 0 {
		f.Set("threshold", formatFloat(v))
	}
	var resp struct {
		Detections []struct {
			Index int32   `json:"index"`
			Label string  `json:"label"`
			Score float32 `json:"score"`
		} `json:"detections"`
	}
	if err := p.postForm(ctx, "/v1/audio/classification",
		multipartForm{fields: f, files: []formFile{{field: "file", path: req.GetSrc()}}}, &resp); err != nil {
		return nil, err
	}
	out := &pb.SoundDetectionResponse{}
	for _, d := range resp.Detections {
		out.Detections = append(out.Detections, &pb.SoundClass{Index: d.Index, Label: d.Label, Score: d.Score})
	}
	return out, nil
}

// stemsHeader names the other outputs of a separation run: the body carries
// one file, and the rest are served under /generated-audio/.
const stemsHeader = "X-Audio-Stems"

// AudioTransform uploads the input (and reference) to /audio/transformations
// and writes the returned audio to Dst. SampleRate and Samples stay 0: the
// REST reply does not report them, and core only uses them for tracing.
func (p *LocalAIProxy) AudioTransform(req *pb.AudioTransformRequest) (*pb.AudioTransformResult, error) {
	f := url.Values{}
	f.Set("model", p.model(""))
	for k, v := range req.GetParams() {
		f.Set("params["+k+"]", v)
	}
	form := multipartForm{fields: f, files: []formFile{{field: "audio", path: req.GetAudioPath()}}}
	if ref := req.GetReferencePath(); ref != "" {
		form.files = append(form.files, formFile{field: "reference", path: ref})
	}

	ctx := context.Background()
	header, err := p.postMultipartToFile(ctx, "/audio/transformations", form, req.GetDst())
	if err != nil {
		return nil, err
	}
	return &pb.AudioTransformResult{
		Dst:               req.GetDst(),
		ReferenceProvided: req.GetReferencePath() != "",
		Stems:             p.fetchStems(ctx, header.Get(stemsHeader), req.GetDst()),
	}, nil
}

// fetchStems downloads each stem the upstream names and writes it beside
// dst, where core looks for them. A stem that cannot be fetched is dropped
// with a warning rather than failing the call: the caller still gets the
// audio it asked for.
func (p *LocalAIProxy) fetchStems(ctx context.Context, header, dst string) []*pb.AudioTransformStem {
	if header == "" {
		return nil
	}
	var entries []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal([]byte(header), &entries); err != nil {
		xlog.Warn("localai-proxy: ignore malformed stems header", "error", err)
		return nil
	}
	dir := filepath.Dir(dst)
	prefix := strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst))
	var stems []*pb.AudioTransformStem
	for _, e := range entries {
		// Only files under /generated-audio/ are stems. Anything else is not
		// a path this API hands out, so it is not fetched.
		escaped, ok := strings.CutPrefix(e.URL, "/generated-audio/")
		if !ok || e.Name == "" {
			xlog.Warn("localai-proxy: skip stem with unexpected url", "name", e.Name, "url", e.URL)
			continue
		}
		name, err := url.PathUnescape(escaped)
		if err != nil || name == "" || name != filepath.Base(name) || name == "." || name == ".." {
			xlog.Warn("localai-proxy: skip stem with unsafe file name", "name", e.Name, "url", e.URL)
			continue
		}
		local := filepath.Join(dir, prefix+"-"+name)
		if err := p.getToFile(ctx, e.URL, local); err != nil {
			xlog.Warn("localai-proxy: stem download failed", "name", e.Name, "error", err)
			continue
		}
		stems = append(stems, &pb.AudioTransformStem{Name: e.Name, Dst: local})
	}
	return stems
}

// formatFloat prints a float32 form value without float64 noise
// (0.2, not 0.20000000298023224).
func formatFloat(v float32) string {
	return strconv.FormatFloat(float64(v), 'g', -1, 32)
}

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// fileToBase64 reads path and base64-encodes its contents, or returns "" for
// an empty path. Core stages image/video/3D conditioning inputs to local
// files before calling the backend, but the REST endpoints on the other side
// take the same media inline as base64 (or a URL/data-URI, neither of which
// a local path is), so every media method needs this same conversion.
func fileToBase64(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "localai-proxy: read %s: %v", path, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// genItem is one schema.Item as the image/video/3D generation endpoints
// return it: either inline base64 data, or a URL to download the asset from.
type genItem struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

type genResponse struct {
	Data []genItem `json:"data"`
}

// writeGenItem writes the first item of a generation reply to dst: it
// base64-decodes inline data directly, or downloads the URL relative to the
// configured upstream (same client, same auth) when the upstream sent one
// instead.
func (p *LocalAIProxy) writeGenItem(ctx context.Context, path string, items []genItem, dst string) error {
	if len(items) == 0 {
		return status.Errorf(codes.Internal, "localai-proxy: upstream %s returned no data", path)
	}
	item := items[0]
	if item.B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return status.Errorf(codes.Internal, "localai-proxy: decode %s b64_json: %v", path, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			_ = os.Remove(dst)
			return status.Errorf(codes.Internal, "localai-proxy: write %s: %v", dst, err)
		}
		return nil
	}
	if item.URL == "" {
		return status.Errorf(codes.Internal, "localai-proxy: upstream %s returned neither b64_json nor url", path)
	}
	return p.getToFile(ctx, p.relativePath(item.URL), dst)
}

// relativePath strips the configured upstream base from a URL the upstream
// handed back (e.g. "<upstream>/generated-images/x.png"), so the follow-up
// download goes through the normal request path instead of concatenating two
// absolute URLs.
func (p *LocalAIProxy) relativePath(raw string) string {
	if cfg := p.cfg.Load(); cfg != nil {
		if rel, ok := strings.CutPrefix(raw, cfg.base); ok {
			return rel
		}
	}
	return raw
}

// --- Images ---------------------------------------------------------

type imageGenerationRequest struct {
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	Size           string   `json:"size,omitempty"`
	Step           int32    `json:"step,omitempty"`
	Seed           int32    `json:"seed,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	File           string   `json:"file,omitempty"`
	RefImages      []string `json:"ref_images,omitempty"`
}

// GenerateImage sends Src and RefImages (local paths) as base64, asks for a
// b64_json reply, and writes the result to Dst.
func (p *LocalAIProxy) GenerateImage(req *pb.GenerateImageRequest) error {
	ctx := context.Background()
	file, err := fileToBase64(req.GetSrc())
	if err != nil {
		return err
	}
	var refImages []string
	for _, ref := range req.GetRefImages() {
		encoded, err := fileToBase64(ref)
		if err != nil {
			return err
		}
		refImages = append(refImages, encoded)
	}
	body := imageGenerationRequest{
		Model:          p.model(""),
		Prompt:         req.GetPositivePrompt(),
		NegativePrompt: req.GetNegativePrompt(),
		Size:           fmt.Sprintf("%dx%d", req.GetWidth(), req.GetHeight()),
		Step:           req.GetStep(),
		Seed:           req.GetSeed(),
		ResponseFormat: "b64_json",
		File:           file,
		RefImages:      refImages,
	}
	var resp genResponse
	if err := p.postJSON(ctx, "/v1/images/generations", body, &resp); err != nil {
		return err
	}
	return p.writeGenItem(ctx, "/v1/images/generations", resp.Data, req.GetDst())
}

// UpscaleImage uploads Src as a multipart file, matching the REST endpoint's
// upload-only contract, and writes the (always URL) reply to Dst.
func (p *LocalAIProxy) UpscaleImage(req *pb.UpscaleImageRequest) error {
	ctx := context.Background()
	fields := url.Values{}
	fields.Set("model", p.model(""))
	if req.GetScale() > 0 {
		fields.Set("scale", strconv.Itoa(int(req.GetScale())))
	}
	form := multipartForm{fields: fields, files: []formFile{{field: "image", path: req.GetSrc()}}}
	var resp genResponse
	if err := p.postForm(ctx, "/v1/images/upscale", form, &resp); err != nil {
		return err
	}
	return p.writeGenItem(ctx, "/v1/images/upscale", resp.Data, req.GetDst())
}

// --- Video ------------------------------------------------------------

type videoGenerationRequest struct {
	Model          string            `json:"model"`
	Prompt         string            `json:"prompt"`
	NegativePrompt string            `json:"negative_prompt,omitempty"`
	StartImage     string            `json:"start_image,omitempty"`
	EndImage       string            `json:"end_image,omitempty"`
	Audio          string            `json:"audio,omitempty"`
	Width          int32             `json:"width,omitempty"`
	Height         int32             `json:"height,omitempty"`
	NumFrames      int32             `json:"num_frames,omitempty"`
	FPS            int32             `json:"fps,omitempty"`
	Seed           int32             `json:"seed,omitempty"`
	CFGScale       float32           `json:"cfg_scale,omitempty"`
	Step           int32             `json:"step,omitempty"`
	ResponseFormat string            `json:"response_format,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
}

// GenerateVideo sends the staged local media (StartImage, EndImage, Audio) as
// base64, asks for a b64_json reply, and writes the result to Dst.
func (p *LocalAIProxy) GenerateVideo(req *pb.GenerateVideoRequest) error {
	ctx := context.Background()
	startImage, err := fileToBase64(req.GetStartImage())
	if err != nil {
		return err
	}
	endImage, err := fileToBase64(req.GetEndImage())
	if err != nil {
		return err
	}
	audio, err := fileToBase64(req.GetAudio())
	if err != nil {
		return err
	}
	body := videoGenerationRequest{
		Model:          p.model(""),
		Prompt:         req.GetPrompt(),
		NegativePrompt: req.GetNegativePrompt(),
		StartImage:     startImage,
		EndImage:       endImage,
		Audio:          audio,
		Width:          req.GetWidth(),
		Height:         req.GetHeight(),
		NumFrames:      req.GetNumFrames(),
		FPS:            req.GetFps(),
		Seed:           req.GetSeed(),
		CFGScale:       req.GetCfgScale(),
		Step:           req.GetStep(),
		ResponseFormat: "b64_json",
		Params:         req.GetParams(),
	}
	var resp genResponse
	if err := p.postJSON(ctx, "/video", body, &resp); err != nil {
		return err
	}
	return p.writeGenItem(ctx, "/video", resp.Data, req.GetDst())
}

// --- 3D -----------------------------------------------------------------

type model3DRequest struct {
	Model          string            `json:"model"`
	Image          string            `json:"image"`
	Seed           int32             `json:"seed,omitempty"`
	Step           int32             `json:"step,omitempty"`
	CFGScale       float32           `json:"cfg_scale,omitempty"`
	TextureSteps   int32             `json:"texture_steps,omitempty"`
	Quality        string            `json:"quality,omitempty"`
	Background     string            `json:"background,omitempty"`
	ResponseFormat string            `json:"response_format,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
}

// Generate3D sends Src (the staged conditioning image, a local path) as
// base64, asks for a b64_json reply, and writes the result to Dst.
func (p *LocalAIProxy) Generate3D(req *pb.Generate3DRequest) error {
	ctx := context.Background()
	image, err := fileToBase64(req.GetSrc())
	if err != nil {
		return err
	}
	body := model3DRequest{
		Model:          p.model(""),
		Image:          image,
		Seed:           req.GetSeed(),
		Step:           req.GetStep(),
		CFGScale:       req.GetCfgScale(),
		TextureSteps:   req.GetTextureSteps(),
		Quality:        req.GetQuality(),
		Background:     req.GetBackground(),
		ResponseFormat: "b64_json",
		Params:         req.GetParams(),
	}
	var resp genResponse
	if err := p.postJSON(ctx, "/3d/generations", body, &resp); err != nil {
		return err
	}
	return p.writeGenItem(ctx, "/3d/generations", resp.Data, req.GetDst())
}

type animationInputBody struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type animate3DRequestBody struct {
	Model          string                        `json:"model"`
	Inputs         map[string]animationInputBody `json:"inputs"`
	Params         map[string]string             `json:"params,omitempty"`
	ResponseFormat string                        `json:"response_format,omitempty"`
}

type animate3DResponseBody struct {
	genResponse
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// animate3D does the shared work behind Animate3D and Animate3DWithMetadata:
// non-text inputs are staged local paths, so they go over the wire as
// base64, like every other media field; text inputs travel verbatim.
func (p *LocalAIProxy) animate3D(ctx context.Context, req *pb.Animate3DRequest) (json.RawMessage, error) {
	inputs := make(map[string]animationInputBody, len(req.GetInputs()))
	for name, in := range req.GetInputs() {
		data := in.GetData()
		if in.GetType() != "text" {
			encoded, err := fileToBase64(data)
			if err != nil {
				return nil, err
			}
			data = encoded
		}
		inputs[name] = animationInputBody{Type: in.GetType(), Data: data}
	}
	body := animate3DRequestBody{
		Model:          p.model(""),
		Inputs:         inputs,
		Params:         req.GetParams(),
		ResponseFormat: "b64_json",
	}
	var resp animate3DResponseBody
	if err := p.postJSON(ctx, "/3d/animate", body, &resp); err != nil {
		return nil, err
	}
	if err := p.writeGenItem(ctx, "/3d/animate", resp.Data, req.GetDst()); err != nil {
		return nil, err
	}
	return resp.Metadata, nil
}

func (p *LocalAIProxy) Animate3D(req *pb.Animate3DRequest) error {
	_, err := p.animate3D(context.Background(), req)
	return err
}

// Animate3DWithMetadata implements grpc.AnimationMetadataModel: the REST
// reply's metadata field carries whatever the animation backend reported, and
// the gRPC server prefers this method over Animate3D when it is implemented.
func (p *LocalAIProxy) Animate3DWithMetadata(req *pb.Animate3DRequest) ([]byte, error) {
	return p.animate3D(context.Background(), req)
}

// --- Vision (detection, depth) ------------------------------------------

type detectRequestBody struct {
	Model     string    `json:"model"`
	Image     string    `json:"image"`
	Prompt    string    `json:"prompt,omitempty"`
	Points    []float32 `json:"points,omitempty"`
	Boxes     []float32 `json:"boxes,omitempty"`
	Threshold float32   `json:"threshold,omitempty"`
}

type detectionBody struct {
	X          float32 `json:"x"`
	Y          float32 `json:"y"`
	Width      float32 `json:"width"`
	Height     float32 `json:"height"`
	ClassName  string  `json:"class_name"`
	Confidence float32 `json:"confidence,omitempty"`
	Mask       string  `json:"mask,omitempty"`
}

type detectResponseBody struct {
	Detections []detectionBody `json:"detections"`
}

// Detect posts Src, which core already carries as a base64 payload (the same
// convention DetectionEndpoint uses to call this method locally), and maps
// each detection, decoding its PNG mask.
func (p *LocalAIProxy) Detect(req *pb.DetectOptions) (pb.DetectResponse, error) {
	body := detectRequestBody{
		Model:     p.model(""),
		Image:     req.GetSrc(),
		Prompt:    req.GetPrompt(),
		Points:    req.GetPoints(),
		Boxes:     req.GetBoxes(),
		Threshold: req.GetThreshold(),
	}
	var resp detectResponseBody
	if err := p.postJSON(context.Background(), "/v1/detection", body, &resp); err != nil {
		return pb.DetectResponse{}, err
	}
	var detections []*pb.Detection
	for _, d := range resp.Detections {
		det := &pb.Detection{
			X: d.X, Y: d.Y, Width: d.Width, Height: d.Height,
			Confidence: d.Confidence, ClassName: d.ClassName,
		}
		if d.Mask != "" {
			mask, err := base64.StdEncoding.DecodeString(d.Mask)
			if err != nil {
				return pb.DetectResponse{}, status.Errorf(codes.Internal, "localai-proxy: decode /v1/detection mask: %v", err)
			}
			det.Mask = mask
		}
		detections = append(detections, det)
	}
	// A composite literal here, not a variable of type pb.DetectResponse,
	// avoids copying the protobuf message's embedded lock on return.
	return pb.DetectResponse{Detections: detections}, nil
}

type depthRequestBody struct {
	Model             string   `json:"model"`
	Image             string   `json:"image"`
	Dst               string   `json:"dst,omitempty"`
	IncludeDepth      bool     `json:"include_depth,omitempty"`
	IncludeConfidence bool     `json:"include_confidence,omitempty"`
	IncludePose       bool     `json:"include_pose,omitempty"`
	IncludeSky        bool     `json:"include_sky,omitempty"`
	IncludePoints     bool     `json:"include_points,omitempty"`
	PointsConfThresh  float32  `json:"points_conf_thresh,omitempty"`
	Exports           []string `json:"exports,omitempty"`
}

type depthResponseBody struct {
	Width       int32     `json:"width"`
	Height      int32     `json:"height"`
	Depth       []float32 `json:"depth,omitempty"`
	Confidence  []float32 `json:"confidence,omitempty"`
	Sky         []float32 `json:"sky,omitempty"`
	Extrinsics  []float32 `json:"extrinsics,omitempty"`
	Intrinsics  []float32 `json:"intrinsics,omitempty"`
	NumPoints   int32     `json:"num_points,omitempty"`
	Points      []float32 `json:"points,omitempty"`
	PointColors string    `json:"point_colors,omitempty"`
	ExportPaths []string  `json:"export_paths,omitempty"`
	IsMetric    bool      `json:"is_metric"`
}

// Depth posts Src (a base64 payload, per the same convention as Detect) and
// maps the full response, decoding the point-cloud color bytes.
func (p *LocalAIProxy) Depth(req *pb.DepthRequest) (pb.DepthResponse, error) {
	body := depthRequestBody{
		Model:             p.model(""),
		Image:             req.GetSrc(),
		Dst:               req.GetDst(),
		IncludeDepth:      req.GetIncludeDepth(),
		IncludeConfidence: req.GetIncludeConfidence(),
		IncludePose:       req.GetIncludePose(),
		IncludeSky:        req.GetIncludeSky(),
		IncludePoints:     req.GetIncludePoints(),
		PointsConfThresh:  req.GetPointsConfThresh(),
		Exports:           req.GetExports(),
	}
	var resp depthResponseBody
	if err := p.postJSON(context.Background(), "/v1/depth", body, &resp); err != nil {
		return pb.DepthResponse{}, err
	}
	var colors []byte
	if resp.PointColors != "" {
		var err error
		colors, err = base64.StdEncoding.DecodeString(resp.PointColors)
		if err != nil {
			return pb.DepthResponse{}, status.Errorf(codes.Internal, "localai-proxy: decode /v1/depth point_colors: %v", err)
		}
	}
	// A composite literal here, not a variable of type pb.DepthResponse,
	// avoids copying the protobuf message's embedded lock on return.
	return pb.DepthResponse{
		Width: resp.Width, Height: resp.Height, Depth: resp.Depth, Confidence: resp.Confidence,
		Sky: resp.Sky, Extrinsics: resp.Extrinsics, Intrinsics: resp.Intrinsics,
		NumPoints: resp.NumPoints, Points: resp.Points, ExportPaths: resp.ExportPaths, IsMetric: resp.IsMetric,
		PointColors: colors,
	}, nil
}

// --- Face recognition -----------------------------------------------------

type facialAreaBody struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	W float32 `json:"w"`
	H float32 `json:"h"`
}

func (a facialAreaBody) toProto() *pb.FacialArea {
	return &pb.FacialArea{X: a.X, Y: a.Y, W: a.W, H: a.H}
}

type faceVerifyRequestBody struct {
	Model        string  `json:"model"`
	Img1         string  `json:"img1"`
	Img2         string  `json:"img2"`
	Threshold    float32 `json:"threshold,omitempty"`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty"`
}

type faceVerifyResponseBody struct {
	Verified           bool           `json:"verified"`
	Distance           float32        `json:"distance"`
	Threshold          float32        `json:"threshold"`
	Confidence         float32        `json:"confidence"`
	Model              string         `json:"model"`
	Img1Area           facialAreaBody `json:"img1_area"`
	Img2Area           facialAreaBody `json:"img2_area"`
	ProcessingTimeMs   float32        `json:"processing_time_ms,omitempty"`
	Img1IsReal         *bool          `json:"img1_is_real,omitempty"`
	Img1AntispoofScore *float32       `json:"img1_antispoof_score,omitempty"`
	Img2IsReal         *bool          `json:"img2_is_real,omitempty"`
	Img2AntispoofScore *float32       `json:"img2_antispoof_score,omitempty"`
}

// FaceVerify posts Img1/Img2, which core already carries as base64 (the same
// convention FaceVerifyEndpoint uses to call this method locally).
func (p *LocalAIProxy) FaceVerify(req *pb.FaceVerifyRequest) (pb.FaceVerifyResponse, error) {
	body := faceVerifyRequestBody{
		Model: p.model(""), Img1: req.GetImg1(), Img2: req.GetImg2(),
		Threshold: req.GetThreshold(), AntiSpoofing: req.GetAntiSpoofing(),
	}
	var resp faceVerifyResponseBody
	if err := p.postJSON(context.Background(), "/v1/face/verify", body, &resp); err != nil {
		return pb.FaceVerifyResponse{}, err
	}
	// A composite literal here, not a variable of type pb.FaceVerifyResponse,
	// avoids copying the protobuf message's embedded lock on return.
	return pb.FaceVerifyResponse{
		Verified: resp.Verified, Distance: resp.Distance, Threshold: resp.Threshold,
		Confidence: resp.Confidence, Model: resp.Model,
		Img1Area: resp.Img1Area.toProto(), Img2Area: resp.Img2Area.toProto(),
		ProcessingTimeMs:   resp.ProcessingTimeMs,
		Img1IsReal:         boolValue(resp.Img1IsReal),
		Img1AntispoofScore: float32Value(resp.Img1AntispoofScore),
		Img2IsReal:         boolValue(resp.Img2IsReal),
		Img2AntispoofScore: float32Value(resp.Img2AntispoofScore),
	}, nil
}

// boolValue and float32Value read the liveness fields the REST endpoints only
// populate when anti_spoofing was requested; proto keeps them as plain
// bool/float32 (no "not checked" state), so an absent pointer becomes zero.
func boolValue(b *bool) bool {
	return b != nil && *b
}

func float32Value(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

type faceAnalyzeRequestBody struct {
	Model        string   `json:"model"`
	Img          string   `json:"img"`
	Actions      []string `json:"actions,omitempty"`
	AntiSpoofing bool     `json:"anti_spoofing,omitempty"`
}

type faceAnalysisBody struct {
	Region          facialAreaBody     `json:"region"`
	FaceConfidence  float32            `json:"face_confidence"`
	Age             float32            `json:"age,omitempty"`
	DominantGender  string             `json:"dominant_gender,omitempty"`
	Gender          map[string]float32 `json:"gender,omitempty"`
	DominantEmotion string             `json:"dominant_emotion,omitempty"`
	Emotion         map[string]float32 `json:"emotion,omitempty"`
	DominantRace    string             `json:"dominant_race,omitempty"`
	Race            map[string]float32 `json:"race,omitempty"`
	IsReal          *bool              `json:"is_real,omitempty"`
	AntispoofScore  *float32           `json:"antispoof_score,omitempty"`
}

type faceAnalyzeResponseBody struct {
	Faces []faceAnalysisBody `json:"faces"`
}

// FaceAnalyze posts Img, which core already carries as base64.
func (p *LocalAIProxy) FaceAnalyze(req *pb.FaceAnalyzeRequest) (pb.FaceAnalyzeResponse, error) {
	body := faceAnalyzeRequestBody{
		Model: p.model(""), Img: req.GetImg(), Actions: req.GetActions(), AntiSpoofing: req.GetAntiSpoofing(),
	}
	var resp faceAnalyzeResponseBody
	if err := p.postJSON(context.Background(), "/v1/face/analyze", body, &resp); err != nil {
		return pb.FaceAnalyzeResponse{}, err
	}
	var faces []*pb.FaceAnalysis
	for _, f := range resp.Faces {
		faces = append(faces, &pb.FaceAnalysis{
			Region: f.Region.toProto(), FaceConfidence: f.FaceConfidence, Age: f.Age,
			DominantGender: f.DominantGender, Gender: f.Gender,
			DominantEmotion: f.DominantEmotion, Emotion: f.Emotion,
			DominantRace: f.DominantRace, Race: f.Race,
			IsReal: boolValue(f.IsReal), AntispoofScore: float32Value(f.AntispoofScore),
		})
	}
	// A composite literal here, not a variable of type pb.FaceAnalyzeResponse,
	// avoids copying the protobuf message's embedded lock on return.
	return pb.FaceAnalyzeResponse{Faces: faces}, nil
}

// --- Voice (speaker) recognition -----------------------------------------

type voiceVerifyRequestBody struct {
	Model        string  `json:"model"`
	Audio1       string  `json:"audio1"`
	Audio2       string  `json:"audio2"`
	Threshold    float32 `json:"threshold,omitempty"`
	AntiSpoofing bool    `json:"anti_spoofing,omitempty"`
}

type voiceVerifyResponseBody struct {
	Verified         bool    `json:"verified"`
	Distance         float32 `json:"distance"`
	Threshold        float32 `json:"threshold"`
	Confidence       float32 `json:"confidence"`
	Model            string  `json:"model"`
	ProcessingTimeMs float32 `json:"processing_time_ms,omitempty"`
}

// VoiceVerify sends Audio1/Audio2 (staged local paths) as base64, matching
// VoiceVerifyRequest's URL/base64/data-URI contract.
func (p *LocalAIProxy) VoiceVerify(req *pb.VoiceVerifyRequest) (pb.VoiceVerifyResponse, error) {
	audio1, err := fileToBase64(req.GetAudio1())
	if err != nil {
		return pb.VoiceVerifyResponse{}, err
	}
	audio2, err := fileToBase64(req.GetAudio2())
	if err != nil {
		return pb.VoiceVerifyResponse{}, err
	}
	body := voiceVerifyRequestBody{
		Model: p.model(""), Audio1: audio1, Audio2: audio2,
		Threshold: req.GetThreshold(), AntiSpoofing: req.GetAntiSpoofing(),
	}
	var resp voiceVerifyResponseBody
	if err := p.postJSON(context.Background(), "/v1/voice/verify", body, &resp); err != nil {
		return pb.VoiceVerifyResponse{}, err
	}
	return pb.VoiceVerifyResponse{
		Verified: resp.Verified, Distance: resp.Distance, Threshold: resp.Threshold,
		Confidence: resp.Confidence, Model: resp.Model, ProcessingTimeMs: resp.ProcessingTimeMs,
	}, nil
}

type voiceAnalyzeRequestBody struct {
	Model   string   `json:"model"`
	Audio   string   `json:"audio"`
	Actions []string `json:"actions,omitempty"`
}

type voiceAnalysisBody struct {
	Start           float32            `json:"start"`
	End             float32            `json:"end"`
	Age             float32            `json:"age,omitempty"`
	DominantGender  string             `json:"dominant_gender,omitempty"`
	Gender          map[string]float32 `json:"gender,omitempty"`
	DominantEmotion string             `json:"dominant_emotion,omitempty"`
	Emotion         map[string]float32 `json:"emotion,omitempty"`
}

type voiceAnalyzeResponseBody struct {
	Segments []voiceAnalysisBody `json:"segments"`
}

// VoiceAnalyze sends Audio (a staged local path) as base64.
func (p *LocalAIProxy) VoiceAnalyze(req *pb.VoiceAnalyzeRequest) (pb.VoiceAnalyzeResponse, error) {
	audio, err := fileToBase64(req.GetAudio())
	if err != nil {
		return pb.VoiceAnalyzeResponse{}, err
	}
	body := voiceAnalyzeRequestBody{Model: p.model(""), Audio: audio, Actions: req.GetActions()}
	var resp voiceAnalyzeResponseBody
	if err := p.postJSON(context.Background(), "/v1/voice/analyze", body, &resp); err != nil {
		return pb.VoiceAnalyzeResponse{}, err
	}
	var segments []*pb.VoiceAnalysis
	for _, s := range resp.Segments {
		segments = append(segments, &pb.VoiceAnalysis{
			Start: s.Start, End: s.End, Age: s.Age,
			DominantGender: s.DominantGender, Gender: s.Gender,
			DominantEmotion: s.DominantEmotion, Emotion: s.Emotion,
		})
	}
	// A composite literal here, not a variable of type pb.VoiceAnalyzeResponse,
	// avoids copying the protobuf message's embedded lock on return.
	return pb.VoiceAnalyzeResponse{Segments: segments}, nil
}

type voiceEmbedRequestBody struct {
	Model string `json:"model"`
	Audio string `json:"audio"`
}

type voiceEmbedResponseBody struct {
	Embedding []float32 `json:"embedding"`
	Model     string    `json:"model,omitempty"`
}

// VoiceEmbed sends Audio (a staged local path) as base64.
func (p *LocalAIProxy) VoiceEmbed(req *pb.VoiceEmbedRequest) (pb.VoiceEmbedResponse, error) {
	audio, err := fileToBase64(req.GetAudio())
	if err != nil {
		return pb.VoiceEmbedResponse{}, err
	}
	body := voiceEmbedRequestBody{Model: p.model(""), Audio: audio}
	var resp voiceEmbedResponseBody
	if err := p.postJSON(context.Background(), "/v1/voice/embed", body, &resp); err != nil {
		return pb.VoiceEmbedResponse{}, err
	}
	return pb.VoiceEmbedResponse{Embedding: resp.Embedding, Model: resp.Model}, nil
}

// --- Stores ---------------------------------------------------------------

// storeKeysToFloats and storeFloatsToKeys convert between the proto's boxed
// StoresKey/StoresValue slices and the plain [][]float32 / []string the REST
// stores endpoints take, per schema.StoresSet and friends.

func storeKeysToFloats(keys []*pb.StoresKey) [][]float32 {
	out := make([][]float32, len(keys))
	for i, k := range keys {
		out[i] = k.GetFloats()
	}
	return out
}

func storeFloatsToKeys(keys [][]float32) []*pb.StoresKey {
	out := make([]*pb.StoresKey, len(keys))
	for i, k := range keys {
		out[i] = &pb.StoresKey{Floats: k}
	}
	return out
}

func storeValuesToStrings(values []*pb.StoresValue) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v.GetBytes())
	}
	return out
}

func storeStringsToValues(values []string) []*pb.StoresValue {
	out := make([]*pb.StoresValue, len(values))
	for i, v := range values {
		out[i] = &pb.StoresValue{Bytes: []byte(v)}
	}
	return out
}

type storesSetRequestBody struct {
	Store  string      `json:"store,omitempty"`
	Keys   [][]float32 `json:"keys"`
	Values []string    `json:"values"`
}

// StoresSet uses the configured upstream model name as the store name: the
// proxy is loaded per store, the same way it is loaded per model for every
// other method.
func (p *LocalAIProxy) StoresSet(req *pb.StoresSetOptions) error {
	body := storesSetRequestBody{
		Store: p.model(""), Keys: storeKeysToFloats(req.GetKeys()), Values: storeValuesToStrings(req.GetValues()),
	}
	return p.postJSON(context.Background(), "/stores/set", body, nil)
}

type storesDeleteRequestBody struct {
	Store string      `json:"store,omitempty"`
	Keys  [][]float32 `json:"keys"`
}

func (p *LocalAIProxy) StoresDelete(req *pb.StoresDeleteOptions) error {
	body := storesDeleteRequestBody{Store: p.model(""), Keys: storeKeysToFloats(req.GetKeys())}
	return p.postJSON(context.Background(), "/stores/delete", body, nil)
}

type storesGetRequestBody struct {
	Store string      `json:"store,omitempty"`
	Keys  [][]float32 `json:"keys"`
}

type storesGetResponseBody struct {
	Keys   [][]float32 `json:"keys"`
	Values []string    `json:"values"`
}

func (p *LocalAIProxy) StoresGet(req *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	body := storesGetRequestBody{Store: p.model(""), Keys: storeKeysToFloats(req.GetKeys())}
	var resp storesGetResponseBody
	if err := p.postJSON(context.Background(), "/stores/get", body, &resp); err != nil {
		return pb.StoresGetResult{}, err
	}
	return pb.StoresGetResult{Keys: storeFloatsToKeys(resp.Keys), Values: storeStringsToValues(resp.Values)}, nil
}

type storesFindRequestBody struct {
	Store string    `json:"store,omitempty"`
	Key   []float32 `json:"key"`
	Topk  int       `json:"topk,omitempty"`
}

type storesFindResponseBody struct {
	Keys         [][]float32 `json:"keys"`
	Values       []string    `json:"values"`
	Similarities []float32   `json:"similarities"`
}

func (p *LocalAIProxy) StoresFind(req *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	body := storesFindRequestBody{Store: p.model(""), Key: req.GetKey().GetFloats(), Topk: int(req.GetTopK())}
	var resp storesFindResponseBody
	if err := p.postJSON(context.Background(), "/stores/find", body, &resp); err != nil {
		return pb.StoresFindResult{}, err
	}
	return pb.StoresFindResult{
		Keys: storeFloatsToKeys(resp.Keys), Values: storeStringsToValues(resp.Values), Similarities: resp.Similarities,
	}, nil
}

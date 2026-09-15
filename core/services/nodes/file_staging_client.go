package nodes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/storage"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

const stagedInputReleaseTimeout = 30 * time.Second

// FileStagingClient wraps a grpc.Backend to transparently handle file transfer
// for distributed mode. Input files are staged on the backend node before the
// gRPC call. Output files are retrieved from the backend after the call.
//
// Uses the FileStager interface — agnostic to transport (an object store, or
// direct HTTP to the worker), and in both cases reached over the worker's
// tunnel.
// The caller gets a grpc.Backend that behaves identically to a local one —
// no changes needed in core/backend/*.go.
//
// Methods that require no file staging are inherited from the embedded
// grpc.Backend; only methods with staging logic are overridden below.
type FileStagingClient struct {
	grpc.WrappedBackend // pass-through of non-staging methods, plus Unwrap
	stager              FileStager
	nodeID              string

	mu              sync.RWMutex
	remoteModelPath string // set during LoadModel from staged ModelPath
	quantization    map[string]quantizationOutput
	quantStore      quantizationStagingStore
	dataPath        string
}

type quantizationOutput struct {
	generation      string
	frontendDir     string
	dataRelativeDir string
	remoteDir       string
	keyPrefix       string
	inputRequestID  string
	inputKeys       []string
	outputFetched   bool
	outputRelative  string
	inputsReleased  bool
	outputReleased  bool
	cleanupPending  bool
}

type ttsReference struct {
	Audio string `json:"audio"`
	Text  string `json:"text"`
}

func (f *FileStagingClient) stageTTSReferences(ctx context.Context, lifecycle *stagedInputLifecycle, in *pb.TTSRequest) error {
	raw := in.Params["multi_reference_cond"]
	if raw == "" {
		return nil
	}
	var references []ttsReference
	if err := json.Unmarshal([]byte(raw), &references); err != nil {
		return fmt.Errorf("decode TTS references: %w", err)
	}
	for index := range references {
		if !isFilePath(references[index].Audio) {
			continue
		}
		backendPath, err := f.stageInputFile(ctx, lifecycle, references[index].Audio, "inputs")
		if err != nil {
			return fmt.Errorf("staging TTS reference %d: %w", index+1, err)
		}
		references[index].Audio = backendPath
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		return fmt.Errorf("encode TTS references: %w", err)
	}
	in.Params["multi_reference_cond"] = string(encoded)
	return nil
}

func (f *FileStagingClient) stageTTSModel(ctx context.Context, lifecycle *stagedInputLifecycle, in *pb.TTSRequest) error {
	if in.Model == "" || !isFilePath(in.Model) {
		return nil
	}
	translated := f.translateModelPath(in.Model)
	if translated != in.Model {
		in.Model = translated
		return nil
	}
	info, err := os.Stat(in.Model)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	in.Model, err = f.stageInputFile(ctx, lifecycle, in.Model, "inputs")
	if err != nil {
		return fmt.Errorf("staging TTS model: %w", err)
	}
	return nil
}

var _ grpc.BackendUnwrapper = (*FileStagingClient)(nil)

// NewFileStagingClient creates a new file staging wrapper.
func NewFileStagingClient(inner grpc.Backend, stager FileStager, nodeID string) *FileStagingClient {
	return NewFileStagingClientWithOptions(inner, stager, nodeID, FileStagingClientOptions{})
}

type FileStagingClientOptions struct {
	DB       *gorm.DB
	DataPath string
}

func NewFileStagingClientWithOptions(inner grpc.Backend, stager FileStager, nodeID string, options FileStagingClientOptions) *FileStagingClient {
	var store quantizationStagingStore
	if options.DB != nil {
		store = gormQuantizationStagingStore{db: options.DB}
	}
	return &FileStagingClient{
		WrappedBackend: grpc.WrappedBackend{Backend: inner},
		stager:         stager,
		nodeID:         nodeID,
		quantization:   map[string]quantizationOutput{},
		quantStore:     store,
		dataPath:       options.DataPath,
	}
}

// requestID generates a unique ID for ephemeral file keys.
func requestID() string {
	return uuid.NewString()
}

type stagedInputLifecycle struct {
	client    *FileStagingClient
	requestID string
	keys      []string
	seen      map[string]struct{}
}

func (f *FileStagingClient) newStagedInputLifecycle() *stagedInputLifecycle {
	return &stagedInputLifecycle{
		client:    f,
		requestID: requestID(),
		keys:      []string{},
		seen:      map[string]struct{}{},
	}
}

func (l *stagedInputLifecycle) track(key string) {
	if _, ok := l.seen[key]; ok {
		return
	}
	l.seen[key] = struct{}{}
	l.keys = append(l.keys, key)
}

func (l *stagedInputLifecycle) release() error {
	if len(l.keys) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), stagedInputReleaseTimeout)
	defer cancel()
	if releaser, ok := l.client.stager.(RequestFileReleaser); ok {
		if err := releaser.ReleaseRemoteRequest(ctx, l.client.nodeID, l.requestID, l.keys); err != nil {
			xlog.Warn("Failed to release staged request inputs", "node", l.client.nodeID, "requestID", l.requestID, "keyCount", len(l.keys), "error", err)
			return err
		}
		return nil
	}
	var releaseErr error
	for _, key := range l.keys {
		if err := l.client.stager.ReleaseRemote(ctx, l.client.nodeID, key); err != nil {
			xlog.Warn("Failed to release staged input", "node", l.client.nodeID, "key", key, "error", err)
			releaseErr = errors.Join(releaseErr, err)
		}
	}
	return releaseErr
}

// stageInputFile uploads a local file to the remote node via the FileStager.
func (f *FileStagingClient) stageInputFile(
	ctx context.Context,
	lifecycle *stagedInputLifecycle,
	localPath,
	category string,
) (string, error) {
	basename := filepath.Base(localPath)
	key := storage.EphemeralKey(lifecycle.requestID, category, basename)
	lifecycle.track(key)

	remotePath, err := f.stager.EnsureRemote(ctx, f.nodeID, localPath, key)
	if err != nil {
		return "", fmt.Errorf("staging input file: %w", err)
	}

	return remotePath, nil
}

// retrieveOutputFile retrieves an output file from the backend to a local path.
func (f *FileStagingClient) retrieveOutputFile(ctx context.Context, backendPath, frontendDst string) error {
	return f.stager.FetchRemote(ctx, f.nodeID, backendPath, frontendDst)
}

// --- grpc.Backend overrides (methods with file staging logic) ---

func (f *FileStagingClient) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	// Capture the remote ModelPath so TTS/TTSStream can translate model file paths.
	// By the time LoadModel is called, stageModelFiles has already rewritten ModelPath
	// to the worker's absolute path (e.g. "/models/voice-it-paola-medium").
	if in.ModelPath != "" {
		f.mu.Lock()
		f.remoteModelPath = in.ModelPath
		f.mu.Unlock()
	}
	return f.Backend.LoadModel(ctx, in, opts...)
}

// translateModelPath converts a frontend model file path to the remote worker path.
// The frontend constructs paths like filepath.Join(loader.ModelPath, model) = "/models/model.onnx",
// but on the worker the file is under the staging namespace (e.g. "/models/tracking-key/model.onnx").
// Returns the original path unchanged if no translation is possible.
func (f *FileStagingClient) translateModelPath(frontendPath string) string {
	f.mu.RLock()
	rmp := f.remoteModelPath
	f.mu.RUnlock()
	if rmp == "" || frontendPath == "" {
		return frontendPath
	}
	// Use the basename of the frontend path joined with the remote ModelPath
	return filepath.Join(rmp, filepath.Base(frontendPath))
}

func (f *FileStagingClient) Predict(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.Reply, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.PredictOptions)
	var err error
	in, err = f.stageMultimodalInputs(ctx, lifecycle, in)
	if err != nil {
		return nil, err
	}
	return f.Backend.Predict(ctx, in, opts...)
}

func (f *FileStagingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, fn func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.PredictOptions)
	var err error
	in, err = f.stageMultimodalInputs(ctx, lifecycle, in)
	if err != nil {
		return err
	}
	return f.Backend.PredictStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.GenerateImageRequest)

	// Stage input source image if present
	if in.Src != "" && isFilePath(in.Src) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging image src: %w", err)
		}
		in.Src = backendPath
	}

	// Stage reference images
	for i, img := range in.RefImages {
		if isFilePath(img) {
			backendPath, err := f.stageInputFile(ctx, lifecycle, img, "inputs")
			if err != nil {
				return nil, fmt.Errorf("staging ref image: %w", err)
			}
			in.RefImages[i] = backendPath
		}
	}

	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for image output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.Backend.GenerateImage(ctx, in, opts...)
	if err != nil {
		return result, err
	}

	// Retrieve output
	if frontendDst != "" && in.Dst != frontendDst {
		if err := f.retrieveOutputFile(ctx, in.Dst, frontendDst); err != nil {
			xlog.Warn("Failed to retrieve generated image", "error", err)
		}
	}

	return result, nil
}

func (f *FileStagingClient) UpscaleImage(ctx context.Context, in *pb.UpscaleImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.UpscaleImageRequest)
	if in.Src != "" && isFilePath(in.Src) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging upscale source: %w", err)
		}
		in.Src = remote
	}
	frontendDst := in.Dst
	if frontendDst != "" {
		remote, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating upscale output: %w", err)
		}
		in.Dst = remote
	}
	result, err := f.Backend.UpscaleImage(ctx, in, opts...)
	if err == nil && result != nil && result.Success && frontendDst != "" {
		if fetchErr := f.retrieveOutputFile(ctx, in.Dst, frontendDst); fetchErr != nil {
			return result, fmt.Errorf("retrieving upscale output: %w", fetchErr)
		}
	}
	return result, err
}

func (f *FileStagingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.GenerateVideoRequest)

	// Stage start/end images and optional audio conditioning.
	if in.StartImage != "" && isFilePath(in.StartImage) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.StartImage, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging start image: %w", err)
		}
		in.StartImage = backendPath
	}
	if in.EndImage != "" && isFilePath(in.EndImage) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.EndImage, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging end image: %w", err)
		}
		in.EndImage = backendPath
	}
	if in.Audio != "" && isFilePath(in.Audio) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Audio, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging video audio: %w", err)
		}
		in.Audio = backendPath
	}

	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for video output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.Backend.GenerateVideo(ctx, in, opts...)
	if err != nil {
		return result, err
	}

	if frontendDst != "" && in.Dst != frontendDst {
		if err := f.retrieveOutputFile(ctx, in.Dst, frontendDst); err != nil {
			xlog.Warn("Failed to retrieve generated video", "error", err)
		}
	}

	return result, nil
}

func (f *FileStagingClient) Generate3D(ctx context.Context, in *pb.Generate3DRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.Generate3DRequest)

	// Stage the conditioning image or existing GLB used by 3D post-processing.
	if in.Src != "" && isFilePath(in.Src) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging 3D input asset: %w", err)
		}
		in.Src = backendPath
	}

	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for 3D output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.Backend.Generate3D(ctx, in, opts...)
	if err != nil {
		return result, err
	}

	if frontendDst != "" && in.Dst != frontendDst {
		if err := f.retrieveOutputFile(ctx, in.Dst, frontendDst); err != nil {
			return result, fmt.Errorf("retrieving generated 3D asset: %w", err)
		}
	}

	return result, nil
}

func (f *FileStagingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.TTSRequest)

	// Translate model path from frontend to remote worker path.
	// The model and its companion files (e.g. .onnx.json) were already staged
	// during LoadModel, so we just need to point to the correct remote location.
	if err := f.stageTTSModel(ctx, lifecycle, in); err != nil {
		return nil, err
	}
	// Voice may be a named backend speaker or a request-scoped reference WAV.
	// Only path-shaped values are staged; speaker IDs pass through unchanged.
	if in.Voice != "" && isFilePath(in.Voice) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Voice, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging TTS voice reference: %w", err)
		}
		in.Voice = backendPath
	}
	if err := f.stageTTSReferences(ctx, lifecycle, in); err != nil {
		return nil, err
	}

	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for TTS output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.Backend.TTS(ctx, in, opts...)
	if err != nil {
		return result, err
	}

	if frontendDst != "" && in.Dst != frontendDst {
		if err := f.retrieveOutputFile(ctx, in.Dst, frontendDst); err != nil {
			xlog.Warn("Failed to retrieve TTS output", "error", err)
		}
	}

	return result, nil
}

func (f *FileStagingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, fn func(*pb.Reply), opts ...ggrpc.CallOption) error {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.TTSRequest)

	// Translate model path from frontend to remote worker path (same as TTS above)
	if err := f.stageTTSModel(ctx, lifecycle, in); err != nil {
		return err
	}
	if in.Voice != "" && isFilePath(in.Voice) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Voice, "inputs")
		if err != nil {
			return fmt.Errorf("staging streaming TTS voice reference: %w", err)
		}
		in.Voice = backendPath
	}
	if err := f.stageTTSReferences(ctx, lifecycle, in); err != nil {
		return err
	}

	return f.Backend.TTSStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.SoundGenerationRequest)

	// Stage input source
	if in.Src != nil && *in.Src != "" && isFilePath(*in.Src) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, *in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging sound src: %w", err)
		}
		in.Src = &backendPath
	}

	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for sound output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.Backend.SoundGeneration(ctx, in, opts...)
	if err != nil {
		return result, err
	}

	if frontendDst != "" && in.Dst != frontendDst {
		if err := f.retrieveOutputFile(ctx, in.Dst, frontendDst); err != nil {
			xlog.Warn("Failed to retrieve sound output", "error", err)
		}
	}

	return result, nil
}

func (f *FileStagingClient) SoundDetection(ctx context.Context, in *pb.SoundDetectionRequest, opts ...ggrpc.CallOption) (*pb.SoundDetectionResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.SoundDetectionRequest)
	if in.Src != "" && isFilePath(in.Src) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging audio for sound detection: %w", err)
		}
		in.Src = backendPath
	}
	return f.Backend.SoundDetection(ctx, in, opts...)
}

func (f *FileStagingClient) Diarize(ctx context.Context, in *pb.DiarizeRequest, opts ...ggrpc.CallOption) (*pb.DiarizeResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.DiarizeRequest)
	if in.Dst != "" && isFilePath(in.Dst) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Dst, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging diarization audio: %w", err)
		}
		in.Dst = remote
	}
	return f.Backend.Diarize(ctx, in, opts...)
}

func (f *FileStagingClient) VoiceVerify(ctx context.Context, in *pb.VoiceVerifyRequest, opts ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.VoiceVerifyRequest)
	var err error
	if in.Audio1 != "" && isFilePath(in.Audio1) {
		in.Audio1, err = f.stageInputFile(ctx, lifecycle, in.Audio1, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging first voice sample: %w", err)
		}
	}
	if in.Audio2 != "" && isFilePath(in.Audio2) {
		in.Audio2, err = f.stageInputFile(ctx, lifecycle, in.Audio2, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging second voice sample: %w", err)
		}
	}
	return f.Backend.VoiceVerify(ctx, in, opts...)
}

func (f *FileStagingClient) VoiceAnalyze(ctx context.Context, in *pb.VoiceAnalyzeRequest, opts ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.VoiceAnalyzeRequest)
	if in.Audio != "" && isFilePath(in.Audio) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Audio, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging voice analysis audio: %w", err)
		}
		in.Audio = remote
	}
	return f.Backend.VoiceAnalyze(ctx, in, opts...)
}

func (f *FileStagingClient) VoiceEmbed(ctx context.Context, in *pb.VoiceEmbedRequest, opts ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.VoiceEmbedRequest)
	if in.Audio != "" && isFilePath(in.Audio) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Audio, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging voice embedding audio: %w", err)
		}
		in.Audio = remote
	}
	return f.Backend.VoiceEmbed(ctx, in, opts...)
}

func (f *FileStagingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.DetectOptions)
	if in.Src != "" && isFilePath(in.Src) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging detection image: %w", err)
		}
		in.Src = remote
	}
	return f.Backend.Detect(ctx, in, opts...)
}

func (f *FileStagingClient) Depth(ctx context.Context, in *pb.DepthRequest, opts ...ggrpc.CallOption) (*pb.DepthResponse, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.DepthRequest)
	if in.Src != "" && isFilePath(in.Src) {
		remote, err := f.stageInputFile(ctx, lifecycle, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging depth image: %w", err)
		}
		in.Src = remote
	}
	frontendDst := in.Dst
	remoteDir := ""
	keyPrefix := ""
	if frontendDst != "" {
		allocator, ok := f.stager.(RemoteDirectoryAllocator)
		if !ok {
			return nil, fmt.Errorf("depth exports require remote directory allocation")
		}
		keyPrefix = storage.DataKey(path.Join("depth", requestID()))
		var err error
		remoteDir, err = allocator.AllocRemoteDir(ctx, f.nodeID, keyPrefix)
		if err != nil {
			return nil, fmt.Errorf("allocating depth output directory: %w", err)
		}
		in.Dst = remoteDir
		defer func() { _ = f.releaseRemoteDir(keyPrefix) }()
	}
	result, err := f.Backend.Depth(ctx, in, opts...)
	if err != nil || result == nil || frontendDst == "" {
		return result, err
	}
	if err := os.MkdirAll(frontendDst, 0750); err != nil {
		return nil, fmt.Errorf("creating depth output directory: %w", err)
	}
	for i, remotePath := range result.ExportPaths {
		rel, relErr := safeBackendOutputRelative(remoteDir, remotePath)
		if relErr != nil {
			return nil, fmt.Errorf("depth export is outside its allocated remote directory")
		}
		local := filepath.Join(frontendDst, rel)
		if err := validatePathInDir(local, frontendDst); err != nil {
			return nil, fmt.Errorf("depth export destination is unsafe: %w", err)
		}
		remoteKey := path.Join(keyPrefix, filepath.ToSlash(rel))
		if fetchErr := f.stager.FetchRemoteByKey(ctx, f.nodeID, remoteKey, local); fetchErr != nil {
			return nil, fmt.Errorf("retrieving depth export: %w", fetchErr)
		}
		result.ExportPaths[i] = local
	}
	return result, nil
}

func (f *FileStagingClient) AudioTransform(ctx context.Context, in *pb.AudioTransformRequest, opts ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.AudioTransformRequest)
	var err error
	if in.AudioPath != "" && isFilePath(in.AudioPath) {
		in.AudioPath, err = f.stageInputFile(ctx, lifecycle, in.AudioPath, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging audio transform input: %w", err)
		}
	}
	if in.ReferencePath != "" && isFilePath(in.ReferencePath) {
		in.ReferencePath, err = f.stageInputFile(ctx, lifecycle, in.ReferencePath, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging audio transform reference: %w", err)
		}
	}
	frontendDst := in.Dst
	if frontendDst != "" {
		in.Dst, err = f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating audio transform output: %w", err)
		}
	}
	result, err := f.Backend.AudioTransform(ctx, in, opts...)
	if err != nil || result == nil || frontendDst == "" {
		return result, err
	}
	localRoot := filepath.Dir(frontendDst)
	if err := validateLocalOutputPath(frontendDst, localRoot); err != nil {
		return nil, fmt.Errorf("audio transform destination is unsafe: %w", err)
	}
	if fetchErr := f.retrieveOutputFile(ctx, in.Dst, frontendDst); fetchErr != nil {
		return nil, fmt.Errorf("retrieving audio transform output: %w", fetchErr)
	}
	result.Dst = frontendDst
	remoteRoot := filepath.Dir(in.Dst)
	for _, stem := range result.Stems {
		if stem == nil {
			return nil, fmt.Errorf("audio transform returned a nil stem")
		}
		rel, relErr := safeBackendOutputRelative(remoteRoot, stem.Dst)
		if relErr != nil {
			return nil, fmt.Errorf("audio transform stem is outside its allocated remote directory")
		}
		local := filepath.Join(localRoot, rel)
		if err := validateLocalOutputPath(local, localRoot); err != nil {
			return nil, fmt.Errorf("audio transform stem destination is unsafe: %w", err)
		}
		if fetchErr := f.retrieveOutputFile(ctx, stem.Dst, local); fetchErr != nil {
			return nil, fmt.Errorf("retrieving audio transform stem: %w", fetchErr)
		}
		stem.Dst = local
	}
	return result, nil
}

func safeBackendOutputRelative(root, output string) (string, error) {
	rel, err := filepath.Rel(root, output)
	if err != nil {
		return "", err
	}
	return safeRemoteRelativePath(filepath.ToSlash(rel))
}

func validateLocalOutputPath(target, root string) error {
	if _, err := os.Lstat(root); err == nil {
		return validatePathInDir(target, root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output path is outside its destination directory")
	}
	return nil
}

func (f *FileStagingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.TranscriptRequest)

	// Stage input audio file
	if in.Dst != "" && isFilePath(in.Dst) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Dst, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging audio for transcription: %w", err)
		}
		in.Dst = backendPath
	}

	return f.Backend.AudioTranscription(ctx, in, opts...)
}

func (f *FileStagingClient) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, fn func(chunk *pb.TranscriptStreamResponse), opts ...ggrpc.CallOption) error {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.TranscriptRequest)

	// Stage input audio file
	if in.Dst != "" && isFilePath(in.Dst) {
		backendPath, err := f.stageInputFile(ctx, lifecycle, in.Dst, "inputs")
		if err != nil {
			return fmt.Errorf("staging audio for transcription stream: %w", err)
		}
		in.Dst = backendPath
	}

	return f.Backend.AudioTranscriptionStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	lifecycle := f.newStagedInputLifecycle()
	defer func() { _ = lifecycle.release() }()
	in = proto.Clone(in).(*pb.ExportModelRequest)
	var err error
	if in.CheckpointPath != "" && isFilePath(in.CheckpointPath) {
		in.CheckpointPath, err = f.stageInputFile(ctx, lifecycle, in.CheckpointPath, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging export checkpoint: %w", err)
		}
	}
	if in.Model != "" && isFilePath(in.Model) {
		in.Model, err = f.stageInputFile(ctx, lifecycle, in.Model, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging export model: %w", err)
		}
	}
	frontendOutputPath := in.OutputPath
	var exportKeyPrefix string
	if frontendOutputPath != "" {
		if err := os.MkdirAll(frontendOutputPath, 0750); err != nil {
			return nil, fmt.Errorf("creating export output directory: %w", err)
		}
		allocator, ok := f.stager.(RemoteDirectoryAllocator)
		if !ok {
			return nil, fmt.Errorf("exporting a directory requires remote directory allocation")
		}
		exportKeyPrefix = storage.ModelKey(path.Join("exports", filepath.Base(frontendOutputPath), requestID()))
		remoteOutputPath, allocErr := allocator.AllocRemoteDir(ctx, f.nodeID, exportKeyPrefix)
		if allocErr != nil {
			return nil, fmt.Errorf("allocating remote export directory: %w", allocErr)
		}
		in.OutputPath = remoteOutputPath
		defer func() { _ = f.releaseRemoteDir(exportKeyPrefix) }()
	}

	result, err := f.Backend.ExportModel(ctx, in, opts...)
	if err != nil {
		return result, err
	}
	if !result.Success {
		return result, nil
	}

	// Fetch exported files from the worker back to the frontend
	if frontendOutputPath != "" {
		files, err := f.stager.ListRemoteDir(ctx, f.nodeID, exportKeyPrefix)
		if err != nil {
			return &pb.Result{Success: false, Message: fmt.Sprintf("listing remote export dir: %v", err)}, nil
		}
		if len(files) == 0 {
			return &pb.Result{Success: false, Message: "export produced no files on worker"}, nil
		}

		for _, relPath := range files {
			cleanRel, cleanErr := safeRemoteRelativePath(relPath)
			if cleanErr != nil {
				return &pb.Result{Success: false, Message: fmt.Sprintf("invalid export file %q: %v", relPath, cleanErr)}, nil
			}
			key := exportKeyPrefix + "/" + filepath.ToSlash(cleanRel)
			localDst := filepath.Join(frontendOutputPath, cleanRel)
			if err := validatePathInDir(localDst, frontendOutputPath); err != nil {
				return &pb.Result{Success: false, Message: fmt.Sprintf("invalid export destination %q: %v", relPath, err)}, nil
			}
			if err := os.MkdirAll(filepath.Dir(localDst), 0750); err != nil {
				return &pb.Result{Success: false, Message: fmt.Sprintf("creating export destination for %s: %v", relPath, err)}, nil
			}

			if err := f.stager.FetchRemoteByKey(ctx, f.nodeID, key, localDst); err != nil {
				return &pb.Result{Success: false, Message: fmt.Sprintf("fetching export file %s: %v", relPath, err)}, nil
			}
			xlog.Debug("Retrieved export file from worker", "file", relPath, "localDst", localDst)
		}
	}

	return result, nil
}

func safeRemoteRelativePath(value string) (string, error) {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) {
		return "", fmt.Errorf("path must be a non-empty portable relative path")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != value {
		return "", fmt.Errorf("path contains traversal or non-canonical separators")
	}
	return filepath.FromSlash(cleaned), nil
}

func (f *FileStagingClient) StartQuantization(ctx context.Context, in *pb.QuantizationRequest, opts ...ggrpc.CallOption) (*pb.QuantizationJobResult, error) {
	in = proto.Clone(in).(*pb.QuantizationRequest)
	if previous, ok, err := f.lookupQuantization(ctx, in.JobId); err != nil {
		return nil, fmt.Errorf("checking quantization staging state: %w", err)
	} else if ok {
		if !previous.cleanupPending {
			return nil, ErrQuantizationStagingExists
		}
		f.cleanupQuantization(in.JobId, previous)
		if _, remains, lookupErr := f.lookupQuantization(ctx, in.JobId); lookupErr != nil {
			return nil, fmt.Errorf("checking failed-start cleanup: %w", lookupErr)
		} else if remains {
			return nil, fmt.Errorf("retrying failed-start cleanup: %w", ErrQuantizationStagingExists)
		}
	}
	frontendOutputDir := in.OutputDir
	dataRelativeDir := ""
	if f.quantStore != nil {
		if f.dataPath == "" {
			return nil, fmt.Errorf("durable quantization staging requires a local data path")
		}
		if frontendOutputDir != "" {
			if err := validatePathInDir(frontendOutputDir, f.dataPath); err != nil {
				return nil, fmt.Errorf("quantization output directory must be within the local data path: %w", err)
			}
			var err error
			dataRelativeDir, err = filepath.Rel(f.dataPath, frontendOutputDir)
			if err != nil || dataRelativeDir == "." || filepath.IsAbs(dataRelativeDir) || dataRelativeDir == ".." || strings.HasPrefix(dataRelativeDir, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("quantization output directory must be a child of the local data path")
			}
		}
	}
	lifecycle := f.newStagedInputLifecycle()
	keepInputs := false
	defer func() {
		if !keepInputs {
			_ = lifecycle.release()
		}
	}()
	if in.Model != "" && isFilePath(in.Model) {
		remoteModel, err := f.stageInputFile(ctx, lifecycle, in.Model, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging quantization model: %w", err)
		}
		in.Model = remoteModel
	}
	output := quantizationOutput{
		generation:      requestID(),
		inputRequestID:  lifecycle.requestID,
		inputKeys:       append([]string(nil), lifecycle.keys...),
		dataRelativeDir: dataRelativeDir,
	}
	if frontendOutputDir != "" {
		if err := os.MkdirAll(frontendOutputDir, 0750); err != nil {
			return nil, fmt.Errorf("creating quantization output directory: %w", err)
		}
		allocator, ok := f.stager.(RemoteDirectoryAllocator)
		if !ok {
			return nil, fmt.Errorf("quantizing into a directory requires remote directory allocation")
		}
		keyPrefix := storage.DataKey(path.Join("quantization", in.JobId, requestID()))
		remoteOutputDir, err := allocator.AllocRemoteDir(ctx, f.nodeID, keyPrefix)
		if err != nil {
			return nil, fmt.Errorf("allocating remote quantization directory: %w", err)
		}
		in.OutputDir = remoteOutputDir
		output = quantizationOutput{
			frontendDir: frontendOutputDir, remoteDir: remoteOutputDir, keyPrefix: keyPrefix,
			generation: output.generation, dataRelativeDir: dataRelativeDir, inputRequestID: output.inputRequestID, inputKeys: output.inputKeys,
		}
		if f.quantStore == nil && f.dataPath != "" {
			if rel, relErr := filepath.Rel(f.dataPath, frontendOutputDir); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				output.dataRelativeDir = rel
			}
		}
	}
	// Quantization is asynchronous. Persist the staged input lifecycle even when
	// the caller did not request an output directory, so terminal progress (or a
	// stop request) can release the model after the worker is finished with it.
	if err := f.rememberQuantization(ctx, in.JobId, output); err != nil {
		_ = f.releaseRemoteDir(output.keyPrefix)
		return nil, fmt.Errorf("persisting quantization staging state: %w", err)
	}
	// From this point the durable record owns staged resources, including on a
	// backend start failure. Cleanup is phase-aware and retryable.
	keepInputs = true
	result, err := f.Backend.StartQuantization(ctx, in, opts...)
	if err != nil || result == nil || !result.Success {
		output.cleanupPending = true
		if updateErr := f.updateQuantization(context.Background(), in.JobId, output); updateErr != nil {
			xlog.Warn("Failed to persist failed-start cleanup state", "jobID", in.JobId, "error", updateErr)
		}
		f.cleanupQuantization(in.JobId, output)
		return result, err
	}
	return result, err
}

func (f *FileStagingClient) QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, fn func(update *pb.QuantizationProgressUpdate), opts ...ggrpc.CallOption) error {
	return f.Backend.QuantizationProgress(ctx, in, func(update *pb.QuantizationProgressUpdate) {
		update = proto.Clone(update).(*pb.QuantizationProgressUpdate)
		terminal := update.Status == "completed" || update.Status == "failed" || update.Status == "stopped" || update.Status == "cancelled" || update.Status == "canceled"
		output, ok, lookupErr := f.lookupQuantization(ctx, in.JobId)
		if lookupErr != nil {
			update.Status = "failed"
			update.Message = "retrieving quantization staging state: " + lookupErr.Error()
			update.OutputFile = ""
			fn(update)
			return
		}
		// When quantization completes, fetch the output file from the worker.
		// Use a fresh context because quantization can take hours and the
		// original request context may have expired by the time this fires.
		if output.outputFetched && update.Status == "completed" {
			frontendDir := output.frontendDir
			if f.dataPath != "" && output.dataRelativeDir != "" {
				frontendDir = filepath.Join(f.dataPath, output.dataRelativeDir)
			}
			update.OutputFile = filepath.Join(frontendDir, filepath.FromSlash(output.outputRelative))
		} else if update.OutputFile != "" && update.Status == "completed" {
			if !ok {
				update.Status = "failed"
				update.Message = "quantization output staging state is unavailable"
				update.OutputFile = ""
				fn(update)
				return
			}
			relPath, relErr := filepath.Rel(output.remoteDir, update.OutputFile)
			if relErr != nil || relPath == "." || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
				update.Status = "failed"
				update.Message = "quantization output is outside its allocated remote directory"
				update.OutputFile = ""
				fn(update)
				return
			}
			key := path.Join(output.keyPrefix, filepath.ToSlash(relPath))
			frontendDir := output.frontendDir
			if f.dataPath != "" && output.dataRelativeDir != "" {
				frontendDir = filepath.Join(f.dataPath, output.dataRelativeDir)
			}
			localPath := filepath.Join(frontendDir, relPath)
			if f.dataPath != "" {
				if err := validatePathInDir(frontendDir, f.dataPath); err != nil {
					update.Status = "failed"
					update.Message = "quantization output directory is outside the local data path: " + err.Error()
					update.OutputFile = ""
					fn(update)
					return
				}
				if err := validatePathInDir(localPath, f.dataPath); err != nil {
					update.Status = "failed"
					update.Message = "quantization output path is outside the local data path: " + err.Error()
					update.OutputFile = ""
					fn(update)
					return
				}
			}
			fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer fetchCancel()
			if err := f.stager.FetchRemoteByKey(fetchCtx, f.nodeID, key, localPath); err != nil {
				xlog.Warn("Failed to retrieve quantization output", "file", update.OutputFile, "error", err)
				update.Status = "failed"
				update.Message = "retrieving quantization output: " + err.Error()
				update.OutputFile = ""
				fn(update)
				return
			} else {
				update.OutputFile = localPath
				output.outputFetched = true
				output.outputRelative = filepath.ToSlash(relPath)
				if err := f.updateQuantization(context.Background(), in.JobId, output); err != nil {
					update.Status = "failed"
					update.Message = "persisting quantization output state: " + err.Error()
					update.OutputFile = ""
					fn(update)
					return
				}
			}
		}
		fn(update)
		if terminal && ok {
			f.cleanupQuantization(in.JobId, output)
		}
	}, opts...)
}

func (f *FileStagingClient) StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	result, err := f.Backend.StopQuantization(ctx, in, opts...)
	if err == nil && result != nil && result.Success {
		if output, ok, lookupErr := f.lookupQuantization(ctx, in.JobId); lookupErr == nil && ok {
			f.cleanupQuantization(in.JobId, output)
		}
	}
	return result, err
}

func (f *FileStagingClient) rememberQuantization(ctx context.Context, jobID string, output quantizationOutput) error {
	if f.quantStore != nil {
		frontendDir := output.frontendDir
		if f.dataPath != "" {
			// Replica-local absolute paths are neither portable nor needed in the
			// shared store. Every replica rebuilds this from its own DataPath.
			frontendDir = ""
		}
		return f.quantStore.Create(ctx, &QuantizationStagingRecord{
			NodeID: f.nodeID, JobID: jobID, Generation: output.generation, FrontendDir: frontendDir,
			DataRelativeDir: output.dataRelativeDir, RemoteDir: output.remoteDir,
			KeyPrefix: output.keyPrefix, InputRequestID: output.inputRequestID, InputKeys: output.inputKeys,
			OutputFetched: output.outputFetched, OutputRelative: output.outputRelative,
			InputsReleased: output.inputsReleased, OutputReleased: output.outputReleased, CleanupPending: output.cleanupPending,
		})
	}
	f.mu.Lock()
	if _, exists := f.quantization[jobID]; exists {
		f.mu.Unlock()
		return ErrQuantizationStagingExists
	}
	f.quantization[jobID] = output
	f.mu.Unlock()
	return nil
}

func (f *FileStagingClient) lookupQuantization(ctx context.Context, jobID string) (quantizationOutput, bool, error) {
	if f.quantStore != nil {
		record, ok, err := f.quantStore.Get(ctx, f.nodeID, jobID)
		if err != nil || !ok {
			return quantizationOutput{}, ok, err
		}
		return quantizationOutput{generation: record.Generation, frontendDir: record.FrontendDir, dataRelativeDir: record.DataRelativeDir, remoteDir: record.RemoteDir, keyPrefix: record.KeyPrefix, inputRequestID: record.InputRequestID, inputKeys: record.InputKeys, outputFetched: record.OutputFetched, outputRelative: record.OutputRelative, inputsReleased: record.InputsReleased, outputReleased: record.OutputReleased, cleanupPending: record.CleanupPending}, true, nil
	}
	f.mu.RLock()
	output, ok := f.quantization[jobID]
	f.mu.RUnlock()
	return output, ok, nil
}

func (f *FileStagingClient) updateQuantization(ctx context.Context, jobID string, output quantizationOutput) error {
	if f.quantStore != nil {
		return f.quantStore.Update(ctx, &QuantizationStagingRecord{
			NodeID: f.nodeID, JobID: jobID, Generation: output.generation, DataRelativeDir: output.dataRelativeDir,
			RemoteDir: output.remoteDir, KeyPrefix: output.keyPrefix, InputRequestID: output.inputRequestID,
			InputKeys: output.inputKeys, OutputFetched: output.outputFetched, OutputRelative: output.outputRelative,
			InputsReleased: output.inputsReleased, OutputReleased: output.outputReleased, CleanupPending: output.cleanupPending,
		})
	}
	f.mu.Lock()
	if current, ok := f.quantization[jobID]; !ok || current.generation != output.generation {
		f.mu.Unlock()
		return ErrQuantizationStagingOwnership
	}
	f.quantization[jobID] = output
	f.mu.Unlock()
	return nil
}

func (f *FileStagingClient) discardQuantization(jobID string, output quantizationOutput) {
	if f.quantStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stagedInputReleaseTimeout)
		defer cancel()
		if err := f.quantStore.Delete(ctx, f.nodeID, jobID, output.generation); err != nil {
			xlog.Warn("Failed to delete quantization staging state", "jobID", jobID, "error", err)
		}
		return
	}
	f.mu.Lock()
	if current, ok := f.quantization[jobID]; ok && current.generation == output.generation {
		delete(f.quantization, jobID)
	}
	f.mu.Unlock()
}

func (f *FileStagingClient) cleanupQuantization(jobID string, output quantizationOutput) {
	if !output.inputsReleased {
		if output.inputRequestID != "" && len(output.inputKeys) != 0 {
			if err := (&stagedInputLifecycle{client: f, requestID: output.inputRequestID, keys: output.inputKeys}).release(); err != nil {
				return
			}
		}
		output.inputsReleased = true
		if err := f.updateQuantization(context.Background(), jobID, output); err != nil {
			xlog.Warn("Failed to persist quantization input cleanup", "jobID", jobID, "error", err)
			return
		}
	}
	if !output.outputReleased && output.keyPrefix != "" {
		if err := f.releaseRemoteDir(output.keyPrefix); err != nil {
			return
		}
		output.outputReleased = true
		if err := f.updateQuantization(context.Background(), jobID, output); err != nil {
			xlog.Warn("Failed to persist quantization output cleanup", "jobID", jobID, "error", err)
			return
		}
	}
	f.discardQuantization(jobID, output)
}

func (f *FileStagingClient) releaseRemoteDir(keyPrefix string) error {
	releaser, ok := f.stager.(RemoteDirectoryReleaser)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stagedInputReleaseTimeout)
	defer cancel()
	if err := releaser.ReleaseRemoteDir(ctx, f.nodeID, keyPrefix); err != nil {
		if errors.Is(err, ErrWorkerControlUnsupported) {
			xlog.Debug("Worker does not support remote directory cleanup", "node", f.nodeID, "keyPrefix", keyPrefix)
			return nil
		}
		xlog.Warn("Failed to release remote output directory", "node", f.nodeID, "keyPrefix", keyPrefix, "error", err)
		return err
	}
	return nil
}

// --- helpers ---

// stageMultimodalInputs stages Images, Videos, Audios fields in PredictOptions
// if they are file paths (not base64 or URLs).
func (f *FileStagingClient) stageMultimodalInputs(
	ctx context.Context,
	lifecycle *stagedInputLifecycle,
	in *pb.PredictOptions,
) (*pb.PredictOptions, error) {
	var err error
	in.Images, err = f.stagePathSlice(ctx, lifecycle, in.Images, "inputs")
	if err != nil {
		return nil, fmt.Errorf("staging predict images: %w", err)
	}
	in.Videos, err = f.stagePathSlice(ctx, lifecycle, in.Videos, "inputs")
	if err != nil {
		return nil, fmt.Errorf("staging predict videos: %w", err)
	}
	in.Audios, err = f.stagePathSlice(ctx, lifecycle, in.Audios, "inputs")
	if err != nil {
		return nil, fmt.Errorf("staging predict audios: %w", err)
	}
	return in, nil
}

func (f *FileStagingClient) stagePathSlice(
	ctx context.Context,
	lifecycle *stagedInputLifecycle,
	paths []string,
	category string,
) ([]string, error) {
	result := make([]string, len(paths))
	for i, p := range paths {
		if isFilePath(p) {
			backendPath, err := f.stageInputFile(ctx, lifecycle, p, category)
			if err != nil {
				return nil, fmt.Errorf("staging %q: %w", p, err)
			}
			result[i] = backendPath
		} else {
			result[i] = p
		}
	}
	return result, nil
}

// isFilePath checks if a string looks like a local file path (not base64 or URL).
func isFilePath(s string) bool {
	if s == "" {
		return false
	}
	// Base64 data URIs
	if strings.HasPrefix(s, "data:") {
		return false
	}
	// URLs
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Raw JPEG base64 begins with /9j because every JPEG starts with the
	// FF D8 FF marker. Do not mistake that leading slash for an absolute path.
	if isRawJPEGBase64(s) {
		return false
	}
	// Starts with / (absolute path) or contains path separator
	return s[0] == '/' || filepath.IsAbs(s)
}

func isRawJPEGBase64(s string) bool {
	if len(s) < 4 {
		return false
	}
	prefix, err := base64.StdEncoding.DecodeString(s[:4])
	if err != nil || len(prefix) != 3 || prefix[0] != 0xff || prefix[1] != 0xd8 || prefix[2] != 0xff {
		return false
	}
	_, err = io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(s)))
	return err == nil
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

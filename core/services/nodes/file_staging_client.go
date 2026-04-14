package nodes

import (
	"context"
	"fmt"
	"io"
	"os"
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
)

// FileStagingClient wraps a grpc.Backend to transparently handle file transfer
// for distributed mode. Input files are staged on the backend node before the
// gRPC call. Output files are retrieved from the backend after the call.
//
// Uses the FileStager interface — agnostic to transport (S3+NATS or gRPC).
// The caller gets a grpc.Backend that behaves identically to a local one —
// no changes needed in core/backend/*.go.
//
// Methods that require no file staging are inherited from the embedded
// grpc.Backend; only methods with staging logic are overridden below.
type FileStagingClient struct {
	grpc.Backend // embedded for pass-through of non-staging methods
	stager       FileStager
	nodeID       string

	mu              sync.RWMutex
	remoteModelPath string // set during LoadModel from staged ModelPath
}

// NewFileStagingClient creates a new file staging wrapper.
func NewFileStagingClient(inner grpc.Backend, stager FileStager, nodeID string) *FileStagingClient {
	return &FileStagingClient{
		Backend: inner,
		stager:  stager,
		nodeID:  nodeID,
	}
}

// requestID generates a unique ID for ephemeral file keys.
func requestID() string {
	return uuid.New().String()[:8]
}

// stageInputFile uploads a local file to the remote node via the FileStager.
// Returns the remote-local path and the ephemeral key.
func (f *FileStagingClient) stageInputFile(ctx context.Context, reqID, localPath, category string) (string, string, error) {
	basename := filepath.Base(localPath)
	key := storage.EphemeralKey(reqID, category, basename)

	remotePath, err := f.stager.EnsureRemote(ctx, f.nodeID, localPath, key)
	if err != nil {
		return "", "", fmt.Errorf("staging input file: %w", err)
	}

	return remotePath, key, nil
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
	reqID := requestID()
	in, _ = f.stageMultimodalInputs(ctx, reqID, in)
	return f.Backend.Predict(ctx, in, opts...)
}

func (f *FileStagingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, fn func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	reqID := requestID()
	in, _ = f.stageMultimodalInputs(ctx, reqID, in)
	return f.Backend.PredictStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	reqID := requestID()

	// Stage input source image if present
	if in.Src != "" && isFilePath(in.Src) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, in.Src, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging image src: %w", err)
		}
		in.Src = backendPath
	}

	// Stage reference images
	for i, img := range in.RefImages {
		if isFilePath(img) {
			backendPath, _, err := f.stageInputFile(ctx, reqID, img, "inputs")
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

func (f *FileStagingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	reqID := requestID()

	// Stage start/end images
	if in.StartImage != "" && isFilePath(in.StartImage) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, in.StartImage, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging start image: %w", err)
		}
		in.StartImage = backendPath
	}
	if in.EndImage != "" && isFilePath(in.EndImage) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, in.EndImage, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging end image: %w", err)
		}
		in.EndImage = backendPath
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

func (f *FileStagingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	// Translate model path from frontend to remote worker path.
	// The model and its companion files (e.g. .onnx.json) were already staged
	// during LoadModel, so we just need to point to the correct remote location.
	if in.Model != "" && isFilePath(in.Model) {
		in.Model = f.translateModelPath(in.Model)
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
	// Translate model path from frontend to remote worker path (same as TTS above)
	if in.Model != "" && isFilePath(in.Model) {
		in.Model = f.translateModelPath(in.Model)
	}

	return f.Backend.TTSStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	reqID := requestID()

	// Stage input source
	if in.Src != nil && *in.Src != "" && isFilePath(*in.Src) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, *in.Src, "inputs")
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

func (f *FileStagingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	reqID := requestID()

	// Stage input audio file
	if in.Dst != "" && isFilePath(in.Dst) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, in.Dst, "inputs")
		if err != nil {
			return nil, fmt.Errorf("staging audio for transcription: %w", err)
		}
		in.Dst = backendPath
	}

	return f.Backend.AudioTranscription(ctx, in, opts...)
}

func (f *FileStagingClient) AudioTranscriptionStream(ctx context.Context, in *pb.TranscriptRequest, fn func(chunk *pb.TranscriptStreamResponse), opts ...ggrpc.CallOption) error {
	reqID := requestID()

	// Stage input audio file
	if in.Dst != "" && isFilePath(in.Dst) {
		backendPath, _, err := f.stageInputFile(ctx, reqID, in.Dst, "inputs")
		if err != nil {
			return fmt.Errorf("staging audio for transcription stream: %w", err)
		}
		in.Dst = backendPath
	}

	return f.Backend.AudioTranscriptionStream(ctx, in, fn, opts...)
}

func (f *FileStagingClient) ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	frontendOutputPath := in.OutputPath
	if frontendOutputPath != "" {
		os.MkdirAll(frontendOutputPath, 0750)
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
		modelName := filepath.Base(frontendOutputPath)
		keyPrefix := storage.ModelKey(modelName) // "models/<modelName>"

		files, err := f.stager.ListRemoteDir(ctx, f.nodeID, keyPrefix)
		if err != nil {
			return &pb.Result{Success: false, Message: fmt.Sprintf("listing remote export dir: %v", err)}, nil
		}
		if len(files) == 0 {
			return &pb.Result{Success: false, Message: "export produced no files on worker"}, nil
		}

		for _, relPath := range files {
			key := keyPrefix + "/" + relPath
			localDst := filepath.Join(frontendOutputPath, relPath)
			os.MkdirAll(filepath.Dir(localDst), 0750)

			if err := f.stager.FetchRemoteByKey(ctx, f.nodeID, key, localDst); err != nil {
				return &pb.Result{Success: false, Message: fmt.Sprintf("fetching export file %s: %v", relPath, err)}, nil
			}
			xlog.Debug("Retrieved export file from worker", "file", relPath, "localDst", localDst)
		}
	}

	return result, nil
}

func (f *FileStagingClient) StartQuantization(ctx context.Context, in *pb.QuantizationRequest, opts ...ggrpc.CallOption) (*pb.QuantizationJobResult, error) {
	// Ensure the local output directory exists so the fetched file can be written
	if in.OutputDir != "" {
		os.MkdirAll(in.OutputDir, 0750)
	}
	return f.Backend.StartQuantization(ctx, in, opts...)
}

func (f *FileStagingClient) QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, fn func(update *pb.QuantizationProgressUpdate), opts ...ggrpc.CallOption) error {
	return f.Backend.QuantizationProgress(ctx, in, func(update *pb.QuantizationProgressUpdate) {
		// When quantization completes, fetch the output file from the worker.
		// Use a fresh context because quantization can take hours and the
		// original request context may have expired by the time this fires.
		if update.OutputFile != "" && update.Status == "completed" {
			relPath := strings.TrimPrefix(update.OutputFile, "/"+storage.DataKeyPrefix)
			key := storage.DataKey(relPath)
			fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer fetchCancel()
			if err := f.stager.FetchRemoteByKey(fetchCtx, f.nodeID, key, update.OutputFile); err != nil {
				xlog.Warn("Failed to retrieve quantization output", "file", update.OutputFile, "error", err)
			}
		}
		fn(update)
	}, opts...)
}

// --- helpers ---

// stageMultimodalInputs stages Images, Videos, Audios fields in PredictOptions
// if they are file paths (not base64 or URLs).
func (f *FileStagingClient) stageMultimodalInputs(ctx context.Context, reqID string, in *pb.PredictOptions) (*pb.PredictOptions, []string) {
	var keys []string
	in.Images = f.stagePathSlice(ctx, reqID, in.Images, "inputs", &keys)
	in.Videos = f.stagePathSlice(ctx, reqID, in.Videos, "inputs", &keys)
	in.Audios = f.stagePathSlice(ctx, reqID, in.Audios, "inputs", &keys)
	return in, keys
}

func (f *FileStagingClient) stagePathSlice(ctx context.Context, reqID string, paths []string, category string, keys *[]string) []string {
	result := make([]string, len(paths))
	for i, p := range paths {
		if isFilePath(p) {
			backendPath, key, err := f.stageInputFile(ctx, reqID, p, category)
			if err != nil {
				xlog.Warn("Failed to stage multimodal file, passing through", "path", p, "error", err)
				result[i] = p
				continue
			}
			result[i] = backendPath
			*keys = append(*keys, key)
		} else {
			result[i] = p
		}
	}
	return result
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
	// Starts with / (absolute path) or contains path separator
	return s[0] == '/' || filepath.IsAbs(s)
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

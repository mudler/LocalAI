package nodes

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
type FileStagingClient struct {
	inner  grpc.Backend
	stager FileStager
	nodeID string
}

// NewFileStagingClient creates a new file staging wrapper.
func NewFileStagingClient(inner grpc.Backend, stager FileStager, nodeID string) *FileStagingClient {
	return &FileStagingClient{
		inner:  inner,
		stager: stager,
		nodeID: nodeID,
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

// --- grpc.Backend interface implementation ---

func (f *FileStagingClient) IsBusy() bool {
	return f.inner.IsBusy()
}

func (f *FileStagingClient) HealthCheck(ctx context.Context) (bool, error) {
	return f.inner.HealthCheck(ctx)
}

func (f *FileStagingClient) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	return f.inner.Embeddings(ctx, in, opts...)
}

func (f *FileStagingClient) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	return f.inner.LoadModel(ctx, in, opts...)
}

func (f *FileStagingClient) Predict(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.Reply, error) {
	reqID := requestID()
	in, _ = f.stageMultimodalInputs(ctx, reqID, in)
	return f.inner.Predict(ctx, in, opts...)
}

func (f *FileStagingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, fn func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	reqID := requestID()
	in, _ = f.stageMultimodalInputs(ctx, reqID, in)
	return f.inner.PredictStream(ctx, in, fn, opts...)
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

	result, err := f.inner.GenerateImage(ctx, in, opts...)
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

	result, err := f.inner.GenerateVideo(ctx, in, opts...)
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
	// Handle output destination
	frontendDst := in.Dst
	if frontendDst != "" {
		tmpPath, err := f.stager.AllocRemoteTemp(ctx, f.nodeID)
		if err != nil {
			return nil, fmt.Errorf("allocating temp for TTS output: %w", err)
		}
		in.Dst = tmpPath
	}

	result, err := f.inner.TTS(ctx, in, opts...)
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

func (f *FileStagingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, fn func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	// TTSStream sends audio chunks inline — no file staging needed
	return f.inner.TTSStream(ctx, in, fn, opts...)
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

	result, err := f.inner.SoundGeneration(ctx, in, opts...)
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

	return f.inner.AudioTranscription(ctx, in, opts...)
}

func (f *FileStagingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	return f.inner.Detect(ctx, in, opts...)
}

func (f *FileStagingClient) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.TokenizationResponse, error) {
	return f.inner.TokenizeString(ctx, in, opts...)
}

func (f *FileStagingClient) Status(ctx context.Context) (*pb.StatusResponse, error) {
	return f.inner.Status(ctx)
}

func (f *FileStagingClient) StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	return f.inner.StoresSet(ctx, in, opts...)
}

func (f *FileStagingClient) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...ggrpc.CallOption) (*pb.Result, error) {
	return f.inner.StoresDelete(ctx, in, opts...)
}

func (f *FileStagingClient) StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...ggrpc.CallOption) (*pb.StoresGetResult, error) {
	return f.inner.StoresGet(ctx, in, opts...)
}

func (f *FileStagingClient) StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	return f.inner.StoresFind(ctx, in, opts...)
}

func (f *FileStagingClient) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...ggrpc.CallOption) (*pb.RerankResult, error) {
	return f.inner.Rerank(ctx, in, opts...)
}

func (f *FileStagingClient) GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...ggrpc.CallOption) (*pb.MetricsResponse, error) {
	return f.inner.GetTokenMetrics(ctx, in, opts...)
}

func (f *FileStagingClient) VAD(ctx context.Context, in *pb.VADRequest, opts ...ggrpc.CallOption) (*pb.VADResponse, error) {
	return f.inner.VAD(ctx, in, opts...)
}

func (f *FileStagingClient) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...ggrpc.CallOption) (*pb.AudioEncodeResult, error) {
	return f.inner.AudioEncode(ctx, in, opts...)
}

func (f *FileStagingClient) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...ggrpc.CallOption) (*pb.AudioDecodeResult, error) {
	return f.inner.AudioDecode(ctx, in, opts...)
}

func (f *FileStagingClient) ModelMetadata(ctx context.Context, in *pb.ModelOptions, opts ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return f.inner.ModelMetadata(ctx, in, opts...)
}

func (f *FileStagingClient) StartFineTune(ctx context.Context, in *pb.FineTuneRequest, opts ...ggrpc.CallOption) (*pb.FineTuneJobResult, error) {
	return f.inner.StartFineTune(ctx, in, opts...)
}

func (f *FileStagingClient) FineTuneProgress(ctx context.Context, in *pb.FineTuneProgressRequest, fn func(update *pb.FineTuneProgressUpdate), opts ...ggrpc.CallOption) error {
	return f.inner.FineTuneProgress(ctx, in, fn, opts...)
}

func (f *FileStagingClient) StopFineTune(ctx context.Context, in *pb.FineTuneStopRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	return f.inner.StopFineTune(ctx, in, opts...)
}

func (f *FileStagingClient) ListCheckpoints(ctx context.Context, in *pb.ListCheckpointsRequest, opts ...ggrpc.CallOption) (*pb.ListCheckpointsResponse, error) {
	return f.inner.ListCheckpoints(ctx, in, opts...)
}

func (f *FileStagingClient) ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	frontendOutputPath := in.OutputPath
	if frontendOutputPath != "" {
		os.MkdirAll(frontendOutputPath, 0750)
	}

	result, err := f.inner.ExportModel(ctx, in, opts...)
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
	return f.inner.StartQuantization(ctx, in, opts...)
}

func (f *FileStagingClient) QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, fn func(update *pb.QuantizationProgressUpdate), opts ...ggrpc.CallOption) error {
	return f.inner.QuantizationProgress(ctx, in, func(update *pb.QuantizationProgressUpdate) {
		// When quantization completes, fetch the output file from the worker
		if update.OutputFile != "" && update.Status == "completed" {
			relPath := strings.TrimPrefix(update.OutputFile, "/"+storage.DataKeyPrefix)
			key := storage.DataKey(relPath)
			if err := f.stager.FetchRemoteByKey(ctx, f.nodeID, key, update.OutputFile); err != nil {
				xlog.Warn("Failed to retrieve quantization output", "file", update.OutputFile, "error", err)
			}
		}
		fn(update)
	}, opts...)
}

func (f *FileStagingClient) StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	return f.inner.StopQuantization(ctx, in, opts...)
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
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

package model

import (
	"context"
	"sync"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	ggrpc "google.golang.org/grpc"
)

// ConnectionEvictingClient wraps a grpc.Backend. When any inference method
// fails with a connection error (server unreachable), it calls the evict
// callback to remove the model from the ModelLoader's cache. The error is
// still returned to the caller — the NEXT request will trigger rescheduling
// via SmartRouter.
type ConnectionEvictingClient struct {
	grpc.Backend
	modelID string
	evict   func()
	once    sync.Once
}

func newConnectionEvictingClient(inner grpc.Backend, modelID string, evict func()) grpc.Backend {
	return &ConnectionEvictingClient{
		Backend: inner,
		modelID: modelID,
		evict:   evict,
	}
}

func (c *ConnectionEvictingClient) checkErr(err error) {
	if err != nil && isConnectionError(err) {
		c.once.Do(func() {
			xlog.Warn("Connection error during inference, evicting model from cache",
				"model", c.modelID, "error", err)
			c.evict()
		})
	}
}

// --- Intercepted inference methods ---

func (c *ConnectionEvictingClient) Predict(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.Reply, error) {
	reply, err := c.Backend.Predict(ctx, in, opts...)
	c.checkErr(err)
	return reply, err
}

func (c *ConnectionEvictingClient) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	err := c.Backend.PredictStream(ctx, in, f, opts...)
	c.checkErr(err)
	return err
}

func (c *ConnectionEvictingClient) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	result, err := c.Backend.Embeddings(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	result, err := c.Backend.GenerateImage(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	result, err := c.Backend.GenerateVideo(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) TTS(ctx context.Context, in *pb.TTSRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	result, err := c.Backend.TTS(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...ggrpc.CallOption) error {
	err := c.Backend.TTSStream(ctx, in, f, opts...)
	c.checkErr(err)
	return err
}

func (c *ConnectionEvictingClient) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...ggrpc.CallOption) (*pb.Result, error) {
	result, err := c.Backend.SoundGeneration(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	result, err := c.Backend.AudioTranscription(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) Detect(ctx context.Context, in *pb.DetectOptions, opts ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	result, err := c.Backend.Detect(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

func (c *ConnectionEvictingClient) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...ggrpc.CallOption) (*pb.RerankResult, error) {
	result, err := c.Backend.Rerank(ctx, in, opts...)
	c.checkErr(err)
	return result, err
}

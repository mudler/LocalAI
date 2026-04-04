package grpc

import (
	"context"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var _ Backend = new(embedBackend)
var _ pb.Backend_PredictStreamServer = new(embedBackendServerStream)

type embedBackend struct {
	s *server
}

func (e *embedBackend) IsBusy() bool {
	return e.s.llm.Busy()
}

func (e *embedBackend) HealthCheck(ctx context.Context) (bool, error) {
	return true, nil
}

func (e *embedBackend) Embeddings(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.EmbeddingResult, error) {
	return e.s.Embedding(ctx, in)
}

func (e *embedBackend) Predict(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.Reply, error) {
	return e.s.Predict(ctx, in)
}

func (e *embedBackend) LoadModel(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.LoadModel(ctx, in)
}

func (e *embedBackend) PredictStream(ctx context.Context, in *pb.PredictOptions, f func(reply *pb.Reply), opts ...grpc.CallOption) error {
	bs := &embedBackendServerStream{
		ctx: ctx,
		fn:  f,
	}
	return e.s.PredictStream(in, bs)
}

func (e *embedBackend) GenerateImage(ctx context.Context, in *pb.GenerateImageRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.GenerateImage(ctx, in)
}

func (e *embedBackend) GenerateVideo(ctx context.Context, in *pb.GenerateVideoRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.GenerateVideo(ctx, in)
}

func (e *embedBackend) TTS(ctx context.Context, in *pb.TTSRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.TTS(ctx, in)
}

func (e *embedBackend) TTSStream(ctx context.Context, in *pb.TTSRequest, f func(reply *pb.Reply), opts ...grpc.CallOption) error {
	bs := &embedBackendServerStream{
		ctx: ctx,
		fn:  f,
	}
	return e.s.TTSStream(in, bs)
}

func (e *embedBackend) SoundGeneration(ctx context.Context, in *pb.SoundGenerationRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.SoundGeneration(ctx, in)
}

func (e *embedBackend) Detect(ctx context.Context, in *pb.DetectOptions, opts ...grpc.CallOption) (*pb.DetectResponse, error) {
	return e.s.Detect(ctx, in)
}

func (e *embedBackend) AudioTranscription(ctx context.Context, in *pb.TranscriptRequest, opts ...grpc.CallOption) (*pb.TranscriptResult, error) {
	return e.s.AudioTranscription(ctx, in)
}

func (e *embedBackend) TokenizeString(ctx context.Context, in *pb.PredictOptions, opts ...grpc.CallOption) (*pb.TokenizationResponse, error) {
	return e.s.TokenizeString(ctx, in)
}

func (e *embedBackend) Status(ctx context.Context) (*pb.StatusResponse, error) {
	return e.s.Status(ctx, &pb.HealthMessage{})
}

func (e *embedBackend) StoresSet(ctx context.Context, in *pb.StoresSetOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StoresSet(ctx, in)
}

func (e *embedBackend) StoresDelete(ctx context.Context, in *pb.StoresDeleteOptions, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StoresDelete(ctx, in)
}

func (e *embedBackend) StoresGet(ctx context.Context, in *pb.StoresGetOptions, opts ...grpc.CallOption) (*pb.StoresGetResult, error) {
	return e.s.StoresGet(ctx, in)
}

func (e *embedBackend) StoresFind(ctx context.Context, in *pb.StoresFindOptions, opts ...grpc.CallOption) (*pb.StoresFindResult, error) {
	return e.s.StoresFind(ctx, in)
}

func (e *embedBackend) Rerank(ctx context.Context, in *pb.RerankRequest, opts ...grpc.CallOption) (*pb.RerankResult, error) {
	return e.s.Rerank(ctx, in)
}

func (e *embedBackend) VAD(ctx context.Context, in *pb.VADRequest, opts ...grpc.CallOption) (*pb.VADResponse, error) {
	return e.s.VAD(ctx, in)
}

func (e *embedBackend) AudioEncode(ctx context.Context, in *pb.AudioEncodeRequest, opts ...grpc.CallOption) (*pb.AudioEncodeResult, error) {
	return e.s.AudioEncode(ctx, in)
}

func (e *embedBackend) AudioDecode(ctx context.Context, in *pb.AudioDecodeRequest, opts ...grpc.CallOption) (*pb.AudioDecodeResult, error) {
	return e.s.AudioDecode(ctx, in)
}

func (e *embedBackend) ModelMetadata(ctx context.Context, in *pb.ModelOptions, opts ...grpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return e.s.ModelMetadata(ctx, in)
}

func (e *embedBackend) GetTokenMetrics(ctx context.Context, in *pb.MetricsRequest, opts ...grpc.CallOption) (*pb.MetricsResponse, error) {
	return e.s.GetMetrics(ctx, in)
}

func (e *embedBackend) StartFineTune(ctx context.Context, in *pb.FineTuneRequest, opts ...grpc.CallOption) (*pb.FineTuneJobResult, error) {
	return e.s.StartFineTune(ctx, in)
}

func (e *embedBackend) FineTuneProgress(ctx context.Context, in *pb.FineTuneProgressRequest, f func(update *pb.FineTuneProgressUpdate), opts ...grpc.CallOption) error {
	bs := &embedBackendFineTuneProgressStream{
		ctx: ctx,
		fn:  f,
	}
	return e.s.FineTuneProgress(in, bs)
}

func (e *embedBackend) StopFineTune(ctx context.Context, in *pb.FineTuneStopRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StopFineTune(ctx, in)
}

func (e *embedBackend) ListCheckpoints(ctx context.Context, in *pb.ListCheckpointsRequest, opts ...grpc.CallOption) (*pb.ListCheckpointsResponse, error) {
	return e.s.ListCheckpoints(ctx, in)
}

func (e *embedBackend) ExportModel(ctx context.Context, in *pb.ExportModelRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.ExportModel(ctx, in)
}

func (e *embedBackend) StartQuantization(ctx context.Context, in *pb.QuantizationRequest, opts ...grpc.CallOption) (*pb.QuantizationJobResult, error) {
	return e.s.StartQuantization(ctx, in)
}

func (e *embedBackend) QuantizationProgress(ctx context.Context, in *pb.QuantizationProgressRequest, f func(update *pb.QuantizationProgressUpdate), opts ...grpc.CallOption) error {
	bs := &embedBackendQuantizationProgressStream{
		ctx: ctx,
		fn:  f,
	}
	return e.s.QuantizationProgress(in, bs)
}

func (e *embedBackend) StopQuantization(ctx context.Context, in *pb.QuantizationStopRequest, opts ...grpc.CallOption) (*pb.Result, error) {
	return e.s.StopQuantization(ctx, in)
}

func (e *embedBackend) Free(ctx context.Context) error {
	_, err := e.s.Free(ctx, &pb.HealthMessage{})
	return err
}

var _ pb.Backend_FineTuneProgressServer = new(embedBackendFineTuneProgressStream)

type embedBackendFineTuneProgressStream struct {
	ctx context.Context
	fn  func(update *pb.FineTuneProgressUpdate)
}

func (e *embedBackendFineTuneProgressStream) Send(update *pb.FineTuneProgressUpdate) error {
	e.fn(update)
	return nil
}

func (e *embedBackendFineTuneProgressStream) SetHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendFineTuneProgressStream) SendHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendFineTuneProgressStream) SetTrailer(md metadata.MD) {
}

func (e *embedBackendFineTuneProgressStream) Context() context.Context {
	return e.ctx
}

func (e *embedBackendFineTuneProgressStream) SendMsg(m any) error {
	if x, ok := m.(*pb.FineTuneProgressUpdate); ok {
		return e.Send(x)
	}
	return nil
}

func (e *embedBackendFineTuneProgressStream) RecvMsg(m any) error {
	return nil
}

var _ pb.Backend_QuantizationProgressServer = new(embedBackendQuantizationProgressStream)

type embedBackendQuantizationProgressStream struct {
	ctx context.Context
	fn  func(update *pb.QuantizationProgressUpdate)
}

func (e *embedBackendQuantizationProgressStream) Send(update *pb.QuantizationProgressUpdate) error {
	e.fn(update)
	return nil
}

func (e *embedBackendQuantizationProgressStream) SetHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendQuantizationProgressStream) SendHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendQuantizationProgressStream) SetTrailer(md metadata.MD) {
}

func (e *embedBackendQuantizationProgressStream) Context() context.Context {
	return e.ctx
}

func (e *embedBackendQuantizationProgressStream) SendMsg(m any) error {
	if x, ok := m.(*pb.QuantizationProgressUpdate); ok {
		return e.Send(x)
	}
	return nil
}

func (e *embedBackendQuantizationProgressStream) RecvMsg(m any) error {
	return nil
}

type embedBackendServerStream struct {
	ctx context.Context
	fn  func(reply *pb.Reply)
}

func (e *embedBackendServerStream) Send(reply *pb.Reply) error {
	e.fn(reply)
	return nil
}

func (e *embedBackendServerStream) SetHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendServerStream) SendHeader(md metadata.MD) error {
	return nil
}

func (e *embedBackendServerStream) SetTrailer(md metadata.MD) {
}

func (e *embedBackendServerStream) Context() context.Context {
	return e.ctx
}

func (e *embedBackendServerStream) SendMsg(m any) error {
	if x, ok := m.(*pb.Reply); ok {
		return e.Send(x)
	}
	return nil
}

func (e *embedBackendServerStream) RecvMsg(m any) error {
	return nil
}
